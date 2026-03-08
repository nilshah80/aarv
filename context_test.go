package aarv

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPathParam(t *testing.T) {
	app := New(WithBanner(false))
	app.Get("/users/{id}", func(c *Context) error {
		return c.JSON(200, map[string]string{"id": c.Param("id")})
	})

	tc := NewTestClient(app)
	resp := tc.Get("/users/42")
	resp.AssertStatus(t, 200)

	var body map[string]string
	if err := resp.JSON(&body); err != nil {
		t.Fatal(err)
	}
	if body["id"] != "42" {
		t.Errorf("expected '42', got %q", body["id"])
	}
}

func TestContextStore(t *testing.T) {
	app := New(WithBanner(false))
	app.Get("/store", func(c *Context) error {
		c.Set("key", "value")
		v, ok := c.Get("key")
		if !ok {
			return c.Error(500, "key not found")
		}

		valMustGet := c.MustGet("key").(string)
		if valMustGet != "value" {
			return c.Error(500, "MustGet value mismatch")
		}

		return c.JSON(200, map[string]string{"key": v.(string)})
	})

	tc := NewTestClient(app)
	resp := tc.Get("/store")
	resp.AssertStatus(t, 200)
}

func TestTextResponse(t *testing.T) {
	app := New(WithBanner(false))
	app.Get("/text", func(c *Context) error {
		return c.Text(200, "hello world")
	})

	tc := NewTestClient(app)
	resp := tc.Get("/text")
	resp.AssertStatus(t, 200)

	if resp.Text() != "hello world" {
		t.Errorf("expected 'hello world', got %q", resp.Text())
	}
}

func TestNoContent(t *testing.T) {
	app := New(WithBanner(false))
	app.Delete("/item", func(c *Context) error {
		return c.NoContent(204)
	})

	tc := NewTestClient(app)
	resp := tc.Delete("/item")
	resp.AssertStatus(t, 204)
}

func TestContextHeadersAndCookies(t *testing.T) {
	app := New(WithBanner(false))
	app.Get("/hc", func(c *Context) error {
		userAgent := c.Header("user-agent")
		cookie, _ := c.Cookie("sess")
		c.SetHeader("X-Custom-Resp", userAgent)
		c.SetCookie(&http.Cookie{Name: "resp_sess", Value: cookie.Value})
		return c.Text(200, "ok")
	})

	tc := NewTestClient(app)
	resp := tc.WithHeader("User-Agent", "Test").WithCookie(&http.Cookie{Name: "sess", Value: "data"}).Get("/hc")
	resp.AssertStatus(t, 200)

	if resp.Headers.Get("X-Custom-Resp") != "Test" {
		t.Errorf("Header not echoed")
	}
}

func TestContextQueries(t *testing.T) {
	app := New(WithBanner(false))
	app.Get("/q", func(c *Context) error {
		if c.Query("type") != "active" {
			t.Errorf("Query mismatch")
		}
		if c.QueryDefault("unknown", "default") != "default" {
			t.Errorf("QueryDefault mismatch")
		}
		if c.QueryInt("amount", 0) != 100 {
			t.Errorf("QueryInt mismatch")
		}
		if c.QueryInt64("bigamount", 0) != int64(100) {
			t.Errorf("QueryInt64 mismatch")
		}
		if !c.QueryBool("flag", false) {
			t.Errorf("QueryBool mismatch")
		}
		return c.Text(200, "ok")
	})

	tc := NewTestClient(app)
	resp := tc.WithQuery("type", "active").
		WithQuery("amount", "100").
		WithQuery("bigamount", "100").
		WithQuery("flag", "true").
		Get("/q")
	resp.AssertStatus(t, 200)
}

func TestContextBodyCaching(t *testing.T) {
	app := New(WithBanner(false))
	app.Post("/body", func(c *Context) error {
		body1, err1 := c.Body()
		body2, err2 := c.Body()
		if err1 != nil || err2 != nil {
			t.Errorf("Error reading body: %v %v", err1, err2)
		}
		if string(body1) != "hello" || string(body2) != "hello" {
			t.Errorf("Body mismatch")
		}
		return c.Text(200, "ok")
	})

	req := httptest.NewRequest("POST", "/body", bytes.NewBufferString("hello"))
	w := httptest.NewRecorder()
	app.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("Expected 200")
	}
}

