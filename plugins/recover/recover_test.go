package recover

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nilshah80/aarv"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.StackSize != 4096 || cfg.DisableStackAll || cfg.DisablePrintStack {
		t.Fatalf("unexpected default config: %#v", cfg)
	}
}

func TestNewPassThroughWithoutPanic(t *testing.T) {
	handler := New().Stdlib(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
}

func TestNewRecoversAndLogsStack(t *testing.T) {
	var logBuf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logBuf, nil)))
	t.Cleanup(func() {
		slog.SetDefault(old)
	})

	handler := New(Config{StackSize: 0}).Stdlib(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/panic", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("unexpected content-type %q", got)
	}
	if !strings.Contains(rec.Body.String(), "internal_error") {
		t.Fatalf("unexpected body %q", rec.Body.String())
	}

	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "panic recovered") || !strings.Contains(logOutput, "\"stack\":") {
		t.Fatalf("expected panic log with stack, got %s", logOutput)
	}
}

func TestNewRecoversWithoutStackLogging(t *testing.T) {
	var logBuf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logBuf, nil)))
	t.Cleanup(func() {
		slog.SetDefault(old)
	})

	handler := New(Config{
		DisablePrintStack: true,
		DisableStackAll:   true,
	}).Stdlib(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("quiet-boom")
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/panic", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
	if strings.Contains(logBuf.String(), "\"stack\":") {
		t.Fatalf("did not expect stack field, got %s", logBuf.String())
	}
}

func TestNewNativeRecoverWithStack(t *testing.T) {
	var logBuf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New())
	app.Get("/panic", func(c *aarv.Context) error {
		panic("native-boom")
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/panic", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "internal_error") {
		t.Fatalf("expected error body, got %q", rec.Body.String())
	}
	if !strings.Contains(logBuf.String(), "\"stack\":") {
		t.Fatalf("expected stack in log, got %s", logBuf.String())
	}
}

func TestNewNativeRecoverWithoutStack(t *testing.T) {
	var logBuf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{DisablePrintStack: true}))
	app.Get("/panic", func(c *aarv.Context) error {
		panic("quiet-native")
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/panic", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
	if strings.Contains(logBuf.String(), "\"stack\":") {
		t.Fatalf("did not expect stack, got %s", logBuf.String())
	}
}

func TestDefaultConfigStackResponseOff(t *testing.T) {
	if DefaultConfig().IncludeStackInResponse {
		t.Fatal("IncludeStackInResponse must default to false")
	}
}

// TestStackNotInResponseByDefault is the security guard: the default 500 body
// must never expose the panic value or stack trace. Checked on both paths.
func TestStackNotInResponseByDefault(t *testing.T) {
	cases := map[string]func() *httptest.ResponseRecorder{
		"stdlib": func() *httptest.ResponseRecorder {
			h := New().Stdlib(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				panic("secret-stdlib")
			}))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
			return rec
		},
		"native": func() *httptest.ResponseRecorder {
			app := aarv.New(aarv.WithBanner(false))
			app.Use(New())
			app.Get("/p", func(c *aarv.Context) error { panic("secret-native") })
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, httptest.NewRequest("GET", "/p", nil))
			return rec
		},
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			rec := run()
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("expected 500, got %d", rec.Code)
			}
			body := rec.Body.String()
			if strings.Contains(body, "\"stack\"") || strings.Contains(body, "\"panic\"") {
				t.Fatalf("default body leaked stack/panic: %q", body)
			}
			if strings.Contains(body, "secret-") {
				t.Fatalf("default body leaked panic value: %q", body)
			}
			if !strings.Contains(body, "internal_error") {
				t.Fatalf("expected generic error body, got %q", body)
			}
		})
	}
}

// TestStackInResponseWhenEnabled asserts the opt-in includes the panic value
// and stack trace, with identical behavior across native and stdlib paths.
func TestStackInResponseWhenEnabled(t *testing.T) {
	cases := map[string]func() *httptest.ResponseRecorder{
		"stdlib": func() *httptest.ResponseRecorder {
			h := New(Config{IncludeStackInResponse: true}).Stdlib(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				panic("debug-stdlib")
			}))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
			return rec
		},
		"native": func() *httptest.ResponseRecorder {
			app := aarv.New(aarv.WithBanner(false))
			app.Use(New(Config{IncludeStackInResponse: true}))
			app.Get("/p", func(c *aarv.Context) error { panic("debug-native") })
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, httptest.NewRequest("GET", "/p", nil))
			return rec
		},
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			rec := run()
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("expected 500, got %d", rec.Code)
			}
			body := rec.Body.String()
			if !strings.Contains(body, "\"panic\"") || !strings.Contains(body, "debug-"+name) {
				t.Fatalf("expected panic value in body, got %q", body)
			}
			if !strings.Contains(body, "\"stack\"") {
				t.Fatalf("expected stack in body, got %q", body)
			}
			if !strings.Contains(body, "internal_error") {
				t.Fatalf("expected error/message fields retained, got %q", body)
			}
		})
	}
}

func TestNewCustomPanicHandler(t *testing.T) {
	var gotErr any
	var gotStack []byte

	handler := New(Config{
		Handler: func(w http.ResponseWriter, r *http.Request, err any, stack []byte) {
			gotErr = err
			gotStack = stack
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("custom: " + fmt.Sprintf("%v", err)))
		},
	}).Stdlib(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("custom-boom")
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
	if rec.Body.String() != "custom: custom-boom" {
		t.Fatalf("unexpected body %q", rec.Body.String())
	}
	if gotErr != "custom-boom" {
		t.Fatalf("expected panic value in handler, got %v", gotErr)
	}
	if len(gotStack) == 0 {
		t.Fatal("expected non-empty stack in handler")
	}
}

