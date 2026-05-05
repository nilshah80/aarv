package problem

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nilshah80/aarv"
)

// decode marshals the Details, then unmarshals into a generic map so
// tests can assert the wire shape — including extension-flattening —
// without coupling to MarshalJSON's internal map iteration order.
func decode(t *testing.T, d *Details) map[string]any {
	t.Helper()
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

func TestContentType_RFC7807(t *testing.T) {
	if ContentType != "application/problem+json" {
		t.Fatalf("Content-Type drift: got %q, want application/problem+json", ContentType)
	}
}

func TestDefaultType_RFC7807(t *testing.T) {
	if DefaultType != "about:blank" {
		t.Fatalf("DefaultType drift: got %q, want about:blank", DefaultType)
	}
}

func TestMarshalJSON_StandardMembers(t *testing.T) {
	d := &Details{
		Type:     "https://example.com/problems/oops",
		Title:    "Oops",
		Status:   400,
		Detail:   "Bad input received",
		Instance: "/orders/123",
	}
	got := decode(t, d)

	wants := map[string]any{
		"type":     "https://example.com/problems/oops",
		"title":    "Oops",
		"status":   float64(400), // JSON numbers decode as float64
		"detail":   "Bad input received",
		"instance": "/orders/123",
	}
	for k, want := range wants {
		if got[k] != want {
			t.Errorf("field %q: got %v, want %v", k, got[k], want)
		}
	}
}

func TestMarshalJSON_DefaultsTypeWhenEmpty(t *testing.T) {
	d := &Details{Status: 500}
	got := decode(t, d)
	if got["type"] != DefaultType {
		t.Fatalf("Type empty must serialize as %q, got %v", DefaultType, got["type"])
	}
}

func TestMarshalJSON_OmitsZeroOptionalMembers(t *testing.T) {
	d := &Details{Status: 500}
	got := decode(t, d)
	for _, k := range []string{"title", "detail", "instance"} {
		if _, present := got[k]; present {
			t.Errorf("zero %q must be omitted, got present in %v", k, got)
		}
	}
}

func TestMarshalJSON_OmitsZeroStatus(t *testing.T) {
	d := &Details{Title: "no-status"}
	got := decode(t, d)
	if _, present := got["status"]; present {
		t.Fatal("zero Status must be omitted from output")
	}
}

func TestMarshalJSON_FlattensExtensions(t *testing.T) {
	d := &Details{
		Status: 422,
		Title:  "Validation failed",
		Extensions: map[string]any{
			"errors":     []string{"a", "b"},
			"trace_id":   "abc-123",
			"request_id": "req-7",
		},
	}
	got := decode(t, d)

	if got["trace_id"] != "abc-123" {
		t.Errorf("extension trace_id missing or wrong: %v", got["trace_id"])
	}
	if got["request_id"] != "req-7" {
		t.Errorf("extension request_id missing or wrong: %v", got["request_id"])
	}
	errs, ok := got["errors"].([]any)
	if !ok || len(errs) != 2 || errs[0] != "a" || errs[1] != "b" {
		t.Errorf("extension errors not flattened correctly: %v", got["errors"])
	}
}

func TestMarshalJSON_StandardMembersWinOnCollision(t *testing.T) {
	// RFC 7807 §3.2 forbids extensions overriding standard members. The
	// implementation enforces this by stripping reserved keys from
	// Extensions during the copy. This test guards the happy collision
	// path: every standard field is non-zero so even the older
	// "non-zero overwrites" logic would have passed.
	d := &Details{
		Type:   "real-type",
		Title:  "real-title",
		Status: 400,
		Extensions: map[string]any{
			"type":   "ext-type",
			"title":  "ext-title",
			"status": 999,
		},
	}
	got := decode(t, d)

	if got["type"] != "real-type" {
		t.Errorf("standard 'type' must win: got %v", got["type"])
	}
	if got["title"] != "real-title" {
		t.Errorf("standard 'title' must win: got %v", got["title"])
	}
	if got["status"] != float64(400) {
		t.Errorf("standard 'status' must win: got %v", got["status"])
	}
}

// TestMarshalJSON_ReservedKeysStrippedEvenWhenStandardFieldsZero is the
// regression for the reserved-key injection bug. With the standard
// fields left at the zero value, an extension whose key duplicates a
// standard member name MUST be dropped — otherwise the wire would
// carry "status":999 / "detail":"..." that the application code never
// blessed. RFC 7807 §3.2: extension members must not duplicate
// standard names.
func TestMarshalJSON_ReservedKeysStrippedEvenWhenStandardFieldsZero(t *testing.T) {
	d := &Details{
		// Note: every standard field except Type is zero. Type is
		// emitted unconditionally (defaults to about:blank) so we
		// also test that "type" can never be overridden.
		Extensions: map[string]any{
			"type":     "ext-injected-type",
			"title":    "ext-injected-title",
			"status":   999,
			"detail":   "ext-injected-detail",
			"instance": "ext-injected-instance",
			"trace_id": "real-extension", // non-reserved should still pass through
		},
	}
	got := decode(t, d)

	if got["type"] != DefaultType {
		t.Errorf("'type' must be DefaultType when struct field is zero, got %v (extension impersonation slipped through)", got["type"])
	}
	if _, present := got["title"]; present {
		t.Errorf("'title' must be omitted when struct field is zero; extension impersonator left it set: %v", got["title"])
	}
	if _, present := got["status"]; present {
		t.Errorf("'status' must be omitted when struct field is zero; extension impersonator set it: %v", got["status"])
	}
	if _, present := got["detail"]; present {
		t.Errorf("'detail' must be omitted when struct field is zero; extension impersonator set it: %v", got["detail"])
	}
	if _, present := got["instance"]; present {
		t.Errorf("'instance' must be omitted when struct field is zero; extension impersonator set it: %v", got["instance"])
	}
	if got["trace_id"] != "real-extension" {
		t.Errorf("non-reserved extensions must pass through unchanged; got trace_id=%v", got["trace_id"])
	}
}

// TestMarshalJSON_ValueReceiverFlowsThroughCustomMarshaler ensures that
// passing a Details value (not a pointer) to json.Marshal still emits
// the RFC 7807 wire shape. With a pointer receiver, the value form
// would silently bypass the custom marshaler and emit Go field names
// plus a nested "Extensions" object — a footgun for callers who treat
// Details as a value type.
func TestMarshalJSON_ValueReceiverFlowsThroughCustomMarshaler(t *testing.T) {
	d := Details{
		Type:   "https://example.com/foo",
		Title:  "v",
		Status: 418,
		Extensions: map[string]any{
			"trace_id": "tx",
		},
	}

	// Marshal the value (not the pointer).
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}

	// RFC 7807 lowercase keys present, Go-style PascalCase keys absent.
	if got["type"] != "https://example.com/foo" {
		t.Errorf("value-marshal lost 'type' (custom marshaler bypassed?): %v", got)
	}
	if got["status"] != float64(418) {
		t.Errorf("value-marshal lost 'status': %v", got)
	}
	if got["trace_id"] != "tx" {
		t.Errorf("value-marshal lost extension flatten: %v", got)
	}
	for _, badKey := range []string{"Type", "Status", "Extensions"} {
		if _, present := got[badKey]; present {
			t.Errorf("value-marshal exposed Go field name %q (custom marshaler bypassed): %v", badKey, got)
		}
	}
}

