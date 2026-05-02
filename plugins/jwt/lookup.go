// Package jwt — token lookup from header / query / cookie.
//
// The plugin supports an ordered list of Lookups. The first non-empty
// extraction wins; when every configured lookup returns empty,
// extractToken signals ErrMissingToken so the middleware can emit 401.

package jwt

import (
	"net/http"
	"strings"
)

// Lookup describes one place the middleware should search for a token.
//
// Source must be one of "header", "query", or "cookie". When Source is
// "header" and Scheme is non-empty (e.g. "Bearer"), the header value must
// start with that scheme (case-insensitive, RFC 7235 §2.1) and one space
// before the token; an optional second leading space is tolerated for
// non-conforming clients. When Source is "header" and Scheme is empty,
// the raw header value is returned.
type Lookup struct {
	Source string
	Name   string
	Scheme string
}

const (
	lookupHeader = "header"
	lookupQuery  = "query"
	lookupCookie = "cookie"
)

// extractToken iterates lookups in order against the tokenSource and
// returns the first non-empty token. The tokenSource indirection lets the
// same extractor serve both the native (*aarv.Context) and stdlib
// (*http.Request) paths without duplication. When every configured
// lookup yields an empty value, the function returns ErrMissingToken so
// the middleware (and any future programmatic caller) can branch on the
// sentinel via errors.Is.
func extractToken(lookups []Lookup, h tokenSource) (string, error) {
	for _, lk := range lookups {
		switch lk.Source {
		case lookupHeader:
			if v := readHeader(h.header(lk.Name), lk.Scheme); v != "" {
				return v, nil
			}
		case lookupQuery:
			if v := h.query(lk.Name); v != "" {
				return v, nil
			}
		case lookupCookie:
			if v := h.cookie(lk.Name); v != "" {
				return v, nil
			}
		}
	}
	return "", ErrMissingToken
}

// readHeader applies scheme stripping when scheme is non-empty. It mirrors
// basicauth.parseAuthHeader: case-insensitive scheme prefix match and one
// optional leading space after the scheme.
func readHeader(value, scheme string) string {
	if value == "" {
		return ""
	}
	if scheme == "" {
		return value
	}
	if len(value) < len(scheme)+1 {
		return ""
	}
	if !strings.EqualFold(value[:len(scheme)], scheme) {
		return ""
	}
	rest := value[len(scheme):]
	// Require at least one separator (space). RFC 7235 §2.1.
	if len(rest) == 0 || rest[0] != ' ' {
		return ""
	}
	rest = rest[1:]
	// Tolerate one extra leading space.
	if len(rest) > 0 && rest[0] == ' ' {
		rest = rest[1:]
	}
	return rest
}

// tokenSource abstracts header/query/cookie reads so extractToken can be
// driven by either an *aarv.Context or an *http.Request.
type tokenSource interface {
	header(name string) string
	query(name string) string
	cookie(name string) string
}

// requestSource adapts *http.Request to tokenSource (stdlib path).
type requestSource struct{ r *http.Request }

func (s requestSource) header(name string) string { return s.r.Header.Get(name) }
func (s requestSource) query(name string) string  { return s.r.URL.Query().Get(name) }
func (s requestSource) cookie(name string) string {
	c, err := s.r.Cookie(name)
	if err != nil {
		return ""
	}
	return c.Value
}
