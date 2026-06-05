package hmacauth

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nilshah80/aarv"
)

// --- Finding 1 (High): Sign body resolution ---

func TestSign_ReadsRequestBodyWhenBodyArgIsNil(t *testing.T) {
	body := []byte(`{"hello":"world"}`)
	req, err := http.NewRequest("POST", "http://example.test/x", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if req.GetBody == nil {
		t.Fatalf("test precondition: bytes.Reader should populate GetBody")
	}

	c := Client{ClientID: "x", Secret: []byte("k")}
	if err := Sign(req, c, nil, func() time.Time { return time.Unix(1, 0) }, "n"); err != nil {
		t.Fatal(err)
	}

	// Sign with the body slice directly, on an identical request,
	// must produce the same signature byte-for-byte. That proves
	// Sign read the body via GetBody instead of hashing the empty
	// slice.
	req2, _ := http.NewRequest("POST", "http://example.test/x", bytes.NewReader(body))
	if err := Sign(req2, c, body, func() time.Time { return time.Unix(1, 0) }, "n"); err != nil {
		t.Fatal(err)
	}
	if a, b := req.Header.Get(DefaultSignatureHeader), req2.Header.Get(DefaultSignatureHeader); a != b {
		t.Fatalf("signature mismatch: explicit body %s vs nil body %s", b, a)
	}
}

func TestSign_RejectsUnreplayableRequestBody(t *testing.T) {
	pr, _ := io.Pipe()
	req, err := http.NewRequest("POST", "http://example.test/x", pr)
	if err != nil {
		t.Fatal(err)
	}
	if req.GetBody != nil {
		t.Fatalf("test precondition: pipe-backed body must not have GetBody")
	}
	c := Client{ClientID: "x", Secret: []byte("k")}
	err = Sign(req, c, nil, time.Now, "n")
	if err == nil {
		t.Fatalf("expected error for non-replayable body")
	}
	if !strings.Contains(err.Error(), "GetBody") {
		t.Fatalf("error should mention GetBody, got %v", err)
	}
}

func TestSign_NoBodySignsEmptyHash(t *testing.T) {
	req, _ := http.NewRequest("GET", "http://example.test/x", nil)
	c := Client{ClientID: "x", Secret: []byte("k")}
	if err := Sign(req, c, nil, func() time.Time { return time.Unix(1, 0) }, "n"); err != nil {
		t.Fatal(err)
	}
	// The signature should be deterministic for an empty body.
	req2, _ := http.NewRequest("GET", "http://example.test/x", nil)
	if err := Sign(req2, c, []byte{}, func() time.Time { return time.Unix(1, 0) }, "n"); err != nil {
		t.Fatal(err)
	}
	if a, b := req.Header.Get(DefaultSignatureHeader), req2.Header.Get(DefaultSignatureHeader); a != b {
		t.Fatalf("nil body and empty slice should sign identically: %s vs %s", a, b)
	}
}

func TestNew_DefaultsZeroSkewSeconds(t *testing.T) {
	mw := New(Config{
		Validator: StaticClients(map[string]Client{"x": {Secret: []byte("k")}}),
		Now:       func() time.Time { return time.Unix(1000, 0) },
	})
	if mw.Stdlib == nil {
		t.Fatal("expected middleware with non-nil Stdlib")
	}
}

// --- Finding 2 (Medium): StaticClients empty-ClientID ---

func TestStaticClients_PanicsOnEmptyMapKey(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic on empty map key")
		}
	}()
	_ = StaticClients(map[string]Client{"": {Secret: []byte("k")}})
}

func TestStaticClients_NormalizesEmptyClientIDFromKey(t *testing.T) {
	v := StaticClients(map[string]Client{"alpha": {Secret: []byte("k")}})
	c, err := v("alpha")
	if err != nil {
		t.Fatal(err)
	}
	if c.ClientID != "alpha" {
		t.Fatalf("expected ClientID populated from map key, got %q", c.ClientID)
	}
}

