// Package ipfilter provides allowlist / denylist IP filtering middleware
// for the aarv framework.
//
// CIDRs are parsed once at construction (New panics on invalid input,
// matching jwt / apikey / basicauth). Bare IPs are accepted and treated as
// /32 for IPv4 or /128 for IPv6. The default source IP is (*aarv.Context).RealIP();
// callers fronted by edge proxies that strip the IP can override via IPFunc.
//
// # Fail-closed vs fail-open
//
// When the source IP is empty or unparseable at request time:
//
//   - ModeAllowlist blocks the request (fail closed) — an unknown caller can
//     never satisfy an allowlist.
//   - ModeDenylist passes the request (fail open) — an unknown caller cannot
//     match any denied range.
//
// Operators behind a proxy that strips IPs MUST surface them via IPFunc;
// the default RealIP path will otherwise misclassify every request.
package ipfilter

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/nilshah80/aarv"
)

// Mode selects between allowlist and denylist semantics.
type Mode int

const (
	// ModeAllowlist accepts requests whose source IP matches one of the
	// configured CIDRs and rejects everything else. Empty CIDRs panics in New.
	ModeAllowlist Mode = iota

	// ModeDenylist rejects requests whose source IP matches one of the
	// configured CIDRs and accepts everything else. Empty CIDRs is a no-op.
	ModeDenylist
)

// IPFunc resolves the source IP from a request context. The default
// (when nil) is (*aarv.Context).RealIP(). Override when fronted by a
// proxy that strips RemoteAddr and surfaces the client IP via a custom
// header that aarv's RealIP does not consult.
type IPFunc func(*aarv.Context) string

// Skipper bypasses the filter entirely when it returns true. Combined OR
// with SkipPaths.
type Skipper func(*aarv.Context) bool

// ErrorHandler builds the rejection response. Defaults to a JSON 403.
// The IP argument is the parsed source IP (or nil if it was unparseable).
type ErrorHandler func(c *aarv.Context, ip net.IP) error

// Config holds ipfilter configuration. CIDRs is required for ModeAllowlist;
// invalid CIDRs panic in New.
type Config struct {
	Mode         Mode
	CIDRs        []string
	IPFunc       IPFunc
	StatusCode   int
	Message      string
	ErrorHandler ErrorHandler
	Skipper      Skipper
	SkipPaths    []string
}

// DefaultConfig returns a zero-value Config suitable for passing through
// New after the caller fills in Mode and CIDRs.
func DefaultConfig() Config {
	return Config{
		Mode:       ModeAllowlist,
		StatusCode: http.StatusForbidden,
		Message:    "forbidden",
	}
}

type normalized struct {
	mode       Mode
	nets       []*net.IPNet
	ipFunc     IPFunc
	statusCode int
	message    string
	errHandler ErrorHandler
	skipper    Skipper
	skipPaths  map[string]struct{}
}

// New constructs ipfilter middleware. Panics on invalid CIDRs and on an
// empty CIDRs slice in ModeAllowlist (an empty allowlist would block every
// request — almost certainly a misconfiguration).
func New(cfg Config) aarv.NativeMiddleware {
	n := normalize(cfg)

	native := aarv.MiddlewareFunc(func(next aarv.HandlerFunc) aarv.HandlerFunc {
		return func(c *aarv.Context) error {
			if shouldSkipNative(c, n) {
				return next(c)
			}
			ipStr := n.ipFunc(c)
			ip := net.ParseIP(ipStr)
			if !decide(n, ip) {
				return rejectNative(c, n, ip)
			}
			return next(c)
		}
	})

	m := aarv.Middleware(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, skip := n.skipPaths[r.URL.Path]; skip {
				next.ServeHTTP(w, r)
				return
			}
			c, hasCtx := aarv.FromRequest(r)
			if hasCtx && n.skipper != nil && n.skipper(c) {
				next.ServeHTTP(w, r)
				return
			}
			var ipStr string
			if hasCtx {
				ipStr = n.ipFunc(c)
			} else {
				ipStr = directIPFromRemoteAddr(r.RemoteAddr)
			}
			ip := net.ParseIP(ipStr)
			if !decide(n, ip) {
				rejectStdlib(w, c, hasCtx, n, ip)
				return
			}
			next.ServeHTTP(w, r)
		})
	})

	return aarv.RegisterNativeMiddleware(m, native)
}

