package aarv

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"testing"

	"github.com/nilshah80/aarv/internal/benchutil"
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

	var rw benchutil.DiscardResponseWriter
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
