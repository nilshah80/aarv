package aarv

import (
	"crypto/tls"
	"log/slog"
	"time"
)

// Option configures the App.
type Option func(*App)

func (a *App) setCodec(codec Codec) {
	if codec == nil {
		return
	}
	a.codec = codec
	a.codecDecode = codec.Decode
	a.codecEncode = codec.Encode
	a.codecMarshal = codec.MarshalBytes
	a.codecUnmarshal = codec.UnmarshalBytes
	a.codecContentType = codec.ContentType()
}

// WithCodec sets the JSON codec.
func WithCodec(codec Codec) Option {
	return func(a *App) {
		a.setCodec(codec)
	}
}

// WithLogger sets the structured logger.
func WithLogger(logger *slog.Logger) Option {
	return func(a *App) {
		if logger != nil {
			a.logger = logger
		}
	}
}

// WithErrorHandler sets a custom error handler.
func WithErrorHandler(fn ErrorHandler) Option {
	return func(a *App) { a.errorHandler = fn }
}

// WithReadTimeout sets the server read timeout.
func WithReadTimeout(d time.Duration) Option {
	return func(a *App) { a.config.ReadTimeout = d }
}

// WithWriteTimeout sets the server write timeout.
func WithWriteTimeout(d time.Duration) Option {
	return func(a *App) { a.config.WriteTimeout = d }
}

// WithIdleTimeout sets the server idle timeout.
func WithIdleTimeout(d time.Duration) Option {
	return func(a *App) { a.config.IdleTimeout = d }
}

// WithReadHeaderTimeout sets the server read header timeout.
func WithReadHeaderTimeout(d time.Duration) Option {
	return func(a *App) { a.config.ReadHeaderTimeout = d }
}

// WithShutdownTimeout sets the graceful shutdown timeout.
func WithShutdownTimeout(d time.Duration) Option {
	return func(a *App) { a.config.ShutdownTimeout = d }
}

// WithMaxHeaderBytes sets the max header bytes.
func WithMaxHeaderBytes(n int) Option {
	return func(a *App) { a.config.MaxHeaderBytes = n }
}

// WithMaxBodySize sets the global max body size.
func WithMaxBodySize(n int64) Option {
	return func(a *App) { a.config.MaxBodySize = n }
}

// WithTLSConfig sets a custom TLS configuration.
func WithTLSConfig(cfg *tls.Config) Option {
	return func(a *App) { a.config.TLSConfig = cfg }
}

// WithTrustedProxies sets the trusted proxy CIDRs for IP extraction.
func WithTrustedProxies(cidrs ...string) Option {
	return func(a *App) {
		a.config.TrustedProxies = cidrs
		a.rebuildTrustedProxies()
	}
}

// WithDisableHTTP2 disables HTTP/2.
func WithDisableHTTP2(disabled bool) Option {
	return func(a *App) { a.config.DisableHTTP2 = disabled }
}

// WithCertReload enables periodic re-loading of the cert/key files passed to
// ListenTLS or ListenMutualTLS. The reloader polls each file's (ModTime,
// Size); when either changes on either file, the cert is re-loaded and
// served to subsequent TLS handshakes via tls.Config.GetCertificate.
//
// interval is the poll cadence. Zero means use the default (30s). The
// minimum is 1s, applied after the default substitution.
//
// For ListenMutualTLS this reloads server cert/key only — the client CA
// file is loaded once at startup.
//
// Combining WithCertReload with a caller-supplied
// TLSConfig.GetCertificate causes ListenTLS / ListenMutualTLS to return
// ErrCertReloadConflict before serving.
//
// On plain Listen (HTTP), WithCertReload logs one warning and is otherwise
// a no-op.
func WithCertReload(interval time.Duration) Option {
	return func(a *App) {
		a.config.CertReloadEnabled = true
		a.config.CertReloadInterval = interval
	}
}

// WithBanner enables or disables the startup banner.
func WithBanner(enabled bool) Option {
	return func(a *App) { a.config.Banner = enabled }
}

// WithDebug enables verbose debug logging.
func WithDebug(enabled bool) Option {
	return func(a *App) { a.config.Debug = enabled }
}

// WithRedirectTrailingSlash enables trailing slash redirect.
func WithRedirectTrailingSlash(enabled bool) Option {
	return func(a *App) { a.config.RedirectTrailingSlash = enabled }
}

// WithRequestContextBridge controls whether Aarv clones requests to keep the
// framework Context attached through raw r.WithContext(...) middleware chains.
// Disable it only for middleware stacks that never rely on aarv.FromRequest(...)
// after cloning requests, in exchange for a slightly cheaper hot path.
func WithRequestContextBridge(enabled bool) Option {
	return func(a *App) { a.config.RequestContextBridge = enabled }
}

// Config holds server configuration values.
type Config struct {
	// ReadTimeout is the maximum duration for reading the entire request.
	ReadTimeout time.Duration
	// ReadHeaderTimeout is the maximum duration for reading request headers.
	ReadHeaderTimeout time.Duration
	// WriteTimeout is the maximum duration before timing out response writes.
	WriteTimeout time.Duration
	// IdleTimeout is the maximum keep-alive idle time between requests.
	IdleTimeout time.Duration
	// ShutdownTimeout is the maximum time allowed for graceful shutdown.
	ShutdownTimeout time.Duration
	// MaxHeaderBytes limits the size of incoming request headers.
	MaxHeaderBytes int
	// MaxBodySize is the default request body limit applied by the framework.
	MaxBodySize int64
	// TLSConfig provides the TLS configuration used by HTTPS listeners.
	TLSConfig *tls.Config
	// TrustedProxies contains CIDRs or IPs whose forwarding headers are trusted.
	TrustedProxies []string
	// DisableHTTP2 forces HTTPS listeners to serve HTTP/1.1 only.
	DisableHTTP2 bool
	// Banner controls whether the startup banner is printed.
	Banner bool
	// Debug enables framework-level debug behavior where supported.
	Debug bool
	// RedirectTrailingSlash enables redirects between slash and non-slash route variants.
	RedirectTrailingSlash bool
	// RequestContextBridge clones requests to keep Aarv Context available through
	// raw r.WithContext(...) middleware chains. Disable only for fully opt-in
	// performance-sensitive stacks that do not need that compatibility.
	RequestContextBridge bool
	// CertReloadEnabled toggles cert/key file hot-reload for ListenTLS and
	// ListenMutualTLS. Set via WithCertReload.
	CertReloadEnabled bool
	// CertReloadInterval is the poll cadence for cert hot-reload. Zero means
	// use the default (30s); the minimum 1s is applied after the default.
	CertReloadInterval time.Duration
}

func defaultConfig() *Config {
	return &Config{
		ReadTimeout:          15 * time.Second,
		ReadHeaderTimeout:    5 * time.Second,
		WriteTimeout:         15 * time.Second,
		IdleTimeout:          60 * time.Second,
		ShutdownTimeout:      30 * time.Second,
		MaxHeaderBytes:       1 << 20, // 1 MB
		MaxBodySize:          4 << 20, // 4 MB
		Banner:               true,
		RequestContextBridge: true,
	}
}
