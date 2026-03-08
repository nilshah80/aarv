package aarv

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

var contextBenchmarkSink *Context

func BenchmarkContextLifecycle(b *testing.B) {
	app := New(WithBanner(false))
	req := httptest.NewRequest(http.MethodGet, "/bench", nil)

	b.Run("PooledAcquireRelease", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			rec := httptest.NewRecorder()
			ctx := app.AcquireContext(rec, req)
			contextBenchmarkSink = ctx
			app.ReleaseContext(ctx)
		}
	})

	b.Run("AllocPerRequest", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			rec := httptest.NewRecorder()
			ctx := &Context{store: make(map[string]any, 4)}
			ctx.reset(rec, req)
			ctx.app = app
			contextBenchmarkSink = ctx
		}
	})
}