func TestContextMetadata(t *testing.T) {
	app := New(WithBanner(false))
	app.Get("/meta", func(c *Context) error {
		if c.Method() != "GET" {
			t.Errorf("Method mismatch")
		}
		if c.Path() != "/meta" {
			t.Errorf("Path mismatch")
		}
		if c.RealIP() == "" {
			t.Errorf("IP missing")
		}
		if c.Protocol() != "HTTP/1.1" {
			t.Errorf("Protocol mismatch")
		}
		if c.Scheme() != "http" {
			t.Errorf("Scheme mismatch")
		}
		return c.Text(200, "ok")
	})

	tc := NewTestClient(app)
	resp := tc.Get("/meta")
	resp.AssertStatus(t, 200)
}

func TestContextHelpers(t *testing.T) {
	app := New(WithBanner(false))

	app.Get("/helpers", func(c *Context) error {
		c.SetContext(c.Request().Context())
		if c.Host() == "" {
			t.Errorf("Host missing")
		}

		c.Set("requestId", "test-123")
		if c.RequestID() != "test-123" {
			t.Errorf("RequestID missing or mismatch")
		}

		c.Logger().Info("test logging")
		if err := c.ErrorWithDetail(400, "bad", "req"); err == nil {
			t.Errorf("expected ErrorWithDetail to return an app error")
		}

		return c.Redirect(302, "https://example.com")
	})

	app.Get("/file", func(c *Context) error {
		return c.File("context_test.go")
	})

	app.Get("/attachment", func(c *Context) error {
		return c.Attachment("context_test.go", "test.txt")
	})

	app.Get("/status", func(c *Context) error {
		return c.SendStatus(202)
	})

	tc := NewTestClient(app)

	resp := tc.Get("/helpers")
	if resp.Status != 302 || resp.Headers.Get("Location") != "https://example.com" {
		t.Errorf("Redirect expected")
	}

	respFile := tc.Get("/file")
	respFile.AssertStatus(t, 200)

	respAtt := tc.Get("/attachment")
	respAtt.AssertStatus(t, 200)
	if respAtt.Headers.Get("Content-Disposition") != "attachment; filename=\"test.txt\"" {
		t.Errorf("Attachment expected")
	}

	respStatus := tc.Get("/status")
	respStatus.AssertStatus(t, 202)
}

