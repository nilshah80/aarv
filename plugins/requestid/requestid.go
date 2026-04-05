// Package requestid provides request ID middleware for the aarv framework.
//
// It generates or propagates a unique request identifier for each HTTP request.
// The ID is read from an incoming header (default X-Request-ID) or generated
// as a ULID (Universally Unique Lexicographically Sortable Identifier),
// then set on the response header and stored in the request context for
// downstream handlers.
package requestid

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	fastrand "math/rand/v2"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nilshah80/aarv"
)

// contextKey is the type used for storing the request ID in context.Context.
type contextKey struct{}

// Config holds configuration for the request ID middleware.
type Config struct {
	// Header is the HTTP header name to read/write the request ID.
	// Default: "X-Request-ID".
	Header string

	// Generator is a function that returns a new unique request ID.
	// Default: ULID generator.
	Generator func() string
}

// DefaultConfig returns the default request ID configuration.
func DefaultConfig() Config {
	return Config{
		Header:    "X-Request-ID",
		Generator: GenerateULID,
	}
}

// FastConfig returns the request ID configuration optimized for throughput.
// It keeps the default header name but switches generation to FastGenerator.
func FastConfig() Config {
	return Config{
		Header:    "X-Request-ID",
		Generator: FastGenerator,
	}
}

// ulidState packs the last-seen millisecond (upper 48 bits) and a monotonic
// counter (lower 16 bits) into a single uint64 for lock-free CAS updates.
var ulidState atomic.Uint64

// fastRandPool is a pool of independently-seeded ChaCha8 PRNGs.
// Each goroutine gets its own PRNG instance, avoiding both the cost of
// crypto/rand syscalls and mutex contention from a shared generator.
var fastRandPool = sync.Pool{
	New: func() any {
		var seed [32]byte
		if _, err := rand.Read(seed[:]); err != nil {
			panic("requestid: failed to seed fast PRNG: " + err.Error())
		}
		return fastrand.New(fastrand.NewChaCha8(seed))
	},
}