func TestStaticClients_PanicsOnIDMismatch(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic on ID/key mismatch")
		}
	}()
	_ = StaticClients(map[string]Client{"alpha": {ClientID: "beta", Secret: []byte("k")}})
}

func TestStaticClients_AllowsConsistentClientID(t *testing.T) {
	v := StaticClients(map[string]Client{"alpha": {ClientID: "alpha", Secret: []byte("k")}})
	c, err := v("alpha")
	if err != nil {
		t.Fatal(err)
	}
	if c.ClientID != "alpha" {
		t.Fatalf("got %q want alpha", c.ClientID)
	}
}

// TestVerify_RejectsValidatorReturningEmptyClientID guards the case
// where a custom Validator (not StaticClients) returns a Client with
// non-empty Secret but empty ClientID. The verifier must NOT
// authenticate it — otherwise the per-client nonce namespace
// collapses across all such requests.
func TestVerify_RejectsValidatorReturningEmptyClientID(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SkewSeconds = 60
	cfg.Now = stubClock(1735000000)
	cfg.NonceStore = NewMemoryNonceStore(64)
	// Custom validator returns a "configured" client whose ClientID
	// is blank but secret is set. This is the exact failure mode
	// finding 2 calls out.
	cfg.Validator = func(id string) (Client, error) {
		return Client{Secret: []byte("k")}, nil
	}
	mw := New(cfg)

	req := httptest.NewRequest("GET", "/x", http.NoBody)
	signRequest(t, req, nil, Client{ClientID: "presented", Secret: []byte("k")}, 1735000000, "n-empty-id")

	rec := httptest.NewRecorder()
	mw.Stdlib(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler reached even though validator returned empty ClientID")
	})).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d want 401", rec.Code)
	}
}

// --- Finding 4 (Medium): SigningTransport.CheckRedirect honors custom headers ---

func TestSigningTransport_CheckRedirect_HonorsCustomHeaders(t *testing.T) {
	const (
		clientHdr = "X-Auth-Client"
		tsHdr     = "X-Auth-TS"
		nonceHdr  = "X-Auth-Nonce"
		sigHdr    = "X-Auth-Sig"
	)

	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The redirect request must arrive carrying the CUSTOM
		// headers, not the defaults. If ResignOnRedirect was used
		// (or any helper that ignores Transport config), the
		// custom headers would be missing.
		if r.Header.Get(clientHdr) == "" || r.Header.Get(sigHdr) == "" {
			t.Errorf("redirect arrived without custom headers: %v", r.Header)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		// And the DEFAULT headers should NOT be present.
		if r.Header.Get(DefaultClientIDHeader) != "" {
			t.Errorf("redirect leaked default client header")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer final.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, final.URL+r.URL.Path, http.StatusFound)
	}))
	defer redirector.Close()

	client := Client{ClientID: "x", Secret: []byte("k")}
	tr := NewSigningTransport(client, WithTransportHeaders(clientHdr, tsHdr, nonceHdr, sigHdr))
	httpClient := &http.Client{Transport: tr, CheckRedirect: tr.CheckRedirect}

	resp, err := httpClient.Get(redirector.URL + "/x")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got %d", resp.StatusCode)
	}
}

func TestSigningTransport_RoundTripStillSigns(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(DefaultSignatureHeader) == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tr := NewSigningTransport(Client{ClientID: "x", Secret: []byte("k")})
	c := &http.Client{Transport: tr}
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got %d", resp.StatusCode)
	}
}

// --- v0.7.7 review: explicit 2^53 timestamp upper bound ---

func TestVerify_RejectsTimestampsAbove2to53(t *testing.T) {
	cfg, client := newConfig(t, NewMemoryNonceStore(64))
	mw := New(cfg)
	const beyond2to53 = int64(1)<<53 + 1
	req := httptest.NewRequest("GET", "/x", http.NoBody)
	signRequest(t, req, nil, client, beyond2to53, "n-huge")
	rec := httptest.NewRecorder()
	mw.Stdlib(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler reached with timestamp past 2^53")
	})).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d want 401", rec.Code)
	}
}