func TestFromError_NilReturnsOK(t *testing.T) {
	d := FromError(nil)
	if d.Status != http.StatusOK {
		t.Fatalf("nil err: status=%d, want 200", d.Status)
	}
}

func TestFromError_AppError(t *testing.T) {
	src := aarv.NewError(http.StatusConflict, "stale_revision", "Resource has changed").
		WithDetail("supplied If-Match=W/\"42\" but server has v=43")
	d := FromError(src)

	if d.Status != http.StatusConflict {
		t.Errorf("status=%d, want 409", d.Status)
	}
	if d.Title != "stale_revision" {
		t.Errorf("title=%q, want stale_revision (Code())", d.Title)
	}
	if !strings.Contains(d.Detail, "Resource has changed") || !strings.Contains(d.Detail, "v=43") {
		t.Errorf("detail must combine Message + Detail; got %q", d.Detail)
	}
}

func TestFromError_AppErrorEmptyCodeFallsBackToStatusText(t *testing.T) {
	d := FromError(aarv.NewError(http.StatusTeapot, "", "teapot"))
	if d.Title != http.StatusText(http.StatusTeapot) {
		t.Errorf("empty Code() should fall back to StatusText, got %q", d.Title)
	}
}

func TestFromError_GenericErrorMaskedAs500(t *testing.T) {
	d := FromError(errors.New("database timeout: dialing 10.0.0.1:5432"))
	if d.Status != http.StatusInternalServerError {
		t.Errorf("status=%d, want 500", d.Status)
	}
	if d.Title != "internal_error" {
		t.Errorf("title=%q, want internal_error", d.Title)
	}
	// Critical: the original error string must NOT appear on the wire.
	if strings.Contains(d.Detail, "database timeout") || strings.Contains(d.Detail, "10.0.0.1") {
		t.Errorf("generic error message must be masked; got detail=%q", d.Detail)
	}
}

