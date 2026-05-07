package hmacauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nilshah80/aarv"
)

// --- accessor nil/empty guards ---

func TestFrom_NilContext(t *testing.T) {
	if c, ok := From(nil); ok || c.ClientID != "" {
		t.Fatalf("From(nil) = (%+v, %v); want zero", c, ok)
	}
}

// TestFrom_NoKeyOnContext covers the c.Get(...) miss branch — context
// is non-nil but the middleware never ran for it, so identityStoreKey
// is unset. Drive via a real handler.
func TestFrom_NoKeyOnContext(t *testing.T) {
	app := aarv.New()
	app.Get("/x", func(c *aarv.Context) error {
		if got, ok := From(c); ok || got.ClientID != "" {
			t.Errorf("From(no-key) = (%+v, %v); want zero", got, ok)
		}
		return c.JSON(http.StatusOK, "ok")
	})
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
}

// TestPrincipalFrom_NoKeyOnContext covers the c.Get miss branch on
// PrincipalFrom — same shape as the From counterpart.
func TestPrincipalFrom_NoKeyOnContext(t *testing.T) {
	app := aarv.New()
	app.Get("/x", func(c *aarv.Context) error {
		if got, ok := PrincipalFrom(c); ok || got.ClientID != "" {
			t.Errorf("PrincipalFrom(no-key) = (%+v, %v); want zero", got, ok)
		}
		return c.JSON(http.StatusOK, "ok")
	})
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestFromContext_NilContext(t *testing.T) {
	var nilCtx context.Context // typed nil — staticcheck SA1012 is not the contract here
	if c, ok := FromContext(nilCtx); ok || c.ClientID != "" {
		t.Fatalf("FromContext(nil) = (%+v, %v); want zero", c, ok)
	}
}

func TestFromContext_NoValue(t *testing.T) {
	if c, ok := FromContext(context.Background()); ok || c.ClientID != "" {
		t.Fatalf("FromContext(empty) = (%+v, %v); want zero", c, ok)
	}
}

func TestPrincipalFrom_NilContext(t *testing.T) {
	if p, ok := PrincipalFrom(nil); ok || p.ClientID != "" {
		t.Fatalf("PrincipalFrom(nil) = (%+v, %v); want zero", p, ok)
	}
}

func TestPrincipalFromContext_NilContext(t *testing.T) {
	var nilCtx context.Context
	if p, ok := PrincipalFromContext(nilCtx); ok || p.ClientID != "" {
		t.Fatalf("PrincipalFromContext(nil) = (%+v, %v); want zero", p, ok)
	}
}

func TestPrincipalFromContext_NoValue(t *testing.T) {
	if p, ok := PrincipalFromContext(context.Background()); ok || p.ClientID != "" {
		t.Fatalf("PrincipalFromContext(empty) = (%+v, %v); want zero", p, ok)
	}
}

// --- codeForStatus exhaustive ---

func TestCodeForStatusAllBranches(t *testing.T) {
	cases := []struct {
		status int
		want   string
	}{
		{http.StatusUnauthorized, "unauthorized"},
		{http.StatusRequestEntityTooLarge, "payload_too_large"},
		{http.StatusForbidden, http.StatusText(http.StatusForbidden)}, // default branch
		{http.StatusBadRequest, http.StatusText(http.StatusBadRequest)},
	}
	for _, c := range cases {
		if got := codeForStatus(c.status); got != c.want {
			t.Errorf("codeForStatus(%d) = %q; want %q", c.status, got, c.want)
		}
	}
}

// --- WithTransportBase option ---

// stubRT lets us assert WithTransportBase actually replaced the base.
type stubRT struct{ called bool }

func (s *stubRT) RoundTrip(*http.Request) (*http.Response, error) {
	s.called = true
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       http.NoBody,
		Header:     http.Header{},
	}, nil
}

func TestWithTransportBaseReplacesBase(t *testing.T) {
	stub := &stubRT{}
	client := Client{ClientID: "c1", Secret: []byte("k1")}
	tp := NewSigningTransport(client, WithTransportBase(stub))

	req := httptest.NewRequest(http.MethodGet, "http://example/", nil)
	resp, err := tp.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	if !stub.called {
		t.Fatal("WithTransportBase did not install the supplied RoundTripper")
	}
}

