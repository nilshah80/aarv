package aarv

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"reflect"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func newSilentApp(opts ...Option) *App {
	base := []Option{WithBanner(false), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))}
	return New(append(base, opts...)...)
}

func TestListenServerNilArgs(t *testing.T) {
	app := newSilentApp()

	if err := app.ListenServer(nil, func() error { return nil }, "test"); !errors.Is(err, ErrNilServer) {
		t.Fatalf("expected ErrNilServer, got %v", err)
	}

	srv := &http.Server{}
	if err := app.ListenServer(srv, nil, "test"); !errors.Is(err, ErrNilServeFunc) {
		t.Fatalf("expected ErrNilServeFunc, got %v", err)
	}
}

func TestListenServerLifecycleOrder(t *testing.T) {
	app := newSilentApp()

	var (
		mu     sync.Mutex
		events []string
	)
	record := func(name string) {
		mu.Lock()
		events = append(events, name)
		mu.Unlock()
	}

	app.AddHook(OnStartup, func(*Context) error {
		record("OnStartup")
		return nil
	})
	app.AddHook(OnShutdown, func(*Context) error {
		record("OnShutdown")
		return nil
	})
	app.OnShutdown(func(interface{ Done() <-chan struct{} }) error {
		record("LegacyShutdown")
		return nil
	})

	srv := &http.Server{Handler: app}
	cleanup := func() { record("cleanup") }
	err := app.listenServerWithCleanup(srv, func() error {
		record("serve")
		return http.ErrServerClosed
	}, "test", cleanup)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("unexpected error: %v", err)
	}

	mu.Lock()
	got := append([]string(nil), events...)
	mu.Unlock()

	// OnShutdown / legacy hooks run before srv.Shutdown so they can act
	// while the listener is still open (drain dependencies, emit
	// "shutting down" notices). Transport-coupled cleanup runs LAST,
	// after the listener has fully drained, so it cannot race in-flight
	// handshakes (the cert-reload Stop case).
	want := []string{"OnStartup", "serve", "OnShutdown", "LegacyShutdown", "cleanup"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("lifecycle order mismatch:\n got:  %v\n want: %v", got, want)
	}
}

func TestListenServerServeReturnsNilStillRunsShutdown(t *testing.T) {
	app := newSilentApp()
	var shutdownCount atomic.Int32
	app.AddHook(OnShutdown, func(*Context) error {
		shutdownCount.Add(1)
		return nil
	})

	srv := &http.Server{Handler: app}
	if err := app.ListenServer(srv, func() error { return http.ErrServerClosed }, "test"); err != nil && !errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := shutdownCount.Load(); got != 1 {
		t.Fatalf("expected OnShutdown to run exactly once, got %d", got)
	}
}

func TestListenServerServeReturnsErrorStillRunsShutdown(t *testing.T) {
	app := newSilentApp()
	var shutdownCount atomic.Int32
	app.AddHook(OnShutdown, func(*Context) error {
		shutdownCount.Add(1)
		return nil
	})

	srv := &http.Server{Handler: app}
	wantErr := errors.New("serve boom")
	err := app.ListenServer(srv, func() error { return wantErr }, "test")
	if err == nil || !errors.Is(err, wantErr) {
		t.Fatalf("expected wrapped serve error, got %v", err)
	}
	if got := shutdownCount.Load(); got != 1 {
		t.Fatalf("expected OnShutdown to run exactly once after serve error, got %d", got)
	}
}

func TestListenServerOnStartupFailureRunsCleanup(t *testing.T) {
	app := newSilentApp()
	app.AddHook(OnStartup, func(*Context) error { return errors.New("startup boom") })

	var cleanupCalled atomic.Bool
	srv := &http.Server{Handler: app}
	err := app.listenServerWithCleanup(srv, func() error { return nil }, "test", func() {
		cleanupCalled.Store(true)
	})
	if err == nil {
		t.Fatal("expected startup error to surface")
	}
	if !cleanupCalled.Load() {
		t.Fatal("cleanup must run when OnStartup fails so transport-coupled resources do not leak")
	}
}

func TestListenServerNilArgsRunCleanup(t *testing.T) {
	app := newSilentApp()
	var cleanupCalled atomic.Bool
	cleanup := func() { cleanupCalled.Store(true) }

	if err := app.listenServerWithCleanup(nil, func() error { return nil }, "test", cleanup); !errors.Is(err, ErrNilServer) {
		t.Fatalf("expected ErrNilServer, got %v", err)
	}
	if !cleanupCalled.Load() {
		t.Fatal("cleanup must run on ErrNilServer")
	}

	cleanupCalled.Store(false)
	if err := app.listenServerWithCleanup(&http.Server{}, nil, "test", cleanup); !errors.Is(err, ErrNilServeFunc) {
		t.Fatalf("expected ErrNilServeFunc, got %v", err)
	}
	if !cleanupCalled.Load() {
		t.Fatal("cleanup must run on ErrNilServeFunc")
	}
}