func TestValidationProblem(t *testing.T) {
	errs := &aarv.ValidationErrors{
		Errors: []aarv.ValidationError{
			{Field: "email", Tag: "email", Message: "must be a valid email"},
			{Field: "age", Tag: "min", Param: "18", Message: "must be at least 18"},
		},
	}
	d := ValidationProblem(errs)

	if d.Status != http.StatusUnprocessableEntity {
		t.Errorf("status=%d, want 422", d.Status)
	}
	if d.Title != "Validation failed" {
		t.Errorf("title=%q, want 'Validation failed'", d.Title)
	}
	got := decode(t, d)
	wireErrs, ok := got["errors"].([]any)
	if !ok {
		t.Fatalf("'errors' extension missing or wrong type: %v", got["errors"])
	}
	if len(wireErrs) != 2 {
		t.Fatalf("'errors' extension count=%d, want 2", len(wireErrs))
	}
}

func TestValidationProblem_Nil(t *testing.T) {
	d := ValidationProblem(nil)
	if d.Status != http.StatusUnprocessableEntity {
		t.Fatalf("nil ValidationErrors: status=%d, want 422", d.Status)
	}
	if d.Extensions != nil {
		t.Fatalf("nil ValidationErrors should not produce 'errors' extension; got %v", d.Extensions)
	}
}

// --- Handler integration tests ---

