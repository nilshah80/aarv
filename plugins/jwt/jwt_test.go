package jwt

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nilshah80/aarv"
)

// --- helpers ---

// nonNativeMiddleware forces fallback to the stdlib http.Handler chain.
// Mirrors plugins/apikey/apikey_test.go:nonNativeMiddleware so JWT's stdlib
// path gets exercised by the same trick as the existing auth plugins.
func nonNativeMiddleware() aarv.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}
}

// hmacKey returns 32 / 48 / 64 bytes for HS256/384/512.
func hmacKey(t *testing.T, n int) []byte {
	t.Helper()
	k := make([]byte, n)
	if _, err := rand.Read(k); err != nil {
		t.Fatal(err)
	}
	return k
}

func mustRSA(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func mustECDSA(t *testing.T, c elliptic.Curve) *ecdsa.PrivateKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(c, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func mustEd25519(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

// makeApp builds an aarv app with the JWT middleware installed and a single
// handler at / that returns 200 plus the claims as JSON.
func makeApp(t *testing.T, mw aarv.Middleware) *aarv.App {
	t.Helper()
	app := aarv.New(aarv.WithBanner(false))
	app.Use(mw)
	app.Get("/", func(c *aarv.Context) error {
		claims, ok := From(c)
		if !ok {
			return aarv.ErrInternal(errors.New("no claims"))
		}
		return c.JSON(http.StatusOK, claims)
	})
	app.Get("/skip", func(c *aarv.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"skipped": "yes"})
	})
	return app
}

// makeStdlibApp installs nonNativeMiddleware before the JWT mw to force the
// stdlib http.Handler path.
func makeStdlibApp(t *testing.T, mw aarv.Middleware) *aarv.App {
	t.Helper()
	app := aarv.New(aarv.WithBanner(false))
	app.Use(nonNativeMiddleware(), mw)
	app.Get("/", func(c *aarv.Context) error {
		claims, ok := From(c)
		if !ok {
			return aarv.ErrInternal(errors.New("no claims"))
		}
		return c.JSON(http.StatusOK, claims)
	})
	return app
}

// signWith builds a token by hand-signing a header/payload pair with the
// given alg and key. Used for round-trip tests and for crafting tampered or
// confused tokens.
func signWith(t *testing.T, alg Algorithm, key any, claims map[string]any) string {
	t.Helper()
	tok, err := SignToken(alg, key, claims)
	if err != nil {
		t.Fatalf("SignToken(%s): %v", alg, err)
	}
	return tok
}

// validClaimsNow returns a baseline claim set that passes standard
// validation when checked at time.Now() with zero leeway.
func validClaimsNow() map[string]any {
	now := time.Now().Unix()
	return map[string]any{
		"sub": "alice",
		"iat": float64(now),
		"exp": float64(now + 3600),
	}
}

// --- algorithm round-trips ---

func TestAlgorithms_RoundTrip(t *testing.T) {
	hs := hmacKey(t, 32)
	rsaPriv := mustRSA(t)
	ec256 := mustECDSA(t, elliptic.P256())
	ec384 := mustECDSA(t, elliptic.P384())
	ec521 := mustECDSA(t, elliptic.P521())
	edPub, edPriv := mustEd25519(t)

	cases := []struct {
		name      string
		alg       Algorithm
		signKey   any
		verifyKey any
	}{
		{"HS256", HS256, hs, hs},
		{"HS384", HS384, hmacKey(t, 48), nil}, // verify key set below
		{"HS512", HS512, hmacKey(t, 64), nil},
		{"RS256", RS256, rsaPriv, &rsaPriv.PublicKey},
		{"RS384", RS384, rsaPriv, &rsaPriv.PublicKey},
		{"RS512", RS512, rsaPriv, &rsaPriv.PublicKey},
		{"ES256", ES256, ec256, &ec256.PublicKey},
		{"ES384", ES384, ec384, &ec384.PublicKey},
		{"ES512", ES512, ec521, &ec521.PublicKey},
		{"EdDSA", EdDSA, edPriv, edPub},
	}
	for i, c := range cases {
		if c.verifyKey == nil { // HS384/HS512 reuse signKey
			c.verifyKey = c.signKey
			cases[i] = c
		}
	}
	for _, c := range cases {
		c := c
		t.Run(string(c.alg), func(t *testing.T) {
			tok := signWith(t, c.alg, c.signKey, validClaimsNow())
			cfg := Config{
				Algorithms: []Algorithm{c.alg},
				KeyFunc:    func(_ map[string]any) (any, error) { return c.verifyKey, nil },
			}
			_, claims, err := Parse(tok, cfg)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if claims["sub"] != "alice" {
				t.Fatalf("missing sub claim: %v", claims)
			}
		})
	}
}

// mutateFirstByte returns seg with its first byte changed to a different
// base64url alphabet character. Mutating byte 0 (rather than the last
// byte) avoids landing on base64 padding bits, which can leave the
// decoded data unchanged or produce a CorruptInputError; byte 0 always
// maps to six data bits of the first decoded byte.
func mutateFirstByte(seg string) string {
	if seg == "" {
		return seg
	}
	first := seg[0]
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	for i := 0; i < len(alphabet); i++ {
		if alphabet[i] != first {
			return string(alphabet[i]) + seg[1:]
		}
	}
	return seg // unreachable
}

func TestParse_TamperedPayload(t *testing.T) {
	hs := hmacKey(t, 32)
	tok := signWith(t, HS256, hs, validClaimsNow())
	parts := strings.Split(tok, ".")
	parts[1] = mutateFirstByte(parts[1])
	tok = strings.Join(parts, ".")
	cfg := Config{HMACSecret: hs}
	if _, _, err := Parse(tok, cfg); !errors.Is(err, ErrInvalidSignature) && !errors.Is(err, ErrMalformedToken) {
		t.Fatalf("want ErrInvalidSignature or ErrMalformedToken, got %v", err)
	}
}

func TestParse_TamperedSignature(t *testing.T) {
	hs := hmacKey(t, 32)
	tok := signWith(t, HS256, hs, validClaimsNow())
	parts := strings.Split(tok, ".")
	parts[2] = mutateFirstByte(parts[2])
	tok = strings.Join(parts, ".")
	cfg := Config{HMACSecret: hs}
	if _, _, err := Parse(tok, cfg); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("want ErrInvalidSignature, got %v", err)
	}
}

func TestParse_WrongKeySameFamily(t *testing.T) {
	signer := hmacKey(t, 32)
	verifier := hmacKey(t, 32)
	tok := signWith(t, HS256, signer, validClaimsNow())
	cfg := Config{HMACSecret: verifier}
	if _, _, err := Parse(tok, cfg); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("want ErrInvalidSignature, got %v", err)
	}
}

// --- alg confusion ---

func TestAlgConfusion_HSTokenInRSConfig(t *testing.T) {
	// Attacker signs HS256 token using the RSA public key as the HMAC
	// secret; legitimate verifier holds RSA pub key but allows only RS256.
	rsaPriv := mustRSA(t)
	pubBytes, _ := json.Marshal(&rsaPriv.PublicKey) // any deterministic byte string
	tok := signWith(t, HS256, pubBytes, validClaimsNow())

	cfg := Config{
		Algorithms: []Algorithm{RS256},
		KeyFunc:    func(_ map[string]any) (any, error) { return &rsaPriv.PublicKey, nil },
	}
	_, _, err := Parse(tok, cfg)
	if !errors.Is(err, ErrAlgNotAllowed) {
		t.Fatalf("want ErrAlgNotAllowed, got %v", err)
	}
}

func TestAlgConfusion_HSConfigButRSAKeyReturned(t *testing.T) {
	hs := hmacKey(t, 32)
	tok := signWith(t, HS256, hs, validClaimsNow())
	rsaPriv := mustRSA(t)
	cfg := Config{
		Algorithms: []Algorithm{HS256},
		KeyFunc:    func(_ map[string]any) (any, error) { return &rsaPriv.PublicKey, nil },
	}
	_, _, err := Parse(tok, cfg)
	if !errors.Is(err, ErrKeyTypeMismatch) {
		t.Fatalf("want ErrKeyTypeMismatch, got %v", err)
	}
}

