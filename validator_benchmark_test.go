package aarv

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"testing"
)

type validatorBenchmarkPayload struct {
	Name    string `json:"name" validate:"required,min=2,max=50"`
	Email   string `json:"email" validate:"required,email"`
	Age     int    `json:"age" validate:"gte=0,lte=150"`
	Phone   string `json:"phone" validate:"required,min=5"`
	Street  string `json:"street" validate:"required"`
	City    string `json:"city" validate:"required,min=2"`
	State   string `json:"state" validate:"required,len=2"`
	Zip     string `json:"zip" validate:"required,numeric,len=5"`
	Country string `json:"country" validate:"required,len=2"`
	Role    string `json:"role" validate:"required,oneof=admin user editor"`
}

type binderBenchmarkPayload struct {
	ID      int      `param:"id"`
	Name    string   `query:"name" default:"guest"`
	Enabled bool     `query:"enabled"`
	Role    string   `header:"X-Role" default:"user"`
	Token   string   `cookie:"token"`
	Age     int      `form:"age" default:"21"`
	Tags    []string `query:"tags"`
}

type queryBenchmarkPayload struct {
	Page    int      `query:"page" default:"1"`
	Name    string   `query:"name"`
	Enabled bool     `query:"enabled"`
	Tags    []string `query:"tags"`
}

type formBenchmarkPayload struct {
	Age  int    `form:"age"`
	Name string `json:"name"`
}

type bindJSONBenchmarkPayload struct {
	Name    string `json:"name"`
	Email   string `json:"email"`
	Age     int    `json:"age"`
	Phone   string `json:"phone"`
	Street  string `json:"street"`
	City    string `json:"city"`
	State   string `json:"state"`
	Zip     string `json:"zip"`
	Country string `json:"country"`
	Role    string `json:"role"`
}

type benchmarkDiscardResponseWriter struct {
	header http.Header
	Status int
}

func (w *benchmarkDiscardResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *benchmarkDiscardResponseWriter) Write(p []byte) (int, error) {
	return len(p), nil
}

func (w *benchmarkDiscardResponseWriter) WriteHeader(status int) {
	w.Status = status
}

func (w *benchmarkDiscardResponseWriter) Reset() {
	for k := range w.header {
		delete(w.header, k)
	}
	w.Status = 0
}

var (
	validatorBenchmarkData = validatorBenchmarkPayload{
		Name:    "alice",
		Email:   "a@t.com",
		Age:     30,
		Phone:   "55555",
		Street:  "123 Main",
		City:    "NYC",
		State:   "NY",
		Zip:     "10001",
		Country: "US",
		Role:    "admin",
	}
	validatorBenchmarkBody      = []byte(`{"name":"alice","email":"a@t.com","age":30,"phone":"55555","street":"123 Main","city":"NYC","state":"NY","zip":"10001","country":"US","role":"admin"}`)
	validatorBenchmarkSliceSink []ValidationError
	binderBenchmarkSink         binderBenchmarkPayload
	queryBenchmarkSink          queryBenchmarkPayload
	formBenchmarkSink           formBenchmarkPayload
	bindJSONBenchmarkSink       bindJSONBenchmarkPayload
)

func newValidatorBenchmarkRequest() *http.Request {
	return &http.Request{
		Method:        http.MethodPost,
		URL:           &url.URL{Path: "/users"},
		Header:        http.Header{"Content-Type": []string{"application/json"}},
		Body:          io.NopCloser(bytes.NewReader(validatorBenchmarkBody)),
		ContentLength: int64(len(validatorBenchmarkBody)),
	}
}

func newBinderBenchmarkRequest() *http.Request {
	req := &http.Request{
		Method:        http.MethodPost,
		URL:           &url.URL{Path: "/users/42", RawQuery: "name=alice&enabled=true&tags=a,b"},
		Header:        http.Header{"Content-Type": []string{"application/x-www-form-urlencoded"}, "X-Role": []string{"admin"}},
		Body:          io.NopCloser(bytes.NewReader([]byte("age=12"))),
		ContentLength: int64(len("age=12")),
	}
	req.AddCookie(&http.Cookie{Name: "token", Value: "cookie-token"})
	req.SetPathValue("id", "42")
	return req
}

func BenchmarkValidator10FieldsAarv(b *testing.B) {
	sv := buildStructValidator(reflect.TypeOf(validatorBenchmarkPayload{}))
	if sv == nil {
		b.Fatal("expected validator")
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		validatorBenchmarkSliceSink = sv.validate(&validatorBenchmarkData)
	}
}

func BenchmarkBindValidate10FieldsAarv(b *testing.B) {
	app := New(WithBanner(false))
	reqType := reflect.TypeOf(validatorBenchmarkPayload{})
	binder := buildStructBinder(reqType)
	validator := buildStructValidator(reqType)
	if binder == nil || validator == nil {
		b.Fatal("expected binder and validator")
	}

	var rw benchmarkDiscardResponseWriter
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rw.Reset()
		req := newValidatorBenchmarkRequest()
		ctx := app.AcquireContext(&rw, req)

		var payload validatorBenchmarkPayload
		if err := binder.bind(ctx, &payload); err != nil {
			b.Fatal(err)
		}
		if binder.needBody && ctx.Request().ContentLength > 0 {
			if err := ctx.BindJSON(&payload); err != nil {
				b.Fatal(err)
			}
		}
		binder.applyDefaults(&payload)
		validatorBenchmarkSliceSink = validator.validate(&payload)

		app.ReleaseContext(ctx)
	}
}

