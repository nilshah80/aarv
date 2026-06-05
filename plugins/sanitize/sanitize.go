// Package sanitize provides JSON request-body sanitization middleware
// for the aarv framework.
//
// The middleware decodes the request body as JSON, recursively walks
// strings under map and slice nodes, applies HTML stripping (best-effort,
// stdlib state-machine — not a substitute for bluemonday on adversarial
// input), Unicode NFC normalization, and any caller-supplied SanitizerFuncs,
// then re-encodes and replaces the request body.
//
// # Submodule placement
//
// This plugin is a separate submodule because Unicode NFC normalization
// requires golang.org/x/text/unicode/norm. The aarv root module is
// strict zero-dep; pulling x/text into the root would violate that
// invariant. The cost is one more replace directive at release tagging,
// which is identical to the plugins/prometheus and plugins/otel cadence.
package sanitize

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/nilshah80/aarv"
	"golang.org/x/text/unicode/norm"
)

// SanitizerFunc transforms a single string value. Custom sanitizers run
// after the built-in HTML and NFC steps, in declaration order.
type SanitizerFunc func(string) string

// Skipper bypasses the middleware when it returns true. OR-combined with
// SkipPaths.
type Skipper func(*aarv.Context) bool

// Config holds sanitizer configuration. Pass through New.
type Config struct {
	// Fields, when non-empty, restricts sanitization to JSON keys with
	// these names (any depth). Empty (default) sanitizes every string.
	Fields []string

	// SkipFields names JSON keys whose values are NEVER sanitized
	// (passwords, tokens, signed payloads).
	SkipFields []string

	// StripHTML removes HTML tags and decodes a small set of named
	// entities (&amp; &lt; &gt; &quot; &#39;) from string values.
	StripHTML bool

	// NormalizeUnicode applies NFC normalization to string values.
	NormalizeUnicode bool

	// Custom sanitizers run after StripHTML and NormalizeUnicode, in
	// declaration order.
	Custom []SanitizerFunc

	// MaxBodyBytes caps the request body size before sanitization. 0
	// disables the cap. Bodies exceeding the cap result in 413.
	MaxBodyBytes int64

	// ContentTypes is the set of Content-Type media types that are
	// processed; everything else passes through. Defaults to
	// {"application/json"}.
	ContentTypes []string

	Skipper   Skipper
	SkipPaths []string
}

// DefaultConfig returns an opinionated default suitable for typical SPA
// JSON APIs.
func DefaultConfig() Config {
	return Config{
		StripHTML:        true,
		NormalizeUnicode: true,
		MaxBodyBytes:     1 << 20, // 1 MiB
	}
}

type normalized struct {
	fields           map[string]struct{}
	useFieldFilter   bool
	skipFields       map[string]struct{}
	stripHTML        bool
	normalizeUnicode bool
	custom           []SanitizerFunc
	maxBodyBytes     int64
	contentTypes     map[string]struct{}
	skipper          Skipper
	skipPaths        map[string]struct{}
}

// New constructs the sanitizer middleware.
func New(cfg Config) aarv.NativeMiddleware {
	n := normalize(cfg)

	native := aarv.MiddlewareFunc(func(next aarv.HandlerFunc) aarv.HandlerFunc {
		return func(c *aarv.Context) error {
			if n.shouldSkip(c) {
				return next(c)
			}
			if !n.matchesContentType(c.Header("Content-Type")) {
				return next(c)
			}
			body, err := readAndCap(c.BodyReader(), n.maxBodyBytes)
			if err != nil {
				if errors.Is(err, errBodyTooLarge) {
					return aarv.ErrPayloadTooLarge("request body too large")
				}
				return aarv.ErrBadRequest("failed to read request body").WithInternal(err)
			}
			sanitized, ok := n.sanitize(body)
			if !ok {
				// Invalid JSON: passthrough unchanged. Sanitizer is not a
				// body validator — Bind handles malformed payloads.
				c.SetBody(io.NopCloser(bytes.NewReader(body)))
				return next(c)
			}
			c.SetBody(io.NopCloser(bytes.NewReader(sanitized)))
			c.Request().ContentLength = int64(len(sanitized))
			return next(c)
		}
	})

	m := aarv.Middleware(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, hasCtx := aarv.FromRequest(r)
			if hasCtx && n.shouldSkip(c) {
				next.ServeHTTP(w, r)
				return
			}
			if !n.matchesContentType(r.Header.Get("Content-Type")) {
				next.ServeHTTP(w, r)
				return
			}
			body, err := readAndCap(r.Body, n.maxBodyBytes)
			if err != nil {
				if errors.Is(err, errBodyTooLarge) {
					writeJSONError(w, http.StatusRequestEntityTooLarge, "request body too large")
					return
				}
				writeJSONError(w, http.StatusBadRequest, "failed to read request body")
				return
			}
			sanitized, ok := n.sanitize(body)
			if !ok {
				r.Body = io.NopCloser(bytes.NewReader(body))
				next.ServeHTTP(w, r)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(sanitized))
			r.ContentLength = int64(len(sanitized))
			next.ServeHTTP(w, r)
		})
	})

	return aarv.RegisterNativeMiddleware(m, native)
}

