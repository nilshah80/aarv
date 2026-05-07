// Package idempotencyredis is the Redis-backed Store for the
// plugins/idempotency middleware. It implements both
// idempotency.Store (the minimal contract) and
// idempotency.WaitableStore (the optional ConflictWait extension).
//
// # Lock and response live in separate keys
//
// The lock key (lock:<key>) and the response key (resp:<key>) are
// distinct. A handler that errors before writing a response unlocks
// without leaving a phantom resp:<key> behind, so subsequent
// retries see the lock has been released and proceed normally.
//
// # Lock TTL
//
// Locks are acquired with a configurable LockTTL (default 30s) so a
// crashed process cannot pin a key forever. Set this to comfortably
// exceed the longest plausible handler runtime; the configured TTL
// is also the maximum time a ConflictWait subscriber will block.
//
// # Response encoding
//
// Cached responses are stored as JSON: status code, headers, body
// (base64 to survive non-UTF-8 bytes), and the request payload hash.
// Backwards-compatible: future fields can be added without breaking
// existing entries.
//
// # Wait via Redis pub/sub
//
// The optional Wait method subscribes to a channel keyed on the
// idempotency key and waits for the original holder to publish a
// "saved" notification. To handle the race where the publisher
// fires before the subscriber connects, Wait does an immediate Get
// before returning to the channel — if the key is already saved,
// the Get short-circuits the wait.
//
// # Wiring
//
//	rdb := redis.NewClient(&redis.Options{Addr: "redis:6379"})
//	store := idempotencyredis.New(idempotencyredis.Config{Client: rdb})
//	mw := idempotency.New(idempotency.Config{
//	    Store:            store,
//	    TTL:              24 * time.Hour,
//	    HashRequestBody:  true,
//	    ConflictBehavior: idempotency.ConflictWait,
//	    WaitTimeout:      30 * time.Second,
//	})
//	app.Use(mw)
//	app.Post("/v1/links", createLinkHandler, aarv.WithRouteIdempotencyTTL(2*time.Hour))
package idempotencyredis

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/nilshah80/aarv/plugins/idempotency"
	"github.com/redis/go-redis/v9"
)

// DefaultKeyPrefix is the default Redis key namespace.
const DefaultKeyPrefix = "aarv:idempotency:"

// DefaultLockTTL is the default lifetime of an idempotency lock. A
// crashed process can never hold the key longer than this.
const DefaultLockTTL = 30 * time.Second

// Config holds the Redis-backed Store configuration.
type Config struct {
	// Client is the redis client. Required.
	Client *redis.Client

	// KeyPrefix is prepended to every Redis key. Defaults to
	// DefaultKeyPrefix.
	KeyPrefix string

	// LockTTL is the lifetime of an in-flight lock. Defaults to
	// DefaultLockTTL. Set to comfortably exceed the longest
	// plausible handler runtime.
	LockTTL time.Duration
}

// Store implements idempotency.Store and idempotency.WaitableStore
// over Redis.
type Store struct {
	client  *redis.Client
	prefix  string
	lockTTL time.Duration
}

// Compile-time interface checks. Failing here means an upstream
// shape change in idempotency would surface as a build break, not
// as a runtime decoupling.
var (
	_ idempotency.Store         = (*Store)(nil)
	_ idempotency.WaitableStore = (*Store)(nil)
)

// New constructs a Redis-backed Store. Panics on a nil client —
// silently degrading to a no-op store would defeat caching and
// allow duplicate writes through.
func New(cfg Config) *Store {
	if cfg.Client == nil {
		panic("idempotencyredis: Config.Client is required")
	}
	prefix := cfg.KeyPrefix
	if prefix == "" {
		prefix = DefaultKeyPrefix
	}
	lockTTL := cfg.LockTTL
	if lockTTL <= 0 {
		lockTTL = DefaultLockTTL
	}
	return &Store{
		client:  cfg.Client,
		prefix:  prefix,
		lockTTL: lockTTL,
	}
}

func (s *Store) lockKey(key string) string { return s.prefix + "lock:" + key }
func (s *Store) respKey(key string) string { return s.prefix + "resp:" + key }
func (s *Store) waitChan(key string) string {
	return s.prefix + "saved:" + key
}

// Lock attempts to acquire the lock. Implementation: Redis SETNX
// with TTL. Returns (true, nil) on success and (false, nil) when
// another process is holding the lock.
//
// idempotency.Store.Lock has no context. We use Background, which
// gives this call a default 5s dial+read+write timeout via the
// caller-configured *redis.Client. That mirrors plugins/idempotency's
// MemoryStore semantics — Lock is expected to be a fast path.
func (s *Store) Lock(key string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ok, err := s.client.SetNX(ctx, s.lockKey(key), "1", s.lockTTL).Result()
	if err != nil {
		return false, err
	}
	return ok, nil
}

// Unlock deletes the lock key. Idempotent — a Delete on an absent
// key is a no-op in Redis.
func (s *Store) Unlock(key string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.client.Del(ctx, s.lockKey(key)).Err()
}

