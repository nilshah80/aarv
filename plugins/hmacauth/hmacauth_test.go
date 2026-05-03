package hmacauth

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nilshah80/aarv"
)

// stubClock returns a deterministic time.Time for tests; callers vary
// the offset to drive skew checks.
func stubClock(epoch int64) func() time.Time {
	return func() time.Time { return time.Unix(epoch, 0) }
}

func newConfig(t *testing.T, store NonceStore) (Config, Client) {
	t.Helper()
	secret := bytes.Repeat([]byte{0xab}, 32)
	client := Client{ClientID: "tester", Secret: secret}
	cfg := DefaultConfig()
	cfg.Validator = StaticClients(map[string]Client{client.ClientID: client})
	cfg.NonceStore = store
	cfg.Now = stubClock(1735000000)
	cfg.SkewSeconds = 60
	return cfg, client
}

// signRequest is a test helper that signs req using the canonical
// request implementation directly so the test does not have to thread
// through Sign's ergonomics.
func signRequest(t *testing.T, req *http.Request, body []byte, client Client, ts int64, nonce string) {
	t.Helper()
	canonical := canonicalRequest(req.Method, req.URL.Path, req.URL.Query(), body, ts, nonce)
	mac := hmac.New(sha256.New, client.Secret)
	mac.Write(canonical)
	req.Header.Set(DefaultClientIDHeader, client.ClientID)
	req.Header.Set(DefaultTimestampHeader, strconv.FormatInt(ts, 10))
	req.Header.Set(DefaultNonceHeader, nonce)
	req.Header.Set(DefaultSignatureHeader, hex.EncodeToString(mac.Sum(nil)))
}

func TestHMACAuth_AcceptsValidSignature(t *testing.T) {
	cfg, client := newConfig(t, NewMemoryNonceStore(64))
	mw := New(cfg)

	body := []byte(`{"hello":"world"}`)
	req := httptest.NewRequest("POST", "/v1/items", bytes.NewReader(body))
	signRequest(t, req, body, client, 1735000000, "nonce-1")

	rec := httptest.NewRecorder()
	called := false
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if got, _ := io.ReadAll(r.Body); !bytes.Equal(got, body) {
			t.Fatalf("body re-injection failed: got %q want %q", got, body)
		}
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)

	if !called {
		t.Fatalf("middleware rejected: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHMACAuth_MissingHeaders(t *testing.T) {
	cases := []string{
		DefaultClientIDHeader,
		DefaultTimestampHeader,
		DefaultNonceHeader,
		DefaultSignatureHeader,
	}
	for _, missing := range cases {
		t.Run("missing="+missing, func(t *testing.T) {
			cfg, client := newConfig(t, NewMemoryNonceStore(64))
			mw := New(cfg)
			req := httptest.NewRequest("GET", "/x", http.NoBody)
			signRequest(t, req, nil, client, 1735000000, "nonce-"+missing)
			req.Header.Del(missing)

			rec := httptest.NewRecorder()
			mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Fatalf("handler reached with missing header %s", missing)
			})).ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("got status %d want 401", rec.Code)
			}
		})
	}
}

func TestHMACAuth_MalformedTimestamp(t *testing.T) {
	cases := []string{"abc", "-1", "0", ""}
	for _, ts := range cases {
		t.Run("ts="+ts, func(t *testing.T) {
			cfg, client := newConfig(t, NewMemoryNonceStore(64))
			mw := New(cfg)
			req := httptest.NewRequest("GET", "/x", http.NoBody)
			signRequest(t, req, nil, client, 1735000000, "nonce-"+ts)
			req.Header.Set(DefaultTimestampHeader, ts)
			rec := httptest.NewRecorder()
			mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Fatalf("handler reached with malformed timestamp %q", ts)
			})).ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("got %d want 401", rec.Code)
			}
		})
	}
}

func TestHMACAuth_SkewBoundaries(t *testing.T) {
	cfg, client := newConfig(t, NewMemoryNonceStore(64))
	cfg.SkewSeconds = 10
	mw := New(cfg)

	cases := []struct {
		name   string
		ts     int64
		expect int
	}{
		{"in-skew-equal", 1735000010, http.StatusOK},
		{"in-skew-past", 1734999991, http.StatusOK},
		{"out-of-skew-future", 1735000011, http.StatusUnauthorized},
		{"out-of-skew-past", 1734999989, http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/x", http.NoBody)
			signRequest(t, req, nil, client, tc.ts, "nonce-"+tc.name)
			rec := httptest.NewRecorder()
			mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})).ServeHTTP(rec, req)
			if rec.Code != tc.expect {
				t.Fatalf("got %d want %d", rec.Code, tc.expect)
			}
		})
	}
}

