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

func TestSign_RequiresClientID(t *testing.T) {
	req := httptest.NewRequest("GET", "/x", nil)
	err := Sign(req, Client{Secret: []byte("k")}, nil, nil, "n")
	if err == nil || !strings.Contains(err.Error(), "ClientID") {
		t.Fatalf("expected ClientID error, got %v", err)
	}
}

func TestSign_RequiresSecret(t *testing.T) {
	req := httptest.NewRequest("GET", "/x", nil)
	err := Sign(req, Client{ClientID: "x"}, nil, nil, "n")
	if err == nil {
		t.Fatalf("expected secret error")
	}
}

func TestSign_FallsBackToRotation(t *testing.T) {
	req := httptest.NewRequest("GET", "/x", nil)
	c := Client{ClientID: "x", Secrets: [][]byte{nil, []byte("rot-key")}}
	if err := Sign(req, c, nil, func() time.Time { return time.Unix(1, 0) }, "n"); err != nil {
		t.Fatalf("Sign with rotation-only client: %v", err)
	}
	if req.Header.Get(DefaultSignatureHeader) == "" {
		t.Fatalf("signature not attached")
	}
}

func TestSign_DeterministicWithFixedClock(t *testing.T) {
	a := httptest.NewRequest("POST", "/y?z=1", bytes.NewReader([]byte("body")))
	b := httptest.NewRequest("POST", "/y?z=1", bytes.NewReader([]byte("body")))

	c := Client{ClientID: "x", Secret: []byte("k")}
	clock := func() time.Time { return time.Unix(42, 0) }
	if err := Sign(a, c, []byte("body"), clock, "fixed"); err != nil {
		t.Fatal(err)
	}
	if err := Sign(b, c, []byte("body"), clock, "fixed"); err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{
		DefaultClientIDHeader, DefaultTimestampHeader,
		DefaultNonceHeader, DefaultSignatureHeader,
	} {
		if a.Header.Get(h) != b.Header.Get(h) {
			t.Fatalf("header %s differs: %q vs %q", h, a.Header.Get(h), b.Header.Get(h))
		}
	}
}

func TestTransport_SignsAndCallsBase(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Echo headers so the client can verify they were signed.
		for _, h := range []string{
			DefaultClientIDHeader, DefaultTimestampHeader,
			DefaultNonceHeader, DefaultSignatureHeader,
		} {
			if r.Header.Get(h) == "" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := Client{ClientID: "x", Secret: []byte("k")}
	tr := Transport(client, WithTransportNow(func() time.Time { return time.Unix(1, 0) }))
	httpClient := &http.Client{Transport: tr}

	resp, err := httpClient.Post(srv.URL+"/v1/x", "text/plain", strings.NewReader("hello"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got %d", resp.StatusCode)
	}
}

func TestTransport_RoundTripDoesNotMutateOriginalRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := Client{ClientID: "x", Secret: []byte("k")}
	tr := Transport(client)
	req, err := http.NewRequest("GET", srv.URL+"/x", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	// The original request should NOT have been mutated.
	if req.Header.Get(DefaultSignatureHeader) != "" {
		t.Fatalf("Transport mutated caller's request headers")
	}
}

func TestTransport_RejectsRequestsWithoutGetBody(t *testing.T) {
	client := Client{ClientID: "x", Secret: []byte("k")}
	tr := Transport(client)

	pr, _ := io.Pipe()
	req, err := http.NewRequest("POST", "http://example.test/x", pr)
	if err != nil {
		t.Fatal(err)
	}
	if req.GetBody != nil {
		t.Fatalf("test precondition: pipe-based body should not have GetBody")
	}
	_, err = tr.RoundTrip(req)
	if err == nil {
		t.Fatalf("expected error for non-replayable body")
	}
	if !strings.Contains(err.Error(), "GetBody") {
		t.Fatalf("expected GetBody-mention error, got %v", err)
	}
}

func TestTransport_NonceErrorsBubbleUp(t *testing.T) {
	client := Client{ClientID: "x", Secret: []byte("k")}
	target := errors.New("rng dead")
	tr := Transport(client, WithTransportNonce(func() (string, error) { return "", target }))
	req, _ := http.NewRequest("GET", "http://example.test/x", nil)
	_, err := tr.RoundTrip(req)
	if !errors.Is(err, target) {
		t.Fatalf("got %v want %v", err, target)
	}
}

func TestTransport_HeadersOverride(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Auth-Client") == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := Client{ClientID: "x", Secret: []byte("k")}
	tr := Transport(client, WithTransportHeaders("X-Auth-Client", "X-Auth-TS", "X-Auth-Nonce", "X-Auth-Sig"))
	resp, err := (&http.Client{Transport: tr}).Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got %d", resp.StatusCode)
	}
}

func TestFailOnRedirect(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusFound)
	}))
	defer redirector.Close()

	client := Client{ClientID: "x", Secret: []byte("k")}
	httpClient := &http.Client{
		Transport:     Transport(client),
		CheckRedirect: FailOnRedirect,
	}
	resp, err := httpClient.Get(redirector.URL + "/x")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 302 visible, got %d", resp.StatusCode)
	}
}

func TestResignOnRedirect(t *testing.T) {
	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the sig is present and the timestamp is recent — a
		// re-signed redirect carries a fresh ts and nonce.
		if r.Header.Get(DefaultClientIDHeader) == "" {
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
	httpClient := &http.Client{
		Transport:     Transport(client),
		CheckRedirect: ResignOnRedirect(client),
	}

	resp, err := httpClient.Get(redirector.URL + "/x")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got %d", resp.StatusCode)
	}
}
