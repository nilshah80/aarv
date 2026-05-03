package hmacauth

import (
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"sync"
)

//go:embed testdata/vectors.json
var vectorsFS embed.FS

// Vector is a deterministic test vector for the canonical encoding +
// signature path. Every other implementation that wants to interop
// with this plugin (e.g. ALP's internal client) MUST round-trip these
// vectors byte-for-byte.
//
// Field encoding:
//
//   - SecretHex / ExpectedSignatureHex are lowercase hex without
//     leading "0x".
//   - BodyB64 is standard base64 (RFC 4648); empty string means an
//     empty body.
//   - Query is the raw query string (NOT canonicalized). Vectors
//     pin both the input form and the expected canonical form so a
//     bug in the canonicalizer is caught.
type Vector struct {
	Description           string `json:"description"`
	ClientID              string `json:"client_id"`
	SecretHex             string `json:"secret_hex"`
	Method                string `json:"method"`
	Path                  string `json:"path"`
	Query                 string `json:"query"`
	BodyB64               string `json:"body_b64"`
	Timestamp             int64  `json:"timestamp"`
	Nonce                 string `json:"nonce"`
	ExpectedSignatureHex  string `json:"expected_signature_hex"`
}

// Body returns the decoded body bytes for the vector.
func (v Vector) Body() ([]byte, error) {
	if v.BodyB64 == "" {
		return nil, nil
	}
	return base64.StdEncoding.DecodeString(v.BodyB64)
}

// Secret returns the decoded secret bytes for the vector.
func (v Vector) Secret() ([]byte, error) {
	return hex.DecodeString(v.SecretHex)
}

// Values returns the parsed query values for the vector.
func (v Vector) Values() (url.Values, error) {
	if v.Query == "" {
		return url.Values{}, nil
	}
	return url.ParseQuery(v.Query)
}

var (
	vectorsOnce sync.Once
	vectorsList []Vector
	vectorsErr  error
)

// Vectors returns the bundled test vectors. The slice is cached on
// first call; returned values are shared but immutable in practice
// (callers should not mutate fields).
func Vectors() ([]Vector, error) {
	vectorsOnce.Do(func() {
		data, err := vectorsFS.ReadFile("testdata/vectors.json")
		if err != nil {
			vectorsErr = fmt.Errorf("hmacauth: read vectors: %w", err)
			return
		}
		var v []Vector
		if err := json.Unmarshal(data, &v); err != nil {
			vectorsErr = fmt.Errorf("hmacauth: parse vectors: %w", err)
			return
		}
		vectorsList = v
	})
	return vectorsList, vectorsErr
}