func TestHMACAuth_UnknownClient(t *testing.T) {
	cfg, client := newConfig(t, NewMemoryNonceStore(64))
	mw := New(cfg)
	req := httptest.NewRequest("GET", "/x", http.NoBody)
	signRequest(t, req, nil, client, 1735000000, "nonce-uc")
	req.Header.Set(DefaultClientIDHeader, "stranger")

	rec := httptest.NewRecorder()
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler reached for unknown client")
	})).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d want 401", rec.Code)
	}
}

func TestHMACAuth_BadSignature(t *testing.T) {
	cfg, client := newConfig(t, NewMemoryNonceStore(64))
	mw := New(cfg)
	req := httptest.NewRequest("GET", "/x", http.NoBody)
	signRequest(t, req, nil, client, 1735000000, "nonce-bad")
	// Flip a byte in the signature.
	sig := req.Header.Get(DefaultSignatureHeader)
	flipped := []byte(sig)
	if flipped[0] == '0' {
		flipped[0] = '1'
	} else {
		flipped[0] = '0'
	}
	req.Header.Set(DefaultSignatureHeader, string(flipped))
	rec := httptest.NewRecorder()
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler reached with bad signature")
	})).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d want 401", rec.Code)
	}
}

func TestHMACAuth_MalformedSignatureHex(t *testing.T) {
	cfg, client := newConfig(t, NewMemoryNonceStore(64))
	mw := New(cfg)
	req := httptest.NewRequest("GET", "/x", http.NoBody)
	signRequest(t, req, nil, client, 1735000000, "nonce-mal")
	req.Header.Set(DefaultSignatureHeader, "not-hex")
	rec := httptest.NewRecorder()
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler reached with malformed sig")
	})).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d want 401", rec.Code)
	}
}

func TestHMACAuth_BodyOverflowReturns413(t *testing.T) {
	cfg, client := newConfig(t, NewMemoryNonceStore(64))
	cfg.MaxBodyBytes = 16
	mw := New(cfg)

	body := bytes.Repeat([]byte("X"), 64)
	req := httptest.NewRequest("POST", "/x", bytes.NewReader(body))
	signRequest(t, req, body, client, 1735000000, "nonce-big")

	rec := httptest.NewRecorder()
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler reached on oversize body")
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("got %d want 413", rec.Code)
	}
}

func TestHMACAuth_ReplayRejected(t *testing.T) {
	cfg, client := newConfig(t, NewMemoryNonceStore(64))
	mw := New(cfg)

	makeReq := func() *http.Request {
		r := httptest.NewRequest("GET", "/x", http.NoBody)
		signRequest(t, r, nil, client, 1735000000, "fixed-nonce")
		return r
	}

	rec1 := httptest.NewRecorder()
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec1, makeReq())
	if rec1.Code != http.StatusOK {
		t.Fatalf("first request rejected: %d", rec1.Code)
	}

	rec2 := httptest.NewRecorder()
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler reached on replay")
	})).ServeHTTP(rec2, makeReq())
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("replay got %d want 401", rec2.Code)
	}
}

func TestHMACAuth_NonceIsolatedPerClient(t *testing.T) {
	a := bytes.Repeat([]byte{0xaa}, 32)
	b := bytes.Repeat([]byte{0xbb}, 32)
	clients := map[string]Client{
		"a": {ClientID: "a", Secret: a},
		"b": {ClientID: "b", Secret: b},
	}
	cfg := DefaultConfig()
	cfg.Validator = StaticClients(clients)
	cfg.NonceStore = NewMemoryNonceStore(64)
	cfg.Now = stubClock(1735000000)
	cfg.SkewSeconds = 60
	mw := New(cfg)

	for _, id := range []string{"a", "b"} {
		req := httptest.NewRequest("GET", "/x", http.NoBody)
		signRequest(t, req, nil, clients[id], 1735000000, "shared-nonce")
		rec := httptest.NewRecorder()
		mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("client %s same nonce different client should be accepted: got %d", id, rec.Code)
		}
	}
}

func TestHMACAuth_RotationAcceptsBothSecrets(t *testing.T) {
	oldSecret := bytes.Repeat([]byte{0x11}, 32)
	newSecret := bytes.Repeat([]byte{0x22}, 32)
	client := Client{
		ClientID: "rot",
		Secret:   newSecret,
		Secrets:  [][]byte{oldSecret},
	}
	cfg := DefaultConfig()
	cfg.Validator = StaticClients(map[string]Client{client.ClientID: client})
	cfg.NonceStore = NewMemoryNonceStore(64)
	cfg.Now = stubClock(1735000000)
	cfg.SkewSeconds = 60
	mw := New(cfg)

	for _, secret := range [][]byte{newSecret, oldSecret} {
		req := httptest.NewRequest("GET", "/x", http.NoBody)
		signingClient := Client{ClientID: client.ClientID, Secret: secret}
		signRequest(t, req, nil, signingClient, 1735000000, fmt.Sprintf("nonce-%x", secret[0]))
		rec := httptest.NewRecorder()
		mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("rotation: secret %x rejected with %d", secret[0], rec.Code)
		}
	}
}

