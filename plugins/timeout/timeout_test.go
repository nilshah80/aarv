package timeout

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nilshah80/aarv"
)

func TestDefaultConfig(t *testing.T) {
	if got := DefaultConfig().Timeout; got != 30*time.Second {
		t.Fatalf("expected 30s default timeout, got %v", got)
	}
}

func TestTimeoutWriterHelpers(t *testing.T) {
	rec := httptest.NewRecorder()
	tw := acquireTimeoutWriter(rec)
	tw.statusCode = http.StatusAccepted

	tw.WriteHeader(http.StatusCreated)
	tw.WriteHeader(http.StatusNoContent)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected first status to win, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	tw = acquireTimeoutWriter(rec)
	tw.statusCode = http.StatusAccepted
	n, err := tw.Write([]byte("ok"))
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if n != 2 || rec.Code != http.StatusAccepted || rec.Body.String() != "ok" {
		t.Fatalf("unexpected write result code=%d body=%q n=%d", rec.Code, rec.Body.String(), n)
	}

	tw.timedOut = true
	n, err = tw.Write([]byte("late"))
	if err != context.DeadlineExceeded || n != 0 {
		t.Fatalf("expected deadline exceeded after timeout, got n=%d err=%v", n, err)
	}
	if tw.Unwrap() != rec {
		t.Fatal("unwrap should return underlying writer")
	}
	releaseTimeoutWriter(tw)
	releaseTimeoutWriter(nil)
}

