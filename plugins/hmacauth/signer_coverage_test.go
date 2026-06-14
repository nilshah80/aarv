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

// TestSign_RejectsNilRequest covers the Sign(req == nil) guard.
func TestSign_RejectsNilRequest(t *testing.T) {
	err := Sign(nil, Client{ClientID: "x", Secret: []byte("k")}, nil, nil, "n")
	if err == nil || !strings.Contains(err.Error(), "non-nil request") {
		t.Fatalf("expected non-nil-request error, got %v", err)
	}
}

// TestSign_DefaultsNowWhenNil covers the `now == nil` default branch.
// We pass nil for `now` and a deterministic nonce; Sign must use
// time.Now and produce a valid signature header set.
func TestSign_DefaultsNowWhenNil(t *testing.T) {
	req := httptest.NewRequest("GET", "/x", nil)
	err := Sign(req, Client{ClientID: "x", Secret: []byte("k")}, nil, nil, "deterministic")
	if err != nil {
		t.Fatalf("Sign with nil now must succeed (defaults to time.Now): %v", err)
	}
	if req.Header.Get(DefaultTimestampHeader) == "" {
		t.Fatal("expected timestamp header to be set")
	}
}

// TestSign_GeneratesRandomNonceWhenEmpty covers the `nonce == ""`
// → randomNonce() branch and proves the nonce is set on the request.
func TestSign_GeneratesRandomNonceWhenEmpty(t *testing.T) {
	req := httptest.NewRequest("GET", "/x", nil)
	err := Sign(req, Client{ClientID: "x", Secret: []byte("k")}, nil,
		func() time.Time { return time.Unix(1700000000, 0) }, "")
	if err != nil {
		t.Fatalf("Sign with empty nonce must succeed: %v", err)
	}
	nonce := req.Header.Get(DefaultNonceHeader)
	if len(nonce) != 32 {
		t.Fatalf("expected 32-char hex nonce, got %q (len=%d)", nonce, len(nonce))
	}
}

func TestSignWithNonce_PropagatesNonceError(t *testing.T) {
	sentinel := errors.New("synthetic nonce failure")
	req := httptest.NewRequest("GET", "/x", nil)
	err := signWithNonce(req, Client{ClientID: "x", Secret: []byte("k")}, nil,
		func() time.Time { return time.Unix(1700000000, 0) }, "",
		func() (string, error) { return "", sentinel })
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected synthetic nonce error, got %v", err)
	}
}

func TestRandomNonceFrom_PropagatesReaderError(t *testing.T) {
	sentinel := errors.New("synthetic rand failure")
	_, err := randomNonceFrom(errOnReadCloser{err: sentinel})
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected synthetic rand error, got %v", err)
	}
}

// errReadCloser wraps a bytes.Reader and returns a fixed error from Close,
// or a fixed error from Read after first call. Used to simulate GetBody
// returning a reader whose Read fails partway.
type errOnReadCloser struct{ err error }

func (e errOnReadCloser) Read(_ []byte) (int, error) { return 0, e.err }
func (errOnReadCloser) Close() error                 { return nil }

// TestSign_ResolveBodyReturnsGetBodyError covers resolveSignBody's
// `rc, err := req.GetBody(); if err != nil` branch.
func TestSign_ResolveBodyReturnsGetBodyError(t *testing.T) {
	req := httptest.NewRequest("POST", "/x", bytes.NewReader([]byte("hello")))
	sentinel := errors.New("synthetic GetBody failure")
	req.GetBody = func() (io.ReadCloser, error) {
		return nil, sentinel
	}
	err := Sign(req, Client{ClientID: "x", Secret: []byte("k")}, nil, nil, "n")
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected synthetic GetBody error, got %v", err)
	}
}

// TestSign_ResolveBodyReturnsReadAllError covers the case where
// GetBody returns a reader whose Read errors out (io.ReadAll surfaces it).
func TestSign_ResolveBodyReturnsReadAllError(t *testing.T) {
	req := httptest.NewRequest("POST", "/x", bytes.NewReader([]byte("hi")))
	sentinel := errors.New("synthetic Read failure")
	req.GetBody = func() (io.ReadCloser, error) {
		return errOnReadCloser{err: sentinel}, nil
	}
	err := Sign(req, Client{ClientID: "x", Secret: []byte("k")}, nil, nil, "n")
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected synthetic Read error, got %v", err)
	}
}

// --- TransportOption tests ---

// stubRoundTripper is a local http.RoundTripper used to capture the
// outgoing signed request for assertion. The stdlib does not ship a
// public RoundTripperFunc adapter.
type stubRoundTripper func(*http.Request) (*http.Response, error)