func TestVerify_AcceptsTimestampAtBoundary(t *testing.T) {
	// Exactly 2^53 must still be accepted (boundary is inclusive
	// upper) — the rejection rule is `> 2^53`. We pin this so a
	// future refactor that changes the comparison direction is
	// caught immediately.
	cfg, client := newConfig(t, NewMemoryNonceStore(64))
	cfg.SkewSeconds = 1 << 30 // disable skew for this case
	cfg.Now = func() time.Time { return time.Unix(int64(1)<<53, 0) }
	mw := New(cfg)

	req := httptest.NewRequest("GET", "/x", http.NoBody)
	signRequest(t, req, nil, client, int64(1)<<53, "n-boundary")
	rec := httptest.NewRecorder()
	called := false
	mw.Stdlib(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)
	if !called {
		t.Fatalf("boundary timestamp 2^53 must be accepted; got %d", rec.Code)
	}
}

// --- v0.7.7 review: secretless Principal accessor ---

func TestPrincipalFrom_ExposesIDAndIdentityNoSecret(t *testing.T) {
	secret := bytes.Repeat([]byte{0xab}, 32)
	type myIdentity struct{ Tier string }
	srcClient := Client{
		ClientID: "alpha",
		Secret:   secret,
		Identity: myIdentity{Tier: "gold"},
	}
	cfg := DefaultConfig()
	cfg.Validator = func(id string) (Client, error) {
		if id == srcClient.ClientID {
			return srcClient, nil
		}
		return Client{}, errors.New("unknown")
	}
	cfg.NonceStore = NewMemoryNonceStore(64)
	cfg.SkewSeconds = 60
	cfg.Now = stubClock(1735000000)
	mw := New(cfg)

	app := aarv.New()
	app.Use(mw)
	var (
		seenPrincipal Principal
		seenOk        bool
	)
	app.Get("/x", func(c *aarv.Context) error {
		seenPrincipal, seenOk = PrincipalFrom(c)
		// Mirror via context.Context path too to confirm both sides
		// store the secretless Principal.
		if _, ok := PrincipalFromContext(c.Context()); !ok {
			return errors.New("PrincipalFromContext returned no value")
		}
		return c.NoContent(http.StatusOK)
	})

	req := httptest.NewRequest("GET", "/x", http.NoBody)
	signRequest(t, req, nil, srcClient, 1735000000, "n-principal")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d body=%s", rec.Code, rec.Body.String())
	}
	if !seenOk {
		t.Fatalf("PrincipalFrom returned ok=false")
	}
	if seenPrincipal.ClientID != "alpha" {
		t.Fatalf("ClientID: got %q want alpha", seenPrincipal.ClientID)
	}
	id, ok := seenPrincipal.Identity.(myIdentity)
	if !ok {
		t.Fatalf("Identity type lost: %T", seenPrincipal.Identity)
	}
	if id.Tier != "gold" {
		t.Fatalf("Identity payload lost: %v", id)
	}
}

// TestPrincipal_StructIsSecretless is the static-shape lock-in: the
// struct exposed to handlers must NOT carry secret bytes. If a
// future change adds Secret/Secrets to Principal, this test fails
// loudly so the regression cannot land silently.
func TestPrincipal_StructIsSecretless(t *testing.T) {
	rt := reflect.TypeOf(Principal{})
	for i := 0; i < rt.NumField(); i++ {
		name := rt.Field(i).Name
		if name == "Secret" || name == "Secrets" {
			t.Fatalf("Principal must not expose %s", name)
		}
	}
}

func TestPrincipalFrom_NilContextSafe(t *testing.T) {
	if _, ok := PrincipalFrom(nil); ok {
		t.Fatalf("PrincipalFrom(nil) should return ok=false")
	}
	// Pass an explicit nil context.Context via a typed nil so the
	// staticcheck SA1012 advisory does not flag the call site;
	// PrincipalFromContext is documented to tolerate this.
	var nilCtx context.Context
	if _, ok := PrincipalFromContext(nilCtx); ok {
		t.Fatalf("PrincipalFromContext(nil) should return ok=false")
	}
}

// keep imports honest
var _ = errors.New
