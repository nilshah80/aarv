package aarv

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestErrorsConstructors(t *testing.T) {
	errTests := []struct {
		err      *AppError
		expected int
		msg      string
	}{
		{NewError(http.StatusBadRequest, "bad_req", "bad req"), http.StatusBadRequest, "bad req"},
		{ErrBadRequest("bad req 2"), http.StatusBadRequest, "bad req 2"},
		{ErrUnauthorized("unauth"), http.StatusUnauthorized, "unauth"},
		{ErrForbidden("forbid"), http.StatusForbidden, "forbid"},
		{ErrNotFound("not found"), http.StatusNotFound, "not found"},
		{ErrMethodNotAllowed("method not allowed"), http.StatusMethodNotAllowed, "method not allowed"},
		{ErrConflict("conflict"), http.StatusConflict, "conflict"},
		{ErrUnprocessable("unprocessable"), http.StatusUnprocessableEntity, "unprocessable"},
		{ErrTooManyRequests("too many reqs"), http.StatusTooManyRequests, "too many reqs"},
		{ErrInternal(errors.New("db error")), http.StatusInternalServerError, "Internal server error"},
		{ErrBadGateway("bad gateway"), http.StatusBadGateway, "bad gateway"},
		{ErrServiceUnavailable("service unavailable"), http.StatusServiceUnavailable, "service unavailable"},
		{ErrGatewayTimeout("gateway timeout"), http.StatusGatewayTimeout, "gateway timeout"},
	}

	for _, tt := range errTests {
		if tt.err.StatusCode() != tt.expected {
			t.Errorf("Expected status %d, got %d", tt.expected, tt.err.StatusCode())
		}
		if tt.err.Message() != tt.msg {
			t.Errorf("Expected message %q, got %q", tt.msg, tt.err.Message())
		}
	}
}

func TestErrorWithOpts(t *testing.T) {
	internalErr := errors.New("underlying DB connection failed")
	err := ErrBadRequest("invalid payload").WithDetail("the field 'user' is required").WithInternal(internalErr)

	if err.Detail() != "the field 'user' is required" {
		t.Errorf("Expected detail to match")
	}
	if err.Internal() != internalErr {
		t.Errorf("Expected internal error to match")
	}
	if err.Unwrap() != internalErr {
		t.Errorf("Expected unwrap to return internal error")
	}

	if err.Error() == "" {
		t.Errorf("Expected error string to not be empty")
	}
	if ErrBadRequest("plain").Error() != "plain" {
		t.Errorf("Expected plain app error string")
	}
}

func TestValidationErrorsType(t *testing.T) {
	err := &ValidationErrors{
		Errors: []ValidationError{
			{Field: "foo", Tag: "required"},
		},
	}
	if err.Error() == "" {
		t.Errorf("ValidationErrors should output non-empty string")
	}
}

func TestErrorHandlingAdditionalCoverage(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("default error variants", func(t *testing.T) {
		app := New(WithBanner(false), WithLogger(logger))
		var onErrorCalls int
		app.AddHook(OnError, func(c *Context) error {
			onErrorCalls++
			return nil
		})

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		ctx, rec := newAppContext(app, req)
		defer app.ReleaseContext(ctx)

		app.defaultErrorHandler(ctx, &ValidationErrors{
			Errors: []ValidationError{{Field: "name", Tag: "required"}},
		})
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("expected validation status, got %d", rec.Code)
		}

		ctx, rec = newAppContext(app, req)
		app.defaultErrorHandler(ctx, &BindError{Err: errors.New("broken payload"), Source: "body"})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected bind status, got %d", rec.Code)
		}

		ctx, rec = newAppContext(app, req)
		app.defaultErrorHandler(ctx, ErrInternal(errors.New("db down")).WithDetail("detail"))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("expected app error status, got %d", rec.Code)
		}

		ctx, rec = newAppContext(app, req)
		app.defaultErrorHandler(ctx, errors.New("unexpected"))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("expected generic error status, got %d", rec.Code)
		}

		if onErrorCalls != 4 {
			t.Fatalf("expected OnError hook to run for each case, got %d", onErrorCalls)
		}
	})

	t.Run("custom error handler and response already written", func(t *testing.T) {
		app := New(WithBanner(false), WithLogger(logger))
		app.errorHandler = func(c *Context, err error) {
			_ = c.Text(http.StatusTeapot, err.Error())
		}
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		ctx, rec := newAppContext(app, req)
		defer app.ReleaseContext(ctx)

		app.handleError(ctx, errors.New("custom"))
		if rec.Code != http.StatusTeapot {
			t.Fatalf("expected custom handler status, got %d", rec.Code)
		}

		app.errorHandler = nil
		ctx, rec = newAppContext(app, req)
		app.handleError(ctx, errors.New("fallback"))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("expected fallback default handler status, got %d", rec.Code)
		}

		ctx.written = true
		if err := ctx.JSON(http.StatusOK, map[string]string{"ok": "true"}); !errors.Is(err, ErrResponseAlreadyWritten) {
			t.Fatalf("expected response already written error, got %v", err)
		}
	})
}