func normalize(cfg Config) *normalized {
	n := &normalized{
		mode:       cfg.Mode,
		ipFunc:     cfg.IPFunc,
		statusCode: cfg.StatusCode,
		message:    cfg.Message,
		errHandler: cfg.ErrorHandler,
		skipper:    cfg.Skipper,
	}
	if n.statusCode == 0 {
		n.statusCode = http.StatusForbidden
	}
	if n.message == "" {
		n.message = "forbidden"
	}
	if n.ipFunc == nil {
		n.ipFunc = defaultIPFunc
	}

	cidrs := append([]string(nil), cfg.CIDRs...)
	if cfg.Mode == ModeAllowlist && len(cidrs) == 0 {
		panic("ipfilter: ModeAllowlist requires at least one CIDR — empty list would block all traffic")
	}
	n.nets = make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		ipNet, err := parseCIDROrIP(c)
		if err != nil {
			panic(fmt.Sprintf("ipfilter: invalid CIDR %q: %v", c, err))
		}
		n.nets = append(n.nets, ipNet)
	}

	if len(cfg.SkipPaths) > 0 {
		n.skipPaths = make(map[string]struct{}, len(cfg.SkipPaths))
		for _, p := range cfg.SkipPaths {
			n.skipPaths[p] = struct{}{}
		}
	}
	return n
}

// parseCIDROrIP accepts either a CIDR ("10.0.0.0/8") or a bare IP
// ("10.0.0.1"). Bare IPs are converted to /32 (IPv4) or /128 (IPv6).
func parseCIDROrIP(s string) (*net.IPNet, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("empty input")
	}
	if strings.ContainsRune(s, '/') {
		_, ipNet, err := net.ParseCIDR(s)
		if err != nil {
			return nil, err
		}
		return ipNet, nil
	}
	ip := net.ParseIP(s)
	if ip == nil {
		return nil, fmt.Errorf("not a valid IP")
	}
	if ip4 := ip.To4(); ip4 != nil {
		return &net.IPNet{IP: ip4, Mask: net.CIDRMask(32, 32)}, nil
	}
	return &net.IPNet{IP: ip, Mask: net.CIDRMask(128, 128)}, nil
}

// decide returns true if the request should pass.
func decide(n *normalized, ip net.IP) bool {
	if ip == nil {
		// Fail-closed in allowlist, fail-open in denylist.
		return n.mode == ModeDenylist
	}
	matched := false
	for _, network := range n.nets {
		if network.Contains(ip) {
			matched = true
			break
		}
	}
	if n.mode == ModeAllowlist {
		return matched
	}
	return !matched
}

func shouldSkipNative(c *aarv.Context, n *normalized) bool {
	if _, ok := n.skipPaths[c.Path()]; ok {
		return true
	}
	if n.skipper != nil && n.skipper(c) {
		return true
	}
	return false
}

func rejectNative(c *aarv.Context, n *normalized, ip net.IP) error {
	if n.errHandler != nil {
		return n.errHandler(c, ip)
	}
	return c.JSON(n.statusCode, errorBody{
		Error:     codeForStatus(n.statusCode),
		Message:   n.message,
		RequestID: c.RequestID(),
	})
}

func rejectStdlib(w http.ResponseWriter, c *aarv.Context, hasCtx bool, n *normalized, ip net.IP) {
	if hasCtx && n.errHandler != nil {
		if err := n.errHandler(c, ip); err != nil {
			// ErrorHandler returned an error of its own — surface as the
			// configured message at the configured status. The native path
			// would route through handleError; stdlib has no equivalent
			// hook, so emit a JSON body directly.
			writeJSONError(w, n.statusCode, n.message, "")
		}
		return
	}
	requestID := ""
	if hasCtx {
		requestID = c.RequestID()
	}
	writeJSONError(w, n.statusCode, n.message, requestID)
}

func defaultIPFunc(c *aarv.Context) string { return c.RealIP() }

// directIPFromRemoteAddr is the fallback for stdlib paths reached without
// an aarv.Context (e.g. middleware wired via App.Mount). It mirrors the
// host-only portion of RemoteAddr without the trusted-proxy resolution
// chain — operators relying on X-Forwarded-For must surface a context.
func directIPFromRemoteAddr(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

// --- error response helpers ---

type errorBody struct {
	Error     string `json:"error"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

func writeJSONError(w http.ResponseWriter, status int, message, requestID string) {
	body := errorBody{
		Error:     codeForStatus(status),
		Message:   message,
		RequestID: requestID,
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func codeForStatus(status int) string {
	switch status {
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusTooManyRequests:
		return "too_many_requests"
	default:
		return http.StatusText(status)
	}
}
