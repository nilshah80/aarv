package aarv

// Escape-analysis audit — "ensure Req struct stays on stack in Bind[T]"
// (tasks.md, Cross-Cutting / Performance).
//
// Finding (reproduce with: go test -gcflags='-m=2' -run=__none__ . 2>&1 | grep req):
//
//	./bind.go:138:8: moved to heap: req   // body-only BindReq closure
//	./bind.go:174:7: moved to heap: req   // multi-source BindReq closure
//
// reported for EVERY instantiation of BindReq[T] / Bind[Req, Res]. So the Req
// value does NOT stay on the stack — it is heap-allocated once per bound
// request. (This is the Req value's own allocation only; the full bind path
// performs additional allocations — see BenchmarkBindReqAllocs.)
//
// Cause: the prepared closure declares `var req Req`, then passes `&req` as an
// interface value (`dest any`) to Context.BindJSON, binder.bind, and
// validator.validate, and passes req by value to the user handler whose
// parameter leaks. Boxing `&req` into `any` forces the escape; this is inherent
// to the pluggable Codec (Unmarshal(data []byte, v any)) and the
// reflection-based binder/validator. Keeping Req on the stack would require
// monomorphic decode paths that abandon those abstractions.
//
// Outcome: the Req value's escape is expected and unavoidable — not a
// regression. BenchmarkBindReqAllocs tracks the TOTAL bind-path allocation
// count (more than one; the Req escape is a subset) for visibility. No
// AllocsPerRun assertion is added: CI runs `go test -race`, and
// race instrumentation distorts allocation counts, so a hard ceiling would be
// flaky across race/non-race builds.

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

type benchPayload struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

// BenchmarkBindReqAllocs reports allocations for the body-only BindReq path.
// Run with: go test -run=__none__ -bench=BindReqAllocs -benchmem .
func BenchmarkBindReqAllocs(b *testing.B) {
	app := New(WithBanner(false))
	h := BindReq(func(c *Context, _ benchPayload) error {
		return c.NoContent(http.StatusNoContent)
	})
	body := []byte(`{"name":"x","age":3}`)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		ctx, _ := newAppContext(app, req)
		_ = h(ctx)
		app.ReleaseContext(ctx)
	}
}
