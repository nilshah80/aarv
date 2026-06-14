// Package h2c serves HTTP/2 over cleartext (h2c) for internal-mesh and
// sidecar deployments where TLS is terminated upstream (e.g., a service
// mesh sidecar or an internal load balancer). Use [Listen] to run an
// aarv App as an h2c server, or [Wrap] to layer h2c onto any http.Handler.
//
// THREAT MODEL: h2c is cleartext. Never expose an h2c listener to the
// public internet. Run it only behind a trusted TLS terminator on a
// private network. Without TLS, h2c offers no confidentiality, no
// integrity, and no authentication; an on-path attacker can read and
// modify traffic at will.
//
// HTTP/2 stream timeouts: net/http's WriteTimeout terminates a connection
// after the configured duration regardless of stream activity. For long-
// lived gRPC server-streaming or bidirectional streams, leave WriteTimeout
// zero and bound stream lifetime via your application logic instead.
package h2c

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/nilshah80/aarv"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c" //nolint:staticcheck // This plugin intentionally adapts h2c handlers.
)

// HTTP/2 frame-size limits per RFC 7540 §6.5.2 (SETTINGS_MAX_FRAME_SIZE).
const (
	minFrameSize     = 1 << 14       // 16384 — protocol minimum
	maxFrameSize     = (1 << 24) - 1 // 16777215 — protocol maximum
	defaultFrameSize = 1 << 20       // 1 MiB — plugin default

	defaultMaxConcurrentStreams = 250

	// defaultMaxFirstRequestBytes bounds the body of the *first* request
	// on each h2c connection. Per x/net/http2/h2c docs, the first request
	// is read entirely into memory before the handler runs, so an
	// unbounded first request is a remote-memory-exhaustion vector. 1 MiB
	// is generous for the upgrade preface yet small enough to defang the
	// attack on a public endpoint.
	defaultMaxFirstRequestBytes int64 = 1 << 20 // 1 MiB
)

// ErrInvalidFrameSize is returned by [Listen] and [Wrap] when
// Config.MaxReadFrameSize is non-zero and outside the RFC 7540 §6.5.2
// range [16384, 16777215].
var ErrInvalidFrameSize = errors.New("h2c: MaxReadFrameSize must be 0 or in [16384, 16777215]")

// ErrNilHandler is returned by [Wrap] when its handler argument is nil.
var ErrNilHandler = errors.New("h2c: handler is nil")

// ErrNilApp is returned by [Listen] when its app argument is nil.
var ErrNilApp = errors.New("h2c: app is nil")

// Config tunes the embedded *http2.Server and the surrounding *http.Server
// produced by [Listen]. All fields are optional with documented defaults.
type Config struct {
	// MaxConcurrentStreams caps in-flight streams per connection. Zero
	// uses the plugin default (250). Set to 1 for strict per-connection
	// serialization (rarely useful).
	MaxConcurrentStreams uint32

	// MaxReadFrameSize is the largest HTTP/2 frame the server will accept.
	// Zero uses the plugin default (1 MiB). Non-zero values outside the
	// RFC 7540 §6.5.2 range [16384, 16777215] return ErrInvalidFrameSize.
	MaxReadFrameSize uint32

	// IdleTimeout bounds idle keep-alive connection lifetime. Zero leaves
	// the net/http default behavior.
	IdleTimeout time.Duration

	// ReadTimeout bounds full-request read time. Zero means NO read
	// timeout — exposes the listener to slowloris on the connection
	// preface and SETTINGS exchange. Production listeners should set this.
	ReadTimeout time.Duration

	// ReadHeaderTimeout bounds header-read time. Zero means NO header
	// timeout. Same slowloris caveat as ReadTimeout.
	ReadHeaderTimeout time.Duration

	// WriteTimeout bounds full-response write time. Leave zero for gRPC
	// server-streaming / bidirectional streaming; setting it terminates
	// long-lived streams once the timer fires.
	WriteTimeout time.Duration

	// MaxHeaderBytes caps incoming header total size. Zero leaves the
	// net/http default (1 MiB).
	MaxHeaderBytes int

	// MaxFirstRequestBytes caps the body size of the FIRST request on
	// each h2c connection. The upstream x/net/http2/h2c library reads
	// the entire first request into memory before invoking the handler,
	// which is a memory-exhaustion vector for public listeners. Zero
	// uses the plugin default (1 MiB). Set to a negative value to
	// disable the cap entirely (NOT recommended for public listeners).
	MaxFirstRequestBytes int64
}

// Wrap layers h2c upgrade handling onto h, returning an http.Handler that
// serves both HTTP/1.1 (regular) and HTTP/2 prior-knowledge requests on
// the same listener. The result is wrapped in http.MaxBytesHandler bounded
// by cfg.MaxFirstRequestBytes so the first-request memory exposure
// documented by x/net/http2/h2c cannot exhaust the process.
//
// Returns ErrNilHandler when h is nil, or ErrInvalidFrameSize if
// cfg.MaxReadFrameSize is set to an out-of-range value.
//
// Wrap is independent of *aarv.App so it can be used to upgrade non-aarv
// handlers (e.g. a stdlib mux behind a sidecar).
func Wrap(h http.Handler, cfg Config) (http.Handler, error) {
	if h == nil {
		return nil, ErrNilHandler
	}
	h2srv, err := buildH2Server(cfg)
	if err != nil {
		return nil, err
	}
	wrapped := h2c.NewHandler(h, h2srv) //nolint:staticcheck // Keep Wrap(http.Handler) support and first-request limiting.

	limit := cfg.MaxFirstRequestBytes
	switch {
	case limit < 0:
		// Caller explicitly opted out — return the raw h2c handler.
		return wrapped, nil
	case limit == 0:
		limit = defaultMaxFirstRequestBytes
	}
	return http.MaxBytesHandler(wrapped, limit), nil
}

// Listen runs the App as an h2c server bound to addr through
// app.ListenServer, so OnStartup/OnShutdown hooks and graceful shutdown
// fire as they would for HTTPS or plain HTTP.
//
// Config is validated before the lifecycle starts: an out-of-range
// MaxReadFrameSize returns ErrInvalidFrameSize without invoking any
// hook. nil app returns aarv.ErrNilServer-equivalent semantics by
// short-circuiting at the framework's ListenServer guard.
func Listen(app *aarv.App, addr string, cfg Config) error {
	if app == nil {
		return ErrNilApp
	}
	wrapped, err := Wrap(app, cfg)
	if err != nil {
		return err
	}
	srv := buildHTTPServer(addr, wrapped, cfg)
	return app.ListenServer(srv, srv.ListenAndServe, "h2c")
}

func buildH2Server(cfg Config) (*http2.Server, error) {
	frameSize := cfg.MaxReadFrameSize
	switch {
	case frameSize == 0:
		frameSize = defaultFrameSize
	case frameSize < minFrameSize || frameSize > maxFrameSize:
		return nil, fmt.Errorf("%w: got %d", ErrInvalidFrameSize, cfg.MaxReadFrameSize)
	}

	streams := cfg.MaxConcurrentStreams
	if streams == 0 {
		streams = defaultMaxConcurrentStreams
	}

	return &http2.Server{
		MaxConcurrentStreams: streams,
		MaxReadFrameSize:     frameSize,
		IdleTimeout:          cfg.IdleTimeout,
	}, nil
}

func buildHTTPServer(addr string, handler http.Handler, cfg Config) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadTimeout:       cfg.ReadTimeout,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		MaxHeaderBytes:    cfg.MaxHeaderBytes,
	}
}
