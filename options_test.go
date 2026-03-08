package aarv

import (
	"crypto/tls"
	"log/slog"
	"testing"
	"time"
)

func TestOptions(t *testing.T) {
	tlsCfg := &tls.Config{}
	logger := slog.Default()
	errHandler := func(c *Context, err error) {}
	codec := failingCodec{}

	opts := []Option{
		WithCodec(codec),
		WithLogger(logger),
		WithErrorHandler(errHandler),
		WithReadTimeout(1 * time.Second),
		WithWriteTimeout(2 * time.Second),
		WithIdleTimeout(3 * time.Second),
		WithReadHeaderTimeout(4 * time.Second),
		WithShutdownTimeout(5 * time.Second),
		WithMaxHeaderBytes(1024),
		WithMaxBodySize(2048),
		WithTLSConfig(tlsCfg),
		WithTrustedProxies("127.0.0.1"),
		WithDisableHTTP2(true),
		WithBanner(false),
		WithDebug(true),
		WithRedirectTrailingSlash(true),
	}

	app := New(opts...)
	cfg := app.config

	if cfg.ReadTimeout != 1*time.Second {
		t.Errorf("expected read timeout 1s, got %v", cfg.ReadTimeout)
	}
	if app.codec != codec {
		t.Errorf("expected codec to be applied")
	}
	if cfg.WriteTimeout != 2*time.Second {
		t.Errorf("expected write timeout 2s, got %v", cfg.WriteTimeout)
	}
	if cfg.IdleTimeout != 3*time.Second {
		t.Errorf("expected idle timeout 3s, got %v", cfg.IdleTimeout)
	}
	if cfg.ReadHeaderTimeout != 4*time.Second {
		t.Errorf("expected read header timeout 4s, got %v", cfg.ReadHeaderTimeout)
	}
	if cfg.ShutdownTimeout != 5*time.Second {
		t.Errorf("expected shutdown timeout 5s, got %v", cfg.ShutdownTimeout)
	}
	if cfg.MaxHeaderBytes != 1024 {
		t.Errorf("expected max header bytes 1024, got %v", cfg.MaxHeaderBytes)
	}
	if cfg.MaxBodySize != 2048 {
		t.Errorf("expected max body size 2048, got %v", cfg.MaxBodySize)
	}
	if cfg.TLSConfig != tlsCfg {
		t.Errorf("expected tls config to match")
	}
	if len(cfg.TrustedProxies) != 1 || cfg.TrustedProxies[0] != "127.0.0.1" {
		t.Errorf("expected trusted proxies to match")
	}
	if !cfg.DisableHTTP2 {
		t.Errorf("expected disable http2 to be true")
	}
	if cfg.Banner {
		t.Errorf("expected banner to be false")
	}
	if !cfg.Debug {
		t.Errorf("expected debug to be true")
	}
	if !cfg.RedirectTrailingSlash {
		t.Errorf("expected redirect trailing slash to be true")
	}
}