// --- alg=none rejection ---

func TestAlgNone_AlwaysRejected(t *testing.T) {
	// Build a "none" token by hand.
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"alice"}`))
	tok := header + "." + payload + "."

	cfg := Config{HMACSecret: hmacKey(t, 32)}
	_, _, err := Parse(tok, cfg)
	if !errors.Is(err, ErrAlgNone) {
		t.Fatalf("want ErrAlgNone, got %v", err)
	}
}

// --- config validation ---

func TestValidateConfig(t *testing.T) {
	hs := hmacKey(t, 32)
	rsaPriv := mustRSA(t)
	cases := []struct {
		name string
		cfg  Config
		want error
	}{
		{
			"missing key",
			Config{},
			ErrMissingKey,
		},
		{
			"both keys",
			Config{KeyFunc: func(_ map[string]any) (any, error) { return hs, nil }, HMACSecret: hs},
			ErrConflictingKey,
		},
		{
			"keyfunc no algorithms",
			Config{KeyFunc: func(_ map[string]any) (any, error) { return &rsaPriv.PublicKey, nil }},
			ErrNoAlgorithms,
		},
		{
			"hmac-secret defaults HS256",
			Config{HMACSecret: hs},
			nil,
		},
		{
			"hmac-secret with non-HS alg",
			Config{HMACSecret: hs, Algorithms: []Algorithm{RS256}},
			ErrSecretAlgMismatch,
		},
		{
			"unknown alg",
			Config{HMACSecret: hs, Algorithms: []Algorithm{"FOO"}},
			ErrUnknownAlg,
		},
		{
			"none in algorithms",
			Config{HMACSecret: hs, Algorithms: []Algorithm{"none"}},
			ErrAlgNone,
		},
		{
			"invalid lookup",
			Config{HMACSecret: hs, Lookups: []Lookup{{Source: "body", Name: "tok"}}},
			ErrInvalidLookup,
		},
		{
			"weak hmac secret",
			Config{HMACSecret: make([]byte, 16)},
			ErrWeakKey,
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			_, err := validateConfig(c.cfg)
			if c.want == nil {
				if err != nil {
					t.Fatalf("unexpected err: %v", err)
				}
				return
			}
			if !errors.Is(err, c.want) {
				t.Fatalf("want %v, got %v", c.want, err)
			}
		})
	}
}

func TestNew_PanicsOnBadConfig(t *testing.T) {
	cases := []Config{
		{}, // missing key
		{HMACSecret: hmacKey(t, 32), Algorithms: []Algorithm{RS256}}, // alg mismatch
	}
	for i, cfg := range cases {
		i, cfg := i, cfg
		t.Run("case-"+string(rune('a'+i)), func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("expected panic")
				}
			}()
			_ = New(cfg)
		})
	}
}

// --- malformed tokens ---

func TestMalformedTokens(t *testing.T) {
	hs := hmacKey(t, 32)
	cfg := Config{HMACSecret: hs}
	cases := map[string]string{
		"one segment":          "abc",
		"two segments":         "abc.def",
		"four segments":        "a.b.c.d",
		"invalid b64 header":   "@@@.eyJzdWIiOiJ4In0.AAAA",
		"invalid b64 payload":  base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`)) + ".@@@.AAAA",
		"invalid b64 sig":      base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`)) + "." + base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"x"}`)) + ".@@@",
		"invalid json header":  base64.RawURLEncoding.EncodeToString([]byte(`{not json`)) + "." + base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"x"}`)) + ".AAAA",
		"invalid json payload": base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`)) + "." + base64.RawURLEncoding.EncodeToString([]byte(`not json`)) + ".AAAA",
		"missing alg":          base64.RawURLEncoding.EncodeToString([]byte(`{"typ":"JWT"}`)) + "." + base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"x"}`)) + ".AAAA",
		"non-string alg":       base64.RawURLEncoding.EncodeToString([]byte(`{"alg":123,"typ":"JWT"}`)) + "." + base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"x"}`)) + ".AAAA",
	}
	for name, tok := range cases {
		name, tok := name, tok
		t.Run(name, func(t *testing.T) {
			if _, _, err := Parse(tok, cfg); !errors.Is(err, ErrMalformedToken) {
				t.Fatalf("want ErrMalformedToken, got %v", err)
			}
		})
	}
}

func TestMalformedTokens_NullPayload(t *testing.T) {
	// json.Unmarshal("null", &m) leaves the map nil without error, which
	// used to let a null-payload token slip past claim validation when no
	// issuer/audience/custom validator was configured. Now rejected as
	// ErrMalformedToken because JWT claims must be a JSON object.
	hs := hmacKey(t, 32)
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`null`))
	signingInput := header + "." + payload
	spec, _ := lookupAlg(HS256)
	sig, err := spec.sign(hs, []byte(signingInput))
	if err != nil {
		t.Fatal(err)
	}
	tok := signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
	if _, _, err := Parse(tok, Config{HMACSecret: hs}); !errors.Is(err, ErrMalformedToken) {
		t.Fatalf("want ErrMalformedToken for null payload, got %v", err)
	}
}

func TestMalformedTokens_EmptySignatureNonNone(t *testing.T) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"x"}`))
	tok := header + "." + payload + "."
	cfg := Config{HMACSecret: hmacKey(t, 32)}
	_, _, err := Parse(tok, cfg)
	if !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("want ErrInvalidSignature, got %v", err)
	}
}

func TestParse_UnknownHeaderFieldsIgnored(t *testing.T) {
	hs := hmacKey(t, 32)
	// Manually build a token with extra header fields.
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT","kid":"k1","cty":"x"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"alice","exp":` + itoaFloat(time.Now().Unix()+3600) + `}`))
	signingInput := header + "." + payload
	spec, _ := lookupAlg(HS256)
	sig, err := spec.sign(hs, []byte(signingInput))
	if err != nil {
		t.Fatal(err)
	}
	tok := signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
	cfg := Config{HMACSecret: hs}
	if _, _, err := Parse(tok, cfg); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

// itoaFloat formats an int as a JSON number string with no fractional part.
func itoaFloat(n int64) string {
	b, _ := json.Marshal(float64(n))
	return string(b)
}

// --- claims ---

func TestClaims_Expired(t *testing.T) {
	hs := hmacKey(t, 32)
	now := time.Now().Unix()
	claims := map[string]any{
		"sub": "x",
		"exp": float64(now - 60),
	}
	tok := signWith(t, HS256, hs, claims)
	if _, _, err := Parse(tok, Config{HMACSecret: hs}); !errors.Is(err, ErrExpired) {
		t.Fatalf("want ErrExpired, got %v", err)
	}
}