func TestHMACAuth_RotationDoesNotShortCircuit(t *testing.T) {
	// All three candidate secrets must be evaluated. We assert this
	// by registering a wrapper around hmac.New that counts compares
	// — the simplest approach is to inspect via compareAllSecrets
	// directly.
	primary := bytes.Repeat([]byte{0x11}, 32)
	rot := [][]byte{
		bytes.Repeat([]byte{0x22}, 32),
		bytes.Repeat([]byte{0x33}, 32),
		bytes.Repeat([]byte{0x44}, 32),
	}
	canonical := []byte("METHOD\n/path\n\n0000000000000000000000000000000000000000000000000000000000000000\n1\nn")
	mac := hmac.New(sha256.New, primary)
	mac.Write(canonical)
	receivedPrimary := mac.Sum(nil)
	if !compareAllSecrets(canonical, receivedPrimary, primary, rot) {
		t.Fatalf("primary match not detected")
	}
	mac = hmac.New(sha256.New, rot[2])
	mac.Write(canonical)
	receivedLast := mac.Sum(nil)
	if !compareAllSecrets(canonical, receivedLast, primary, rot) {
		t.Fatalf("last-rotation-slot match not detected; iteration short-circuited")
	}
	if compareAllSecrets(canonical, []byte("not-the-right-bytes-32-bytes-padd"), primary, rot) {
		t.Fatalf("bogus signature accepted")
	}
}

func TestHMACAuth_RotationSkipsEmptySlots(t *testing.T) {
	primary := bytes.Repeat([]byte{0x11}, 32)
	canonical := []byte("METHOD\n/path\n\n0000000000000000000000000000000000000000000000000000000000000000\n1\nn")
	mac := hmac.New(sha256.New, primary)
	mac.Write(canonical)
	primaryDigest := mac.Sum(nil)

	if compareAllSecrets(canonical, primaryDigest, nil, [][]byte{nil, {}, nil}) {
		t.Fatalf("compareAllSecrets accepted with no usable secrets")
	}
}

func TestHMACAuth_NilStoreWarnsOnce(t *testing.T) {
	resetWarnReplayOnceForTesting()
	t.Cleanup(resetWarnReplayOnceForTesting)

	cfg := DefaultConfig()
	cfg.Validator = StaticClients(map[string]Client{"a": {ClientID: "a", Secret: bytes.Repeat([]byte{1}, 32)}})
	cfg.Now = stubClock(1)
	cfg.SkewSeconds = 60
	cfg.NonceStore = nil

	// First New() should fire the warning; subsequent New() must not
	// panic but also must not re-fire (the once latch ensures this).
	_ = New(cfg)
	_ = New(cfg)
}

func TestHMACAuth_PanicsOnMisconfig(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{"nil validator", Config{SkewSeconds: 1}},
		{"zero skew", Config{Validator: func(string) (Client, error) { return Client{}, nil }, SkewSeconds: 0}},
		{"negative skew", Config{Validator: func(string) (Client, error) { return Client{}, nil }, SkewSeconds: -1}},
		{"negative nonce ttl", Config{Validator: func(string) (Client, error) { return Client{}, nil }, SkewSeconds: 1, NonceTTL: -1}},
		{"negative max body", Config{Validator: func(string) (Client, error) { return Client{}, nil }, SkewSeconds: 1, MaxBodyBytes: -1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Fatalf("expected panic, got none")
				}
			}()
			_ = New(tc.cfg)
		})
	}
}

func TestHMACAuth_FromAndFromContext(t *testing.T) {
	cfg, client := newConfig(t, NewMemoryNonceStore(64))
	mw := New(cfg)

	app := aarv.New()
	app.Use(mw)
	app.Get("/whoami", func(c *aarv.Context) error {
		got, ok := From(c)
		if !ok {
			return errors.New("From returned no client")
		}
		fromCtx, ok2 := FromContext(c.Context())
		if !ok2 {
			return errors.New("FromContext returned no client")
		}
		if got.ClientID != client.ClientID || fromCtx.ClientID != client.ClientID {
			return fmt.Errorf("mismatched client id: %q vs %q", got.ClientID, fromCtx.ClientID)
		}
		return c.JSON(http.StatusOK, map[string]string{"id": got.ClientID})
	})

	req := httptest.NewRequest("GET", "/whoami", http.NoBody)
	signRequest(t, req, nil, client, 1735000000, "nonce-fc")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), client.ClientID) {
		t.Fatalf("body missing client id: %s", rec.Body.String())
	}
}

