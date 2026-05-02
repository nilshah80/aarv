package h2c

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nilshah80/aarv"
	"golang.org/x/net/http2"
)

func newSilentApp(opts ...aarv.Option) *aarv.App {
	base := []aarv.Option{
		aarv.WithBanner(false),
		aarv.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	}
	return aarv.New(append(base, opts...)...)
}

// startWrappedListener wraps handler via Wrap(cfg) and serves it on a
// random localhost port. Returns the listener URL and a cleanup func.
func startWrappedListener(t *testing.T, handler http.Handler, cfg Config) (string, func()) {
	t.Helper()
	wrapped, err := Wrap(handler, cfg)
	if err != nil {
		t.Fatalf("Wrap: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: wrapped}
	served := make(chan struct{})
	go func() {
		_ = srv.Serve(ln)
		close(served)
	}()
	cleanup := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		<-served
	}
	return "http://" + ln.Addr().String(), cleanup
}

func TestWrapInvalidFrameSizeReturnsError(t *testing.T) {
	cases := []uint32{1, minFrameSize - 1, maxFrameSize + 1}
	for _, fs := range cases {
		t.Run(fmt.Sprintf("frame_%d", fs), func(t *testing.T) {
			_, err := Wrap(http.NotFoundHandler(), Config{MaxReadFrameSize: fs})
			if !errors.Is(err, ErrInvalidFrameSize) {
				t.Fatalf("expected ErrInvalidFrameSize, got %v", err)
			}
		})
	}
}

func TestWrapAcceptsBoundaryFrameSizes(t *testing.T) {
	for _, fs := range []uint32{minFrameSize, maxFrameSize} {
		if _, err := Wrap(http.NotFoundHandler(), Config{MaxReadFrameSize: fs}); err != nil {
			t.Fatalf("frame %d: %v", fs, err)
		}
	}
}

func TestWrapZeroFrameSizeUsesDefault(t *testing.T) {
	if _, err := Wrap(http.NotFoundHandler(), Config{}); err != nil {
		t.Fatalf("zero MaxReadFrameSize must use default, got %v", err)
	}
}

func TestBuildH2ServerAppliesConfig(t *testing.T) {
	srv, err := buildH2Server(Config{
		MaxConcurrentStreams: 7,
		MaxReadFrameSize:     32 * 1024,
		IdleTimeout:          15 * time.Second,
	})
	if err != nil {
		t.Fatalf("buildH2Server: %v", err)
	}
	if srv.MaxConcurrentStreams != 7 {
		t.Errorf("MaxConcurrentStreams: got %d", srv.MaxConcurrentStreams)
	}
	if srv.MaxReadFrameSize != 32*1024 {
		t.Errorf("MaxReadFrameSize: got %d", srv.MaxReadFrameSize)
	}
	if srv.IdleTimeout != 15*time.Second {
		t.Errorf("IdleTimeout: got %v", srv.IdleTimeout)
	}
}

func TestBuildH2ServerDefaults(t *testing.T) {
	srv, err := buildH2Server(Config{})
	if err != nil {
		t.Fatalf("buildH2Server: %v", err)
	}
	if srv.MaxConcurrentStreams != defaultMaxConcurrentStreams {
		t.Errorf("MaxConcurrentStreams default: got %d", srv.MaxConcurrentStreams)
	}
	if srv.MaxReadFrameSize != defaultFrameSize {
		t.Errorf("MaxReadFrameSize default: got %d", srv.MaxReadFrameSize)
	}
}

func TestBuildHTTPServerCopiesTimeouts(t *testing.T) {
	cfg := Config{
		IdleTimeout:       time.Second,
		ReadTimeout:       2 * time.Second,
		ReadHeaderTimeout: 3 * time.Second,
		WriteTimeout:      4 * time.Second,
		MaxHeaderBytes:    8192,
	}
	srv := buildHTTPServer(":443", http.NotFoundHandler(), cfg)
	if srv.Addr != ":443" {
		t.Errorf("Addr: got %q", srv.Addr)
	}
	if srv.IdleTimeout != cfg.IdleTimeout || srv.ReadTimeout != cfg.ReadTimeout ||
		srv.ReadHeaderTimeout != cfg.ReadHeaderTimeout || srv.WriteTimeout != cfg.WriteTimeout ||
		srv.MaxHeaderBytes != cfg.MaxHeaderBytes {
		t.Fatalf("server timeouts not copied: %#v", srv)
	}
}

func TestWrapServesHTTP1AndHTTP2OnSameListener(t *testing.T) {
	var (
		h1Hits atomic.Int32
		h2Hits atomic.Int32
	)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.ProtoMajor {
		case 1:
			h1Hits.Add(1)
		case 2:
			h2Hits.Add(1)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, r.Proto)
	})

	url, cleanup := startWrappedListener(t, handler, Config{})
	defer cleanup()

	// HTTP/1.1 round trip
	resp, err := http.Get(url + "/")
	if err != nil {
		t.Fatalf("http/1.1 get: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.ProtoMajor != 1 || !strings.HasPrefix(string(body), "HTTP/1.") {
		t.Fatalf("expected HTTP/1.x response, got proto=%d body=%q", resp.ProtoMajor, body)
	}

	// HTTP/2 prior-knowledge round trip
	h2Client := &http.Client{
		Transport: &http2.Transport{
			AllowHTTP: true,
			DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, network, addr)
			},
		},
	}
	resp2, err := h2Client.Get(url + "/")
	if err != nil {
		t.Fatalf("http/2 get: %v", err)
	}
	body2, _ := io.ReadAll(resp2.Body)
	_ = resp2.Body.Close()
	if resp2.ProtoMajor != 2 {
		t.Fatalf("expected HTTP/2 response, got proto=%d body=%q", resp2.ProtoMajor, body2)
	}

	if got := h1Hits.Load(); got != 1 {
		t.Errorf("HTTP/1.1 handler hits: got %d want 1", got)
	}
	if got := h2Hits.Load(); got != 1 {
		t.Errorf("HTTP/2 handler hits: got %d want 1", got)
	}
}