func TestContextAdditionalCoverage(t *testing.T) {
	t.Run("request metadata and query helpers", func(t *testing.T) {
		type replacementCtxKey string
		app := New(WithBanner(false), WithTrustedProxies("127.0.0.1/32"))
		req := httptest.NewRequest(http.MethodGet, "/users/123?count=9&big=99&ratio=1.5&flag=true&list=a&list=b", nil)
		req.RemoteAddr = "127.0.0.1:1234"
		req.Host = "example.test"
		req.Header.Set("X-Forwarded-Proto", "https")
		req.Header.Set("X-Real-IP", "10.0.0.1")
		req.Header.Add("X-Multi", "a")
		req.Header.Add("X-Multi", "b")
		req.SetPathValue("id", "123")
		req.TLS = &tls.ConnectionState{}

		ctx, rec := newAppContext(app, req)
		defer app.ReleaseContext(ctx)

		if ctx.Response() != rec || ctx.Context() == nil {
			t.Fatal("expected response writer and request context")
		}
		ctx.SetContext(context.WithValue(context.Background(), replacementCtxKey("k"), "v"))
		if ctx.Context().Value(replacementCtxKey("k")) != "v" {
			t.Fatal("expected replaced request context")
		}
		if ctx.Scheme() != "https" || !ctx.IsTLS() {
			t.Fatalf("unexpected scheme or TLS flag: scheme=%s tls=%v", ctx.Scheme(), ctx.IsTLS())
		}
		if ctx.RealIP() != "10.0.0.1" {
			t.Fatalf("unexpected trusted proxy real IP: %s", ctx.RealIP())
		}
		if !ctx.isTrustedProxy("127.0.0.1") || ctx.isTrustedProxy("bad-ip") {
			t.Fatal("unexpected trusted proxy evaluation")
		}
		if n, err := ctx.ParamInt("id"); err != nil || n != 123 {
			t.Fatalf("unexpected ParamInt result: %d %v", n, err)
		}
		if n, err := ctx.ParamInt64("id"); err != nil || n != 123 {
			t.Fatalf("unexpected ParamInt64 result: %d %v", n, err)
		}
		req.SetPathValue("uuid", "123e4567-e89b-12d3-a456-426614174000")
		if v, err := ctx.ParamUUID("uuid"); err != nil || v == "" {
			t.Fatalf("unexpected ParamUUID result: %q %v", v, err)
		}
		req.SetPathValue("uuid", "bad")
		if _, err := ctx.ParamUUID("uuid"); err == nil {
			t.Fatal("expected invalid UUID error")
		}
		if ctx.QueryFloat64("ratio", 0) != 1.5 || ctx.QueryFloat64("missing", 3.2) != 3.2 {
			t.Fatal("unexpected query float results")
		}
		if ctx.QueryDefault("count", "0") != "9" {
			t.Fatal("expected query default to return actual value")
		}
		if ctx.QueryInt("missing-int", 11) != 11 || ctx.QueryInt64("missing-int64", 12) != 12 || ctx.QueryBool("missing-bool", false) != false {
			t.Fatal("expected empty query fallbacks")
		}
		if got := ctx.QuerySlice("list"); len(got) != 2 || got[0] != "a" || got[1] != "b" {
			t.Fatalf("unexpected query slice values: %#v", got)
		}
		if ctx.QueryInt("bad", 7) != 7 || ctx.QueryInt64("bad", 8) != 8 || ctx.QueryBool("bad", true) != true {
			t.Fatal("expected fallback query values")
		}
		if got := ctx.QueryParams().Get("count"); got != "9" {
			t.Fatalf("unexpected query params value: %q", got)
		}
		req.URL.RawQuery = "count=bad&big=bad&ratio=bad&flag=bad"
		ctx.query = nil
		if ctx.QueryInt("count", 7) != 7 || ctx.QueryInt64("big", 8) != 8 || ctx.QueryFloat64("ratio", 1.2) != 1.2 || ctx.QueryBool("flag", true) != true {
			t.Fatal("expected invalid query parsing fallbacks")
		}
		ctx.AddHeader("X-Test", "1")
		if got := ctx.HeaderValues("X-Multi"); len(got) != 2 {
			t.Fatalf("unexpected header values: %#v", got)
		}
		if rec.Header().Values("X-Test")[0] != "1" {
			t.Fatal("expected response header to be added")
		}
	})

	t.Run("real ip without trusted proxy", func(t *testing.T) {
		app := New(WithBanner(false), WithTrustedProxies("10.0.0.0/24"))
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "8.8.8.8:4567"
		req.Header.Set("X-Forwarded-For", "1.1.1.1, 2.2.2.2")
		ctx, _ := newAppContext(app, req)
		defer app.ReleaseContext(ctx)

		if ctx.RealIP() != "8.8.8.8" {
			t.Fatalf("expected direct remote IP, got %s", ctx.RealIP())
		}

		ctx.app = nil
		if !ctx.isTrustedProxy("8.8.8.8") {
			t.Fatal("expected trust-all behavior without app config")
		}

		app = New(WithBanner(false), WithTrustedProxies("8.8.8.8"))
		req = httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "8.8.8.8"
		req.Header.Set("X-Forwarded-Proto", "https")
		req.Header.Set("X-Forwarded-For", "5.5.5.5")
		ctx, _ = newAppContext(app, req)
		if ctx.RealIP() != "5.5.5.5" {
			t.Fatalf("expected direct string IP trusted proxy result, got %s", ctx.RealIP())
		}
		if ctx.Scheme() != "https" {
			t.Fatalf("expected forwarded proto scheme, got %s", ctx.Scheme())
		}

		app = New(WithBanner(false), WithTrustedProxies("invalid-cidr"))
		req = httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "9.9.9.9:1234"
		ctx, _ = newAppContext(app, req)
		if ctx.isTrustedProxy("9.9.9.9") {
			t.Fatal("expected invalid CIDR entry to be ignored when it does not match the IP string")
		}
	})

	t.Run("body binding and form file helpers", func(t *testing.T) {
		type payload struct {
			Name string `json:"name"`
		}

		app := New(WithBanner(false))
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"small"}`))
		req.ContentLength = int64(len(`{"name":"small"}`))
		ctx, _ := newAppContext(app, req)
		defer app.ReleaseContext(ctx)

		var small payload
		if err := ctx.Bind(&small); err != nil || small.Name != "small" {
			t.Fatalf("unexpected bind result: %+v err=%v", small, err)
		}

		largeBody := strings.NewReader(`{"name":"large"}`)
		req = httptest.NewRequest(http.MethodPost, "/", largeBody)
		req.ContentLength = 20000
		ctx, _ = newAppContext(app, req)
		var large payload
		if err := ctx.BindJSON(&large); err != nil || large.Name != "large" {
			t.Fatalf("unexpected large bind result: %+v err=%v", large, err)
		}

		req = httptest.NewRequest(http.MethodPost, "/", nil)
		req.Body = errReadCloser{err: errors.New("read failure")}
		req.ContentLength = 1
		ctx, _ = newAppContext(app, req)
		if _, err := ctx.Body(); err == nil {
			t.Fatal("expected body read failure")
		}
		if err := ctx.BindJSON(&large); err == nil {
			t.Fatal("expected bind JSON body-read error")
		}

		app.codec = failingCodec{}
		req = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"large"}`))
		req.ContentLength = 20000
		ctx, _ = newAppContext(app, req)
		if err := ctx.BindJSON(&large); err == nil {
			t.Fatal("expected bind JSON decode error")
		}
		app.codec = StdJSONCodec{}

		var queryTarget struct {
			Page int `query:"page" default:"5"`
		}
		req = httptest.NewRequest(http.MethodGet, "/?page=2", nil)
		ctx, _ = newAppContext(app, req)
		if err := ctx.BindQuery(&queryTarget); err != nil || queryTarget.Page != 2 {
			t.Fatalf("unexpected bind query result: %+v err=%v", queryTarget, err)
		}

		req = httptest.NewRequest(http.MethodPost, "/", strings.NewReader("age=7"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		ctx, _ = newAppContext(app, req)
		var formTarget struct {
			Age int `form:"age"`
		}
		if err := ctx.BindForm(&formTarget); err != nil || formTarget.Age != 7 {
			t.Fatalf("unexpected bind form result: %+v err=%v", formTarget, err)
		}

		req = httptest.NewRequest(http.MethodPost, "/", nil)
		req.Body = errReadCloser{err: errors.New("parse form failure")}
		req.ContentLength = 1
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		ctx, _ = newAppContext(app, req)
		if err := ctx.BindForm(&formTarget); err == nil {
			t.Fatal("expected bind form error")
		}

		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, err := writer.CreateFormFile("file", "upload.txt")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte("file-data")); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		req = httptest.NewRequest(http.MethodPost, "/", &body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		ctx, _ = newAppContext(app, req)
		fh, err := ctx.FormFile("file")
		if err != nil || fh.Filename != "upload.txt" {
			t.Fatalf("unexpected form file result: %#v err=%v", fh, err)
		}
	})

	t.Run("response helpers and typed accessors", func(t *testing.T) {
		app := New(WithBanner(false), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		ctx, rec := newAppContext(app, req)
		defer app.ReleaseContext(ctx)

		ctx.Status(http.StatusCreated)
		if err := ctx.JSON(0, map[string]string{"ok": "true"}); err != nil {
			t.Fatalf("unexpected JSON error: %v", err)
		}
		if rec.Code != http.StatusCreated {
			t.Fatalf("unexpected JSON status: %d", rec.Code)
		}

		ctx, _ = newAppContext(app, httptest.NewRequest(http.MethodGet, "/", nil))
		if err := ctx.JSONPretty(http.StatusOK, map[string]string{"pretty": "yes"}); err != nil {
			t.Fatalf("unexpected JSONPretty error: %v", err)
		}
		ctx, _ = newAppContext(app, httptest.NewRequest(http.MethodGet, "/", nil))
		app.codec = failingCodec{}
		if err := ctx.JSONPretty(http.StatusOK, map[string]string{"fail": "yes"}); err == nil {
			t.Fatal("expected JSONPretty error from codec")
		}
		app.codec = StdJSONCodec{}

		ctx, rec = newAppContext(app, httptest.NewRequest(http.MethodGet, "/", nil))
		if err := ctx.HTML(http.StatusAccepted, "<b>ok</b>"); err != nil || rec.Code != http.StatusAccepted {
			t.Fatalf("unexpected HTML result: code=%d err=%v", rec.Code, err)
		}
		ctx, _ = newAppContext(app, httptest.NewRequest(http.MethodGet, "/", nil))
		if err := ctx.XML(http.StatusOK, struct {
			XMLName string `xml:"name"`
		}{XMLName: "value"}); err != nil {
			t.Fatalf("unexpected XML error: %v", err)
		}
		ctx, rec = newAppContext(app, httptest.NewRequest(http.MethodGet, "/", nil))
		if err := ctx.Blob(http.StatusAccepted, "application/octet-stream", []byte("blob")); err != nil || rec.Body.String() != "blob" {
			t.Fatalf("unexpected blob result: body=%q err=%v", rec.Body.String(), err)
		}

		base := &featureResponseWriter{}
		bw := acquireBufferedWriter(base)
		streamCtx := &Context{req: httptest.NewRequest(http.MethodGet, "/", nil), res: bw, app: app}
		if err := streamCtx.Stream(http.StatusOK, "text/plain", strings.NewReader("stream")); err != nil {
			t.Fatalf("unexpected stream error: %v", err)
		}
		if base.body.String() != "stream" {
			t.Fatalf("unexpected streamed body %q", base.body.String())
		}
		releaseBufferedWriter(bw)

		tmpFile := filepath.Join(t.TempDir(), "asset.txt")
		if err := os.WriteFile(tmpFile, []byte("asset"), 0o600); err != nil {
			t.Fatal(err)
		}
		ctx, rec = newAppContext(app, httptest.NewRequest(http.MethodGet, "/", nil))
		if err := ctx.Attachment(tmpFile, ""); err != nil {
			t.Fatalf("unexpected attachment error: %v", err)
		}
		if !strings.Contains(rec.Header().Get("Content-Disposition"), "asset.txt") {
			t.Fatalf("unexpected attachment header %q", rec.Header().Get("Content-Disposition"))
		}
		ctx, rec = newAppContext(app, httptest.NewRequest(http.MethodGet, "/", nil))
		if err := ctx.Attachment("plain.txt", ""); err != nil {
			t.Fatalf("unexpected plain attachment error: %v", err)
		}
		if rec.Header().Get("Content-Disposition") != "attachment; filename=\"plain.txt\"" {
			t.Fatalf("unexpected plain attachment header %q", rec.Header().Get("Content-Disposition"))
		}

		ctx = &Context{store: map[string]any{"requestId": "req-1", "value": 3}, app: app}
		if ctx.RequestID() != "req-1" {
			t.Fatal("expected request ID")
		}
		logger := ctx.Logger()
		if logger != ctx.Logger() {
			t.Fatal("expected cached logger instance")
		}
		if v, ok := GetTyped[int](ctx, "value"); !ok || v != 3 {
			t.Fatalf("unexpected typed getter result: %d %v", v, ok)
		}
		if _, ok := GetTyped[string](ctx, "missing"); ok {
			t.Fatal("expected missing typed getter to fail")
		}
		appErrValue := ctx.Error(http.StatusBadRequest, "bad")
		appErr := appErrValue.(*AppError)
		if appErr.Code() != http.StatusText(http.StatusBadRequest) {
			t.Fatal("expected context error helper to use HTTP status text")
		}
		detailedErrValue := ctx.ErrorWithDetail(http.StatusBadRequest, "bad", "detail")
		detailedErr := detailedErrValue.(*AppError)
		if detailedErr.Detail() != "detail" {
			t.Fatal("expected context error detail")
		}

		defer func() {
			if recover() == nil {
				t.Fatal("expected MustGet panic")
			}
		}()
		_ = ctx.MustGet("missing")
	})

	if isValidUUID("bad-value") {
		t.Fatal("expected invalid UUID helper result")
	}
	if isValidUUID("123e4567_e89b-12d3-a456-426614174000") {
		t.Fatal("expected invalid UUID punctuation result")
	}
	if isValidUUID("123e4567-e89b-12d3-a456-42661417400g") {
		t.Fatal("expected invalid UUID hex result")
	}
}