// Get returns the cached response, or (nil, nil) when no entry
// exists or the entry has expired (Redis surfaces both cases as
// "key not found").
func (s *Store) Get(key string) (*idempotency.Response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	raw, err := s.client.Get(ctx, s.respKey(key)).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, err
	}
	resp, decErr := decodeResponse(raw)
	if decErr != nil {
		// Treat a malformed cached entry as a cache miss rather than
		// a 500. This protects against entries written by a future
		// version of this package whose schema we cannot read; the
		// user's request still completes, just without replay.
		return nil, nil
	}
	return resp, nil
}

// Save persists the response under key with the given TTL, then
// publishes a "saved" notification so any concurrent Wait
// subscribers can wake up.
//
// ttl <= 0 is rejected. The middleware never calls Save with a
// non-positive TTL — the per-route caching opt-out path
// (WithRouteIdempotencyTTL(0)) is decided BEFORE Save is called,
// not by passing zero down to the store. We reject here so direct
// callers of the public Store cannot accidentally create a
// non-expiring entry via go-redis's "0 ⇒ no expiry" behavior.
func (s *Store) Save(key string, resp *idempotency.Response, ttl time.Duration) error {
	if resp == nil {
		return errors.New("idempotencyredis: Save called with nil response")
	}
	if ttl <= 0 {
		return fmt.Errorf("idempotencyredis: Save ttl must be > 0, got %v", ttl)
	}
	raw, err := encodeResponse(resp)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.client.Set(ctx, s.respKey(key), raw, ttl).Err(); err != nil {
		return err
	}
	// Notify any pub/sub subscribers waiting on this key. Failure
	// to publish is non-fatal — the subscriber's deadline will
	// eventually fire and they'll fall through to a normal Get.
	_ = s.client.Publish(ctx, s.waitChan(key), "saved").Err()
	return nil
}

// Wait blocks until either:
//
//   - the holder calls Save, in which case the cached response is
//     returned;
//   - ctx fires, in which case (nil, ctx.Err()) is returned.
//
// Wait subscribes to the per-key channel BEFORE doing the
// short-circuit Get to close the subscribe-after-publish race.
func (s *Store) Wait(ctx context.Context, key string) (*idempotency.Response, error) {
	pubsub := s.client.Subscribe(ctx, s.waitChan(key))
	defer pubsub.Close()

	// Drain the channel buffer once so the goroutine inside
	// Subscribe completes the SUBSCRIBE handshake before we proceed.
	if _, err := pubsub.Receive(ctx); err != nil {
		// If we cannot subscribe, fall back to a single Get probe.
		// On Redis outage this surfaces as the same error path that
		// the caller already handles.
		resp, getErr := s.Get(key)
		if resp != nil {
			return resp, nil
		}
		if getErr != nil {
			return nil, getErr
		}
		return nil, err
	}

	// Now we are subscribed. The Save publish either already
	// happened (Get short-circuit) or is yet to happen (channel
	// receive). Both paths are race-safe.
	if resp, err := s.Get(key); err != nil {
		return nil, err
	} else if resp != nil {
		return resp, nil
	}

	ch := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case msg := <-ch:
			if msg == nil {
				// Channel closed. Try one final Get; the holder
				// may have saved between the subscribe and the
				// channel close.
				resp, _ := s.Get(key)
				if resp != nil {
					return resp, nil
				}
				return nil, errors.New("idempotencyredis: pub/sub channel closed without save")
			}
			// "saved" notification fired. The Save in the holder
			// completed before the publish, so a Get now returns
			// the response.
			return s.Get(key)
		}
	}
}

// --- response codec ---

type wireResponse struct {
	Status      int                 `json:"status"`
	Headers     map[string][]string `json:"headers,omitempty"`
	BodyB64     string              `json:"body_b64,omitempty"`
	PayloadHash []byte              `json:"payload_hash,omitempty"`
}

func encodeResponse(resp *idempotency.Response) ([]byte, error) {
	w := wireResponse{
		Status:  resp.StatusCode,
		Headers: resp.Headers,
		BodyB64: base64.StdEncoding.EncodeToString(resp.Body),
	}
	// PayloadHash is a fixed-size array; ship it as a slice on the
	// wire so json marshaling does not produce a stringly-typed
	// numeric array. Decode reverses.
	if resp.PayloadHash != ([32]byte{}) {
		w.PayloadHash = resp.PayloadHash[:]
	}
	return json.Marshal(w)
}

func decodeResponse(raw []byte) (*idempotency.Response, error) {
	var w wireResponse
	if err := json.Unmarshal(raw, &w); err != nil {
		return nil, err
	}
	body, err := base64.StdEncoding.DecodeString(w.BodyB64)
	if err != nil {
		return nil, err
	}
	r := &idempotency.Response{
		StatusCode: w.Status,
		Headers:    http.Header(w.Headers),
		Body:       body,
	}
	if len(w.PayloadHash) == 32 {
		copy(r.PayloadHash[:], w.PayloadHash)
	} else if len(w.PayloadHash) != 0 {
		return nil, fmt.Errorf("idempotencyredis: invalid PayloadHash length %d", len(w.PayloadHash))
	}
	return r, nil
}