func TestListenServerOnStartupFailureAborts(t *testing.T) {
	app := newSilentApp()
	var (
		serveCalled    atomic.Bool
		shutdownCalled atomic.Bool
	)
	app.AddHook(OnStartup, func(*Context) error {
		return errors.New("startup boom")
	})
	app.AddHook(OnShutdown, func(*Context) error {
		shutdownCalled.Store(true)
		return nil
	})

	srv := &http.Server{Handler: app}
	err := app.ListenServer(srv, func() error {
		serveCalled.Store(true)
		return nil
	}, "test")
	if err == nil {
		t.Fatal("expected startup hook error to surface")
	}
	if serveCalled.Load() {
		t.Fatal("serve must not run when OnStartup fails")
	}
	if shutdownCalled.Load() {
		t.Fatal("OnShutdown must not run when OnStartup fails")
	}
}

// TestListenServerCleanupRunsAfterShutdownHooks asserts the post-PR-12.0
// ordering: OnShutdown hooks fire while the listener may still be open;
// transport-coupled cleanup (e.g. CertReloader.Stop) runs after the listener
// has drained.
func TestListenServerCleanupRunsAfterShutdownHooks(t *testing.T) {
	app := newSilentApp()

	var (
		mu     sync.Mutex
		events []string
	)
	record := func(name string) {
		mu.Lock()
		events = append(events, name)
		mu.Unlock()
	}

	app.AddHook(OnShutdown, func(*Context) error {
		record("OnShutdown")
		return nil
	})

	srv := &http.Server{Handler: app}
	if err := app.listenServerWithCleanup(srv, func() error {
		return http.ErrServerClosed
	}, "test", func() { record("cleanup") }); err != nil && !errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("unexpected error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(events) != 2 || events[0] != "OnShutdown" || events[1] != "cleanup" {
		t.Fatalf("expected OnShutdown before cleanup, got %v", events)
	}
}

func TestListenServerSetsServerForExternalShutdown(t *testing.T) {
	app := newSilentApp()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: app}

	done := make(chan error, 1)
	go func() {
		done <- app.ListenServer(srv, func() error { return srv.Serve(listener) }, "test")
	}()

	// Wait until ListenServer has stored the server pointer.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && app.getServer() == nil {
		time.Sleep(5 * time.Millisecond)
	}
	if app.getServer() == nil {
		t.Fatal("ListenServer must call setServer before launching serve")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := app.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("ListenServer returned unexpected error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ListenServer did not return after Shutdown")
	}
}

func TestTLSConfigCloneIsolation(t *testing.T) {
	base := &tls.Config{MinVersion: tls.VersionTLS13}
	app := newSilentApp(WithTLSConfig(base))

	a := app.TLSConfig()
	b := app.TLSConfig()
	if a == b {
		t.Fatal("TLSConfig must return a fresh clone per call")
	}
	a.MinVersion = tls.VersionTLS10
	if app.TLSConfig().MinVersion != tls.VersionTLS13 {
		t.Fatal("mutating returned clone must not affect subsequent calls")
	}
	if base.MinVersion != tls.VersionTLS13 {
		t.Fatal("mutating returned clone must not affect base config")
	}
}

func TestTLSConfigEnforcesMinTLS12(t *testing.T) {
	base := &tls.Config{MinVersion: tls.VersionTLS10}
	app := newSilentApp(WithTLSConfig(base))

	cfg := app.TLSConfig()
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Fatalf("expected MinVersion floored to TLS 1.2, got %v", cfg.MinVersion)
	}
}

func TestTLSConfigDisableHTTP2ForcesExactSlice(t *testing.T) {
	t.Run("nil NextProtos is replaced", func(t *testing.T) {
		app := newSilentApp(WithDisableHTTP2(true))
		cfg := app.TLSConfig()
		if !reflect.DeepEqual(cfg.NextProtos, []string{"http/1.1"}) {
			t.Fatalf("expected exact [\"http/1.1\"] when HTTP/2 disabled, got %#v", cfg.NextProtos)
		}
	})

	t.Run("h2 in user config is overwritten", func(t *testing.T) {
		base := &tls.Config{NextProtos: []string{"h2", "http/1.1"}}
		app := newSilentApp(WithTLSConfig(base), WithDisableHTTP2(true))
		cfg := app.TLSConfig()
		if !reflect.DeepEqual(cfg.NextProtos, []string{"http/1.1"}) {
			t.Fatalf("expected NextProtos forced to [\"http/1.1\"] regardless of user config, got %#v", cfg.NextProtos)
		}
	})

	t.Run("custom protos preserved when HTTP/2 not disabled", func(t *testing.T) {
		base := &tls.Config{NextProtos: []string{"h2", "http/1.1"}}
		app := newSilentApp(WithTLSConfig(base))
		cfg := app.TLSConfig()
		if !reflect.DeepEqual(cfg.NextProtos, []string{"h2", "http/1.1"}) {
			t.Fatalf("expected NextProtos preserved, got %#v", cfg.NextProtos)
		}
	})
}

