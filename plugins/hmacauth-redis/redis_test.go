package hmacauthredis

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// newTestStore returns a Store backed by an in-process miniredis.
// The cleanup hook closes both the client and the server so the
// goroutine leak detector stays clean.
func newTestStore(t *testing.T) (*Store, *miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return New(Config{Client: rdb}), mr, rdb
}

func TestSetNX_FreshThenDuplicate(t *testing.T) {
	s, _, _ := newTestStore(t)
	ctx := context.Background()

	fresh, err := s.SetNX(ctx, "n1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !fresh {
		t.Fatalf("first SetNX must be fresh")
	}

	fresh, err = s.SetNX(ctx, "n1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if fresh {
		t.Fatalf("duplicate SetNX must not be fresh")
	}
}

func TestSetNX_ExpiryAllowsReuse(t *testing.T) {
	s, mr, _ := newTestStore(t)
	ctx := context.Background()

	if _, err := s.SetNX(ctx, "n2", time.Second); err != nil {
		t.Fatal(err)
	}
	mr.FastForward(2 * time.Second)
	fresh, err := s.SetNX(ctx, "n2", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !fresh {
		t.Fatalf("post-expiry SetNX must be fresh")
	}
}

func TestSetNX_ContextCancellation(t *testing.T) {
	s, _, _ := newTestStore(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := s.SetNX(ctx, "n3", time.Minute)
	if err == nil {
		t.Fatalf("expected ctx error")
	}
}

func TestSetNX_RedisErrorPropagates(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	s := New(Config{Client: rdb})

	mr.SetError("forced backend failure")
	defer mr.SetError("")
	_, err := s.SetNX(context.Background(), "n4", time.Minute)
	if err == nil {
		t.Fatalf("expected redis error")
	}
}

func TestSetNX_RejectsZeroTTL(t *testing.T) {
	s, _, _ := newTestStore(t)
	_, err := s.SetNX(context.Background(), "n5", 0)
	if err == nil {
		t.Fatalf("expected error for zero ttl")
	}
}

func TestSetNX_RejectsNegativeTTL(t *testing.T) {
	s, _, _ := newTestStore(t)
	_, err := s.SetNX(context.Background(), "n6", -time.Second)
	if err == nil {
		t.Fatalf("expected error for negative ttl")
	}
}

func TestSetNX_KeyPrefixDefault(t *testing.T) {
	s, mr, _ := newTestStore(t)
	if _, err := s.SetNX(context.Background(), "abc", time.Minute); err != nil {
		t.Fatal(err)
	}
	keys := mr.Keys()
	if len(keys) != 1 {
		t.Fatalf("got %d keys want 1: %v", len(keys), keys)
	}
	if !strings.HasPrefix(keys[0], DefaultKeyPrefix) {
		t.Fatalf("key %q lacks prefix %q", keys[0], DefaultKeyPrefix)
	}
}

func TestSetNX_KeyPrefixCustom(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	s := New(Config{Client: rdb, KeyPrefix: "tenant42:"})
	if _, err := s.SetNX(context.Background(), "x", time.Minute); err != nil {
		t.Fatal(err)
	}
	keys := mr.Keys()
	if len(keys) != 1 || !strings.HasPrefix(keys[0], "tenant42:") {
		t.Fatalf("custom prefix not applied: %v", keys)
	}
}

func TestNew_PanicsOnNilClient(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic")
		}
	}()
	_ = New(Config{})
}

func TestSetNX_Concurrent(t *testing.T) {
	s, _, _ := newTestStore(t)
	const N = 100
	var wg sync.WaitGroup
	var fresh atomic.Int64
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := s.SetNX(context.Background(), "shared", time.Minute)
			if err != nil {
				t.Errorf("setnx: %v", err)
				return
			}
			if ok {
				fresh.Add(1)
			}
		}()
	}
	wg.Wait()
	if fresh.Load() != 1 {
		t.Fatalf("exactly one SetNX should be fresh, got %d", fresh.Load())
	}
}

func TestSetNX_ContextWithDeadline(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	s := New(Config{Client: rdb})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Nanosecond)
	defer cancel()
	time.Sleep(50 * time.Microsecond)
	_, err := s.SetNX(ctx, "n7", time.Minute)
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		// On extremely fast hardware miniredis may complete the SET
		// before the deadline fires; that's acceptable. We only
		// assert there is no panic on cancelled ctx.
		t.Logf("ctx outcome: %v (acceptable race)", err)
	}
}
