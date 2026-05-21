package hmacauth

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nilshah80/aarv"
)

// captureObserver records every Event the middleware emits. Tests
// assert against the full slice so a behavior change that fires twice,
// drops an event, or fires on a Skipper-bypassed request shows up
// loudly.
type captureObserver struct {
	events []Event
}

func (c *captureObserver) record(_ *aarv.Context, e Event) {
	c.events = append(c.events, e)
}

func TestObserver_FiresOnSuccess(t *testing.T) {
	cfg, client := newConfig(t, NewMemoryNonceStore(64))
	cap := &captureObserver{}
	cfg.Observer = cap.record
	mw := New(cfg)

	body := []byte(`{"hello":"world"}`)
	req := httptest.NewRequest("POST", "/v1/items", bytes.NewReader(body))
	signRequest(t, req, body, client, 1735000000, "nonce-success")

	rec := httptest.NewRecorder()
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("handler did not run: status=%d", rec.Code)
	}
	if got := len(cap.events); got != 1 {
		t.Fatalf("expected 1 observer event, got %d: %+v", got, cap.events)
	}
	e := cap.events[0]
	if e.Outcome != OutcomeOK {
		t.Errorf("Outcome = %q, want %q", e.Outcome, OutcomeOK)
	}
	if e.ClientID != client.ClientID {
		t.Errorf("ClientID = %q, want %q", e.ClientID, client.ClientID)
	}
	// On success the middleware did not write a response itself; the
	// handler did. The event reports Status=0 to make that boundary
	// explicit.
	if e.Status != 0 {
		t.Errorf("Status on success = %d, want 0 (handler decides)", e.Status)
	}
	if e.SkewSeconds != 0 {
		t.Errorf("SkewSeconds on success = %d, want 0", e.SkewSeconds)
	}
}

func TestObserver_FiresOnClockSkew(t *testing.T) {
	cfg, client := newConfig(t, NewMemoryNonceStore(64))
	cap := &captureObserver{}
	cfg.Observer = cap.record
	mw := New(cfg)

	body := []byte("{}")
	req := httptest.NewRequest("POST", "/v1/items", bytes.NewReader(body))
	// Sign 10 minutes in the past against a 60s skew window.
	signRequest(t, req, body, client, 1735000000-600, "nonce-skew")

	rec := httptest.NewRecorder()
	mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler must not run when timestamp is outside skew")
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if got := len(cap.events); got != 1 {
		t.Fatalf("expected 1 observer event, got %d", got)
	}
	e := cap.events[0]
	if e.Outcome != OutcomeClockSkew {
		t.Errorf("Outcome = %q, want %q", e.Outcome, OutcomeClockSkew)
	}
	if e.SkewSeconds != 600 {
		t.Errorf("SkewSeconds = %d, want 600", e.SkewSeconds)
	}
	if e.Status != http.StatusUnauthorized {
		t.Errorf("Status = %d, want 401", e.Status)
	}
}

func TestObserver_FiresOnSignatureInvalid(t *testing.T) {
	cfg, client := newConfig(t, NewMemoryNonceStore(64))
	cap := &captureObserver{}
	cfg.Observer = cap.record
	mw := New(cfg)

	body := []byte("{}")
	req := httptest.NewRequest("POST", "/v1/items", bytes.NewReader(body))
	signRequest(t, req, body, client, 1735000000, "nonce-sig")
	// Tamper after signing — the recomputed HMAC will not match.
	req.Header.Set(DefaultSignatureHeader, "0000000000000000000000000000000000000000000000000000000000000000")

	rec := httptest.NewRecorder()
	mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if got := len(cap.events); got != 1 {
		t.Fatalf("expected 1 observer event, got %d", got)
	}
	if e := cap.events[0]; e.Outcome != OutcomeSignatureInvalid {
		t.Errorf("Outcome = %q, want %q", e.Outcome, OutcomeSignatureInvalid)
	}
}

func TestObserver_FiresOnReplay(t *testing.T) {
	cfg, client := newConfig(t, NewMemoryNonceStore(64))
	cap := &captureObserver{}
	cfg.Observer = cap.record
	mw := New(cfg)

	body := []byte("{}")
	mk := func() *http.Request {
		req := httptest.NewRequest("POST", "/v1/items", bytes.NewReader(body))
		signRequest(t, req, body, client, 1735000000, "nonce-replay")
		return req
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw(handler).ServeHTTP(httptest.NewRecorder(), mk())
	rec := httptest.NewRecorder()
	mw(handler).ServeHTTP(rec, mk())

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("second request status = %d, want 401", rec.Code)
	}
	if got := len(cap.events); got != 2 {
		t.Fatalf("expected 2 observer events, got %d", got)
	}
	if cap.events[0].Outcome != OutcomeOK {
		t.Errorf("first event Outcome = %q, want %q", cap.events[0].Outcome, OutcomeOK)
	}
	if cap.events[1].Outcome != OutcomeReplayDetected {
		t.Errorf("replay event Outcome = %q, want %q", cap.events[1].Outcome, OutcomeReplayDetected)
	}
}