// --- stdlib-path errStdlib branches ---
//
// Force the stdlib path by inserting a stdlib-only sibling middleware
// with no native pair. errStdlib runs when verification fails on that
// path — covered here by a request that lacks the four required HMAC
// headers.

func stdlibSibling() aarv.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}
}

func TestStdlibPathDefaultErrorShape(t *testing.T) {
	store, _ := newMemNonceStore(t)
	mw := New(Config{
		Validator: StaticClients(map[string]Client{
			"c1": {ClientID: "c1", Secret: []byte("supersecretkey1234567890")},
		}),
		NonceStore: store,
	})

	app := aarv.New()
	app.Use(mw, stdlibSibling())
	app.Post("/x", func(c *aarv.Context) error { return c.JSON(http.StatusOK, "ok") })

	// No HMAC headers → middleware rejects via errStdlib default branch.
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("body"))
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d; want 401 from default errStdlib", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"unauthorized"`) {
		t.Fatalf("expected canonical 'unauthorized' code in body: %s", rec.Body)
	}
}

// TestStdlibPathCustomErrorHandlerReturnsNil covers the
// "errorHandler returned nil → assume the handler wrote the response"
// branch in errStdlib.
func TestStdlibPathCustomErrorHandlerReturnsNil(t *testing.T) {
	store, _ := newMemNonceStore(t)
	mw := New(Config{
		Validator: StaticClients(map[string]Client{
			"c1": {ClientID: "c1", Secret: []byte("supersecretkey1234567890")},
		}),
		NonceStore: store,
		ErrorHandler: func(c *aarv.Context, status int, msg string) error {
			// Write a custom response and return nil — errStdlib must
			// not fall through to its default writer.
			return c.JSON(status, map[string]string{"custom": msg})
		},
	})

	app := aarv.New()
	app.Use(mw, stdlibSibling())
	app.Post("/x", func(c *aarv.Context) error { return c.JSON(http.StatusOK, "ok") })

	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("body"))
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), `"custom"`) {
		t.Fatalf("custom errorHandler body missing: %s", rec.Body)
	}
}

// TestStdlibPathCustomErrorHandlerReturnsErr covers the
// "errorHandler returned non-nil → fall through to default JSON"
// branch in errStdlib.
func TestStdlibPathCustomErrorHandlerReturnsErr(t *testing.T) {
	store, _ := newMemNonceStore(t)
	mw := New(Config{
		Validator: StaticClients(map[string]Client{
			"c1": {ClientID: "c1", Secret: []byte("supersecretkey1234567890")},
		}),
		NonceStore: store,
		ErrorHandler: func(c *aarv.Context, status int, msg string) error {
			return aarv.ErrInternal(nil) // any non-nil to trip the fallback
		},
	})

	app := aarv.New()
	app.Use(mw, stdlibSibling())
	app.Post("/x", func(c *aarv.Context) error { return c.JSON(http.StatusOK, "ok") })

	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("body"))
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d; want 401 from fallback writer", rec.Code)
	}
}

// --- errNative payload-too-large branch ---

// TestNativeErrPayloadTooLarge covers errNative's
// http.StatusRequestEntityTooLarge case — triggered by exceeding the
// configured MaxBodyBytes on a body-read path.
func TestNativeErrPayloadTooLarge(t *testing.T) {
	store, _ := newMemNonceStore(t)
	mw := New(Config{
		Validator: StaticClients(map[string]Client{
			"c1": {ClientID: "c1", Secret: []byte("supersecretkey1234567890")},
		}),
		NonceStore:   store,
		MaxBodyBytes: 1, // reject anything beyond a single byte
	})

	app := aarv.New()
	app.Use(mw)
	app.Post("/x", func(c *aarv.Context) error { return c.JSON(http.StatusOK, "ok") })

	body := strings.Repeat("x", 4096)
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body))
	// The HMAC headers don't need to be valid — the body-size guard
	// runs before signature verification, which is exactly the path
	// we're exercising for the 413 error code branch. We do, however,
	// need at least the four headers to skip the missing-headers
	// short-circuit in some paths.
	req.Header.Set(DefaultClientIDHeader, "c1")
	req.Header.Set(DefaultTimestampHeader, "0")
	req.Header.Set(DefaultNonceHeader, "n")
	req.Header.Set(DefaultSignatureHeader, "deadbeef")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatalf("expected non-2xx; got %d", rec.Code)
	}
}