func TestListenNilAppReturnsError(t *testing.T) {
	if err := Listen(nil, "127.0.0.1:0", Config{}); !errors.Is(err, ErrNilApp) {
		t.Fatalf("expected ErrNilApp, got %v", err)
	}
}

func TestWrapNilHandlerReturnsError(t *testing.T) {
	if _, err := Wrap(nil, Config{}); !errors.Is(err, ErrNilHandler) {
		t.Fatalf("expected ErrNilHandler, got %v", err)
	}
}

func TestWrapBoundsFirstRequestBody(t *testing.T) {
	const limit = 32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// MaxBytesHandler enforces by failing reads of r.Body that exceed
		// the limit. A handler that drains the body must surface the error
		// to make the cap observable to tests.
		buf, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
			return
		}
		_, _ = w.Write(buf)
	})

	url, cleanup := startWrappedListener(t, handler, Config{MaxFirstRequestBytes: limit})
	defer cleanup()

	// Body just under the cap should succeed.
	resp, err := http.Post(url+"/", "application/octet-stream", strings.NewReader(strings.Repeat("a", limit)))
	if err != nil {
		t.Fatalf("under-cap post: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || len(body) != limit {
		t.Fatalf("under-cap: status=%d len=%d", resp.StatusCode, len(body))
	}

	// Body over the cap must be rejected.
	resp2, err := http.Post(url+"/", "application/octet-stream", strings.NewReader(strings.Repeat("b", limit*4)))
	if err != nil {
		t.Fatalf("over-cap post: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp2.Body)
	_ = resp2.Body.Close()
	if resp2.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("over-cap: expected 413, got %d", resp2.StatusCode)
	}
}

func TestWrapNegativeMaxFirstRequestBytesDisablesCap(t *testing.T) {
	wrapped, err := Wrap(http.NotFoundHandler(), Config{MaxFirstRequestBytes: -1})
	if err != nil {
		t.Fatalf("Wrap: %v", err)
	}
	// When the cap is disabled, Wrap returns the raw h2c handler — i.e.
	// not the *maxBytesHandler wrapper. We can detect this by type.
	typeName := fmt.Sprintf("%T", wrapped)
	if strings.Contains(typeName, "maxBytesHandler") {
		t.Fatalf("expected raw h2c handler when cap disabled, got %s", typeName)
	}
}

func TestListenInvalidFrameSizeAbortsBeforeLifecycle(t *testing.T) {
	app := newSilentApp()
	var startupRan atomic.Bool
	app.AddHook(aarv.OnStartup, func(*aarv.Context) error {
		startupRan.Store(true)
		return nil
	})

	err := Listen(app, "127.0.0.1:0", Config{MaxReadFrameSize: 1})
	if !errors.Is(err, ErrInvalidFrameSize) {
		t.Fatalf("expected ErrInvalidFrameSize, got %v", err)
	}
	if startupRan.Load() {
		t.Fatal("OnStartup must not run when Config validation fails")
	}
}

func TestListenDelegatesToAppLifecycle(t *testing.T) {
	app := newSilentApp()
	abort := errors.New("abort before serving")
	app.AddHook(aarv.OnStartup, func(*aarv.Context) error { return abort })

	if err := Listen(app, "127.0.0.1:0", Config{}); !errors.Is(err, abort) {
		t.Fatalf("expected startup hook error to surface, got %v", err)
	}
}