func (f stubRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestTransportOptions_AllSettersApply covers WithTransportNow,
// WithTransportNonce, and WithTransportBase setter branches.
func TestTransportOptions_AllSettersApply(t *testing.T) {
	stubRT := stubRoundTripper(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: http.NoBody, Header: http.Header{}, Request: r}, nil
	})
	tt := NewSigningTransport(
		Client{ClientID: "x", Secret: []byte("k")},
		WithTransportNow(func() time.Time { return time.Unix(1700000000, 0) }),
		WithTransportNonce(func() (string, error) { return "fixed-nonce", nil }),
		WithTransportBase(stubRT),
	)
	if tt == nil {
		t.Fatal("NewSigningTransport returned nil")
	}
	req := httptest.NewRequest("GET", "http://example.invalid/x", nil)
	resp, err := tt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.Request.Header.Get(DefaultTimestampHeader) != "1700000000" {
		t.Fatalf("expected timestamp 1700000000, got %q", resp.Request.Header.Get(DefaultTimestampHeader))
	}
	if resp.Request.Header.Get(DefaultNonceHeader) != "fixed-nonce" {
		t.Fatalf("expected nonce fixed-nonce, got %q", resp.Request.Header.Get(DefaultNonceHeader))
	}
}

// TestCheckRedirect_DefaultsNowWhenNil covers CheckRedirect's
// `if s.t.now == nil { s.t.now = time.Now }` branch.
func TestCheckRedirect_DefaultsNowWhenNil(t *testing.T) {
	tt := NewSigningTransport(
		Client{ClientID: "x", Secret: []byte("k")},
		WithTransportNonce(func() (string, error) { return "n", nil }),
	)
	tt.t.now = nil
	req := httptest.NewRequest("GET", "http://example.invalid/x", nil)
	err := tt.CheckRedirect(req, nil)
	if err != nil {
		t.Fatalf("CheckRedirect: %v", err)
	}
	if req.Header.Get(DefaultTimestampHeader) == "" {
		t.Fatal("expected timestamp header to be set after CheckRedirect default-now")
	}
}

func TestTransportRoundTrip_PropagatesSignError(t *testing.T) {
	called := false
	tr := &transport{
		client: Client{},
		base: stubRoundTripper(func(*http.Request) (*http.Response, error) {
			called = true
			return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
		}),
		now:             func() time.Time { return time.Unix(1700000000, 0) },
		nonce:           func() (string, error) { return "n", nil },
		clientIDHeader:  DefaultClientIDHeader,
		timestampHeader: DefaultTimestampHeader,
		nonceHeader:     DefaultNonceHeader,
		signatureHeader: DefaultSignatureHeader,
	}

	_, err := tr.RoundTrip(httptest.NewRequest("GET", "http://example.invalid/x", nil))
	if err == nil || !strings.Contains(err.Error(), "ClientID") {
		t.Fatalf("expected ClientID signing error, got %v", err)
	}
	if called {
		t.Fatal("base transport must not run after signing fails")
	}
}

func TestSignWithHeaders_RotationSecretAndNilNow(t *testing.T) {
	req := httptest.NewRequest("GET", "http://example.invalid/x", nil)
	err := signWithHeaders(req,
		Client{ClientID: "x", Secrets: [][]byte{nil, []byte("k")}},
		nil, nil, "n", "X-Client", "X-Ts", "X-Nonce", "X-Sig")
	if err != nil {
		t.Fatalf("signWithHeaders: %v", err)
	}
	if req.Header.Get("X-Client") != "x" || req.Header.Get("X-Sig") == "" {
		t.Fatalf("custom headers not populated: %v", req.Header)
	}
}

func TestSignWithHeaders_RejectsMissingSecret(t *testing.T) {
	req := httptest.NewRequest("GET", "http://example.invalid/x", nil)
	err := signWithHeaders(req,
		Client{ClientID: "x"}, nil,
		func() time.Time { return time.Unix(1700000000, 0) },
		"n", "X-Client", "X-Ts", "X-Nonce", "X-Sig")
	if err == nil || !strings.Contains(err.Error(), "secret") {
		t.Fatalf("expected missing secret error, got %v", err)
	}
}

func TestSignWithHeaders_PropagatesBodyError(t *testing.T) {
	sentinel := errors.New("synthetic GetBody failure")
	req := httptest.NewRequest("POST", "http://example.invalid/x", bytes.NewReader([]byte("hi")))
	req.GetBody = func() (io.ReadCloser, error) { return nil, sentinel }
	err := signWithHeaders(req,
		Client{ClientID: "x", Secret: []byte("k")}, nil,
		func() time.Time { return time.Unix(1700000000, 0) },
		"n", "X-Client", "X-Ts", "X-Nonce", "X-Sig")
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected synthetic body error, got %v", err)
	}
}