func BenchmarkBinderMultiSourceDefaultsAarv(b *testing.B) {
	app := New(WithBanner(false))
	reqType := reflect.TypeOf(binderBenchmarkPayload{})
	binder := buildStructBinder(reqType)
	if binder == nil {
		b.Fatal("expected binder")
	}

	var rw benchmarkDiscardResponseWriter
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rw.Reset()
		req := newBinderBenchmarkRequest()
		ctx := app.AcquireContext(&rw, req)
		var payload binderBenchmarkPayload
		if err := binder.bind(ctx, &payload); err != nil {
			b.Fatal(err)
		}
		binder.applyDefaults(&payload)
		binderBenchmarkSink = payload
		app.ReleaseContext(ctx)
	}
}

func BenchmarkBindQueryAarv(b *testing.B) {
	app := New(WithBanner(false))
	req := &http.Request{
		Method: http.MethodGet,
		URL:    &url.URL{Path: "/users", RawQuery: "page=9&name=alice&enabled=true&tags=a,b"},
		Header: make(http.Header),
	}
	var rw benchmarkDiscardResponseWriter

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rw.Reset()
		ctx := app.AcquireContext(&rw, req)
		var payload queryBenchmarkPayload
		if err := bindQueryParams(ctx, &payload); err != nil {
			b.Fatal(err)
		}
		queryBenchmarkSink = payload
		app.ReleaseContext(ctx)
	}
}

func BenchmarkContextBindQueryAarv(b *testing.B) {
	app := New(WithBanner(false))
	req := &http.Request{
		Method: http.MethodGet,
		URL:    &url.URL{Path: "/users", RawQuery: "page=9&name=alice&enabled=true&tags=a,b"},
		Header: make(http.Header),
	}
	var rw benchmarkDiscardResponseWriter

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rw.Reset()
		ctx := app.AcquireContext(&rw, req)
		var payload queryBenchmarkPayload
		if err := ctx.BindQuery(&payload); err != nil {
			b.Fatal(err)
		}
		queryBenchmarkSink = payload
		app.ReleaseContext(ctx)
	}
}

func BenchmarkBindFormAarv(b *testing.B) {
	app := New(WithBanner(false))
	var rw benchmarkDiscardResponseWriter

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rw.Reset()
		req := &http.Request{
			Method:        http.MethodPost,
			URL:           &url.URL{Path: "/users"},
			Header:        http.Header{"Content-Type": []string{"application/x-www-form-urlencoded"}},
			Body:          io.NopCloser(bytes.NewReader([]byte("age=12&name=form-name"))),
			ContentLength: int64(len("age=12&name=form-name")),
		}
		ctx := app.AcquireContext(&rw, req)
		if err := ctx.req.ParseForm(); err != nil {
			b.Fatal(err)
		}
		var payload formBenchmarkPayload
		if err := bindFormValues(ctx, &payload); err != nil {
			b.Fatal(err)
		}
		formBenchmarkSink = payload
		app.ReleaseContext(ctx)
	}
}

func BenchmarkContextBindFormAarv(b *testing.B) {
	app := New(WithBanner(false))
	var rw benchmarkDiscardResponseWriter

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rw.Reset()
		req := &http.Request{
			Method:        http.MethodPost,
			URL:           &url.URL{Path: "/users"},
			Header:        http.Header{"Content-Type": []string{"application/x-www-form-urlencoded"}},
			Body:          io.NopCloser(bytes.NewReader([]byte("age=12&name=form-name"))),
			ContentLength: int64(len("age=12&name=form-name")),
		}
		ctx := app.AcquireContext(&rw, req)
		var payload formBenchmarkPayload
		if err := ctx.BindForm(&payload); err != nil {
			b.Fatal(err)
		}
		formBenchmarkSink = payload
		app.ReleaseContext(ctx)
	}
}

func BenchmarkBindJSONAarv(b *testing.B) {
	app := New(WithBanner(false))
	var rw benchmarkDiscardResponseWriter

	b.Run("small", func(b *testing.B) {
		body := validatorBenchmarkBody
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			rw.Reset()
			req := &http.Request{
				Method:        http.MethodPost,
				URL:           &url.URL{Path: "/users"},
				Header:        http.Header{"Content-Type": []string{"application/json"}},
				Body:          io.NopCloser(bytes.NewReader(body)),
				ContentLength: int64(len(body)),
			}
			ctx := app.AcquireContext(&rw, req)
			var payload bindJSONBenchmarkPayload
			if err := ctx.BindJSON(&payload); err != nil {
				b.Fatal(err)
			}
			bindJSONBenchmarkSink = payload
			app.ReleaseContext(ctx)
		}
	})

	b.Run("medium_12kb", func(b *testing.B) {
		var body bytes.Buffer
		body.WriteByte('[')
		for i := 0; i < 64; i++ {
			if i > 0 {
				body.WriteByte(',')
			}
			body.Write(validatorBenchmarkBody)
		}
		body.WriteByte(']')
		payloadBody := body.Bytes()
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			rw.Reset()
			req := &http.Request{
				Method:        http.MethodPost,
				URL:           &url.URL{Path: "/users"},
				Header:        http.Header{"Content-Type": []string{"application/json"}},
				Body:          io.NopCloser(bytes.NewReader(payloadBody)),
				ContentLength: int64(len(payloadBody)),
			}
			ctx := app.AcquireContext(&rw, req)
			var payload []bindJSONBenchmarkPayload
			if err := ctx.BindJSON(&payload); err != nil {
				b.Fatal(err)
			}
			if len(payload) > 0 {
				bindJSONBenchmarkSink = payload[0]
			}
			app.ReleaseContext(ctx)
		}
	})
}
