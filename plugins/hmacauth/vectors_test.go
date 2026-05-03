package hmacauth

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

var updateVectors = flag.Bool("update-vectors", false, "regenerate testdata/vectors.json with computed signatures")

// TestVectors_Update fills in expected_signature_hex for any vector
// where the value is "PLACEHOLDER". Run with:
//
//	go test -run TestVectors_Update -update-vectors ./plugins/hmacauth
//
// The regenerated file is committed; CI runs without the flag and
// asserts the committed signatures match what the production code
// produces.
func TestVectors_Update(t *testing.T) {
	if !*updateVectors {
		t.Skip("pass -update-vectors to regenerate")
	}
	const path = "testdata/vectors.json"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var vs []Vector
	if err := json.Unmarshal(raw, &vs); err != nil {
		t.Fatal(err)
	}
	for i := range vs {
		body, err := vs[i].Body()
		if err != nil {
			t.Fatalf("vector %d body: %v", i, err)
		}
		secret, err := vs[i].Secret()
		if err != nil {
			t.Fatalf("vector %d secret: %v", i, err)
		}
		query, err := vs[i].Values()
		if err != nil {
			t.Fatalf("vector %d query: %v", i, err)
		}
		canonical := canonicalRequest(vs[i].Method, vs[i].Path, query, body, vs[i].Timestamp, vs[i].Nonce)
		mac := hmac.New(sha256.New, secret)
		mac.Write(canonical)
		vs[i].ExpectedSignatureHex = hex.EncodeToString(mac.Sum(nil))
	}
	out, err := json.MarshalIndent(vs, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	out = append(out, '\n')
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestVectors_SignRoundtrip asserts every committed vector's
// expected_signature_hex matches what canonicalRequest + HMAC-SHA256
// produces. This pins the bytes for any third-party signer that
// targets compatibility (notably ALP's internal client).
func TestVectors_SignRoundtrip(t *testing.T) {
	vs, err := Vectors()
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) == 0 {
		t.Fatal("no vectors loaded")
	}
	for _, v := range vs {
		v := v
		t.Run(v.Description, func(t *testing.T) {
			if v.ExpectedSignatureHex == "PLACEHOLDER" {
				t.Skip("placeholder — run -update-vectors")
			}
			body, err := v.Body()
			if err != nil {
				t.Fatal(err)
			}
			secret, err := v.Secret()
			if err != nil {
				t.Fatal(err)
			}
			query, err := v.Values()
			if err != nil {
				t.Fatal(err)
			}
			canonical := canonicalRequest(v.Method, v.Path, query, body, v.Timestamp, v.Nonce)
			mac := hmac.New(sha256.New, secret)
			mac.Write(canonical)
			got := hex.EncodeToString(mac.Sum(nil))
			if got != v.ExpectedSignatureHex {
				t.Fatalf("signature mismatch:\n got: %s\nwant: %s\ncanonical: %q", got, v.ExpectedSignatureHex, canonical)
			}
		})
	}
}

// TestVectors_VerifyMiddleware drives the production verification
// middleware against every vector. A request is constructed exactly
// as the vector specifies, signed via Sign, then run through New(...)
// — the middleware MUST accept it.
func TestVectors_VerifyMiddleware(t *testing.T) {
	vs, err := Vectors()
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range vs {
		v := v
		t.Run(v.Description, func(t *testing.T) {
			if v.ExpectedSignatureHex == "PLACEHOLDER" {
				t.Skip("placeholder — run -update-vectors")
			}
			secret, err := v.Secret()
			if err != nil {
				t.Fatal(err)
			}
			body, err := v.Body()
			if err != nil {
				t.Fatal(err)
			}

			client := Client{ClientID: v.ClientID, Secret: secret}
			validator := func(id string) (Client, error) {
				if id == client.ClientID {
					return client, nil
				}
				return Client{}, errUnknownClient
			}
			cfg := DefaultConfig()
			cfg.Validator = validator
			cfg.NonceStore = NewMemoryNonceStore(64)
			cfg.SkewSeconds = 1 << 30 // disable skew check; vectors use fixed timestamps
			cfg.Now = func() time.Time { return time.Unix(v.Timestamp, 0) }

			mw := New(cfg)

			path := v.Path
			if v.Query != "" {
				path += "?" + v.Query
			}
			var bodyReader *bytes.Reader
			if body != nil {
				bodyReader = bytes.NewReader(body)
			}
			var req *http.Request
			if bodyReader == nil {
				req = httptest.NewRequest(v.Method, path, http.NoBody)
			} else {
				req = httptest.NewRequest(v.Method, path, bodyReader)
			}
			req.Header.Set(DefaultClientIDHeader, v.ClientID)
			req.Header.Set(DefaultTimestampHeader, formatTs(v.Timestamp))
			req.Header.Set(DefaultNonceHeader, v.Nonce)
			req.Header.Set(DefaultSignatureHeader, v.ExpectedSignatureHex)

			rec := httptest.NewRecorder()
			handlerCalled := false
			h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				handlerCalled = true
				w.WriteHeader(http.StatusOK)
			}))
			h.ServeHTTP(rec, req)

			if !handlerCalled {
				t.Fatalf("middleware rejected valid signed request: status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestVectors_NoPlaceholders is the gate that fails CI if the
// committed file still has placeholder signatures.
func TestVectors_NoPlaceholders(t *testing.T) {
	vs, err := Vectors()
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range vs {
		if strings.Contains(v.ExpectedSignatureHex, "PLACEHOLDER") {
			t.Fatalf("vector %q has placeholder signature; run -update-vectors", v.Description)
		}
	}
}

func formatTs(ts int64) string {
	return strconv.FormatInt(ts, 10)
}