// TestVectorsLoadable covers the bundled-test-vector loader. Calling
// Vectors() drives the embed.FS read, JSON unmarshal, and sync.Once
// caching paths.
func TestVectorsLoadable(t *testing.T) {
	v, err := Vectors()
	if err != nil {
		t.Fatalf("Vectors: %v", err)
	}
	if len(v) == 0 {
		t.Fatal("expected at least one bundled vector")
	}
	// Second call exercises the cached branch.
	v2, err := Vectors()
	if err != nil {
		t.Fatal(err)
	}
	if len(v2) != len(v) {
		t.Fatalf("cached call mismatch: %d vs %d", len(v), len(v2))
	}
}

// TestSigningTransportFollowsRedirect exercises the
// CheckRedirect / signWithHeaders path that re-signs an outbound
// request after a 3xx redirect, with a replayable body.
func TestSigningTransportFollowsRedirect(t *testing.T) {
	var seenSignatures []string
	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenSignatures = append(seenSignatures, r.Header.Get(DefaultSignatureHeader))
		w.WriteHeader(http.StatusOK)
	}))
	defer final.Close()

	// Redirector returns 307 (preserves method + body) to the final server.
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenSignatures = append(seenSignatures, r.Header.Get(DefaultSignatureHeader))
		http.Redirect(w, r, final.URL, http.StatusTemporaryRedirect)
	}))
	defer redir.Close()

	client := Client{ClientID: "c1", Secret: []byte("supersecretkey1234567890")}
	tp := NewSigningTransport(client)

	httpClient := &http.Client{
		Transport:     tp,
		CheckRedirect: tp.CheckRedirect,
	}
	body := strings.NewReader(`{"hello":"world"}`)
	req, _ := http.NewRequest(http.MethodPost, redir.URL, body)
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if len(seenSignatures) < 2 {
		t.Fatalf("expected at least 2 signatures (initial + post-redirect); got %d", len(seenSignatures))
	}
	// The signature MUST change between hops (different host header).
	if seenSignatures[0] == seenSignatures[1] {
		t.Fatal("signature was not refreshed across the redirect — CheckRedirect did not re-sign")
	}
}

// TestResignOnRedirectFunction is a parallel exercise for the
// stand-alone helper-function form of CheckRedirect (the version
// that doesn't carry a SigningTransport reference).
func TestResignOnRedirectFunction(t *testing.T) {
	var seenSignatures []string
	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenSignatures = append(seenSignatures, r.Header.Get(DefaultSignatureHeader))
		w.WriteHeader(http.StatusOK)
	}))
	defer final.Close()
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenSignatures = append(seenSignatures, r.Header.Get(DefaultSignatureHeader))
		http.Redirect(w, r, final.URL, http.StatusTemporaryRedirect)
	}))
	defer redir.Close()

	client := Client{ClientID: "c1", Secret: []byte("supersecretkey1234567890")}
	tp := NewSigningTransport(client)
	httpClient := &http.Client{
		Transport:     tp,
		CheckRedirect: ResignOnRedirect(client),
	}
	body := strings.NewReader(`{"hello":"world"}`)
	req, _ := http.NewRequest(http.MethodPost, redir.URL, body)
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

// --- helpers ---

func newMemNonceStore(t *testing.T) (NonceStore, func()) {
	t.Helper()
	store, stop := NewMemoryNonceStoreWithJanitor(1024, time.Minute)
	t.Cleanup(func() { _ = stop() })
	return store, func() { _ = stop() }
}