func TestClaims_ExpiredWithinLeeway(t *testing.T) {
	hs := hmacKey(t, 32)
	now := time.Now().Unix()
	claims := map[string]any{
		"sub": "x",
		"exp": float64(now - 5),
	}
	tok := signWith(t, HS256, hs, claims)
	cfg := Config{HMACSecret: hs, Leeway: 30 * time.Second}
	if _, _, err := Parse(tok, cfg); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestClaims_NotYetValid(t *testing.T) {
	hs := hmacKey(t, 32)
	now := time.Now().Unix()
	claims := map[string]any{
		"sub": "x",
		"nbf": float64(now + 600),
	}
	tok := signWith(t, HS256, hs, claims)
	if _, _, err := Parse(tok, Config{HMACSecret: hs}); !errors.Is(err, ErrNotYetValid) {
		t.Fatalf("want ErrNotYetValid, got %v", err)
	}
}

func TestClaims_IssuerMismatch(t *testing.T) {
	hs := hmacKey(t, 32)
	tok := signWith(t, HS256, hs, map[string]any{"sub": "x", "iss": "https://other"})
	cfg := Config{HMACSecret: hs, Issuer: "https://expected"}
	if _, _, err := Parse(tok, cfg); !errors.Is(err, ErrInvalidIssuer) {
		t.Fatalf("want ErrInvalidIssuer, got %v", err)
	}
}

func TestClaims_IssuerMatch(t *testing.T) {
	hs := hmacKey(t, 32)
	tok := signWith(t, HS256, hs, map[string]any{"sub": "x", "iss": "https://expected"})
	cfg := Config{HMACSecret: hs, Issuer: "https://expected"}
	if _, _, err := Parse(tok, cfg); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestClaims_AudString(t *testing.T) {
	hs := hmacKey(t, 32)
	tok := signWith(t, HS256, hs, map[string]any{"sub": "x", "aud": "svc-a"})
	cfg := Config{HMACSecret: hs, Audience: "svc-a"}
	if _, _, err := Parse(tok, cfg); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestClaims_AudArrayMatch(t *testing.T) {
	hs := hmacKey(t, 32)
	tok := signWith(t, HS256, hs, map[string]any{"sub": "x", "aud": []any{"other", "svc-a"}})
	cfg := Config{HMACSecret: hs, Audience: "svc-a"}
	if _, _, err := Parse(tok, cfg); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestClaims_AudArrayWithNonString(t *testing.T) {
	hs := hmacKey(t, 32)
	tok := signWith(t, HS256, hs, map[string]any{"sub": "x", "aud": []any{"svc-a", 42}})
	cfg := Config{HMACSecret: hs, Audience: "svc-a"}
	if _, _, err := Parse(tok, cfg); !errors.Is(err, ErrInvalidAudience) {
		t.Fatalf("want ErrInvalidAudience, got %v", err)
	}
}

func TestClaims_NumericDateStrictness(t *testing.T) {
	hs := hmacKey(t, 32)
	cases := map[string]any{
		"string":   "1700000000",
		"fraction": 1700000000.5,
		"negative": float64(-1),
		"too big":  float64(maxNumericDate + 1),
		"ms scale": float64(time.Now().UnixMilli()),
	}
	for name, exp := range cases {
		name, exp := name, exp
		t.Run(name, func(t *testing.T) {
			tok := signWith(t, HS256, hs, map[string]any{"sub": "x", "exp": exp})
			if _, _, err := Parse(tok, Config{HMACSecret: hs}); !errors.Is(err, ErrInvalidNumericDate) {
				t.Fatalf("want ErrInvalidNumericDate, got %v", err)
			}
		})
	}
}

func TestClaimsValidator_PlainError(t *testing.T) {
	hs := hmacKey(t, 32)
	tok := signWith(t, HS256, hs, validClaimsNow())
	cfg := Config{
		HMACSecret: hs,
		ClaimsValidator: func(c map[string]any) error {
			return errors.New("nope")
		},
	}
	if _, _, err := Parse(tok, cfg); err == nil {
		t.Fatal("expected error")
	}
}

func TestClaimsValidator_AppErrorOnBothPaths(t *testing.T) {
	hs := hmacKey(t, 32)
	tok := signWith(t, HS256, hs, validClaimsNow())
	mwCfg := func() Config {
		return Config{
			HMACSecret: hs,
			ClaimsValidator: func(c map[string]any) error {
				return aarv.ErrForbidden("revoked")
			},
		}
	}
	t.Run("native", func(t *testing.T) {
		app := makeApp(t, New(mwCfg()))
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		app.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("want 403, got %d body=%s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "revoked") {
			t.Fatalf("missing message: %s", rec.Body.String())
		}
	})
	t.Run("stdlib", func(t *testing.T) {
		app := makeStdlibApp(t, New(mwCfg()))
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		app.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("want 403, got %d body=%s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "revoked") {
			t.Fatalf("missing message: %s", rec.Body.String())
		}
	})
}

// --- lookup ---

func TestLookup_HeaderBearer(t *testing.T) {
	hs := hmacKey(t, 32)
	tok := signWith(t, HS256, hs, validClaimsNow())
	app := makeApp(t, New(Config{HMACSecret: hs}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
}

func TestLookup_HeaderCustomScheme(t *testing.T) {
	hs := hmacKey(t, 32)
	tok := signWith(t, HS256, hs, validClaimsNow())
	cfg := Config{
		HMACSecret: hs,
		Lookups: []Lookup{
			{Source: lookupHeader, Name: "X-Auth", Scheme: "Token"},
		},
	}
	app := makeApp(t, New(cfg))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Auth", "token "+tok) // case-insensitive
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestLookup_HeaderNoScheme(t *testing.T) {
	hs := hmacKey(t, 32)
	tok := signWith(t, HS256, hs, validClaimsNow())
	cfg := Config{
		HMACSecret: hs,
		Lookups: []Lookup{
			{Source: lookupHeader, Name: "X-Token"},
		},
	}
	app := makeApp(t, New(cfg))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Token", tok)
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
}

func TestLookup_Query(t *testing.T) {
	hs := hmacKey(t, 32)
	tok := signWith(t, HS256, hs, validClaimsNow())
	cfg := Config{
		HMACSecret: hs,
		Lookups: []Lookup{
			{Source: lookupQuery, Name: "access_token"},
		},
	}
	app := makeApp(t, New(cfg))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/?access_token="+tok, nil)
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
}

func TestLookup_Cookie(t *testing.T) {
	hs := hmacKey(t, 32)
	tok := signWith(t, HS256, hs, validClaimsNow())
	cfg := Config{
		HMACSecret: hs,
		Lookups: []Lookup{
			{Source: lookupCookie, Name: "jwt"},
		},
	}
	app := makeApp(t, New(cfg))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "jwt", Value: tok})
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
}

func TestLookup_Priority(t *testing.T) {
	hs := hmacKey(t, 32)
	good := signWith(t, HS256, hs, validClaimsNow())
	cfg := Config{
		HMACSecret: hs,
		Lookups: []Lookup{
			{Source: lookupHeader, Name: "Authorization", Scheme: "Bearer"},
			{Source: lookupQuery, Name: "access_token"},
		},
	}
	app := makeApp(t, New(cfg))
	rec := httptest.NewRecorder()
	// header has good token; query has garbage. Header wins.
	req := httptest.NewRequest(http.MethodGet, "/?access_token=garbage", nil)
	req.Header.Set("Authorization", "Bearer "+good)
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
}

func TestLookup_MissingToken(t *testing.T) {
	hs := hmacKey(t, 32)
	app := makeApp(t, New(Config{HMACSecret: hs}))
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}

// stubSource implements tokenSource with all-empty results so we can
// exercise extractToken's missing-token path without going through HTTP.
type stubSource struct{}

func (stubSource) header(string) string { return "" }
func (stubSource) query(string) string  { return "" }
func (stubSource) cookie(string) string { return "" }

func TestExtractToken_ErrMissingToken(t *testing.T) {
	// extractToken's contract: when every configured lookup yields empty,
	// return ("", ErrMissingToken). The middleware translates that to a
	// 401 on the wire, but the sentinel must be reachable via errors.Is
	// so callers can branch on it.
	lookups := []Lookup{
		{Source: lookupHeader, Name: "Authorization", Scheme: "Bearer"},
		{Source: lookupQuery, Name: "access_token"},
		{Source: lookupCookie, Name: "jwt"},
	}
	tok, err := extractToken(lookups, stubSource{})
	if tok != "" {
		t.Fatalf("want empty token, got %q", tok)
	}
	if !errors.Is(err, ErrMissingToken) {
		t.Fatalf("want ErrMissingToken, got %v", err)
	}
}

func TestNew_MissingTokenAttachedAsAppErrorCause(t *testing.T) {
	// Native middleware path attaches ErrMissingToken as the *aarv.AppError
	// internal cause so the framework's OnError hook (and any custom
	// error handler) can branch via errors.Is even though the wire
	// response is the configured 401 message.
	hs := hmacKey(t, 32)
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{HMACSecret: hs}))
	app.Get("/", func(c *aarv.Context) error { return nil })

	var captured error
	app.AddHook(aarv.OnError, func(c *aarv.Context) error {
		captured = c.HookError()
		return nil
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
	if captured == nil {
		t.Fatal("OnError hook was not invoked")
	}
	if !errors.Is(captured, ErrMissingToken) {
		t.Fatalf("want errors.Is(captured, ErrMissingToken); got %v", captured)
	}
}

// --- skip paths ---

func TestSkipPaths(t *testing.T) {
	hs := hmacKey(t, 32)
	app := makeApp(t, New(Config{HMACSecret: hs, SkipPaths: []string{"/skip"}}))
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/skip", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	// Non-skipped path still requires auth.
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}

// --- KeyFunc ---

func TestKeyFunc_KidDispatch(t *testing.T) {
	hs1 := hmacKey(t, 32)
	hs2 := hmacKey(t, 32)
	// Sign with hs2 by manually building a token whose header includes kid.
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT","kid":"k2"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"x","exp":` + itoaFloat(time.Now().Unix()+3600) + `}`))
	signingInput := header + "." + payload
	spec, _ := lookupAlg(HS256)
	sig, _ := spec.sign(hs2, []byte(signingInput))
	tok := signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)

	cfg := Config{
		Algorithms: []Algorithm{HS256},
		KeyFunc: func(h map[string]any) (any, error) {
			switch h["kid"] {
			case "k1":
				return hs1, nil
			case "k2":
				return hs2, nil
			}
			return nil, errors.New("unknown kid")
		},
	}
	if _, _, err := Parse(tok, cfg); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestKeyFunc_NilNilFails(t *testing.T) {
	hs := hmacKey(t, 32)
	tok := signWith(t, HS256, hs, validClaimsNow())
	cfg := Config{
		Algorithms: []Algorithm{HS256},
		KeyFunc:    func(_ map[string]any) (any, error) { return nil, nil },
	}
	if _, _, err := Parse(tok, cfg); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("want ErrInvalidSignature on nil key, got %v", err)
	}
}

func TestKeyFunc_AppErrorOnBothPaths(t *testing.T) {
	hs := hmacKey(t, 32)
	tok := signWith(t, HS256, hs, validClaimsNow())
	makeCfg := func() Config {
		return Config{
			Algorithms: []Algorithm{HS256},
			KeyFunc: func(_ map[string]any) (any, error) {
				return nil, aarv.ErrForbidden("rotated")
			},
		}
	}
	t.Run("native", func(t *testing.T) {
		app := makeApp(t, New(makeCfg()))
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		app.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("want 403, got %d", rec.Code)
		}
	})
	t.Run("stdlib", func(t *testing.T) {
		app := makeStdlibApp(t, New(makeCfg()))
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		app.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("want 403, got %d", rec.Code)
		}
	})
}

// --- storage / typed access ---

type typedClaims struct {
	Sub string  `json:"sub"`
	Exp float64 `json:"exp"`
}

func TestGetClaims_Typed(t *testing.T) {
	hs := hmacKey(t, 32)
	tok := signWith(t, HS256, hs, validClaimsNow())
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{HMACSecret: hs}))
	captured := typedClaims{}
	app.Get("/", func(c *aarv.Context) error {
		tc, ok := GetClaims[typedClaims](c)
		if !ok {
			return aarv.ErrInternal(errors.New("no claims"))
		}
		captured = tc
		return c.NoContent(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if captured.Sub != "alice" {
		t.Fatalf("sub: %q", captured.Sub)
	}
}

func TestFromContext_StdlibPath(t *testing.T) {
	hs := hmacKey(t, 32)
	tok := signWith(t, HS256, hs, validClaimsNow())

	mux := http.NewServeMux()
	var captured map[string]any
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		c, _ := FromContext(r.Context())
		captured = c
		w.WriteHeader(http.StatusOK)
	})

	mw := New(Config{HMACSecret: hs})(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	mw.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if captured["sub"] != "alice" {
		t.Fatalf("captured: %v", captured)
	}
}

// --- SignToken ---

func TestSignToken_WeakHMAC(t *testing.T) {
	short := make([]byte, 16) // < 32 for HS256
	if _, err := SignToken(HS256, short, validClaimsNow()); !errors.Is(err, ErrWeakKey) {
		t.Fatalf("want ErrWeakKey, got %v", err)
	}
}

func TestParse_WeakHMACKey(t *testing.T) {
	short := []byte("0123456789abcdef") // 16 bytes, too short for HS256.
	header := b64Encode([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payloadBytes, err := json.Marshal(validClaimsNow())
	if err != nil {
		t.Fatal(err)
	}
	payload := b64Encode(payloadBytes)
	signingInput := append(append(header, '.'), payload...)
	mac := hmac.New(sha256.New, short)
	mac.Write(signingInput)
	token := string(signingInput) + "." + string(b64Encode(mac.Sum(nil)))

	cfg := Config{
		Algorithms: []Algorithm{HS256},
		KeyFunc: func(_ map[string]any) (any, error) {
			return short, nil
		},
	}
	if _, _, err := Parse(token, cfg); !errors.Is(err, ErrWeakKey) {
		t.Fatalf("want ErrWeakKey, got %v", err)
	}
}

func TestSignToken_WrongKeyType(t *testing.T) {
	if _, err := SignToken(RS256, []byte("not rsa"), validClaimsNow()); !errors.Is(err, ErrKeyTypeMismatch) {
		t.Fatalf("want ErrKeyTypeMismatch, got %v", err)
	}
}

func TestSignToken_UnknownAlg(t *testing.T) {
	if _, err := SignToken(Algorithm("FOO"), []byte("k"), validClaimsNow()); !errors.Is(err, ErrUnknownAlg) {
		t.Fatalf("want ErrUnknownAlg, got %v", err)
	}
}

func TestSignToken_NilClaims(t *testing.T) {
	// A nil claims map JSON-marshals to "null", which Parse rejects as
	// ErrMalformedToken. SignToken refuses to emit a token its own
	// package would not accept.
	hs := hmacKey(t, 32)
	if _, err := SignToken(HS256, hs, nil); !errors.Is(err, ErrMalformedToken) {
		t.Fatalf("want ErrMalformedToken on nil claims, got %v", err)
	}
}

// --- RefreshToken ---

func TestRefreshToken_HappyPath(t *testing.T) {
	hs := hmacKey(t, 64) // HS512-grade size, works for HS256 too
	claims := validClaimsNow()
	claims["sub"] = "alice"
	claims["jti"] = "abc"
	tok := signWith(t, HS256, hs, claims)

	cfg := Config{HMACSecret: hs}
	// Use a ttl strictly larger than the original 1h validity so the new
	// exp is observably different even when both timestamps share the
	// same time.Now() second.
	newTok, err := RefreshToken(tok, cfg, hs, 2*time.Hour)
	if err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	_, newClaims, err := Parse(newTok, cfg)
	if err != nil {
		t.Fatalf("Parse new: %v", err)
	}
	if newClaims["sub"] != "alice" {
		t.Fatalf("sub lost")
	}
	if newClaims["jti"] != "abc" {
		t.Fatalf("jti lost")
	}
	if newClaims["exp"].(float64) <= claims["exp"].(float64) {
		t.Fatalf("exp not advanced: old=%v new=%v", claims["exp"], newClaims["exp"])
	}
}

func TestRefreshToken_InvalidTTL(t *testing.T) {
	hs := hmacKey(t, 32)
	tok := signWith(t, HS256, hs, validClaimsNow())
	cfg := Config{HMACSecret: hs}
	if _, err := RefreshToken(tok, cfg, hs, 0); !errors.Is(err, ErrInvalidTTL) {
		t.Fatalf("want ErrInvalidTTL on zero, got %v", err)
	}
	if _, err := RefreshToken(tok, cfg, hs, -time.Second); !errors.Is(err, ErrInvalidTTL) {
		t.Fatalf("want ErrInvalidTTL on negative, got %v", err)
	}
}

func TestRefreshToken_ExpiredInput(t *testing.T) {
	hs := hmacKey(t, 32)
	now := time.Now().Unix()
	tok := signWith(t, HS256, hs, map[string]any{
		"sub": "x",
		"exp": float64(now - 60),
	})
	if _, err := RefreshToken(tok, Config{HMACSecret: hs}, hs, time.Hour); !errors.Is(err, ErrExpired) {
		t.Fatalf("want ErrExpired, got %v", err)
	}
}

func TestRefreshToken_KeyTypeMismatch(t *testing.T) {
	hs := hmacKey(t, 32)
	tok := signWith(t, HS256, hs, validClaimsNow())
	cfg := Config{HMACSecret: hs}
	if _, err := RefreshToken(tok, cfg, "not bytes", time.Hour); !errors.Is(err, ErrKeyTypeMismatch) {
		t.Fatalf("want ErrKeyTypeMismatch, got %v", err)
	}
}

func TestRefreshToken_SubSecondTTLRejected(t *testing.T) {
	// A positive-but-sub-second ttl used to truncate to zero and issue
	// a token whose exp equals iat; now rejected with ErrInvalidTTL.
	hs := hmacKey(t, 32)
	tok := signWith(t, HS256, hs, validClaimsNow())
	cfg := Config{HMACSecret: hs}
	if _, err := RefreshToken(tok, cfg, hs, 500*time.Millisecond); !errors.Is(err, ErrInvalidTTL) {
		t.Fatalf("want ErrInvalidTTL on sub-second ttl, got %v", err)
	}
	if _, err := RefreshToken(tok, cfg, hs, time.Millisecond); !errors.Is(err, ErrInvalidTTL) {
		t.Fatalf("want ErrInvalidTTL on 1ms ttl, got %v", err)
	}
}

func TestRefreshToken_PreservesCustomHeader(t *testing.T) {
	// Refresh must carry kid (and other custom JOSE header fields) across,
	// otherwise JWKS-style rotation breaks: a verifier that selects keys by
	// kid would have nothing to dispatch on after the first refresh.
	hs := hmacKey(t, 32)
	// Build a token with kid + a custom field by hand.
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT","kid":"k1","cty":"x"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"alice","exp":` + itoaFloat(time.Now().Unix()+3600) + `}`))
	signingInput := header + "." + payload
	spec, _ := lookupAlg(HS256)
	sig, err := spec.sign(hs, []byte(signingInput))
	if err != nil {
		t.Fatal(err)
	}
	tok := signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)

	cfg := Config{HMACSecret: hs}
	newTok, err := RefreshToken(tok, cfg, hs, time.Hour)
	if err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	newHeader, _, err := Parse(newTok, cfg)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if newHeader["kid"] != "k1" {
		t.Fatalf("kid lost across refresh: %#v", newHeader)
	}
	if newHeader["cty"] != "x" {
		t.Fatalf("cty lost across refresh: %#v", newHeader)
	}
	if newHeader["alg"] != "HS256" {
		t.Fatalf("alg drift: %#v", newHeader)
	}
	if newHeader["typ"] != "JWT" {
		t.Fatalf("typ lost: %#v", newHeader)
	}
}

func TestRefreshToken_PreservesNbf(t *testing.T) {
	// Doc contract: only iat/exp are rewritten; nbf is preserved verbatim.
	hs := hmacKey(t, 32)
	now := time.Now().Unix()
	originalNbf := float64(now - 30) // already-valid token, nbf in the past
	tok := signWith(t, HS256, hs, map[string]any{
		"sub": "alice",
		"iat": float64(now - 60),
		"nbf": originalNbf,
		"exp": float64(now + 3600),
	})
	cfg := Config{HMACSecret: hs}
	newTok, err := RefreshToken(tok, cfg, hs, time.Hour)
	if err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	_, claims, err := Parse(newTok, cfg)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if claims["nbf"].(float64) != originalNbf {
		t.Fatalf("nbf rewritten: want %v, got %v", originalNbf, claims["nbf"])
	}
}

func TestRefreshToken_WholeSecondTTLDelta(t *testing.T) {
	// Whole-second TTLs produce an exp - iat delta equal to ttl in
	// seconds. This test does NOT distinguish time.Add(ttl) from the
	// older int64(ttl/time.Second) path (both are equivalent for whole
	// seconds); the genuine behavioral split is covered by
	// TestRefreshToken_SubSecondTTLRejected, which asserts that the
	// sub-second range that would have silently truncated to zero in the
	// old code now returns ErrInvalidTTL.
	hs := hmacKey(t, 32)
	tok := signWith(t, HS256, hs, validClaimsNow())
	cfg := Config{HMACSecret: hs}
	newTok, err := RefreshToken(tok, cfg, hs, 90*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_, claims, err := Parse(newTok, cfg)
	if err != nil {
		t.Fatal(err)
	}
	iat := int64(claims["iat"].(float64))
	exp := int64(claims["exp"].(float64))
	if exp-iat != 90 {
		t.Fatalf("want exp-iat=90, got %d", exp-iat)
	}
}

// --- signWithHeader defensive branches ---
//
// signWithHeader is reached from RefreshToken with already-validated inputs;
// the guards below are unreachable on that path. These tests cover them
// directly so a future caller (or refactor that surfaces the helper
// publicly) cannot silently regress them.

func TestSignWithHeader_NilClaimsRejected(t *testing.T) {
	hs := hmacKey(t, 32)
	if _, err := signWithHeader(HS256, hs, map[string]any{"alg": "HS256"}, nil); !errors.Is(err, ErrMalformedToken) {
		t.Fatalf("want ErrMalformedToken, got %v", err)
	}
}

func TestSignWithHeader_UnknownAlgRejected(t *testing.T) {
	hs := hmacKey(t, 32)
	if _, err := signWithHeader(Algorithm("HS999"), hs, nil, map[string]any{"sub": "x"}); !errors.Is(err, ErrUnknownAlg) {
		t.Fatalf("want ErrUnknownAlg, got %v", err)
	}
}

func TestSignWithHeader_TypDefaultedWhenAbsent(t *testing.T) {
	hs := hmacKey(t, 32)
	// Source header without typ — signWithHeader must default it to "JWT".
	tok, err := signWithHeader(HS256, hs, map[string]any{"kid": "k1"}, validClaimsNow())
	if err != nil {
		t.Fatal(err)
	}
	hdr, _, err := Parse(tok, Config{HMACSecret: hs})
	if err != nil {
		t.Fatal(err)
	}
	if hdr["typ"] != "JWT" {
		t.Fatalf("typ default not applied: %#v", hdr)
	}
	if hdr["kid"] != "k1" {
		t.Fatalf("kid lost: %#v", hdr)
	}
}

func TestSignWithHeader_AlgRewrittenFromArg(t *testing.T) {
	// Even if the source header carries a stale "alg", the signed header
	// must reflect the alg argument (header/alg coherence guard).
	hs := hmacKey(t, 32)
	tok, err := signWithHeader(HS256, hs, map[string]any{"alg": "RS256", "typ": "JWT"}, validClaimsNow())
	if err != nil {
		t.Fatal(err)
	}
	hdr, _, err := Parse(tok, Config{HMACSecret: hs, Algorithms: []Algorithm{HS256}})
	if err != nil {
		t.Fatal(err)
	}
	if hdr["alg"] != "HS256" {
		t.Fatalf("alg drift: %#v", hdr)
	}
}

func TestSignWithHeader_HeaderMarshalFailure(t *testing.T) {
	// Inject a value json.Marshal cannot encode (a channel) into the
	// header to exercise the marshal-error return.
	hs := hmacKey(t, 32)
	bad := map[string]any{"x": make(chan int)}
	if _, err := signWithHeader(HS256, hs, bad, map[string]any{"sub": "x"}); err == nil {
		t.Fatal("want marshal error, got nil")
	}
}

func TestSignWithHeader_ClaimsMarshalFailure(t *testing.T) {
	hs := hmacKey(t, 32)
	bad := map[string]any{"x": make(chan int)}
	if _, err := signWithHeader(HS256, hs, map[string]any{"typ": "JWT"}, bad); err == nil {
		t.Fatal("want marshal error, got nil")
	}
}

// --- DefaultConfig ---

func TestDefaultConfig_Shape(t *testing.T) {
	cfg := DefaultConfig()
	if len(cfg.Lookups) != 1 || cfg.Lookups[0].Name != "Authorization" {
		t.Fatalf("lookups: %#v", cfg.Lookups)
	}
	if cfg.ErrorMessage == "" {
		t.Fatal("ErrorMessage default missing")
	}
	if len(cfg.Algorithms) != 0 || cfg.KeyFunc != nil || len(cfg.HMACSecret) != 0 {
		t.Fatalf("DefaultConfig must not provide key material: %#v", cfg)
	}
}

func TestDefaultConfig_NotFunctional(t *testing.T) {
	if _, err := validateConfig(DefaultConfig()); !errors.Is(err, ErrMissingKey) {
		t.Fatalf("want ErrMissingKey from raw DefaultConfig, got %v", err)
	}
}

func TestNew_LookupsDefensivelyCopied(t *testing.T) {
	// validateConfig must snapshot cfg.Lookups so callers cannot mutate
	// auth behavior or race with in-flight requests after middleware
	// construction.
	hs := hmacKey(t, 32)
	tok := signWith(t, HS256, hs, validClaimsNow())
	lookups := []Lookup{{Source: lookupHeader, Name: "Authorization", Scheme: "Bearer"}}
	cfg := Config{HMACSecret: hs, Lookups: lookups}
	app := makeApp(t, New(cfg))

	// Mutate the original slice to a config that, if honored, would
	// require the token in a different header.
	lookups[0] = Lookup{Source: lookupHeader, Name: "X-Other", Scheme: "Bearer"}

	// Original Authorization header must still authenticate.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("middleware honored mutated Lookups: code=%d body=%s", rec.Code, rec.Body.String())
	}
}

// --- error body shape ---

func TestCodeForStatus(t *testing.T) {
	if got := codeForStatus(http.StatusUnauthorized); got != "unauthorized" {
		t.Fatalf("want unauthorized, got %q", got)
	}
	if got := codeForStatus(http.StatusForbidden); got != "forbidden" {
		t.Fatalf("want forbidden, got %q", got)
	}
	// Default branch must fall through to http.StatusText (parity with
	// plugins/apikey and plugins/basicauth) so validator-returned
	// AppError statuses produce the framework-style JSON shape.
	if got := codeForStatus(http.StatusTooManyRequests); got != http.StatusText(http.StatusTooManyRequests) {
		t.Fatalf("default branch must fall through to http.StatusText, got %q", got)
	}
}

func TestStdlibPath_AppErrorRendersStatusText(t *testing.T) {
	// Regression: when ClaimsValidator returned an *aarv.AppError with a
	// non-401/403 status, the stdlib path used to emit a numeric "error"
	// field (e.g. "429") instead of the stdlib reason phrase.
	hs := hmacKey(t, 32)
	tok := signWith(t, HS256, hs, validClaimsNow())
	cfg := Config{
		HMACSecret: hs,
		ClaimsValidator: func(map[string]any) error {
			return aarv.ErrTooManyRequests("slow down")
		},
	}
	app := makeStdlibApp(t, New(cfg))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("want 429, got %d", rec.Code)
	}
	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error != http.StatusText(http.StatusTooManyRequests) {
		t.Fatalf("error field: want %q, got %q", http.StatusText(http.StatusTooManyRequests), body.Error)
	}
	if body.Message != "slow down" {
		t.Fatalf("message: %q", body.Message)
	}
}

// --- From / FromContext nil safety ---

func TestFrom_NilContext(t *testing.T) {
	if _, ok := From(nil); ok {
		t.Fatal("From(nil) must be ok=false")
	}
}

func TestFromContext_NilAndMissing(t *testing.T) {
	var nilCtx context.Context
	if _, ok := FromContext(nilCtx); ok {
		t.Fatal("FromContext(nil) must be ok=false")
	}
	if _, ok := FromContext(context.Background()); ok {
		t.Fatal("FromContext(empty) must be ok=false")
	}
}

// --- stdlib path basic auth flow ---

func TestStdlibPath_Success(t *testing.T) {
	hs := hmacKey(t, 32)
	tok := signWith(t, HS256, hs, validClaimsNow())
	app := makeStdlibApp(t, New(Config{HMACSecret: hs}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestStdlibPath_Failure(t *testing.T) {
	hs := hmacKey(t, 32)
	app := makeStdlibApp(t, New(Config{HMACSecret: hs}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer not-a-token")
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}

// --- coverage gap tests ---

// errReader is a failing io.Reader used to drive the rand-failure branch
// inside ecdsa.Sign without inventing a malformed private key.
type errReader struct{}

func (errReader) Read(_ []byte) (int, error) { return 0, errors.New("rand fail") }

// craftToken assembles a compact-serialized JWT from raw header/payload
// JSON bytes and a caller-supplied signature. It is the test-side dual to
// SignToken, used when a test needs a token whose alg is unrecognized,
// whose payload is non-object, or whose signature is the wrong length —
// shapes SignToken refuses to emit.
func craftToken(t *testing.T, headerJSON, payloadJSON, sig []byte) string {
	t.Helper()
	hb := base64.RawURLEncoding.EncodeToString(headerJSON)
	pb := base64.RawURLEncoding.EncodeToString(payloadJSON)
	sb := base64.RawURLEncoding.EncodeToString(sig)
	return hb + "." + pb + "." + sb
}

// --- alg.go: hashFunc panic ---

func TestHashFunc_PanicOnUnsupportedHash(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on unsupported hash")
		}
	}()
	// crypto.MD5 is a registered crypto.Hash but is not in the JWT
	// algorithm registry, so the default branch must panic.
	_ = hashFunc(0)
}

// --- alg.go: rsaSpec error branches ---

func TestRSASpec_SignWrongKeyType(t *testing.T) {
	spec := algRegistry[RS256]
	if _, err := spec.sign("not rsa", []byte("msg")); !errors.Is(err, ErrKeyTypeMismatch) {
		t.Fatalf("want ErrKeyTypeMismatch, got %v", err)
	}
}

func TestRSASpec_VerifyWrongKeyType(t *testing.T) {
	spec := algRegistry[RS256]
	if err := spec.verify("not rsa", []byte("msg"), []byte("sig")); !errors.Is(err, ErrKeyTypeMismatch) {
		t.Fatalf("want ErrKeyTypeMismatch, got %v", err)
	}
}

func TestRSASpec_VerifySignatureFailure(t *testing.T) {
	// Sign with one RSA key, verify with another. Drives
	// rsa.VerifyPKCS1v15 to return rsa.ErrVerification, which the spec
	// translates to ErrInvalidSignature.
	privA := mustRSA(t)
	privB := mustRSA(t)
	tok := signWith(t, RS256, privA, validClaimsNow())
	cfg := Config{
		Algorithms: []Algorithm{RS256},
		KeyFunc:    func(_ map[string]any) (any, error) { return &privB.PublicKey, nil },
	}
	if _, _, err := Parse(tok, cfg); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("want ErrInvalidSignature, got %v", err)
	}
}

// --- alg.go: ecdsaSpec error branches ---

func TestECDSASpec_SignWrongKeyType(t *testing.T) {
	spec := algRegistry[ES256]
	if _, err := spec.sign("not ecdsa", []byte("msg")); !errors.Is(err, ErrKeyTypeMismatch) {
		t.Fatalf("want ErrKeyTypeMismatch, got %v", err)
	}
}

func TestECDSASpec_SignCurveMismatch(t *testing.T) {
	// ES256 spec expects P256, but we hand it a P384 key.
	spec := algRegistry[ES256]
	priv384 := mustECDSA(t, elliptic.P384())
	if _, err := spec.sign(priv384, []byte("msg")); !errors.Is(err, ErrKeyTypeMismatch) {
		t.Fatalf("want ErrKeyTypeMismatch, got %v", err)
	}
}

func TestECDSASpec_SignRandFailure(t *testing.T) {
	// ecdsa.Sign reads from ecdsaRand for nonce generation. When the
	// reader fails, ecdsa.Sign returns the underlying error, which the
	// spec passes through unchanged (it is not ErrKeyTypeMismatch and
	// not ErrInvalidSignature — those would be misleading).
	orig := ecdsaRand
	ecdsaRand = errReader{}
	t.Cleanup(func() { ecdsaRand = orig })

	priv := mustECDSA(t, elliptic.P256())
	if _, err := SignToken(ES256, priv, validClaimsNow()); err == nil {
		t.Fatal("want error from rand failure, got nil")
	}
}

func TestECDSASpec_VerifyWrongKeyType(t *testing.T) {
	spec := algRegistry[ES256]
	if err := spec.verify("not ecdsa", []byte("msg"), make([]byte, 64)); !errors.Is(err, ErrKeyTypeMismatch) {
		t.Fatalf("want ErrKeyTypeMismatch, got %v", err)
	}
}

func TestECDSASpec_VerifyCurveMismatch(t *testing.T) {
	// ES256 spec expects P256; verify with a P384 public key fails the
	// curve check before any signature math runs.
	spec := algRegistry[ES256]
	priv384 := mustECDSA(t, elliptic.P384())
	if err := spec.verify(&priv384.PublicKey, []byte("msg"), make([]byte, 64)); !errors.Is(err, ErrKeyTypeMismatch) {
		t.Fatalf("want ErrKeyTypeMismatch, got %v", err)
	}
}

func TestECDSASpec_VerifySigLengthWrong(t *testing.T) {
	// ES256 expects 64-byte R||S signatures. A 32-byte segment (decoded
	// from the token) is rejected with ErrInvalidSignature before
	// ecdsa.Verify is invoked.
	priv := mustECDSA(t, elliptic.P256())
	tok := craftToken(t,
		[]byte(`{"alg":"ES256","typ":"JWT"}`),
		[]byte(`{"sub":"x"}`),
		make([]byte, 32),
	)
	cfg := Config{
		Algorithms: []Algorithm{ES256},
		KeyFunc:    func(_ map[string]any) (any, error) { return &priv.PublicKey, nil },
	}
	if _, _, err := Parse(tok, cfg); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("want ErrInvalidSignature, got %v", err)
	}
}

func TestECDSASpec_VerifyFalse(t *testing.T) {
	// Same curve, different key: ecdsa.Verify returns false, spec
	// translates to ErrInvalidSignature.
	privA := mustECDSA(t, elliptic.P256())
	privB := mustECDSA(t, elliptic.P256())
	tok := signWith(t, ES256, privA, validClaimsNow())
	cfg := Config{
		Algorithms: []Algorithm{ES256},
		KeyFunc:    func(_ map[string]any) (any, error) { return &privB.PublicKey, nil },
	}
	if _, _, err := Parse(tok, cfg); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("want ErrInvalidSignature, got %v", err)
	}
}

// --- alg.go: ed25519Spec error branches ---

func TestEd25519Spec_SignWrongKeyType(t *testing.T) {
	spec := algRegistry[EdDSA]
	if _, err := spec.sign("not ed25519", []byte("msg")); !errors.Is(err, ErrKeyTypeMismatch) {
		t.Fatalf("want ErrKeyTypeMismatch, got %v", err)
	}
}

func TestEd25519Spec_SignWrongKeySize(t *testing.T) {
	// ed25519.PrivateKey is a []byte alias, so a 16-byte slice still
	// passes the type assertion. The size check that follows must catch
	// it and return ErrKeyTypeMismatch.
	spec := algRegistry[EdDSA]
	short := ed25519.PrivateKey(make([]byte, 16))
	if _, err := spec.sign(short, []byte("msg")); !errors.Is(err, ErrKeyTypeMismatch) {
		t.Fatalf("want ErrKeyTypeMismatch, got %v", err)
	}
}

func TestEd25519Spec_VerifyWrongKeyType(t *testing.T) {
	spec := algRegistry[EdDSA]
	if err := spec.verify("not ed25519", []byte("msg"), []byte("sig")); !errors.Is(err, ErrKeyTypeMismatch) {
		t.Fatalf("want ErrKeyTypeMismatch, got %v", err)
	}
}

func TestEd25519Spec_VerifyWrongKeySize(t *testing.T) {
	spec := algRegistry[EdDSA]
	short := ed25519.PublicKey(make([]byte, 16))
	if err := spec.verify(short, []byte("msg"), []byte("sig")); !errors.Is(err, ErrKeyTypeMismatch) {
		t.Fatalf("want ErrKeyTypeMismatch, got %v", err)
	}
}

func TestEd25519Spec_VerifyFalse(t *testing.T) {
	pubA, _ := mustEd25519(t)
	_, privB := mustEd25519(t)
	tok := signWith(t, EdDSA, privB, validClaimsNow())
	cfg := Config{
		Algorithms: []Algorithm{EdDSA},
		KeyFunc:    func(_ map[string]any) (any, error) { return pubA, nil },
	}
	if _, _, err := Parse(tok, cfg); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("want ErrInvalidSignature, got %v", err)
	}
}

// --- claims.go: validateStandardClaims branches ---

func TestClaims_NbfInvalidNumericDate(t *testing.T) {
	hs := hmacKey(t, 32)
	tok := signWith(t, HS256, hs, map[string]any{"sub": "x", "nbf": "not a number"})
	if _, _, err := Parse(tok, Config{HMACSecret: hs}); !errors.Is(err, ErrInvalidNumericDate) {
		t.Fatalf("want ErrInvalidNumericDate, got %v", err)
	}
}

func TestClaims_IatInvalidNumericDate(t *testing.T) {
	hs := hmacKey(t, 32)
	tok := signWith(t, HS256, hs, map[string]any{"sub": "x", "iat": "not a number"})
	if _, _, err := Parse(tok, Config{HMACSecret: hs}); !errors.Is(err, ErrInvalidNumericDate) {
		t.Fatalf("want ErrInvalidNumericDate, got %v", err)
	}
}

func TestClaims_IssuerMissing(t *testing.T) {
	hs := hmacKey(t, 32)
	tok := signWith(t, HS256, hs, map[string]any{"sub": "x"})
	cfg := Config{HMACSecret: hs, Issuer: "https://expected"}
	if _, _, err := Parse(tok, cfg); !errors.Is(err, ErrInvalidIssuer) {
		t.Fatalf("want ErrInvalidIssuer, got %v", err)
	}
}

func TestClaims_IssuerNonString(t *testing.T) {
	hs := hmacKey(t, 32)
	tok := signWith(t, HS256, hs, map[string]any{"sub": "x", "iss": 42})
	cfg := Config{HMACSecret: hs, Issuer: "https://expected"}
	if _, _, err := Parse(tok, cfg); !errors.Is(err, ErrInvalidIssuer) {
		t.Fatalf("want ErrInvalidIssuer, got %v", err)
	}
}

func TestClaims_AudienceMissing(t *testing.T) {
	hs := hmacKey(t, 32)
	tok := signWith(t, HS256, hs, map[string]any{"sub": "x"})
	cfg := Config{HMACSecret: hs, Audience: "svc-a"}
	if _, _, err := Parse(tok, cfg); !errors.Is(err, ErrInvalidAudience) {
		t.Fatalf("want ErrInvalidAudience, got %v", err)
	}
}

func TestClaims_AudienceStringMismatch(t *testing.T) {
	hs := hmacKey(t, 32)
	tok := signWith(t, HS256, hs, map[string]any{"sub": "x", "aud": "svc-b"})
	cfg := Config{HMACSecret: hs, Audience: "svc-a"}
	if _, _, err := Parse(tok, cfg); !errors.Is(err, ErrInvalidAudience) {
		t.Fatalf("want ErrInvalidAudience, got %v", err)
	}
}

func TestClaims_AudienceArrayNoMatch(t *testing.T) {
	hs := hmacKey(t, 32)
	tok := signWith(t, HS256, hs, map[string]any{"sub": "x", "aud": []any{"svc-b", "svc-c"}})
	cfg := Config{HMACSecret: hs, Audience: "svc-a"}
	if _, _, err := Parse(tok, cfg); !errors.Is(err, ErrInvalidAudience) {
		t.Fatalf("want ErrInvalidAudience, got %v", err)
	}
}

func TestClaims_AudienceUnsupportedType(t *testing.T) {
	// aud as a JSON number falls into the default branch of the type
	// switch and is rejected with ErrInvalidAudience.
	hs := hmacKey(t, 32)
	tok := signWith(t, HS256, hs, map[string]any{"sub": "x", "aud": 42})
	cfg := Config{HMACSecret: hs, Audience: "svc-a"}
	if _, _, err := Parse(tok, cfg); !errors.Is(err, ErrInvalidAudience) {
		t.Fatalf("want ErrInvalidAudience, got %v", err)
	}
}

// --- jwt.go: Parse / RefreshToken bad-config paths ---

func TestParse_BadConfig(t *testing.T) {
	// Parse must surface configuration errors as typed sentinels rather
	// than panicking (parity with RefreshToken, contrast with New).
	if _, _, err := Parse("a.b.c", Config{}); !errors.Is(err, ErrMissingKey) {
		t.Fatalf("want ErrMissingKey, got %v", err)
	}
}

func TestRefreshToken_BadConfig(t *testing.T) {
	if _, err := RefreshToken("a.b.c", Config{}, nil, time.Hour); !errors.Is(err, ErrMissingKey) {
		t.Fatalf("want ErrMissingKey, got %v", err)
	}
}

// --- jwt.go: SignToken claims-marshal failure ---

func TestSignToken_UnmarshalableClaims(t *testing.T) {
	hs := hmacKey(t, 32)
	bad := map[string]any{"ch": make(chan int)}
	if _, err := SignToken(HS256, hs, bad); err == nil {
		t.Fatal("want error from json.Marshal failure, got nil")
	}
}

// --- jwt.go: parseAndVerify unknown alg path ---

func TestParseAndVerify_UnknownAlg(t *testing.T) {
	// Token's alg header is recognized JSON but not in the registry.
	// parseAndVerify must reject with ErrUnknownAlg before checking the
	// caller's allow-list (which would also reject, but with a different
	// error). The validateConfig allow-list path is exercised separately
	// in TestValidateConfig.
	hs := hmacKey(t, 32)
	tok := craftToken(t,
		[]byte(`{"alg":"PS256","typ":"JWT"}`),
		[]byte(`{"sub":"x"}`),
		[]byte("sig"),
	)
	if _, _, err := Parse(tok, Config{HMACSecret: hs}); !errors.Is(err, ErrUnknownAlg) {
		t.Fatalf("want ErrUnknownAlg, got %v", err)
	}
}

// --- jwt.go: splitToken empty header / payload ---

func TestSplitToken_EmptyHeader(t *testing.T) {
	if _, _, err := Parse(".eyJzdWIiOiJ4In0.AAAA", Config{HMACSecret: hmacKey(t, 32)}); !errors.Is(err, ErrMalformedToken) {
		t.Fatalf("want ErrMalformedToken, got %v", err)
	}
}

func TestSplitToken_EmptyPayload(t *testing.T) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	tok := header + ".." + "AAAA"
	if _, _, err := Parse(tok, Config{HMACSecret: hmacKey(t, 32)}); !errors.Is(err, ErrMalformedToken) {
		t.Fatalf("want ErrMalformedToken, got %v", err)
	}
}