// TestCheckRedirect_PropagatesNonceError covers `nonce, err := s.t.nonce()
// if err != nil { return err }`.
func TestCheckRedirect_PropagatesNonceError(t *testing.T) {
	sentinel := errors.New("synthetic nonce failure")
	tt := NewSigningTransport(
		Client{ClientID: "x", Secret: []byte("k")},
		WithTransportNonce(func() (string, error) { return "", sentinel }),
	)
	req := httptest.NewRequest("GET", "http://example.invalid/x", nil)
	err := tt.CheckRedirect(req, nil)
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected synthetic nonce error, got %v", err)
	}
}

// TestCheckRedirect_PropagatesReadReplayableBodyError covers
// `body, err := readReplayableBody(req); if err != nil { return err }`.
func TestCheckRedirect_PropagatesReadReplayableBodyError(t *testing.T) {
	req := httptest.NewRequest("POST", "http://example.invalid/x", bytes.NewReader([]byte("hi")))
	sentinel := errors.New("synthetic GetBody failure")
	req.GetBody = func() (io.ReadCloser, error) { return nil, sentinel }

	tt := NewSigningTransport(Client{ClientID: "x", Secret: []byte("k")})
	err := tt.CheckRedirect(req, nil)
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected synthetic GetBody error, got %v", err)
	}
}

// TestReadReplayableBody_FallsBackToBodyWhenGetBodyNil simulates the
// Go 1.22 post-redirect state: Body is populated but GetBody is nil.
// readReplayableBody must read Body, return the bytes, and rebuffer
// Body + populate GetBody so the request stays replayable.
func TestReadReplayableBody_FallsBackToBodyWhenGetBodyNil(t *testing.T) {
	req := httptest.NewRequest("POST", "http://example.invalid/x", bytes.NewReader([]byte("payload-bytes")))
	// Simulate Go 1.22 redirect: Body set, GetBody nil.
	req.GetBody = nil

	b, err := readReplayableBody(req)
	if err != nil {
		t.Fatalf("readReplayableBody: %v", err)
	}
	if string(b) != "payload-bytes" {
		t.Fatalf("read body = %q; want %q", b, "payload-bytes")
	}
	// Body must be re-readable for the downstream HTTP send.
	if req.Body == nil {
		t.Fatal("Body was not rebuffered")
	}
	again, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("re-read Body: %v", err)
	}
	if string(again) != "payload-bytes" {
		t.Fatalf("re-read body = %q; want %q", again, "payload-bytes")
	}
	// GetBody should now be populated so a second fetch works too.
	if req.GetBody == nil {
		t.Fatal("GetBody was not populated by fallback")
	}
	rc, err := req.GetBody()
	if err != nil {
		t.Fatalf("GetBody: %v", err)
	}
	defer func() { _ = rc.Close() }()
	third, _ := io.ReadAll(rc)
	if string(third) != "payload-bytes" {
		t.Fatalf("GetBody read = %q; want %q", third, "payload-bytes")
	}
}

func TestReadReplayableBody_FallbackReadError(t *testing.T) {
	sentinel := errors.New("synthetic Body read failure")
	req := httptest.NewRequest("POST", "http://example.invalid/x", nil)
	req.Body = errOnReadCloser{err: sentinel}
	req.ContentLength = 1
	req.GetBody = nil

	_, err := readReplayableBody(req)
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected synthetic read error, got %v", err)
	}
}

// TestResignOnRedirect_PropagatesGetBodyError covers the helper's
// `if err != nil { return err }` after a failing GetBody.
func TestResignOnRedirect_PropagatesGetBodyError(t *testing.T) {
	sentinel := errors.New("synthetic GetBody failure")
	req := httptest.NewRequest("POST", "http://example.invalid/x", bytes.NewReader([]byte("hi")))
	req.GetBody = func() (io.ReadCloser, error) { return nil, sentinel }

	fn := ResignOnRedirect(Client{ClientID: "x", Secret: []byte("k")})
	err := fn(req, nil)
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected synthetic GetBody error, got %v", err)
	}
}

// TestResignOnRedirect_PropagatesReadAllError covers the helper's
// io.ReadAll error branch via a reader whose Read fails.
func TestResignOnRedirect_PropagatesReadAllError(t *testing.T) {
	sentinel := errors.New("synthetic Read failure")
	req := httptest.NewRequest("POST", "http://example.invalid/x", bytes.NewReader([]byte("hi")))
	req.GetBody = func() (io.ReadCloser, error) { return errOnReadCloser{err: sentinel}, nil }

	fn := ResignOnRedirect(Client{ClientID: "x", Secret: []byte("k")})
	err := fn(req, nil)
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected synthetic Read error, got %v", err)
	}
}

func TestResignOnRedirect_PropagatesNonceError(t *testing.T) {
	sentinel := errors.New("synthetic nonce failure")
	req := httptest.NewRequest("GET", "http://example.invalid/x", nil)
	fn := resignOnRedirectWithNonce(
		Client{ClientID: "x", Secret: []byte("k")},
		func() (string, error) { return "", sentinel },
	)
	err := fn(req, nil)
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected synthetic nonce error, got %v", err)
	}
}