func TestListenServerDisableHTTP2SetsServerPolicy(t *testing.T) {
	app := newSilentApp(WithDisableHTTP2(true))
	srv := &http.Server{Handler: app, TLSConfig: app.TLSConfig()}
	var policyErr error

	if err := app.ListenServer(srv, func() error {
		if srv.TLSNextProto == nil {
			policyErr = errors.New("expected TLSNextProto empty map when HTTP/2 is disabled")
		} else if len(srv.TLSNextProto) != 0 {
			policyErr = fmt.Errorf("expected empty TLSNextProto map, got %#v", srv.TLSNextProto)
		}
		return nil
	}, "test"); err != nil {
		t.Fatalf("ListenServer: %v", err)
	}
	if policyErr != nil {
		t.Fatal(policyErr)
	}
}

func TestListenServerDoesNotSetServerPolicyWhenHTTP2Allowed(t *testing.T) {
	app := newSilentApp()
	srv := &http.Server{Handler: app, TLSConfig: app.TLSConfig()}

	if err := app.ListenServer(srv, func() error { return nil }, "test"); err != nil {
		t.Fatalf("ListenServer: %v", err)
	}
	if srv.TLSNextProto != nil {
		t.Fatalf("TLSNextProto should remain nil when HTTP/2 is allowed, got %#v", srv.TLSNextProto)
	}
}

func TestListenServerPreservesUserTLSNextProto(t *testing.T) {
	app := newSilentApp(WithDisableHTTP2(true))
	customCalled := false
	custom := map[string]func(*http.Server, *tls.Conn, http.Handler){
		"my-proto/1": func(*http.Server, *tls.Conn, http.Handler) {
			customCalled = true
		},
	}
	srv := &http.Server{Handler: app, TLSConfig: app.TLSConfig(), TLSNextProto: custom}

	if err := app.ListenServer(srv, func() error { return nil }, "test"); err != nil {
		t.Fatalf("ListenServer: %v", err)
	}
	// User's TLSNextProto must be preserved verbatim — the framework must
	// not clobber a caller-supplied upgrade-protocol policy even when
	// WithDisableHTTP2 is set.
	if _, ok := srv.TLSNextProto["my-proto/1"]; !ok {
		t.Fatalf("user TLSNextProto entry was clobbered, got %#v", srv.TLSNextProto)
	}
	_ = customCalled
}

func TestMutualTLSConfigSetsClientAuth(t *testing.T) {
	app := newSilentApp()
	cfg := app.MutualTLSConfig()
	if cfg.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Fatalf("expected ClientAuth = RequireAndVerifyClientCert, got %v", cfg.ClientAuth)
	}
	// Sanity: still floors MinVersion.
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Fatalf("expected MinVersion floored to TLS 1.2, got %v", cfg.MinVersion)
	}
}

// TestListenServerServeReturnsTrueNilStillRunsShutdown exercises the explicit
// nil-return path. The earlier serve-returns-nil test returned
// http.ErrServerClosed (which the goroutine filters); this returns plain nil.
func TestListenServerServeReturnsTrueNilStillRunsShutdown(t *testing.T) {
	app := newSilentApp()
	var shutdownCount atomic.Int32
	app.AddHook(OnShutdown, func(*Context) error {
		shutdownCount.Add(1)
		return nil
	})

	srv := &http.Server{Handler: app}
	if err := app.ListenServer(srv, func() error { return nil }, "test"); err != nil {
		t.Fatalf("expected nil error from serve returning nil, got %v", err)
	}
	if got := shutdownCount.Load(); got != 1 {
		t.Fatalf("expected OnShutdown to run exactly once on nil-return, got %d", got)
	}
}