func TestNewNativeCustomPanicHandler(t *testing.T) {
	var handlerCalled bool

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{
		Handler: func(w http.ResponseWriter, r *http.Request, err any, stack []byte) {
			handlerCalled = true
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("native-custom"))
		},
	}))
	app.Get("/panic", func(c *aarv.Context) error {
		panic("native-custom-boom")
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/panic", nil))

	if !handlerCalled {
		t.Fatal("expected custom panic handler to be called")
	}
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", rec.Code)
	}
}

func TestNewNestedPanicInCustomHandlerStdlib(t *testing.T) {
	var logBuf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	handler := New(Config{
		Handler: func(w http.ResponseWriter, r *http.Request, err any, stack []byte) {
			panic("handler-boom")
		},
	}).Stdlib(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("original-boom")
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 fallback, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "internal_error") {
		t.Fatalf("expected default error body after nested panic, got %q", rec.Body.String())
	}
	if !strings.Contains(logBuf.String(), "panic in custom recovery handler") {
		t.Fatalf("expected nested panic logged, got %s", logBuf.String())
	}
}

func TestNewNestedPanicInCustomHandlerNative(t *testing.T) {
	var logBuf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{
		Handler: func(w http.ResponseWriter, r *http.Request, err any, stack []byte) {
			panic("native-handler-boom")
		},
	}))
	app.Get("/panic", func(c *aarv.Context) error {
		panic("native-original")
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/panic", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 fallback, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "internal_error") {
		t.Fatalf("expected default error body after nested panic, got %q", rec.Body.String())
	}
	if !strings.Contains(logBuf.String(), "panic in custom recovery handler") {
		t.Fatalf("expected nested panic logged, got %s", logBuf.String())
	}
}

func TestNewPartialWriteThenPanicStdlib(t *testing.T) {
	handler := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Upstream", "kept")
			next.ServeHTTP(w, r)
		})
	}(New(Config{
		Handler: func(w http.ResponseWriter, r *http.Request, err any, stack []byte) {
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("X-Custom", "discarded")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("partial"))
			panic("mid-write-boom")
		},
	}).Stdlib(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("original")
	})))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 (not 503 from partial write), got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "internal_error") {
		t.Fatalf("expected clean fallback body, got %q", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "partial") {
		t.Fatal("partial write from crashed handler should have been discarded")
	}
	if got := rec.Header().Get("X-Upstream"); got != "kept" {
		t.Fatalf("expected upstream header to survive fallback, got %q", got)
	}
	if got := rec.Header().Get("X-Custom"); got != "" {
		t.Fatalf("expected crashed handler header to be discarded, got %q", got)
	}
}

func TestNewPartialWriteThenPanicNative(t *testing.T) {
	app := aarv.New(aarv.WithBanner(false))
	app.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Upstream", "kept")
			next.ServeHTTP(w, r)
		})
	})
	app.Use(New(Config{
		Handler: func(w http.ResponseWriter, r *http.Request, err any, stack []byte) {
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("X-Custom", "discarded")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("partial-native"))
			panic("native-mid-write-boom")
		},
	}))
	app.Get("/panic", func(c *aarv.Context) error {
		panic("native-original")
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/panic", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 fallback, got %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "partial-native") {
		t.Fatal("partial write from crashed handler should have been discarded")
	}
	if !strings.Contains(rec.Body.String(), "internal_error") {
		t.Fatalf("expected clean fallback body, got %q", rec.Body.String())
	}
	if got := rec.Header().Get("X-Upstream"); got != "kept" {
		t.Fatalf("expected upstream header to survive fallback, got %q", got)
	}
	if got := rec.Header().Get("X-Custom"); got != "" {
		t.Fatalf("expected crashed handler header to be discarded, got %q", got)
	}
}

func TestPanicGuardWriterEdgeCases(t *testing.T) {
	t.Run("Write without WriteHeader defaults to 200", func(t *testing.T) {
		rec := httptest.NewRecorder()
		g := &panicGuardWriter{real: rec}
		_, _ = g.Write([]byte("body-first"))
		g.commit()

		if rec.Code != http.StatusOK {
			t.Fatalf("expected implicit 200, got %d", rec.Code)
		}
		if rec.Body.String() != "body-first" {
			t.Fatalf("unexpected body %q", rec.Body.String())
		}
	})

	t.Run("double WriteHeader is idempotent", func(t *testing.T) {
		rec := httptest.NewRecorder()
		g := &panicGuardWriter{real: rec}
		g.WriteHeader(http.StatusCreated)
		g.WriteHeader(http.StatusTeapot) // should be ignored
		g.commit()

		if rec.Code != http.StatusCreated {
			t.Fatalf("expected first status 201, got %d", rec.Code)
		}
	})

	t.Run("double commit is no-op", func(t *testing.T) {
		rec := httptest.NewRecorder()
		g := &panicGuardWriter{real: rec}
		g.WriteHeader(http.StatusAccepted)
		_, _ = g.Write([]byte("once"))
		g.commit()
		g.commit() // second commit should be no-op

		if rec.Code != http.StatusAccepted {
			t.Fatalf("expected 202, got %d", rec.Code)
		}
		if rec.Body.String() != "once" {
			t.Fatalf("expected single body write, got %q", rec.Body.String())
		}
	})
}

func TestNewNativePassThrough(t *testing.T) {
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New())
	app.Get("/ok", func(c *aarv.Context) error {
		return c.Text(http.StatusOK, "safe")
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/ok", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "safe" {
		t.Fatalf("expected pass-through, got status=%d body=%q", rec.Code, rec.Body.String())
	}
}