// ulidEncoding is Crockford's Base32 encoding.
const ulidEncoding = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// GenerateULID generates a ULID (Universally Unique Lexicographically Sortable Identifier).
// Format: 10 characters timestamp (48-bit ms) + 16 characters randomness (80-bit).
// This is the default generator and uses crypto/rand for strong randomness.
// For higher throughput at the cost of weaker randomness, use FastGenerator.
func GenerateULID() string {
	var ulid [26]byte

	// Timestamp: 48-bit milliseconds since Unix epoch
	ms := time.Now().UnixMilli()
	msU48 := uint64(ms) & 0xFFFFFFFFFFFF

	// Lock-free CAS: upper 48 bits = millisecond, lower 16 bits = counter.
	var counter uint16
	for {
		old := ulidState.Load()
		oldMs := old >> 16
		var newState uint64
		if msU48 == oldMs {
			newState = (msU48 << 16) | uint64(uint16(old)+1)
		} else {
			var b [2]byte
			_, _ = rand.Read(b[:])
			newState = (msU48 << 16) | uint64(binary.BigEndian.Uint16(b[:]))
		}
		if ulidState.CompareAndSwap(old, newState) {
			counter = uint16(newState)
			break
		}
	}

	// Encode timestamp (10 chars, 5 bits each = 50 bits, we use 48)
	ulid[0] = ulidEncoding[(ms>>45)&0x1F]
	ulid[1] = ulidEncoding[(ms>>40)&0x1F]
	ulid[2] = ulidEncoding[(ms>>35)&0x1F]
	ulid[3] = ulidEncoding[(ms>>30)&0x1F]
	ulid[4] = ulidEncoding[(ms>>25)&0x1F]
	ulid[5] = ulidEncoding[(ms>>20)&0x1F]
	ulid[6] = ulidEncoding[(ms>>15)&0x1F]
	ulid[7] = ulidEncoding[(ms>>10)&0x1F]
	ulid[8] = ulidEncoding[(ms>>5)&0x1F]
	ulid[9] = ulidEncoding[ms&0x1F]

	// Randomness: 80 bits = 16 chars
	// Use counter in first 16 bits, rest random
	var randomness [10]byte
	binary.BigEndian.PutUint16(randomness[:2], counter)
	_, _ = rand.Read(randomness[2:])

	// Encode 80 bits (10 bytes) as 16 chars (5 bits each)
	ulid[10] = ulidEncoding[(randomness[0]>>3)&0x1F]
	ulid[11] = ulidEncoding[((randomness[0]&0x07)<<2)|((randomness[1]>>6)&0x03)]
	ulid[12] = ulidEncoding[(randomness[1]>>1)&0x1F]
	ulid[13] = ulidEncoding[((randomness[1]&0x01)<<4)|((randomness[2]>>4)&0x0F)]
	ulid[14] = ulidEncoding[((randomness[2]&0x0F)<<1)|((randomness[3]>>7)&0x01)]
	ulid[15] = ulidEncoding[(randomness[3]>>2)&0x1F]
	ulid[16] = ulidEncoding[((randomness[3]&0x03)<<3)|((randomness[4]>>5)&0x07)]
	ulid[17] = ulidEncoding[randomness[4]&0x1F]
	ulid[18] = ulidEncoding[(randomness[5]>>3)&0x1F]
	ulid[19] = ulidEncoding[((randomness[5]&0x07)<<2)|((randomness[6]>>6)&0x03)]
	ulid[20] = ulidEncoding[(randomness[6]>>1)&0x1F]
	ulid[21] = ulidEncoding[((randomness[6]&0x01)<<4)|((randomness[7]>>4)&0x0F)]
	ulid[22] = ulidEncoding[((randomness[7]&0x0F)<<1)|((randomness[8]>>7)&0x01)]
	ulid[23] = ulidEncoding[(randomness[8]>>2)&0x1F]
	ulid[24] = ulidEncoding[((randomness[8]&0x03)<<3)|((randomness[9]>>5)&0x07)]
	ulid[25] = ulidEncoding[randomness[9]&0x1F]

	return string(ulid[:])
}