// TestListenServerSignalPathClosesListenerBeforeCleanup verifies the fixed
// ordering: on SIGTERM the listener is shut down first, the serve goroutine
// drains, THEN cleanup runs (so e.g. CertReloader.Stop cannot race against
// in-flight TLS handshakes calling GetCertificate).
func TestListenServerSignalPathClosesListenerBeforeCleanup(t *testing.T) {
	if !canRaiseSelfSignal {
		t.Skip("self-signal not available on this platform")
	}
	app := newSilentApp()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	var (
		mu     sync.Mutex
		events []string
	)
	record := func(name string) {
		mu.Lock()
		events = append(events, name)
		mu.Unlock()
	}

	cleanup := func() { record("cleanup") }
	app.AddHook(OnShutdown, func(*Context) error {
		record("OnShutdown")
		return nil
	})

	srv := &http.Server{Handler: app}
	done := make(chan error, 1)
	go func() {
		done <- app.listenServerWithCleanup(srv, func() error {
			record("serve-start")
			defer record("serve-return")
			return srv.Serve(listener)
		}, "test", cleanup)
	}()

	// Wait until serve has actually started accepting.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		started := len(events) > 0 && events[0] == "serve-start"
		mu.Unlock()
		if started {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	if err := raiseSelfSignal(syscall.SIGTERM); err != nil {
		t.Fatalf("raise SIGTERM: %v", err)
	}

	select {
	case err := <-done:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("ListenServer returned unexpected error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ListenServer did not return after SIGTERM")
	}

	mu.Lock()
	got := append([]string(nil), events...)
	mu.Unlock()

	// Signal arrives → OnShutdown hooks run while the listener is still
	// open → srv.Shutdown drains in-flight requests → serve goroutine
	// returns → transport-coupled cleanup runs last.
	want := []string{"serve-start", "OnShutdown", "serve-return", "cleanup"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("signal-path order mismatch:\n got:  %v\n want: %v", got, want)
	}
}

func TestListenServerSignalPathTimesOutWaitingForCustomServe(t *testing.T) {
	if !canRaiseSelfSignal {
		t.Skip("self-signal not available on this platform")
	}

	app := newSilentApp(WithShutdownTimeout(10 * time.Millisecond))
	srv := &http.Server{Handler: app}
	done := make(chan error, 1)
	block := make(chan struct{})
	// Closed at test exit so the serve goroutine — which never returns on
	// its own — can finally unblock and exit. This avoids a goroutine leak
	// from this test alone; production code with a non-cooperative serve
	// func actually leaks (documented on ListenServer).
	defer close(block)

	go func() {
		done <- app.ListenServer(srv, func() error {
			<-block
			return nil
		}, "test")
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && app.getServer() == nil {
		time.Sleep(5 * time.Millisecond)
	}
	if app.getServer() == nil {
		t.Fatal("server was not registered")
	}

	if err := raiseSelfSignal(syscall.SIGTERM); err != nil {
		t.Fatalf("raise SIGTERM: %v", err)
	}

	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected shutdown deadline from non-returning serve func, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ListenServer did not return after shutdown deadline")
	}
}

func TestListenServerSignalPathSurfacesServeErrorAfterSignal(t *testing.T) {
	if !canRaiseSelfSignal {
		t.Skip("self-signal not available on this platform")
	}

	app := newSilentApp()
	releaseServe := make(chan struct{})
	wantErr := errors.New("serve failed after signal")
	app.AddHook(OnShutdown, func(*Context) error {
		close(releaseServe)
		return nil
	})

	srv := &http.Server{Handler: app}
	done := make(chan error, 1)
	go func() {
		done <- app.ListenServer(srv, func() error {
			<-releaseServe
			return wantErr
		}, "test")
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && app.getServer() == nil {
		time.Sleep(5 * time.Millisecond)
	}
	if app.getServer() == nil {
		t.Fatal("server was not registered")
	}

	if err := raiseSelfSignal(syscall.SIGTERM); err != nil {
		t.Fatalf("raise SIGTERM: %v", err)
	}

	select {
	case err := <-done:
		if !errors.Is(err, wantErr) {
			t.Fatalf("expected serve error after signal, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ListenServer did not return")
	}
}

// TestListenServerOnStartupHooksRunInPriorityOrder verifies the OnStartup
// priority sort fix. Without sortPhase(OnStartup), hooks fire in registration
// order; with the fix, lower priority runs first.
func TestListenServerOnStartupHooksRunInPriorityOrder(t *testing.T) {
	app := newSilentApp()

	var (
		mu    sync.Mutex
		order []int
	)
	// Register in reverse priority order to prove sorting actually runs.
	app.AddHookWithPriority(OnStartup, 30, func(*Context) error {
		mu.Lock()
		order = append(order, 30)
		mu.Unlock()
		return nil
	})
	app.AddHookWithPriority(OnStartup, 10, func(*Context) error {
		mu.Lock()
		order = append(order, 10)
		mu.Unlock()
		return nil
	})
	app.AddHookWithPriority(OnStartup, 20, func(*Context) error {
		mu.Lock()
		order = append(order, 20)
		mu.Unlock()
		return nil
	})

	srv := &http.Server{Handler: app}
	if err := app.ListenServer(srv, func() error { return nil }, "test"); err != nil {
		t.Fatalf("ListenServer: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	want := []int{10, 20, 30}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("OnStartup priority order mismatch:\n got:  %v\n want: %v", order, want)
	}
}
