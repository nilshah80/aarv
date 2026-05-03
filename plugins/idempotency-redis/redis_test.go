package idempotencyredis

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/nilshah80/aarv/plugins/idempotency"
	"github.com/redis/go-redis/v9"
)

func newStore(t *testing.T) (*Store, *miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return New(Config{Client: rdb}), mr, rdb
}

func TestLockAcquireAndRelease(t *testing.T) {
	s, _, _ := newStore(t)
	got, err := s.Lock("k1")
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Fatalf("first lock should succeed")
	}
	got, err = s.Lock("k1")
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Fatalf("second lock should fail")
	}
	if err := s.Unlock("k1"); err != nil {
		t.Fatal(err)
	}
	got, err = s.Lock("k1")
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Fatalf("post-unlock lock should succeed")
	}
}

func TestUnlockIdempotent(t *testing.T) {
	s, _, _ := newStore(t)
	if err := s.Unlock("never-locked"); err != nil {
		t.Fatalf("unlock of absent key should not error: %v", err)
	}
}

func TestSaveAndGet(t *testing.T) {
	s, _, _ := newStore(t)
	resp := &idempotency.Response{
		StatusCode: 201,
		Headers:    http.Header{"Content-Type": []string{"application/json"}},
		Body:       []byte(`{"id":"abc"}`),
	}
	if err := s.Save("k", resp, time.Hour); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("k")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected response, got nil")
	}
	if got.StatusCode != 201 {
		t.Fatalf("status: got %d want 201", got.StatusCode)
	}
	if string(got.Body) != `{"id":"abc"}` {
		t.Fatalf("body roundtrip: got %q", got.Body)
	}
	if got.Headers.Get("Content-Type") != "application/json" {
		t.Fatalf("header roundtrip: got %v", got.Headers)
	}
}

func TestSavePayloadHashRoundtrip(t *testing.T) {
	s, _, _ := newStore(t)
	resp := &idempotency.Response{StatusCode: 200, Body: []byte("ok")}
	for i := range resp.PayloadHash {
		resp.PayloadHash[i] = byte(i + 1)
	}
	if err := s.Save("ph", resp, time.Hour); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("ph")
	if err != nil {
		t.Fatal(err)
	}
	if got.PayloadHash != resp.PayloadHash {
		t.Fatalf("payload hash mismatch")
	}
}

func TestGetMissingReturnsNil(t *testing.T) {
	s, _, _ := newStore(t)
	got, err := s.Get("nope")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

func TestGetMalformedReturnsNilNotError(t *testing.T) {
	s, _, rdb := newStore(t)
	// Inject garbage at the response key so Get sees a malformed
	// JSON. The behavior contract is "treat malformed as miss" so
	// the user's request still completes.
	if err := rdb.Set(context.Background(), s.respKey("bad"), "{not json", time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("bad")
	if err != nil {
		t.Fatalf("malformed entry should not surface as error: %v", err)
	}
	if got != nil {
		t.Fatalf("malformed entry should produce nil, got %v", got)
	}
}

func TestSaveExpiresAfterTTL(t *testing.T) {
	s, mr, _ := newStore(t)
	resp := &idempotency.Response{StatusCode: 200, Body: []byte("x")}
	if err := s.Save("ttl", resp, time.Second); err != nil {
		t.Fatal(err)
	}
	mr.FastForward(2 * time.Second)
	got, err := s.Get("ttl")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("entry should have expired, got %v", got)
	}
}

func TestSaveRejectsNil(t *testing.T) {
	s, _, _ := newStore(t)
	if err := s.Save("k", nil, time.Hour); err == nil {
		t.Fatalf("expected error on nil response")
	}
}

func TestKeyPrefixDefault(t *testing.T) {
	s, mr, _ := newStore(t)
	resp := &idempotency.Response{StatusCode: 200, Body: []byte("x")}
	_ = s.Save("p", resp, time.Hour)
	keys := mr.Keys()
	if len(keys) == 0 {
		t.Fatal("no keys")
	}
	for _, k := range keys {
		if !strings.HasPrefix(k, DefaultKeyPrefix) {
			t.Fatalf("key %q lacks default prefix", k)
		}
	}
}

func TestKeyPrefixCustom(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	s := New(Config{Client: rdb, KeyPrefix: "tenantA:idem:"})
	resp := &idempotency.Response{StatusCode: 200, Body: []byte("x")}
	_ = s.Save("p", resp, time.Hour)
	for _, k := range mr.Keys() {
		if !strings.HasPrefix(k, "tenantA:idem:") {
			t.Fatalf("custom prefix not applied: %v", k)
		}
	}
}

func TestNewPanicsOnNilClient(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic")
		}
	}()
	_ = New(Config{})
}

// --- WaitableStore ---

func TestWait_GetShortCircuitWhenAlreadySaved(t *testing.T) {
	s, _, _ := newStore(t)
	resp := &idempotency.Response{StatusCode: 200, Body: []byte("ok")}
	if err := s.Save("w1", resp, time.Hour); err != nil {
		t.Fatal(err)
	}
	got, err := s.Wait(context.Background(), "w1")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || string(got.Body) != "ok" {
		t.Fatalf("expected immediate return of saved response, got %v", got)
	}
}

func TestWait_BlocksUntilSavePublishes(t *testing.T) {
	s, _, _ := newStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		time.Sleep(150 * time.Millisecond)
		resp := &idempotency.Response{StatusCode: 201, Body: []byte("late-ok")}
		if err := s.Save("w2", resp, time.Hour); err != nil {
			t.Errorf("save: %v", err)
		}
	}()

	got, err := s.Wait(ctx, "w2")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || string(got.Body) != "late-ok" {
		t.Fatalf("expected late response, got %v", got)
	}
}

func TestWait_ContextDeadlineFires(t *testing.T) {
	s, _, _ := newStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_, err := s.Wait(ctx, "never")
	if err == nil {
		t.Fatalf("expected ctx error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v want DeadlineExceeded", err)
	}
}

func TestWait_PostSaveSubscribers(t *testing.T) {
	// Two concurrent Wait callers both observe the published Save.
	s, _, _ := newStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	got := atomic.Int32{}
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := s.Wait(ctx, "fan")
			if err == nil && r != nil {
				got.Add(1)
			}
		}()
	}
	time.Sleep(100 * time.Millisecond)
	if err := s.Save("fan", &idempotency.Response{StatusCode: 200, Body: []byte("v")}, time.Hour); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if got.Load() != 3 {
		t.Fatalf("expected 3 subscribers to receive, got %d", got.Load())
	}
}

// --- error propagation ---

func TestLockRedisErrorPropagates(t *testing.T) {
	s, mr, _ := newStore(t)
	mr.SetError("backend down")
	defer mr.SetError("")
	_, err := s.Lock("err")
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestGetRedisErrorPropagates(t *testing.T) {
	s, mr, _ := newStore(t)
	mr.SetError("backend down")
	defer mr.SetError("")
	_, err := s.Get("err")
	if err == nil {
		t.Fatalf("expected error")
	}
}

// Compile-time interface assertion via package-level _ in redis.go.
// Re-stated here to surface in this test file's coverage.
var _ idempotency.WaitableStore = (*Store)(nil)