func TestObserver_FiresOnBodyTooLarge(t *testing.T) {
	cfg, client := newConfig(t, NewMemoryNonceStore(64))
	cfg.MaxBodyBytes = 16
	cap := &captureObserver{}
	cfg.Observer = cap.record
	mw := New(cfg)

	body := bytes.Repeat([]byte("x"), 64)
	req := httptest.NewRequest("POST", "/v1/items", bytes.NewReader(body))
	// Sign matching the bytes that *would* be hashed if the body
	// fit; signature does not matter because the body cap fires
	// first.
	signRequest(t, req, body, client, 1735000000, "nonce-big")

	rec := httptest.NewRecorder()
	mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
	if got := len(cap.events); got != 1 {
		t.Fatalf("expected 1 observer event, got %d", got)
	}
	if e := cap.events[0]; e.Outcome != OutcomeBodyTooLarge || e.Status != http.StatusRequestEntityTooLarge {
		t.Errorf("Event = %+v, want OutcomeBodyTooLarge with Status 413", e)
	}
}

func TestObserver_NotFiredWhenSkipped(t *testing.T) {
	cfg, _ := newConfig(t, NewMemoryNonceStore(64))
	cap := &captureObserver{}
	cfg.Observer = cap.record
	cfg.Skipper = func(c *aarv.Context) bool { return true }

	// Driving through aarv.App — the Skipper hook on the stdlib path
	// requires aarv.FromRequest(r) to succeed, which only happens when
	// the framework bridge has populated the request context. The
	// native path always has a *aarv.Context available, so this also
	// exercises the native bypass implicitly.
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(cfg))
	app.Get("/v1/items", func(c *aarv.Context) error {
		return c.Text(http.StatusOK, "ok")
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/items", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(cap.events) != 0 {
		t.Fatalf("Skipper bypass must not emit observer events, got %+v", cap.events)
	}
}

// TestObserver_NativePathFires exercises the aarv.Context-based path
// (the fast path the framework uses when both stdlib and native
// middleware are registered), since the stdlib path is what the
// other Observer tests above exercise via mw(handler).ServeHTTP.
func TestObserver_NativePathFires(t *testing.T) {
	cfg, client := newConfig(t, NewMemoryNonceStore(64))
	cap := &captureObserver{}
	cfg.Observer = cap.record

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(cfg))
	app.Post("/v1/items", func(c *aarv.Context) error {
		return c.Text(http.StatusCreated, "ok")
	})

	body := []byte(`{"a":1}`)
	req := httptest.NewRequest("POST", "/v1/items", bytes.NewReader(body))
	signRequest(t, req, body, client, 1735000000, "nonce-native")

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}
	if len(cap.events) != 1 || cap.events[0].Outcome != OutcomeOK {
		t.Fatalf("native-path observer events = %+v, want one OutcomeOK", cap.events)
	}
}

// TestObserver_NilIsZeroOverhead enforces the documented contract:
// when Observer is nil, the middleware must not call Now beyond the
// single call verify() needs for the skew window. The contract was
// regressed previously — start := n.now() ran before the Observer
// nil check, doubling Now calls per request. The instrumented Now
// here counts invocations so a future regression of the same shape
// fails the test loudly.
func TestObserver_NilIsZeroOverhead(t *testing.T) {
	cfg, client := newConfig(t, NewMemoryNonceStore(64))
	cfg.Observer = nil

	var nowCalls int
	cfg.Now = func() time.Time {
		nowCalls++
		return time.Unix(1735000000, 0)
	}

	mw := New(cfg)

	body := []byte("{}")
	req := httptest.NewRequest("POST", "/v1/items", bytes.NewReader(body))
	signRequest(t, req, body, client, 1735000000, "nonce-noop")

	rec := httptest.NewRecorder()
	mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("nil observer produced status %d, want 200", rec.Code)
	}
	// verify() calls n.now() exactly once for the skew check. The
	// Observer pathway adds two more calls (start + end) — those must
	// NOT fire when Observer is nil.
	if nowCalls != 1 {
		t.Fatalf("nowCalls = %d, want 1 (only verify's skew read; Observer pathway must be gated)", nowCalls)
	}
}