type errReader struct {
	err error
}

func (r errReader) Read(_ []byte) (int, error) {
	return 0, r.err
}

func TestContextInternalHelpers(t *testing.T) {
	t.Run("reset drops oversized body cache", func(t *testing.T) {
		app := New(WithBanner(false))
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		ctx, rec := newAppContext(app, req)
		defer app.ReleaseContext(ctx)

		ctx.bodyCache = make([]byte, 0, maxReusableBodyCache+1)
		ctx.store = map[string]any{"k": "v"}
		ctx.reset(rec, req)
		if ctx.bodyCache != nil {
			t.Fatalf("expected oversized cache to be released, got cap=%d", cap(ctx.bodyCache))
		}
		if ctx.store != nil {
			t.Fatalf("expected request store to be cleared, got %#v", ctx.store)
		}
	})

	t.Run("readBodyInto covers short, streaming, and error reads", func(t *testing.T) {
		data, err := readBodyInto(bytes.NewBufferString("abc"), nil, 5)
		if err != nil || string(data) != "abc" {
			t.Fatalf("expected short read to succeed, got data=%q err=%v", string(data), err)
		}

		data, err = readBodyInto(bytes.NewBufferString("stream"), make([]byte, 0, 2), -1)
		if err != nil || string(data) != "stream" {
			t.Fatalf("expected unknown-length read to succeed, got data=%q err=%v", string(data), err)
		}

		if _, err = readBodyInto(errReader{err: errors.New("boom")}, nil, -1); err == nil || err.Error() != "boom" {
			t.Fatalf("expected streaming read error, got %v", err)
		}
	})
}