// problemRequest fires a request through an aarv.App configured with the
// problem.Handler and decodes the response as a generic map.
func problemRequest(t *testing.T, cfg Config, handler aarv.HandlerFunc) (rec *httptest.ResponseRecorder, decoded map[string]any) {
	t.Helper()
	app := aarv.New(aarv.WithBanner(false), aarv.WithErrorHandler(Handler(cfg)))
	app.Get("/p", handler)

	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/p", nil))

	if ct := rec.Header().Get("Content-Type"); ct != ContentType {
		t.Errorf("Content-Type=%q, want %q", ct, ContentType)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	return rec, decoded
}

func TestHandler_AppErrorEmitsProblemJSON(t *testing.T) {
	rec, body := problemRequest(t, Config{}, func(c *aarv.Context) error {
		return aarv.ErrConflict("revision mismatch")
	})

	if rec.Code != http.StatusConflict {
		t.Errorf("status=%d, want 409", rec.Code)
	}
	if body["type"] != DefaultType {
		t.Errorf("type=%v, want %q (no Config.Type and no TypeForCode override)", body["type"], DefaultType)
	}
	if body["status"] != float64(http.StatusConflict) {
		t.Errorf("status field=%v, want 409", body["status"])
	}
	if body["title"] != "conflict" {
		t.Errorf("title=%v, want 'conflict' (AppError.Code())", body["title"])
	}
}

func TestHandler_TypeForCodeOverride(t *testing.T) {
	cfg := Config{
		Type: "https://api.example.com/problems",
		TypeForCode: map[string]string{
			"conflict": "https://api.example.com/problems/stale-revision",
		},
	}
	_, body := problemRequest(t, cfg, func(c *aarv.Context) error {
		return aarv.ErrConflict("revision mismatch")
	})
	if body["type"] != "https://api.example.com/problems/stale-revision" {
		t.Fatalf("Type-for-code override missed; got %v", body["type"])
	}
}

func TestHandler_FallsBackToConfigTypeWhenCodeUnknown(t *testing.T) {
	cfg := Config{
		Type:        "https://api.example.com/problems",
		TypeForCode: map[string]string{"some_other_code": "x"},
	}
	_, body := problemRequest(t, cfg, func(c *aarv.Context) error {
		return aarv.ErrBadRequest("malformed body")
	})
	if body["type"] != "https://api.example.com/problems" {
		t.Fatalf("unknown code must fall back to Config.Type; got %v", body["type"])
	}
}

func TestHandler_InstanceGenerator(t *testing.T) {
	cfg := Config{
		Instance: func(c *aarv.Context) string { return c.Path() + "#req" },
	}
	_, body := problemRequest(t, cfg, func(c *aarv.Context) error {
		return aarv.ErrNotFound("nope")
	})
	if body["instance"] != "/p#req" {
		t.Fatalf("instance=%v, want /p#req", body["instance"])
	}
}

func TestHandler_ValidationErrorsToProblem(t *testing.T) {
	rec, body := problemRequest(t, Config{}, func(c *aarv.Context) error {
		return &aarv.ValidationErrors{Errors: []aarv.ValidationError{
			{Field: "x", Tag: "required", Message: "x is required"},
		}}
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status=%d, want 422", rec.Code)
	}
	if body["title"] != "Validation failed" {
		t.Errorf("title=%v, want 'Validation failed'", body["title"])
	}
	errs, ok := body["errors"].([]any)
	if !ok || len(errs) != 1 {
		t.Fatalf("errors extension missing/short: %v", body["errors"])
	}
}

func TestHandler_RequestIDExtensionPropagated(t *testing.T) {
	// Use a tiny middleware to stamp a request id (rather than pulling
	// in plugins/requestid as a test-only dependency).
	app := aarv.New(aarv.WithBanner(false), aarv.WithErrorHandler(Handler(Config{})))
	app.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if c, ok := aarv.FromRequest(r); ok {
				c.Set("requestId", "req-XYZ")
				r = c.RawRequest()
			}
			next.ServeHTTP(w, r)
		})
	})
	app.Get("/p", func(c *aarv.Context) error { return aarv.ErrBadRequest("nope") })

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/p", nil))

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["request_id"] != "req-XYZ" {
		t.Fatalf("request_id extension missing or wrong: %v", body)
	}
}

func TestHandler_GenericErrorMaskedAndOnInternalCalled(t *testing.T) {
	var captured error
	cfg := Config{
		OnInternal: func(c *aarv.Context, err error) { captured = err },
	}
	rec, body := problemRequest(t, cfg, func(c *aarv.Context) error {
		return errors.New("rds connection refused; dialing 10.0.0.1:5432")
	})

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status=%d, want 500", rec.Code)
	}
	// On the wire: nothing internal leaks.
	for _, secret := range []string{"rds", "10.0.0.1", "5432"} {
		if strings.Contains(rec.Body.String(), secret) {
			t.Errorf("body leaked %q: %s", secret, rec.Body.String())
		}
	}
	if body["title"] != "internal_error" {
		t.Errorf("title=%v, want 'internal_error'", body["title"])
	}
	// Off the wire: OnInternal sees the real error.
	if captured == nil || !strings.Contains(captured.Error(), "rds") {
		t.Fatalf("OnInternal must receive the original err; got %v", captured)
	}
}

func TestHandler_OnInternalNotCalledForExplicitAppError5xx(t *testing.T) {
	// AppError 5xx WITHOUT a non-nil Internal() means the caller
	// explicitly chose the status to communicate state to the client
	// (e.g. ErrServiceUnavailable("draining"), ErrGatewayTimeout(...)).
	// OnInternal must NOT fire — those are routine signals, not
	// observability events.
	called := false
	cfg := Config{
		OnInternal: func(c *aarv.Context, err error) { called = true },
	}
	_, _ = problemRequest(t, cfg, func(c *aarv.Context) error {
		return aarv.ErrServiceUnavailable("draining")
	})
	if called {
		t.Fatal("OnInternal must not fire for explicit AppError 5xx with no Internal()")
	}
}