func normalize(cfg Config) *normalized {
	n := &normalized{
		stripHTML:        cfg.StripHTML,
		normalizeUnicode: cfg.NormalizeUnicode,
		maxBodyBytes:     cfg.MaxBodyBytes,
		skipper:          cfg.Skipper,
	}
	n.custom = append([]SanitizerFunc(nil), cfg.Custom...)
	if len(cfg.Fields) > 0 {
		n.fields = make(map[string]struct{}, len(cfg.Fields))
		for _, f := range cfg.Fields {
			n.fields[f] = struct{}{}
		}
		n.useFieldFilter = true
	}
	if len(cfg.SkipFields) > 0 {
		n.skipFields = make(map[string]struct{}, len(cfg.SkipFields))
		for _, f := range cfg.SkipFields {
			n.skipFields[f] = struct{}{}
		}
	}
	if len(cfg.ContentTypes) == 0 {
		n.contentTypes = map[string]struct{}{"application/json": {}}
	} else {
		n.contentTypes = make(map[string]struct{}, len(cfg.ContentTypes))
		for _, ct := range cfg.ContentTypes {
			n.contentTypes[strings.ToLower(strings.TrimSpace(ct))] = struct{}{}
		}
	}
	if len(cfg.SkipPaths) > 0 {
		n.skipPaths = make(map[string]struct{}, len(cfg.SkipPaths))
		for _, p := range cfg.SkipPaths {
			n.skipPaths[p] = struct{}{}
		}
	}
	return n
}

func (n *normalized) shouldSkip(c *aarv.Context) bool {
	if _, ok := n.skipPaths[c.Path()]; ok {
		return true
	}
	if n.skipper != nil && n.skipper(c) {
		return true
	}
	return false
}

// matchesContentType returns true when the request's Content-Type
// (sans parameters) is in the configured set.
func (n *normalized) matchesContentType(ct string) bool {
	if ct == "" {
		return false
	}
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	ct = strings.ToLower(strings.TrimSpace(ct))
	_, ok := n.contentTypes[ct]
	return ok
}

// sanitize decodes body as JSON, walks the tree, and re-encodes. Returns
// (newBody, true) on success or (nil, false) when the input is not valid
// JSON (in which case callers should pass the original through unchanged).
//
// Re-encoding never fails in practice — walk() only produces JSON-native
// values (map / slice / string / float64 / bool / nil), all of which
// marshal cleanly. We deliberately do not handle a json.Marshal error
// here; if it ever fires, that signals a programming error in walk()
// rather than user input the package should accommodate.
func (n *normalized) sanitize(body []byte) ([]byte, bool) {
	if len(body) == 0 {
		return body, true
	}
	var data any
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, false
	}
	walked := n.walk(data, "", false)
	out, _ := json.Marshal(walked)
	return out, true
}