// TestObserver_StoreErrorReportsUnauthorizedNotReplay covers the
// outcome distinction that originally collapsed Redis/store outages
// into OutcomeReplayDetected and made dashboards see auth-availability
// incidents as security incidents. Store transport errors must report
// as OutcomeUnauthorized; only !fresh (the actual replay signal)
// should report as OutcomeReplayDetected.
func TestObserver_StoreErrorReportsUnauthorizedNotReplay(t *testing.T) {
	cfg, client := newConfig(t, &erroringNonceStore{err: errStoreUnreachable})
	cap := &captureObserver{}
	cfg.Observer = cap.record
	mw := New(cfg)

	body := []byte("{}")
	req := httptest.NewRequest("POST", "/v1/items", bytes.NewReader(body))
	signRequest(t, req, body, client, 1735000000, "nonce-store-err")

	rec := httptest.NewRecorder()
	mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler must not run when nonce store fails")
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if got := len(cap.events); got != 1 {
		t.Fatalf("events = %d, want 1", got)
	}
	if e := cap.events[0]; e.Outcome != OutcomeUnauthorized {
		t.Errorf("Outcome on store error = %q, want %q (store outages must not look like replays)",
			e.Outcome, OutcomeUnauthorized)
	}
}

// TestObserver_BodyReadTransportErrorReportsUnauthorized covers the
// matching distinction on the body-read path: a transport-level body
// failure (truncated body, connection drop) must surface as
// OutcomeUnauthorized rather than OutcomeBodyTooLarge, since the
// actual response status in that branch is 401, not 413.
func TestObserver_BodyReadTransportErrorReportsUnauthorized(t *testing.T) {
	cfg, client := newConfig(t, NewMemoryNonceStore(64))
	cfg.MaxBodyBytes = 1024 // generous; the failure isn't size-related
	cap := &captureObserver{}
	cfg.Observer = cap.record
	mw := New(cfg)

	// signRequest needs a body to compute the canonical request, but
	// the wire request's Body is replaced with a reader that errors
	// after the first byte to simulate a transport failure mid-read.
	body := []byte("xxxxxxxxxx")
	req := httptest.NewRequest("POST", "/v1/items", bytes.NewReader(body))
	signRequest(t, req, body, client, 1735000000, "nonce-body-trans")
	req.Body = io.NopCloser(&erroringReader{err: errBodyTransport})

	rec := httptest.NewRecorder()
	mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler must not run on body transport error")
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if got := len(cap.events); got != 1 {
		t.Fatalf("events = %d, want 1", got)
	}
	if e := cap.events[0]; e.Outcome != OutcomeUnauthorized || e.Status != http.StatusUnauthorized {
		t.Errorf("body-transport-error event = %+v, want OutcomeUnauthorized + 401 (not OutcomeBodyTooLarge)", e)
	}
}

// erroringNonceStore returns err from SetNX so we can drive
// store-transport-failure tests without a real Redis.
type erroringNonceStore struct {
	err error
}

func (s *erroringNonceStore) SetNX(_ context.Context, _ string, _ time.Duration) (bool, error) {
	return false, s.err
}

var errStoreUnreachable = errStub("nonce store unreachable")

// errStub is a tiny error type. We can't import errors.New into this
// test package in a way that makes the value unique-per-test without
// a package-level var — this approach avoids any cross-test sharing.
type errStub string

func (e errStub) Error() string { return string(e) }

// erroringReader returns 0, err on every Read so readBody / readBodyStdlib
// see the transport failure on the very first byte.
type erroringReader struct{ err error }

func (r *erroringReader) Read(_ []byte) (int, error) { return 0, r.err }

var errBodyTransport = errStub("body transport failure")

// TestObserver_DurationIsNonNegative checks that Event.Duration is set
// to a real measurement, not a zero literal. We can't pin a specific
// number (timing is environment-dependent) so we drive the clock
// forward 1ms between start-time capture and the post-verify read by
// stubbing Now to return a small offset on the second call. If the
// implementation captured the end time before the start, Duration
// would land at zero or negative — that's the regression this guards.
func TestObserver_DurationIsNonNegative(t *testing.T) {
	cfg, client := newConfig(t, NewMemoryNonceStore(64))
	cap := &captureObserver{}
	cfg.Observer = cap.record

	// Stub Now to advance by 1ms each call. The middleware reads it
	// at start, then again just before emit; the difference must be
	// the advertised step.
	calls := 0
	cfg.Now = func() time.Time {
		calls++
		return time.Unix(1735000000, int64(calls)*int64(time.Millisecond))
	}

	mw := New(cfg)
	body := []byte("{}")
	req := httptest.NewRequest("POST", "/v1/items", bytes.NewReader(body))
	signRequest(t, req, body, client, 1735000000, "nonce-dur")

	rec := httptest.NewRecorder()
	mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(cap.events) != 1 {
		t.Fatalf("events = %+v", cap.events)
	}
	if d := cap.events[0].Duration; d < time.Millisecond {
		t.Fatalf("Duration = %v, want >= 1ms (start was captured before end)", d)
	}
}
