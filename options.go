package aarv

import (
	"crypto/tls"
	"log/slog"
	"time"
)

// Option configures the App.
type Option func(*App)

// WithCodec sets the JSON codec.
func WithCodec(codec Codec) Option {
	return func(a *App) { a.codec = codec }
}

// WithLogger sets the structured logger.
func WithLogger(logger *slog.Logger) Option {
	return func(a *App) { a.logger = logger }
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
	return func(a *App) { a.config.TrustedProxies = cidrs }
}

// WithDisableHTTP2 disables HTTP/2.
func WithDisableHTTP2(disabled bool) Option {
	return func(a *App) { a.config.DisableHTTP2 = disabled }
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

// Config holds server configuration values.
type Config struct {
	ReadTimeout          time.Duration
	ReadHeaderTimeout    time.Duration
	WriteTimeout         time.Duration
	IdleTimeout          time.Duration
	ShutdownTimeout      time.Duration
	MaxHeaderBytes       int
	MaxBodySize          int64
	TLSConfig            *tls.Config
	TrustedProxies       []string
	DisableHTTP2         bool
	Banner               bool
	Debug                bool
	RedirectTrailingSlash bool
}

func defaultConfig() *Config {
	return &Config{
		ReadTimeout:       15 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		ShutdownTimeout:   30 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1 MB
		MaxBodySize:       4 << 20, // 4 MB
		Banner:            true,
	}
}