// walk recursively visits a decoded JSON value, applying sanitizeString
// to strings whose enclosing key passes the Fields/SkipFields filter.
//
// keyName is the JSON key under which the current value lives (empty at
// the root). insideAllowed indicates that an ancestor's key was in
// n.fields, so all nested strings should be sanitized regardless of the
// individual key — useful when callers want "sanitize the whole body
// once you enter this object".
func (n *normalized) walk(v any, keyName string, insideAllowed bool) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, child := range x {
			if _, skip := n.skipFields[k]; skip {
				out[k] = child
				continue
			}
			childAllowed := insideAllowed
			if !childAllowed && n.useFieldFilter {
				_, childAllowed = n.fields[k]
			}
			out[k] = n.walk(child, k, childAllowed)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, child := range x {
			out[i] = n.walk(child, keyName, insideAllowed)
		}
		return out
	case string:
		if !n.shouldSanitizeString(keyName, insideAllowed) {
			return x
		}
		return n.sanitizeString(x)
	default:
		return v
	}
}

// shouldSanitizeString decides whether a string value should be passed
// through the sanitizer pipeline.
//
// When useFieldFilter is set, walk() has already promoted insideAllowed
// to true before recursing into any value under a configured Fields key,
// so the parent-level check is sufficient — there is no need to re-check
// n.fields[keyName] at the leaf, and reaching this function with
// insideAllowed=false guarantees the leaf was outside the allowlist.
func (n *normalized) shouldSanitizeString(_ string, insideAllowed bool) bool {
	if !n.useFieldFilter {
		return true
	}
	return insideAllowed
}

func (n *normalized) sanitizeString(s string) string {
	if n.stripHTML {
		s = stripHTML(s)
	}
	if n.normalizeUnicode {
		s = norm.NFC.String(s)
	}
	for _, fn := range n.custom {
		s = fn(s)
	}
	return s
}

// --- HTML stripping (stdlib state machine) ---

// stripHTML removes well-formed tags and decodes a small set of named
// entities. Best-effort — comments, CDATA, and adversarial half-tags
// are handled conservatively (a "<" with no closing ">" is dropped along
// with everything to end-of-string).
func stripHTML(s string) string {
	if !strings.ContainsAny(s, "<&") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		switch s[i] {
		case '<':
			// Comment <!-- ... -->
			if strings.HasPrefix(s[i:], "<!--") {
				if j := strings.Index(s[i+4:], "-->"); j >= 0 {
					i += 4 + j + 3
					continue
				}
				return b.String()
			}
			// Tag <foo ...>
			j := strings.IndexByte(s[i:], '>')
			if j < 0 {
				// Unterminated tag: drop the rest.
				return b.String()
			}
			i += j + 1
		case '&':
			ent, n := decodeEntity(s[i:])
			if n > 0 {
				b.WriteString(ent)
				i += n
			} else {
				b.WriteByte('&')
				i++
			}
		default:
			b.WriteByte(s[i])
			i++
		}
	}
	return b.String()
}

func decodeEntity(s string) (decoded string, consumed int) {
	switch {
	case strings.HasPrefix(s, "&amp;"):
		return "&", len("&amp;")
	case strings.HasPrefix(s, "&lt;"):
		return "<", len("&lt;")
	case strings.HasPrefix(s, "&gt;"):
		return ">", len("&gt;")
	case strings.HasPrefix(s, "&quot;"):
		return `"`, len("&quot;")
	case strings.HasPrefix(s, "&#39;"):
		return "'", len("&#39;")
	case strings.HasPrefix(s, "&apos;"):
		return "'", len("&apos;")
	case strings.HasPrefix(s, "&nbsp;"):
		return " ", len("&nbsp;")
	}
	return "", 0
}

// --- body reading ---

var errBodyTooLarge = errors.New("sanitize: request body exceeds MaxBodyBytes")

func readAndCap(r io.Reader, maxBytes int64) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	if maxBytes <= 0 {
		return io.ReadAll(r)
	}
	limited := io.LimitReader(r, maxBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, errBodyTooLarge
	}
	return body, nil
}

// --- error response ---

type errorBody struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorBody{
		Error:   codeForStatus(status),
		Message: message,
	})
}

func codeForStatus(status int) string {
	switch status {
	case http.StatusRequestEntityTooLarge:
		return "payload_too_large"
	case http.StatusBadRequest:
		return "bad_request"
	default:
		return http.StatusText(status)
	}
}
