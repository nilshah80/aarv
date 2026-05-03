package hmacauth

import (
	"crypto/sha256"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// canonicalQuery encodes url.Values as a deterministic byte sequence:
//
//   - Keys are sorted ASCII-ascending.
//   - For each key, the value list is sorted ASCII-ascending.
//   - Keys and values are percent-encoded per RFC 3986 §2.3 (unreserved
//     set: ALPHA / DIGIT / "-" / "." / "_" / "~"). Every other byte —
//     including '+' and ' ' — is encoded as %HH with uppercase hex.
//   - Pairs are joined with '=' and adjacent pairs joined with '&'.
//
// We deliberately do NOT use url.QueryEscape: it follows
// application/x-www-form-urlencoded rules (space → '+'), which differs
// from RFC 3986. Mixing the two encoders silently breaks signature
// verification across implementations. Callers signing or verifying must
// use this function exclusively.
func canonicalQuery(values url.Values) string {
	if len(values) == 0 {
		return ""
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	first := true
	for _, k := range keys {
		ek := percentEncode(k)
		vs := values[k]
		// Sort each key's value list so the encoding is independent of
		// caller insertion order. Sort a copy to avoid mutating the
		// caller's slice.
		sorted := append([]string(nil), vs...)
		sort.Strings(sorted)
		for _, v := range sorted {
			if !first {
				b.WriteByte('&')
			}
			first = false
			b.WriteString(ek)
			b.WriteByte('=')
			b.WriteString(percentEncode(v))
		}
	}
	return b.String()
}

// percentEncode escapes s per RFC 3986 §2.3 unreserved-set rules.
func percentEncode(s string) string {
	// Fast path: scan for any byte that needs encoding. Most query
	// keys/values are pure ASCII identifiers, so the common case
	// returns the input unchanged.
	needs := false
	for i := 0; i < len(s); i++ {
		if !isUnreserved(s[i]) {
			needs = true
			break
		}
	}
	if !needs {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if isUnreserved(c) {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hexUpper[c>>4])
		b.WriteByte(hexUpper[c&0x0f])
	}
	return b.String()
}

const hexUpper = "0123456789ABCDEF"

func isUnreserved(c byte) bool {
	switch {
	case c >= 'A' && c <= 'Z':
		return true
	case c >= 'a' && c <= 'z':
		return true
	case c >= '0' && c <= '9':
		return true
	case c == '-' || c == '.' || c == '_' || c == '~':
		return true
	}
	return false
}

// canonicalRequest builds the byte sequence over which the HMAC is
// computed:
//
//	METHOD\nPATH\nCANONICAL_QUERY\nHEX(SHA256(body))\nTIMESTAMP\nNONCE
//
// Method is uppercased. Path is taken verbatim — callers are expected
// to use the request's URL.Path which the http server has already
// percent-decoded once. Body is hashed with SHA-256 and the lowercase
// hex digest is included; an empty body hashes to the well-known empty
// digest (e3b0c44...).
func canonicalRequest(method, path string, query url.Values, body []byte, timestamp int64, nonce string) []byte {
	bodyHash := sha256.Sum256(body)
	q := canonicalQuery(query)
	ts := strconv.FormatInt(timestamp, 10)

	// Pre-size to avoid the strings.Builder grow path; this function
	// runs on every signed request and per-request allocations matter.
	size := len(method) + 1 + len(path) + 1 + len(q) + 1 + 64 + 1 + len(ts) + 1 + len(nonce)
	out := make([]byte, 0, size)
	out = append(out, strings.ToUpper(method)...)
	out = append(out, '\n')
	out = append(out, path...)
	out = append(out, '\n')
	out = append(out, q...)
	out = append(out, '\n')
	out = appendHex(out, bodyHash[:])
	out = append(out, '\n')
	out = append(out, ts...)
	out = append(out, '\n')
	out = append(out, nonce...)
	return out
}

func appendHex(dst, src []byte) []byte {
	const hexLower = "0123456789abcdef"
	for _, b := range src {
		dst = append(dst, hexLower[b>>4], hexLower[b&0x0f])
	}
	return dst
}