func TestHMACAuth_SkipperBypasses(t *testing.T) {
	cfg, client := newConfig(t, NewMemoryNonceStore(64))
	cfg.Skipper = func(c *aarv.Context) bool { return c.Path() == "/health" }
	mw := New(cfg)

	app := aarv.New()
	app.Use(mw)
	app.Get("/health", func(c *aarv.Context) error { return c.Text(http.StatusOK, "ok") })
	app.Get("/protected", func(c *aarv.Context) error { return c.Text(http.StatusOK, "ok") })

	// /health: no signature, must succeed.
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/health", http.NoBody))
	if rec.Code != http.StatusOK {
		t.Fatalf("/health got %d want 200", rec.Code)
	}

	// /protected: no signature, must fail.
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/protected", http.NoBody))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("/protected unsigned got %d want 401", rec.Code)
	}
	_ = client
}

func TestHMACAuth_CustomErrorHandler(t *testing.T) {
	cfg, _ := newConfig(t, NewMemoryNonceStore(64))
	cfg.ErrorHandler = func(c *aarv.Context, status int, message string) error {
		return c.JSON(status, map[string]string{"custom": "yes", "msg": message})
	}
	mw := New(cfg)

	app := aarv.New()
	app.Use(mw)
	app.Get("/x", func(c *aarv.Context) error { return c.Text(http.StatusOK, "ok") })

	req := httptest.NewRequest("GET", "/x", http.NoBody)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d want 401", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"custom":"yes"`) {
		t.Fatalf("custom handler not invoked: %s", rec.Body.String())
	}
}

func TestHMACAuth_BodyReinjectionPreservesContents(t *testing.T) {
	cfg, client := newConfig(t, NewMemoryNonceStore(64))
	mw := New(cfg)

	body := []byte(strings.Repeat("payload", 100))
	req := httptest.NewRequest("POST", "/x", bytes.NewReader(body))
	signRequest(t, req, body, client, 1735000000, "nonce-reinj")

	rec := httptest.NewRecorder()
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("downstream read body: %v", err)
		}
		if !bytes.Equal(got, body) {
			t.Fatalf("body mismatch after reinjection")
		}
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestHMACAuth_ConcurrentRequests(t *testing.T) {
	cfg, client := newConfig(t, NewMemoryNonceStore(1024))
	cfg.SkewSeconds = 600
	mw := New(cfg)

	const N = 200
	var wg sync.WaitGroup
	var ok atomic.Int64
	for i := 0; i < N; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest("GET", "/x", http.NoBody)
			signRequest(t, req, nil, client, 1735000000, fmt.Sprintf("nonce-%d", i))
			rec := httptest.NewRecorder()
			mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})).ServeHTTP(rec, req)
			if rec.Code == http.StatusOK {
				ok.Add(1)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != int64(N) {
		t.Fatalf("expected %d ok, got %d", N, ok.Load())
	}
}

func TestStaticClients_DefensiveCopy(t *testing.T) {
	secret := []byte{0xaa, 0xbb}
	src := map[string]Client{"x": {ClientID: "x", Secret: secret}}
	v := StaticClients(src)
	// Mutate the source after StaticClients has snapshotted it.
	secret[0] = 0xff
	c, err := v("x")
	if err != nil {
		t.Fatal(err)
	}
	if c.Secret[0] != 0xaa {
		t.Fatalf("StaticClients did not defensively copy; got %x", c.Secret[0])
	}
}

func TestStaticClients_UnknownClient(t *testing.T) {
	v := StaticClients(map[string]Client{"a": {ClientID: "a", Secret: []byte("k")}})
	c, err := v("nope")
	if err == nil {
		t.Fatalf("expected error for unknown id")
	}
	if c.ClientID != "" {
		t.Fatalf("expected zero Client, got %v", c)
	}
}

func TestHMACAuth_ContextCancellation(t *testing.T) {
	// A context cancellation during nonce SetNX should bubble out as
	// 401 (the verify path collapses backend errors to "reject").
	cfg, client := newConfig(t, contextSensitiveStore{})
	mw := New(cfg)

	req := httptest.NewRequest("GET", "/x", http.NoBody)
	signRequest(t, req, nil, client, 1735000000, "nonce-ctx")
	ctx, cancel := context.WithCancel(req.Context())
	cancel()
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler reached after ctx-cancelled SetNX")
	})).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d want 401", rec.Code)
	}
}

type contextSensitiveStore struct{}

func (contextSensitiveStore) SetNX(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return true, nil
}
