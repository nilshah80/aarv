package hmacauth

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

// keep imports honest
var _ = errors.New