func TestNewStdlibWithoutAarvContext(t *testing.T) {
	handler := New(100 * time.Millisecond)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := r.Context().Deadline(); !ok {
			t.Fatal("expected deadline on plain request context")
		}
		_, _ = w.Write([]byte("plain"))
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "plain" {
		t.Fatalf("unexpected response status=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestNewUsesDefaultTimeoutAndPassesThrough(t *testing.T) {
	handler := New(0)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Fatalf("unexpected response status=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestNewReturnsGatewayTimeout(t *testing.T) {
	handler := New(10 * time.Millisecond)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		_, _ = w.Write([]byte("late"))
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("expected 504, got %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("unexpected content-type %q", got)
	}
	if !strings.Contains(rec.Body.String(), "gateway_timeout") {
		t.Fatalf("unexpected body %q", rec.Body.String())
	}
}

func TestNewRePanicsOnHandlerPanic(t *testing.T) {
	defer func() {
		if got := recover(); got != "boom" {
			t.Fatalf("expected boom panic, got %v", got)
		}
	}()

	handler := New(100 * time.Millisecond)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
}

func TestNewWithAarvContextUpdatesRequestContext(t *testing.T) {
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(50 * time.Millisecond))

	app.Get("/ctx", func(c *aarv.Context) error {
		if _, ok := c.Request().Context().Deadline(); !ok {
			t.Fatal("expected timeout middleware to install deadline on aarv request context")
		}
		return c.NoContent(http.StatusNoContent)
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ctx", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected no content, got %d", rec.Code)
	}
}

func TestStdlibPathWithAarvContext(t *testing.T) {
	// Insert a non-native middleware to break the native chain,
	// forcing the stdlib path while aarv context is present.
	nonNative := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}

	app := aarv.New(aarv.WithBanner(false))
	app.Use(nonNative)
	app.Use(New(50 * time.Millisecond))

	app.Get("/stdctx", func(c *aarv.Context) error {
		if _, ok := c.Request().Context().Deadline(); !ok {
			t.Fatal("expected deadline on aarv request context via stdlib path")
		}
		return c.NoContent(http.StatusNoContent)
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/stdctx", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
}

func TestAarvPassThrough(t *testing.T) {
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(100 * time.Millisecond))

	app.Get("/ok", func(c *aarv.Context) error {
		return c.Text(http.StatusOK, "hello")
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ok", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "hello" {
		t.Fatalf("expected 200 hello, got %d %q", rec.Code, rec.Body.String())
	}
}

func TestAarvGatewayTimeout(t *testing.T) {
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(10 * time.Millisecond))

	app.Get("/slow", func(c *aarv.Context) error {
		time.Sleep(50 * time.Millisecond)
		return c.Text(http.StatusOK, "late")
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/slow", nil))

	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("expected 504, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "gateway_timeout") {
		t.Fatalf("unexpected body %q", rec.Body.String())
	}
}

func TestAarvRePanicsOnHandlerPanic(t *testing.T) {
	defer func() {
		if got := recover(); got != "boom" {
			t.Fatalf("expected boom panic, got %v", got)
		}
	}()

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(100 * time.Millisecond))

	app.Get("/panic", func(c *aarv.Context) error {
		panic("boom")
	})

	app.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/panic", nil))
}

func TestAarvErrorPropagation(t *testing.T) {
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(100 * time.Millisecond))
	app.Use(aarv.WrapMiddleware(func(next aarv.HandlerFunc) aarv.HandlerFunc {
		return func(c *aarv.Context) error {
			return errors.New("middleware error")
		}
	}))

	app.Get("/err", func(c *aarv.Context) error {
		return c.Text(http.StatusOK, "should not reach")
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/err", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 from error propagation, got %d", rec.Code)
	}
}

func TestAarvTimeoutAlreadyWritten(t *testing.T) {
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(10 * time.Millisecond))

	app.Get("/partial", func(c *aarv.Context) error {
		c.Response().WriteHeader(http.StatusAccepted)
		_, _ = c.Response().Write([]byte("partial"))
		time.Sleep(50 * time.Millisecond)
		return nil
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/partial", nil))

	// Handler wrote before timeout, so we should see the handler's status, not 504.
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202 (already written), got %d", rec.Code)
	}
}

// --- Context (lightweight deadline-propagation) tests ---

func TestContextDefaultDuration(t *testing.T) {
	handler := Context(0)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dl, ok := r.Context().Deadline()
		if !ok {
			t.Fatal("expected deadline on context")
		}
		// Default is 30s; check it's roughly correct.
		remaining := time.Until(dl)
		if remaining < 29*time.Second || remaining > 31*time.Second {
			t.Fatalf("expected ~30s deadline, got %v remaining", remaining)
		}
		_, _ = w.Write([]byte("ok"))
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Fatalf("unexpected response status=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestContextStdlibWithoutAarvContext(t *testing.T) {
	handler := Context(100 * time.Millisecond)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := r.Context().Deadline(); !ok {
			t.Fatal("expected deadline on plain request context")
		}
		_, _ = w.Write([]byte("ctx"))
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "ctx" {
		t.Fatalf("unexpected response status=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestContextStdlibWithAarvContext(t *testing.T) {
	nonNative := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}

	app := aarv.New(aarv.WithBanner(false))
	app.Use(nonNative)
	app.Use(Context(100 * time.Millisecond))

	app.Get("/stdctx", func(c *aarv.Context) error {
		if _, ok := c.Request().Context().Deadline(); !ok {
			t.Fatal("expected deadline on aarv request context via stdlib path")
		}
		return c.NoContent(http.StatusNoContent)
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/stdctx", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
}

func TestContextNativePassThrough(t *testing.T) {
	app := aarv.New(aarv.WithBanner(false))
	app.Use(Context(100 * time.Millisecond))

	app.Get("/ok", func(c *aarv.Context) error {
		if _, ok := c.Request().Context().Deadline(); !ok {
			t.Fatal("expected deadline on native context")
		}
		return c.Text(http.StatusOK, "fast")
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ok", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "fast" {
		t.Fatalf("expected 200 fast, got %d %q", rec.Code, rec.Body.String())
	}
}

func TestContextNativeErrorPropagation(t *testing.T) {
	app := aarv.New(aarv.WithBanner(false))
	app.Use(Context(100 * time.Millisecond))
	app.Use(aarv.WrapMiddleware(func(next aarv.HandlerFunc) aarv.HandlerFunc {
		return func(c *aarv.Context) error {
			return errors.New("ctx middleware error")
		}
	}))

	app.Get("/err", func(c *aarv.Context) error {
		return c.Text(http.StatusOK, "should not reach")
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/err", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

func TestContextDoesNotEnforceTimeout(t *testing.T) {
	// Context() only propagates the deadline — it does NOT enforce it.
	// A handler that ignores the context runs to completion and gets a 200.
	handler := Context(10 * time.Millisecond)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(30 * time.Millisecond)
		_, _ = w.Write([]byte("completed"))
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "completed" {
		t.Fatalf("expected 200 completed, got %d %q", rec.Code, rec.Body.String())
	}
}