func TestHandler_OnInternalFiresForAppErrorWithInternal(t *testing.T) {
	// aarv.ErrInternal(downstreamErr) wraps the downstream failure on
	// AppError.Internal() and presents a masked "Internal server
	// error" body. Without this branch, the downstream err would be
	// silently swallowed when the caller uses problem+json — losing
	// observability parity with the framework's default error handler
	// (server.go handleError logs in this same case).
	wrapped := errors.New("downstream call failed: connection reset")
	var captured error
	cfg := Config{
		OnInternal: func(c *aarv.Context, err error) { captured = err },
	}
	rec, body := problemRequest(t, cfg, func(c *aarv.Context) error {
		return aarv.ErrInternal(wrapped)
	})

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status=%d, want 500", rec.Code)
	}
	// On the wire: the wrapped error string must NOT leak.
	if strings.Contains(rec.Body.String(), "connection reset") || strings.Contains(rec.Body.String(), "downstream") {
		t.Errorf("wrapped err leaked to wire: %s", rec.Body.String())
	}
	if body["title"] != "internal_error" {
		t.Errorf("title=%v, want 'internal_error'", body["title"])
	}
	// Off the wire: OnInternal must receive the unwrapped Internal() err.
	if captured != wrapped {
		t.Fatalf("OnInternal must receive AppError.Internal() unchanged; got %v, want %v", captured, wrapped)
	}
}

func TestHandler_FallbackBodyOnMarshalFailure(t *testing.T) {
	// Force json.Marshal to fail by putting an unmarshalable channel
	// into Extensions through a custom error that FromError preserves.
	// FromError doesn't carry channels, so we exercise fallbackBody
	// directly to keep the test deterministic.
	body := fallbackBody(http.StatusInternalServerError)
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("fallback body must itself be valid JSON: %v", err)
	}
	if out["type"] != DefaultType || out["status"] != float64(500) {
		t.Errorf("fallback body shape unexpected: %v", out)
	}
}

func TestFallbackBody_DefaultsZeroStatus(t *testing.T) {
	body := fallbackBody(0)
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if out["status"] != float64(500) {
		t.Errorf("zero status must default to 500; got %v", out["status"])
	}
}

func TestItoa_BoundaryCases(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{-1, "500"}, {1000, "500"}, // out-of-range
		{0, "0"}, {9, "9"}, // single digit
		{10, "10"}, {99, "99"}, // two digit
		{100, "100"}, {200, "200"}, {404, "404"}, {500, "500"}, {999, "999"}, // three digit
	}
	for _, tc := range cases {
		if got := itoa(tc.in); got != tc.want {
			t.Errorf("itoa(%d)=%q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCombineMessageAndDetail(t *testing.T) {
	cases := []struct {
		msg, detail, want string
	}{
		{"", "", ""},
		{"hello", "", "hello"},
		{"", "world", "world"},
		{"hello", "world", "hello (world)"},
	}
	for _, tc := range cases {
		if got := combineMessageAndDetail(tc.msg, tc.detail); got != tc.want {
			t.Errorf("combine(%q,%q)=%q, want %q", tc.msg, tc.detail, got, tc.want)
		}
	}
}

func TestMarshalOrFallback_HappyPath(t *testing.T) {
	body := marshalOrFallback(&Details{Status: 400, Title: "x"})
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("happy-path output must be valid JSON: %v", err)
	}
	if out["title"] != "x" {
		t.Errorf("title=%v, want x", out["title"])
	}
}

func TestMarshalOrFallback_UsesFallbackOnUnmarshalable(t *testing.T) {
	// chan is not JSON-marshalable; this is the canonical way to force
	// json.Marshal to fail. Putting it into Extensions ensures the
	// failure originates from MarshalJSON's flatten map, exercising the
	// defense-in-depth fallback.
	bad := &Details{
		Status:     http.StatusInternalServerError,
		Title:      "synthetic",
		Extensions: map[string]any{"chan": make(chan int)},
	}
	body := marshalOrFallback(bad)

	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("fallback body must itself be valid JSON: %v", err)
	}
	// Fallback body asserts the static shape, not the original Title —
	// the original problem failed to marshal and has been replaced.
	if out["title"] != "internal_error" {
		t.Errorf("fallback title=%v, want internal_error", out["title"])
	}
	if out["status"] != float64(http.StatusInternalServerError) {
		t.Errorf("fallback status=%v, want 500", out["status"])
	}
}
