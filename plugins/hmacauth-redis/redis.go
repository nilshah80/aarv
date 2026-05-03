// Package hmacauthredis is the Redis-backed NonceStore for the
// hmacauth plugin. Use it in multi-instance deployments so replay
// protection is shared across processes.
//
// # Wiring
//
//	rdb := redis.NewClient(&redis.Options{Addr: "redis:6379"})
//	store := hmacauthredis.New(hmacauthredis.Config{Client: rdb})
//	mw := hmacauth.New(hmacauth.Config{
//	    Validator:  myValidator,
//	    NonceStore: store,
//	    SkewSeconds: 300,
//	})
//	app.Use(requestid.New(...))
//	app.Use(recover.New(...))
//	app.Use(bodylimit.New(bodylimit.Config{MaxBytes: 1 << 20}))
//	app.Use(mw)
//
// The store does NOT manage the *redis.Client lifecycle; close it
// from your existing shutdown path.
//
// # Atomicity
//
// SetNX uses Redis's `SET key value NX EX ttl` primitive, which is
// a single atomic operation. Two simultaneous SetNX calls for the
// same key from different processes return fresh=true at most once.
package hmacauthredis

import (
	"context"
	"fmt"
	"time"

	"github.com/nilshah80/aarv/plugins/hmacauth"
	"github.com/redis/go-redis/v9"
)

// Compile-time check that Store satisfies hmacauth.NonceStore. If
// the upstream interface changes shape, this file fails to build at
// the same time as hmacauth — preventing a silent decoupling.
var _ hmacauth.NonceStore = (*Store)(nil)

// Config holds the Redis-backed NonceStore configuration.
type Config struct {
	// Client is the redis client to use. Required.
	Client *redis.Client

	// KeyPrefix is prepended to every nonce key. Defaults to
	// "aarv:hmacauth:nonce:". Configure when running multiple
	// applications against a shared Redis so namespaces do not
	// collide.
	KeyPrefix string
}

// DefaultKeyPrefix is the prefix prepended to every nonce key when
// Config.KeyPrefix is empty.
const DefaultKeyPrefix = "aarv:hmacauth:nonce:"

// Store implements hmacauth.NonceStore over Redis.
type Store struct {
	client *redis.Client
	prefix string
}

// New constructs a Redis-backed nonce store. Panics on a nil client
// — silently degrading to a no-op store would defeat replay
// protection.
func New(cfg Config) *Store {
	if cfg.Client == nil {
		panic("hmacauthredis: Config.Client is required")
	}
	prefix := cfg.KeyPrefix
	if prefix == "" {
		prefix = DefaultKeyPrefix
	}
	return &Store{
		client: cfg.Client,
		prefix: prefix,
	}
}

// SetNX implements hmacauth.NonceStore.SetNX. It returns
// (true, nil) when the nonce was previously unseen and was inserted
// for ttl, (false, nil) when the nonce already exists, and
// (false, err) on transport / context errors.
//
// The fresh=false path covers the duplicate-nonce case (a replay
// attempt). The middleware translates that into a generic 401 so
// no information about the cause leaks externally.
func (s *Store) SetNX(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	if ttl <= 0 {
		// Redis SET EX treats zero as "no TTL"; that would leak
		// nonces forever. Reject at the boundary so configuration
		// drift surfaces immediately rather than silently accumulating
		// keys.
		return false, fmt.Errorf("hmacauthredis: ttl must be > 0, got %v", ttl)
	}
	full := s.prefix + key
	// Value is "1" — the nonce contents themselves are encoded in
	// the key. The value is just a presence marker.
	ok, err := s.client.SetNX(ctx, full, "1", ttl).Result()
	if err != nil {
		return false, err
	}
	return ok, nil
}