// --- jwt.go: From / GetClaims edge cases ---

func TestFrom_NonMapValue(t *testing.T) {
	// Set the identity store key to a non-map; From must report ok=false
	// rather than returning a typed-but-wrong value.
	app := aarv.New(aarv.WithBanner(false))
	app.Get("/", func(c *aarv.Context) error {
		c.Set(identityStoreKey, "not a map")
		if _, ok := From(c); ok {
			return errors.New("From returned ok=true for non-map value")
		}
		return c.NoContent(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestGetClaims_AbsentClaims(t *testing.T) {
	// No middleware, no claims in context; GetClaims must report ok=false
	// without panicking on the missing key.
	app := aarv.New(aarv.WithBanner(false))
	app.Get("/", func(c *aarv.Context) error {
		if _, ok := GetClaims[typedClaims](c); ok {
			return errors.New("GetClaims returned ok=true with no claims set")
		}
		return c.NoContent(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestGetClaims_MarshalFailure(t *testing.T) {
	// Plant a non-marshalable value into the claims map. From returns
	// the map; GetClaims's json.Marshal step then fails and reports
	// ok=false rather than panicking.
	app := aarv.New(aarv.WithBanner(false))
	app.Get("/", func(c *aarv.Context) error {
		c.Set(identityStoreKey, map[string]any{"ch": make(chan int)})
		if _, ok := GetClaims[typedClaims](c); ok {
			return errors.New("GetClaims returned ok=true on marshal failure")
		}
		return c.NoContent(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestGetClaims_UnmarshalFailure(t *testing.T) {
	// T = int can't accept a JSON object. GetClaims must report ok=false.
	app := aarv.New(aarv.WithBanner(false))
	app.Get("/", func(c *aarv.Context) error {
		c.Set(identityStoreKey, map[string]any{"sub": "alice"})
		if _, ok := GetClaims[int](c); ok {
			return errors.New("GetClaims returned ok=true on unmarshal failure")
		}
		return c.NoContent(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// --- jwt.go: native path non-AppError parse failure ---

func TestNative_PlainParseError(t *testing.T) {
	// A non-AppError parseAndVerify failure on the native path falls
	// through to the configured 401 message — the catch-all branch
	// after errors.As. Tampered signature is the cheapest way to drive
	// it through HTTP rather than via direct Parse.
	hs := hmacKey(t, 32)
	tok := signWith(t, HS256, hs, validClaimsNow())
	parts := strings.Split(tok, ".")
	parts[2] = mutateFirstByte(parts[2])
	tampered := strings.Join(parts, ".")

	app := makeApp(t, New(Config{HMACSecret: hs, ErrorMessage: "bad token"}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+tampered)
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "bad token") {
		t.Fatalf("want configured ErrorMessage in body, got %s", rec.Body.String())
	}
}

// --- jwt.go: stdlib path coverage ---

func TestStdlibPath_SkipPath(t *testing.T) {
	hs := hmacKey(t, 32)
	app := aarv.New(aarv.WithBanner(false))
	app.Use(nonNativeMiddleware(), New(Config{HMACSecret: hs, SkipPaths: []string{"/health"}}))
	app.Get("/health", func(c *aarv.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"ok": "yes"})
	})
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestStdlibPath_MissingToken(t *testing.T) {
	hs := hmacKey(t, 32)
	app := makeStdlibApp(t, New(Config{HMACSecret: hs}))
	rec := httptest.NewRecorder()
	// No Authorization header at all → stdlib lookupErr branch.
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}

func TestStdlibPath_QueryLookup(t *testing.T) {
	hs := hmacKey(t, 32)
	tok := signWith(t, HS256, hs, validClaimsNow())
	cfg := Config{
		HMACSecret: hs,
		Lookups: []Lookup{
			{Source: lookupQuery, Name: "access_token"},
		},
	}
	app := makeStdlibApp(t, New(cfg))
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?access_token="+tok, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestStdlibPath_CookieLookup(t *testing.T) {
	hs := hmacKey(t, 32)
	tok := signWith(t, HS256, hs, validClaimsNow())
	cfg := Config{
		HMACSecret: hs,
		Lookups: []Lookup{
			{Source: lookupCookie, Name: "jwt"},
		},
	}
	app := makeStdlibApp(t, New(cfg))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "jwt", Value: tok})
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestStdlibPath_CookieMissingFallsThrough(t *testing.T) {
	// Cookie absent: requestSource.cookie hits its err branch and
	// returns "", causing extractToken to report ErrMissingToken.
	hs := hmacKey(t, 32)
	cfg := Config{
		HMACSecret: hs,
		Lookups: []Lookup{
			{Source: lookupCookie, Name: "jwt"},
		},
	}
	app := makeStdlibApp(t, New(cfg))
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}

func TestNativePath_CookieMissingFallsThrough(t *testing.T) {
	// Same as above but on the native path so contextSource.cookie's
	// err branch is exercised.
	hs := hmacKey(t, 32)
	cfg := Config{
		HMACSecret: hs,
		Lookups: []Lookup{
			{Source: lookupCookie, Name: "jwt"},
		},
	}
	app := makeApp(t, New(cfg))
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}

// --- lookup.go: readHeader edge cases ---

func TestReadHeader_EdgeCases(t *testing.T) {
	cases := []struct {
		name, value, scheme, want string
	}{
		{"too short for scheme", "Be", "Bearer", ""},
		{"scheme prefix mismatch", "Basic abc", "Bearer", ""},
		{"no separator after scheme", "Bearerabc", "Bearer", ""},
		{"double leading space tolerated", "Bearer  abc", "Bearer", "abc"},
		{"empty after scheme", "Bearer", "Bearer", ""},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			got := readHeader(c.value, c.scheme)
			if got != c.want {
				t.Fatalf("readHeader(%q, %q) = %q, want %q", c.value, c.scheme, got, c.want)
			}
		})
	}
}
