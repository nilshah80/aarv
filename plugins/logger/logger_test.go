package logger

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nilshah80/aarv"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Level != slog.LevelInfo {
		t.Fatalf("expected default level info, got %v", cfg.Level)
	}
}

func TestResponseWriterHelpers(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := newResponseWriter(rec)

	if rw.statusCode != 200 {
		t.Fatalf("expected default status 200, got %d", rw.statusCode)
	}

	rw.WriteHeader(201)
	if rw.statusCode != 201 {
		t.Fatalf("expected status 201, got %d", rw.statusCode)
	}

	n, err := rw.Write([]byte("ok"))
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected 2 bytes written, got %d", n)
	}
	if rw.bytesWritten != 2 {
		t.Fatalf("expected tracked bytes 2, got %d", rw.bytesWritten)
	}
	if rw.Unwrap() != rec {
		t.Fatal("unwrap should return underlying response writer")
	}

	rec = httptest.NewRecorder()
	rw = newResponseWriter(rec)
	n, err = rw.Write([]byte("body"))
	if err != nil {
		t.Fatalf("write-before-header failed: %v", err)
	}
	if n != 4 || rw.statusCode != 200 || !rw.written {
		t.Fatalf("unexpected write-before-header state status=%d written=%v n=%d", rw.statusCode, rw.written, n)
	}

	releaseResponseWriter(nil)
	releaseResponseWriter(rw)
	if rw.ResponseWriter != nil || rw.statusCode != http.StatusOK || rw.bytesWritten != 0 || rw.written {
		t.Fatalf("expected release to reset pooled writer, got %#v", rw)
	}
}

func TestClientIP(t *testing.T) {
	tests := []struct {
		name  string
		setup func() string
		want  string
	}{
		{
			name: "x-real-ip",
			setup: func() string {
				req := httptest.NewRequest("GET", "/", nil)
				req.Header.Set("X-Real-IP", "1.1.1.1")
				return clientIP(req)
			},
			want: "1.1.1.1",
		},
		{
			name: "xff first value",
			setup: func() string {
				req := httptest.NewRequest("GET", "/", nil)
				req.Header.Set("X-Forwarded-For", "2.2.2.2, 3.3.3.3")
				return clientIP(req)
			},
			want: "2.2.2.2",
		},
		{
			name: "xff single value",
			setup: func() string {
				req := httptest.NewRequest("GET", "/", nil)
				req.Header.Set("X-Forwarded-For", "4.4.4.4")
				return clientIP(req)
			},
			want: "4.4.4.4",
		},
		{
			name: "remote addr",
			setup: func() string {
				req := httptest.NewRequest("GET", "/", nil)
				req.RemoteAddr = "5.5.5.5:4321"
				return clientIP(req)
			},
			want: "5.5.5.5",
		},
		{
			name: "empty remote addr",
			setup: func() string {
				req := httptest.NewRequest("GET", "/", nil)
				req.RemoteAddr = ""
				return clientIP(req)
			},
			want: "",
		},
		{
			name: "ipv6 remote addr",
			setup: func() string {
				req := httptest.NewRequest("GET", "/", nil)
				req.RemoteAddr = "[2001:db8::1]:4321"
				return clientIP(req)
			},
			want: "[2001:db8::1]:4321",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.setup(); got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func TestNewLogsRequestAndSkipsConfiguredPaths(t *testing.T) {
	var logBuf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})))
	t.Cleanup(func() {
		slog.SetDefault(old)
	})

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{
		SkipPaths: []string{"/skip"},
		Level:     slog.LevelDebug,
	}))

	app.Get("/log", func(c *aarv.Context) error {
		c.Set("requestId", "req-123")
		return c.Text(201, "hello")
	})
	app.Get("/skip", func(c *aarv.Context) error {
		return c.Text(200, "skipped")
	})

	req := httptest.NewRequest("GET", "/log", nil)
	req.Header.Set("User-Agent", "logger-test")
	req.Header.Set("X-Forwarded-For", "6.6.6.6, 7.7.7.7")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	logOutput := logBuf.String()
	for _, want := range []string{"request", "\"path\":\"/log\"", "\"status\":201", "\"client_ip\":\"6.6.6.6\"", "\"request_id\":\"req-123\"", "\"bytes_out\":5"} {
		if !strings.Contains(logOutput, want) {
			t.Fatalf("expected log output to contain %q, got %s", want, logOutput)
		}
	}

	logBuf.Reset()
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/skip", nil))
	if strings.Contains(logBuf.String(), "/skip") {
		t.Fatalf("skip path should not be logged, got %s", logBuf.String())
	}
}
