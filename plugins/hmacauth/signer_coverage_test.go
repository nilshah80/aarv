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
		// deliberately no WithTransportNow → s.t.now stays nil
	)
	req := httptest.NewRequest("GET", "http://example.invalid/x", nil)
	err := tt.CheckRedirect(req, nil)
	if err != nil {
		t.Fatalf("CheckRedirect: %v", err)
	}
	if req.Header.Get(DefaultTimestampHeader) == "" {
		t.Fatal("expected timestamp header to be set after CheckRedirect default-now")
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
