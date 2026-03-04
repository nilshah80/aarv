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
	"net/http"
	"sync"
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

// ULID generator state
var (
	ulidMu      sync.Mutex
	ulidLastMs  int64
	ulidCounter uint16
)

// ulidEncoding is Crockford's Base32 encoding.
const ulidEncoding = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// GenerateULID generates a ULID (Universally Unique Lexicographically Sortable Identifier).
// Format: 10 characters timestamp (48-bit ms) + 16 characters randomness (80-bit).
func GenerateULID() string {
	var ulid [26]byte

	// Timestamp: 48-bit milliseconds since Unix epoch
	ms := time.Now().UnixMilli()

	ulidMu.Lock()
	// Increment counter if same millisecond, else reset
	if ms == ulidLastMs {
		ulidCounter++
	} else {
		ulidLastMs = ms
		// Random counter start for new millisecond
		var b [2]byte
		_, _ = rand.Read(b[:])
		ulidCounter = binary.BigEndian.Uint16(b[:])
	}
	counter := ulidCounter
	ulidMu.Unlock()

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

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Read existing request ID from header or generate a new one
			id := r.Header.Get(cfg.Header)
			if id == "" {
				id = cfg.Generator()
			}

			// Set the request ID on the response header
			w.Header().Set(cfg.Header, id)

			// Store the request ID in the request context
			ctx := context.WithValue(r.Context(), contextKey{}, id)
			r = r.WithContext(ctx)

			// Also store in aarv Context if available (so c.RequestID() works)
			if c, ok := aarv.FromRequest(r); ok {
				c.Set("requestId", id)
			}

			next.ServeHTTP(w, r)
		})
	}
}