// FastGenerator generates a ULID using a process-seeded ChaCha8 PRNG instead
// of crypto/rand. This is significantly faster than GenerateULID because it
// avoids per-request system calls to the kernel CSPRNG. The generated IDs are
// still globally unique and lexicographically sortable, but use weaker
// randomness — suitable for request tracing, not for security tokens.
func FastGenerator() string {
	rng := fastRandPool.Get().(*fastrand.Rand)
	defer fastRandPool.Put(rng)

	var ulid [26]byte

	ms := time.Now().UnixMilli()
	msU48 := uint64(ms) & 0xFFFFFFFFFFFF

	var counter uint16
	for {
		old := ulidState.Load()
		oldMs := old >> 16
		var newState uint64
		if msU48 == oldMs {
			newState = (msU48 << 16) | uint64(uint16(old)+1)
		} else {
			newState = (msU48 << 16) | uint64(uint16(rng.Uint32()))
		}
		if ulidState.CompareAndSwap(old, newState) {
			counter = uint16(newState)
			break
		}
	}

	ulid[0] = ulidEncoding[(ms>>45)&0x1F]
	ulid[1] = ulidEncoding[(ms>>40)&0x1F]
	ulid[2] = ulidEncoding[(ms>>35)&0x1F]
	ulid[3] = ulidEncoding[(ms>>30)&0x1F]
	ulid[4] = ulidEncoding[(ms>>25)&0x1F]
	ulid[5] = ulidEncoding[(ms>>20)&0x1F]
	ulid[6] = ulidEncoding[(ms>>15)&0x1F]
	ulid[7] = ulidEncoding[(ms>>10)&0x1F]
	ulid[8] = ulidEncoding[(ms>>5)&0x1F]
	ulid[9] = ulidEncoding[ms&0x1F]

	var randomness [10]byte
	binary.BigEndian.PutUint16(randomness[:2], counter)
	r64 := rng.Uint64()
	binary.LittleEndian.PutUint64(randomness[2:], r64)

	ulid[10] = ulidEncoding[(randomness[0]>>3)&0x1F]
	ulid[11] = ulidEncoding[((randomness[0]&0x07)<<2)|((randomness[1]>>6)&0x03)]
	ulid[12] = ulidEncoding[(randomness[1]>>1)&0x1F]
	ulid[13] = ulidEncoding[((randomness[1]&0x01)<<4)|((randomness[2]>>4)&0x0F)]
	ulid[14] = ulidEncoding[((randomness[2]&0x0F)<<1)|((randomness[3]>>7)&0x01)]
	ulid[15] = ulidEncoding[(randomness[3]>>2)&0x1F]
	ulid[16] = ulidEncoding[((randomness[3]&0x03)<<3)|((randomness[4]>>5)&0x07)]
	ulid[17] = ulidEncoding[randomness[4]&0x1F]
	ulid[18] = ulidEncoding[(randomness[5]>>3)&0x1F]
	ulid[19] = ulidEncoding[((randomness[5]&0x07)<<2)|((randomness[6]>>6)&0x03)]
	ulid[20] = ulidEncoding[(randomness[6]>>1)&0x1F]
	ulid[21] = ulidEncoding[((randomness[6]&0x01)<<4)|((randomness[7]>>4)&0x0F)]
	ulid[22] = ulidEncoding[((randomness[7]&0x0F)<<1)|((randomness[8]>>7)&0x01)]
	ulid[23] = ulidEncoding[(randomness[8]>>2)&0x1F]
	ulid[24] = ulidEncoding[((randomness[8]&0x03)<<3)|((randomness[9]>>5)&0x07)]
	ulid[25] = ulidEncoding[randomness[9]&0x1F]

	return string(ulid[:])
}

// FromContext extracts the request ID from the given context.
// Returns an empty string if no request ID is present.
func FromContext(ctx context.Context) string {
	if id, ok := ctx.Value(contextKey{}).(string); ok {
		return id
	}
	return ""
}

// New creates a request ID middleware with optional configuration.
// If no config is provided, DefaultConfig is used.
func New(config ...Config) aarv.Middleware {
	cfg := DefaultConfig()
	if len(config) > 0 {
		cfg = config[0]
	}
	if cfg.Header == "" {
		cfg.Header = "X-Request-ID"
	}
	if cfg.Generator == nil {
		cfg.Generator = GenerateULID
	}

	native := aarv.MiddlewareFunc(func(next aarv.HandlerFunc) aarv.HandlerFunc {
		return func(c *aarv.Context) error {
			id := c.Header(cfg.Header)
			if id == "" {
				id = cfg.Generator()
			}

			c.SetHeader(cfg.Header, id)
			c.Set("requestId", id)
			c.SetContextValue(contextKey{}, id)

			return next(c)
		}
	})

	m := aarv.Middleware(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Read existing request ID from header or generate a new one
			id := r.Header.Get(cfg.Header)
			if id == "" {
				id = cfg.Generator()
			}

			// Set the request ID on the response header
			w.Header().Set(cfg.Header, id)

			// Store the request ID in the request context and keep aarv's
			// request-to-context mapping aligned if the request gets cloned.
			if c, ok := aarv.FromRequest(r); ok {
				c.Set("requestId", id)
				c.SetContextValue(contextKey{}, id)
				r = c.RawRequest()
			} else {
				ctx := context.WithValue(r.Context(), contextKey{}, id)
				r = r.WithContext(ctx)
			}

			next.ServeHTTP(w, r)
		})
	})
	return aarv.RegisterNativeMiddleware(m, native)
}
