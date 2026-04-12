package aarv

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// --- formatSSEEvent tests ---

func TestFormatSSEDataOnly(t *testing.T) {
	data, err := formatSSEEvent(SSEEvent{Data: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "data: hello\n\n" {
		t.Fatalf("unexpected format: %q", data)
	}
}

func TestFormatSSENamedEvent(t *testing.T) {
	data, err := formatSSEEvent(SSEEvent{Event: "update", Data: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "event: update\ndata: hello\n\n" {
		t.Fatalf("unexpected format: %q", data)
	}
}

func TestFormatSSEWithID(t *testing.T) {
	data, err := formatSSEEvent(SSEEvent{ID: "42", Data: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "id: 42\ndata: hello\n\n" {
		t.Fatalf("unexpected format: %q", data)
	}
}

func TestFormatSSEWithRetry(t *testing.T) {
	data, err := formatSSEEvent(SSEEvent{Retry: 5000, Data: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "retry: 5000\ndata: hello\n\n" {
		t.Fatalf("unexpected format: %q", data)
	}
}

func TestFormatSSEAllFields(t *testing.T) {
	data, err := formatSSEEvent(SSEEvent{
		Event: "x",
		ID:    "1",
		Retry: 3000,
		Data:  "y",
	})
	if err != nil {
		t.Fatal(err)
	}
	expected := "event: x\nid: 1\nretry: 3000\ndata: y\n\n"
	if string(data) != expected {
		t.Fatalf("expected %q, got %q", expected, data)
	}
}

func TestFormatSSEMultiLineData(t *testing.T) {
	data, err := formatSSEEvent(SSEEvent{Data: "line1\nline2\nline3"})
	if err != nil {
		t.Fatal(err)
	}
	expected := "data: line1\ndata: line2\ndata: line3\n\n"
	if string(data) != expected {
		t.Fatalf("expected %q, got %q", expected, data)
	}
}

func TestFormatSSEEmptyData(t *testing.T) {
	data, err := formatSSEEvent(SSEEvent{Data: ""})
	if err != nil {
		t.Fatal(err)
	}
	// Empty data still emits a single "data: " line per spec
	if string(data) != "data: \n\n" {
		t.Fatalf("unexpected format: %q", data)
	}
}

// --- Context integration ---

func TestContextSSEHeaders(t *testing.T) {
	app := New(WithBanner(false), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx, rec := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	sse, err := ctx.SSE()
	if err != nil {
		t.Fatal(err)
	}
	_ = sse.Close()

	if got := rec.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("expected text/event-stream, got %q", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("expected no-cache, got %q", got)
	}
}

func TestContextSSEStatus(t *testing.T) {
	app := New(WithBanner(false), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx, rec := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	_, err := ctx.SSE()
	if err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestContextSSEMarksWritten(t *testing.T) {
	app := New(WithBanner(false), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx, _ := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	if ctx.Written() {
		t.Fatal("expected written=false initially")
	}
	_, err := ctx.SSE()
	if err != nil {
		t.Fatal(err)
	}
	if !ctx.Written() {
		t.Fatal("expected written=true after SSE()")
	}
}

func TestContextSSEMultipleSendAccumulates(t *testing.T) {
	app := New(WithBanner(false), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx, rec := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	sse, _ := ctx.SSE()
	_ = sse.Send(SSEEvent{Data: "first"})
	_ = sse.Send(SSEEvent{Data: "second"})
	_ = sse.Send(SSEEvent{Data: "third"})

	body := rec.Body.String()
	if !strings.Contains(body, "data: first") ||
		!strings.Contains(body, "data: second") ||
		!strings.Contains(body, "data: third") {
		t.Fatalf("expected all three events in body, got %q", body)
	}
}

func TestContextSSENoConnectionHeader(t *testing.T) {
	app := New(WithBanner(false), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx, rec := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	_, _ = ctx.SSE()

	if got := rec.Header().Get("Connection"); got != "" {
		t.Fatalf("expected no Connection header, got %q", got)
	}
}

// --- Response-already-written guard ---

func TestContextSSEAlreadyWrittenAfterJSON(t *testing.T) {
	app := New(WithBanner(false), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx, _ := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	_ = ctx.JSON(http.StatusOK, map[string]string{"ok": "true"})
	_, err := ctx.SSE()
	if !errors.Is(err, ErrResponseAlreadyWritten) {
		t.Fatalf("expected ErrResponseAlreadyWritten, got %v", err)
	}
}

func TestContextSSEAlreadyWrittenAfterText(t *testing.T) {
	app := New(WithBanner(false), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx, _ := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	_ = ctx.Text(http.StatusOK, "hello")
	_, err := ctx.SSE()
	if !errors.Is(err, ErrResponseAlreadyWritten) {
		t.Fatalf("expected ErrResponseAlreadyWritten, got %v", err)
	}
}

func TestContextSSEAlreadyWrittenAfterStream(t *testing.T) {
	app := New(WithBanner(false), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx, _ := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	_ = ctx.Stream(http.StatusOK, "text/plain", strings.NewReader("data"))
	_, err := ctx.SSE()
	if !errors.Is(err, ErrResponseAlreadyWritten) {
		t.Fatalf("expected ErrResponseAlreadyWritten, got %v", err)
	}
}

// --- Client disconnect ---

func TestSSESendReturnsCtxErrorOnCancel(t *testing.T) {
	app := New(WithBanner(false), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	cancelCtx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(cancelCtx)
	ctx, _ := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	sse, _ := ctx.SSE()
	cancel()

	err := sse.Send(SSEEvent{Data: "after-cancel"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestSSEDoneFiresOnCancel(t *testing.T) {
	app := New(WithBanner(false), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	cancelCtx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(cancelCtx)
	ctx, _ := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	sse, _ := ctx.SSE()
	cancel()

	select {
	case <-sse.Done():
		// expected
	default:
		t.Fatal("expected Done() channel to be closed after cancel")
	}
}

func TestSSESendSucceedsOnActiveContext(t *testing.T) {
	app := New(WithBanner(false), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx, _ := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	sse, _ := ctx.SSE()
	if err := sse.Send(SSEEvent{Data: "active"}); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
}

// --- Close contract ---

func TestSSESendAfterCloseReturnsErrSSEClosed(t *testing.T) {
	app := New(WithBanner(false), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx, _ := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	sse, _ := ctx.SSE()
	_ = sse.Close()

	if err := sse.Send(SSEEvent{Data: "after-close"}); err != ErrSSEClosed {
		t.Fatalf("expected ErrSSEClosed, got %v", err)
	}
}

func TestSSECommentAfterCloseReturnsErrSSEClosed(t *testing.T) {
	app := New(WithBanner(false), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx, _ := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	sse, _ := ctx.SSE()
	_ = sse.Close()

	if err := sse.Comment("keepalive"); err != ErrSSEClosed {
		t.Fatalf("expected ErrSSEClosed, got %v", err)
	}
}

func TestSSEFlushAfterCloseReturnsErrSSEClosed(t *testing.T) {
	app := New(WithBanner(false), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx, _ := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	sse, _ := ctx.SSE()
	_ = sse.Close()

	if err := sse.Flush(); err != ErrSSEClosed {
		t.Fatalf("expected ErrSSEClosed, got %v", err)
	}
}

func TestSSECloseIdempotent(t *testing.T) {
	app := New(WithBanner(false), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx, _ := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	sse, _ := ctx.SSE()
	if err := sse.Close(); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if err := sse.Close(); err != nil {
		t.Fatalf("expected nil on second close, got %v", err)
	}
}

func TestSSEDoneStillWorksAfterClose(t *testing.T) {
	app := New(WithBanner(false), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	cancelCtx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(cancelCtx)
	ctx, _ := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	sse, _ := ctx.SSE()
	_ = sse.Close()

	// Done() should still return the context's channel even after Close
	done := sse.Done()
	if done == nil {
		t.Fatal("expected non-nil Done() channel after close (context is cancellable)")
	}
	// And the channel should still respond to the underlying context
	cancel()
	select {
	case <-done:
		// expected
	default:
		t.Fatal("expected Done() to be closed after context cancel")
	}
}

// --- Field validation ---

func TestSSESendEventWithNewlineReturnsErrInvalidSSEField(t *testing.T) {
	app := New(WithBanner(false), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx, rec := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	sse, _ := ctx.SSE()
	err := sse.Send(SSEEvent{Event: "a\nb", Data: "data"})
	if err != ErrInvalidSSEField {
		t.Fatalf("expected ErrInvalidSSEField, got %v", err)
	}
	// Verify nothing was written beyond the SSE headers
	if strings.Contains(rec.Body.String(), "a") {
		t.Fatal("expected no body write after validation failure")
	}
}

func TestSSESendEventWithCarriageReturnReturnsErrInvalidSSEField(t *testing.T) {
	app := New(WithBanner(false), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx, _ := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	sse, _ := ctx.SSE()
	err := sse.Send(SSEEvent{Event: "a\rb", Data: "data"})
	if err != ErrInvalidSSEField {
		t.Fatalf("expected ErrInvalidSSEField, got %v", err)
	}
}

func TestSSESendIDWithNewlineReturnsErrInvalidSSEField(t *testing.T) {
	app := New(WithBanner(false), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx, _ := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	sse, _ := ctx.SSE()
	err := sse.Send(SSEEvent{ID: "1\n2", Data: "data"})
	if err != ErrInvalidSSEField {
		t.Fatalf("expected ErrInvalidSSEField, got %v", err)
	}
}

func TestSSESendDataWithNewlineSplitsCorrectly(t *testing.T) {
	app := New(WithBanner(false), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx, rec := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	sse, _ := ctx.SSE()
	if err := sse.Send(SSEEvent{Data: "alpha\nbeta\ngamma"}); err != nil {
		t.Fatal(err)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "data: alpha\ndata: beta\ndata: gamma\n\n") {
		t.Fatalf("expected multi-line data split, got %q", body)
	}
}

// --- Comment and Flush ---

func TestSSECommentFormat(t *testing.T) {
	app := New(WithBanner(false), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx, rec := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	sse, _ := ctx.SSE()
	if err := sse.Comment("keepalive"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rec.Body.String(), ": keepalive\n\n") {
		t.Fatalf("expected comment format, got %q", rec.Body.String())
	}
}

func TestSSECommentNormalizesNewlines(t *testing.T) {
	app := New(WithBanner(false), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx, rec := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	sse, _ := ctx.SSE()
	if err := sse.Comment("line1\nline2\rline3"); err != nil {
		t.Fatal(err)
	}

	// Newlines in comment body should be replaced with spaces
	body := rec.Body.String()
	if strings.Contains(body, "line1\nline2") {
		t.Fatalf("expected normalized comment, got %q", body)
	}
	if !strings.Contains(body, ": line1 line2 line3\n\n") {
		t.Fatalf("expected ': line1 line2 line3', got %q", body)
	}
}

func TestSSEFlushWithoutFlusherDoesNotPanic(t *testing.T) {
	// httptest.NewRecorder implements http.Flusher, so we need a writer
	// that doesn't. Use a minimal custom writer.
	w := &nonFlusherWriter{header: make(http.Header)}
	app := New(WithBanner(false), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := app.AcquireContext(w, req)
	defer app.ReleaseContext(ctx)

	sse, err := ctx.SSE()
	if err != nil {
		t.Fatal(err)
	}
	// Should not panic even though w does not implement http.Flusher
	if err := sse.Flush(); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

// nonFlusherWriter is an http.ResponseWriter that does NOT implement
// http.Flusher, used to test the nil-flusher code path.
type nonFlusherWriter struct {
	header http.Header
	status int
	body   []byte
}

func (w *nonFlusherWriter) Header() http.Header       { return w.header }
func (w *nonFlusherWriter) WriteHeader(code int)      { w.status = code }
func (w *nonFlusherWriter) Write(b []byte) (int, error) {
	w.body = append(w.body, b...)
	return len(b), nil
}

// --- Flush context cancel ---

func TestSSEFlushReturnsCtxErrorOnCancel(t *testing.T) {
	app := New(WithBanner(false), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	cancelCtx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(cancelCtx)
	ctx, _ := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	sse, _ := ctx.SSE()
	cancel()

	err := sse.Flush()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

// --- End-to-end via app ---

func TestSSEHandlerEndToEnd(t *testing.T) {
	app := New(WithBanner(false), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	app.Get("/events", func(c *Context) error {
		sse, err := c.SSE()
		if err != nil {
			return err
		}
		defer func() { _ = sse.Close() }()
		for i := 1; i <= 3; i++ {
			if err := sse.Send(SSEEvent{
				Event: "tick",
				Data:  "number " + strconv.Itoa(i),
			}); err != nil {
				return err
			}
		}
		return nil
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/events", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	for i := 1; i <= 3; i++ {
		expected := "event: tick\ndata: number " + strconv.Itoa(i) + "\n\n"
		if !strings.Contains(body, expected) {
			t.Fatalf("expected %q in body, got %q", expected, body)
		}
	}
}

func TestSSEHandlerDoneSelectLoop(t *testing.T) {
	app := New(WithBanner(false), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	cancelCtx, cancel := context.WithCancel(context.Background())

	handlerExited := make(chan struct{})
	app.Get("/stream", func(c *Context) error {
		sse, err := c.SSE()
		if err != nil {
			return err
		}
		defer close(handlerExited)
		for {
			select {
			case <-sse.Done():
				return nil
			default:
				if err := sse.Send(SSEEvent{Data: "tick"}); err != nil {
					return nil
				}
			}
		}
	})

	// Cancel context shortly after request starts
	go func() {
		cancel()
	}()

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/stream", nil).WithContext(cancelCtx))

	// Verify the handler exited cleanly
	select {
	case <-handlerExited:
		// success
	default:
		t.Fatal("expected handler to exit cleanly")
	}
}
