package sanitize

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nilshah80/aarv"
	"golang.org/x/text/unicode/norm"
)

// echoApp returns an app whose POST handler echos the parsed JSON body
// back as the response body, so tests can read what the handler saw
// after the sanitizer ran.
func echoApp(t *testing.T, mw any) *aarv.App {
	t.Helper()
	app := aarv.New()
	app.Use(mw)
	app.Post("/", func(c *aarv.Context) error {
		body, err := io.ReadAll(c.BodyReader())
		if err != nil {
			return aarv.ErrBadRequest(err.Error())
		}
		return c.Blob(http.StatusOK, "application/json", body)
	})
	app.Get("/skip", func(c *aarv.Context) error {
		return c.Text(http.StatusOK, "skipped")
	})
	return app
}

func postJSON(app *aarv.App, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	return rec
}

func decodeJSON(t *testing.T, body []byte) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("body is not JSON: %v body=%s", err, body)
	}
	return v
}

func TestStripHTML_TopLevelString(t *testing.T) {
	app := echoApp(t, New(Config{StripHTML: true}))
	rec := postJSON(app, `{"comment":"hello <script>alert(1)</script> world"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	got := decodeJSON(t, rec.Body.Bytes()).(map[string]any)
	if v := got["comment"].(string); strings.Contains(v, "<script>") {
		t.Fatalf("HTML not stripped: %q", v)
	}
}

func TestStripHTML_NestedAndArrays(t *testing.T) {
	app := echoApp(t, New(Config{StripHTML: true}))
	rec := postJSON(app, `{"user":{"bio":"<b>hi</b>","tags":["<i>a</i>","<u>b</u>"]}}`)
	got := decodeJSON(t, rec.Body.Bytes()).(map[string]any)
	user := got["user"].(map[string]any)
	if v := user["bio"].(string); v != "hi" {
		t.Fatalf("bio=%q", v)
	}
	tags := user["tags"].([]any)
	if tags[0].(string) != "a" || tags[1].(string) != "b" {
		t.Fatalf("tags=%v", tags)
	}
}

func TestStripHTML_DecodesEntities(t *testing.T) {
	app := echoApp(t, New(Config{StripHTML: true}))
	rec := postJSON(app, `{"msg":"Tom &amp; Jerry &lt;3"}`)
	got := decodeJSON(t, rec.Body.Bytes()).(map[string]any)
	if v := got["msg"].(string); v != "Tom & Jerry <3" {
		t.Fatalf("msg=%q", v)
	}
}

func TestFields_Allowlist(t *testing.T) {
	app := echoApp(t, New(Config{StripHTML: true, Fields: []string{"comment"}}))
	rec := postJSON(app, `{"comment":"<b>x</b>","title":"<b>y</b>"}`)
	got := decodeJSON(t, rec.Body.Bytes()).(map[string]any)
	if got["comment"].(string) != "x" {
		t.Fatalf("comment=%q", got["comment"])
	}
	if got["title"].(string) != "<b>y</b>" {
		t.Fatalf("title (not in allowlist) was sanitized: %q", got["title"])
	}
}

func TestSkipFields_Blocklist(t *testing.T) {
	app := echoApp(t, New(Config{StripHTML: true, SkipFields: []string{"password"}}))
	rec := postJSON(app, `{"password":"<P@ss>","note":"<b>x</b>"}`)
	got := decodeJSON(t, rec.Body.Bytes()).(map[string]any)
	if got["password"].(string) != "<P@ss>" {
		t.Fatalf("password should be untouched: %q", got["password"])
	}
	if got["note"].(string) != "x" {
		t.Fatalf("note=%q", got["note"])
	}
}

func TestCustom_OrderingAfterBuiltins(t *testing.T) {
	upper := func(s string) string { return strings.ToUpper(s) }
	prefix := func(s string) string { return "X:" + s }
	app := echoApp(t, New(Config{StripHTML: true, Custom: []SanitizerFunc{upper, prefix}}))
	rec := postJSON(app, `{"v":"<b>hi</b>"}`)
	got := decodeJSON(t, rec.Body.Bytes()).(map[string]any)
	if got["v"].(string) != "X:HI" {
		t.Fatalf("v=%q", got["v"])
	}
}

func TestNFC_Normalization(t *testing.T) {
	// "café" with combining acute (NFD) should normalize to NFC.
	nfd := "café"
	expected := norm.NFC.String(nfd)
	body, _ := json.Marshal(map[string]string{"city": nfd})
	app := echoApp(t, New(Config{NormalizeUnicode: true}))
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	got := decodeJSON(t, rec.Body.Bytes()).(map[string]any)
	if got["city"].(string) != expected {
		t.Fatalf("nfc mismatch got=%q want=%q", got["city"], expected)
	}
}

func TestInvalidJSON_Passthrough(t *testing.T) {
	app := echoApp(t, New(Config{StripHTML: true}))
	rec := postJSON(app, `{not json`)
	if rec.Code != http.StatusOK {
		t.Fatalf("invalid JSON should pass through to handler unchanged; got %d", rec.Code)
	}
	if rec.Body.String() != `{not json` {
		t.Fatalf("body=%q", rec.Body.String())
	}
}

func TestMaxBodyBytes_413(t *testing.T) {
	app := echoApp(t, New(Config{StripHTML: true, MaxBodyBytes: 16}))
	big := strings.Repeat(`{"x":"y"}`, 10) // > 16 bytes
	rec := postJSON(app, big)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("want 413, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestContentType_TextBypassed(t *testing.T) {
	app := echoApp(t, New(Config{StripHTML: true}))
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("<b>not json</b>"))
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Body.String() != "<b>not json</b>" {
		t.Fatalf("text body should pass through; got %q", rec.Body.String())
	}
}

func TestSkipper_Bypass(t *testing.T) {
	app := echoApp(t, New(Config{
		StripHTML: true,
		Skipper: func(c *aarv.Context) bool {
			return c.Header("X-Bypass") != ""
		},
	}))
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"v":"<b>x</b>"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Bypass", "1")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "<b>x</b>") {
		t.Fatalf("skipper should have bypassed sanitizer; body=%q", rec.Body.String())
	}
}

func TestSkipPaths_Bypass(t *testing.T) {
	app := echoApp(t, New(Config{StripHTML: true, SkipPaths: []string{"/skip"}}))
	req := httptest.NewRequest(http.MethodGet, "/skip", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("skipped path: %d", rec.Code)
	}
}

func TestPoolReuseIndependence(t *testing.T) {
	// Two requests in sequence with mutating handlers should not see each
	// other's data via any pooled buffer.
	app := echoApp(t, New(Config{StripHTML: true}))
	r1 := postJSON(app, `{"v":"<b>first</b>"}`)
	r2 := postJSON(app, `{"v":"<b>second</b>"}`)
	g1 := decodeJSON(t, r1.Body.Bytes()).(map[string]any)
	g2 := decodeJSON(t, r2.Body.Bytes()).(map[string]any)
	if g1["v"].(string) != "first" || g2["v"].(string) != "second" {
		t.Fatalf("pool independence: g1=%v g2=%v", g1, g2)
	}
}

// nonNativeMW forces the runtime onto the stdlib path.
func nonNativeMW() aarv.Middleware {
	return aarv.Middleware(func(next http.Handler) http.Handler { return next })
}

func TestStdlibPath_StripHTML(t *testing.T) {
	app := aarv.New()
	app.Use(nonNativeMW())
	app.Use(New(Config{StripHTML: true}))
	app.Post("/", func(c *aarv.Context) error {
		body, _ := io.ReadAll(c.BodyReader())
		return c.Blob(http.StatusOK, "application/json", body)
	})

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"v":"<b>x</b>"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	got := decodeJSON(t, rec.Body.Bytes()).(map[string]any)
	if got["v"].(string) != "x" {
		t.Fatalf("stdlib strip: %q", got["v"])
	}
}

func TestStdlibPath_MaxBodyBytes_413(t *testing.T) {
	app := aarv.New()
	app.Use(nonNativeMW())
	app.Use(New(Config{StripHTML: true, MaxBodyBytes: 8}))
	app.Post("/", func(c *aarv.Context) error { return c.Text(http.StatusOK, "ok") })

	big := strings.Repeat(`x`, 200)
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"v":"`+big+`"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("stdlib 413: %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestStdlibPath_ContentTypeBypassed(t *testing.T) {
	app := aarv.New()
	app.Use(nonNativeMW())
	app.Use(New(Config{StripHTML: true}))
	app.Post("/", func(c *aarv.Context) error {
		body, _ := io.ReadAll(c.BodyReader())
		return c.Blob(http.StatusOK, "text/plain", body)
	})

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("<b>x</b>"))
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Body.String() != "<b>x</b>" {
		t.Fatalf("stdlib non-JSON should pass through: %q", rec.Body.String())
	}
}

func TestStdlibPath_InvalidJSONPassthrough(t *testing.T) {
	app := aarv.New()
	app.Use(nonNativeMW())
	app.Use(New(Config{StripHTML: true}))
	app.Post("/", func(c *aarv.Context) error {
		body, _ := io.ReadAll(c.BodyReader())
		return c.Blob(http.StatusOK, "application/json", body)
	})

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{not json`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Body.String() != `{not json` {
		t.Fatalf("stdlib invalid JSON: %q", rec.Body.String())
	}
}

func TestStdlibPath_SkipperAndSkipPaths(t *testing.T) {
	app := aarv.New()
	app.Use(nonNativeMW())
	app.Use(New(Config{
		StripHTML: true,
		Skipper: func(c *aarv.Context) bool {
			return c.Header("X-Bypass") != ""
		},
		SkipPaths: []string{"/skip"},
	}))
	app.Post("/", func(c *aarv.Context) error {
		body, _ := io.ReadAll(c.BodyReader())
		return c.Blob(http.StatusOK, "application/json", body)
	})
	app.Post("/skip", func(c *aarv.Context) error {
		body, _ := io.ReadAll(c.BodyReader())
		return c.Blob(http.StatusOK, "application/json", body)
	})

	// Skipper bypass.
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"v":"<b>x</b>"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Bypass", "1")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "<b>") {
		t.Fatalf("Skipper should bypass: %q", rec.Body.String())
	}

	// SkipPaths bypass.
	req2 := httptest.NewRequest(http.MethodPost, "/skip", strings.NewReader(`{"v":"<b>y</b>"}`))
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	app.ServeHTTP(rec2, req2)
	if !strings.Contains(rec2.Body.String(), "<b>") {
		t.Fatalf("SkipPaths should bypass: %q", rec2.Body.String())
	}
}

func TestStdlibPath_BodyReadError(t *testing.T) {
	// Drive the stdlib middleware directly with a body that errors on
	// Read; expect 400.
	mw := New(Config{StripHTML: true})
	handler := mw.Stdlib(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/", &readErrBody{err: errors.New("boom")})
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("body read error: want 400, got %d", rec.Code)
	}
}

// readErrBody is a body that always fails Read with the configured error.
type readErrBody struct{ err error }

func (b *readErrBody) Read(p []byte) (int, error) { return 0, b.err }
func (b *readErrBody) Close() error               { return nil }

func TestNativePath_BodyReadError(t *testing.T) {
	// Native lane: body error must surface as 400 via aarv.ErrBadRequest.
	app := aarv.New()
	app.Use(New(Config{StripHTML: true}))
	app.Post("/", func(c *aarv.Context) error { return c.Text(http.StatusOK, "ok") })

	req := httptest.NewRequest(http.MethodPost, "/", &readErrBody{err: errors.New("boom")})
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("native body read error: %d", rec.Code)
	}
}

func TestNativePath_MaxBodyBytes_413(t *testing.T) {
	app := aarv.New()
	app.Use(New(Config{StripHTML: true, MaxBodyBytes: 8}))
	app.Post("/", func(c *aarv.Context) error { return c.Text(http.StatusOK, "ok") })
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Repeat(`x`, 200)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("native 413: %d", rec.Code)
	}
}

func TestDefaultConfig_Defaults(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.StripHTML || !cfg.NormalizeUnicode || cfg.MaxBodyBytes != 1<<20 {
		t.Fatalf("DefaultConfig: %+v", cfg)
	}
}

func TestMatchesContentType_EmptyAndUnknown(t *testing.T) {
	n := normalize(Config{}) // ContentTypes empty → defaults to application/json
	if n.matchesContentType("") {
		t.Fatal("empty content-type should not match")
	}
	if n.matchesContentType("text/plain") {
		t.Fatal("text/plain should not match default")
	}
	if !n.matchesContentType("application/json") {
		t.Fatal("application/json should match default")
	}
}

func TestSanitize_EmptyBody(t *testing.T) {
	n := normalize(Config{StripHTML: true})
	out, ok := n.sanitize(nil)
	if !ok || out != nil {
		t.Fatalf("empty body: ok=%v out=%v", ok, out)
	}
}

func TestSanitize_FieldFilter_NestedAllowedPropagates(t *testing.T) {
	// Once an allowed field is entered, all nested strings are
	// sanitized — exercise the insideAllowed=true branch in walk.
	n := normalize(Config{StripHTML: true, Fields: []string{"user"}})
	got, ok := n.sanitize([]byte(`{"user":{"bio":"<b>x</b>","tags":["<i>a</i>"]}}`))
	if !ok {
		t.Fatal("sanitize failed")
	}
	var v map[string]any
	if err := json.Unmarshal(got, &v); err != nil {
		t.Fatal(err)
	}
	user := v["user"].(map[string]any)
	if user["bio"].(string) != "x" {
		t.Fatalf("bio: %q", user["bio"])
	}
	tags := user["tags"].([]any)
	if tags[0].(string) != "a" {
		t.Fatalf("tags[0]: %q", tags[0])
	}
}

func TestDecodeEntity_AllForms(t *testing.T) {
	cases := []struct {
		in   string
		dec  string
		used int
	}{
		{"&amp;rest", "&", len("&amp;")},
		{"&lt;rest", "<", len("&lt;")},
		{"&gt;rest", ">", len("&gt;")},
		{"&quot;rest", `"`, len("&quot;")},
		{"&#39;rest", "'", len("&#39;")},
		{"&apos;rest", "'", len("&apos;")},
		{"&nbsp;rest", " ", len("&nbsp;")},
		{"&unknown;", "", 0},
	}
	for _, tc := range cases {
		dec, used := decodeEntity(tc.in)
		if dec != tc.dec || used != tc.used {
			t.Fatalf("decodeEntity(%q): got %q,%d want %q,%d", tc.in, dec, used, tc.dec, tc.used)
		}
	}
}

func TestStripHTML_BareAmpersand(t *testing.T) {
	// "&" without a following recognized entity should be preserved.
	if got := stripHTML("a & b"); got != "a & b" {
		t.Fatalf("bare ampersand: %q", got)
	}
}

func TestReadAndCap_NilReader(t *testing.T) {
	body, err := readAndCap(nil, 0)
	if err != nil || body != nil {
		t.Fatalf("nil reader: body=%v err=%v", body, err)
	}
}

func TestReadAndCap_Unbounded(t *testing.T) {
	body, err := readAndCap(strings.NewReader("hello"), 0)
	if err != nil || string(body) != "hello" {
		t.Fatalf("unbounded: body=%q err=%v", body, err)
	}
}

func TestReadAndCap_ReadError(t *testing.T) {
	_, err := readAndCap(&readErrBody{err: errors.New("boom")}, 100)
	if err == nil {
		t.Fatal("expected read error")
	}
}

func TestCodeForStatus_AllBranches(t *testing.T) {
	cases := map[int]string{
		http.StatusRequestEntityTooLarge: "payload_too_large",
		http.StatusBadRequest:            "bad_request",
		http.StatusTeapot:                http.StatusText(http.StatusTeapot),
	}
	for s, want := range cases {
		if got := codeForStatus(s); got != want {
			t.Fatalf("codeForStatus(%d): %q != %q", s, got, want)
		}
	}
}

func TestWriteJSONError_Format(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSONError(rec, http.StatusBadRequest, "bad")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "bad_request") || !strings.Contains(rec.Body.String(), "bad") {
		t.Fatalf("body: %q", rec.Body.String())
	}
}

func TestNormalize_CustomContentTypes(t *testing.T) {
	n := normalize(Config{ContentTypes: []string{"application/x-yaml", " text/csv "}})
	if !n.matchesContentType("application/x-yaml") {
		t.Fatal("yaml")
	}
	if !n.matchesContentType("text/csv; charset=utf-8") {
		t.Fatal("csv")
	}
}

func TestSanitize_NonStringNonContainerPassthrough(t *testing.T) {
	// Walk's default branch handles primitives (numbers, bools, nil).
	n := normalize(Config{StripHTML: true})
	got, ok := n.sanitize([]byte(`{"n":42,"b":true,"x":null}`))
	if !ok {
		t.Fatal("sanitize failed")
	}
	if !strings.Contains(string(got), "42") || !strings.Contains(string(got), "true") || !strings.Contains(string(got), "null") {
		t.Fatalf("primitives lost: %s", got)
	}
}

func TestSanitize_FieldNotInAllowlist(t *testing.T) {
	// Fields filter: a top-level field that IS in n.fields exercises
	// the `if _, ok := n.fields[keyName]; ok` branch in shouldSanitizeString
	// for top-level strings.
	n := normalize(Config{StripHTML: true, Fields: []string{"top"}})
	got, ok := n.sanitize([]byte(`{"top":"<b>x</b>","other":"<b>y</b>"}`))
	if !ok {
		t.Fatal("sanitize failed")
	}
	var v map[string]any
	_ = json.Unmarshal(got, &v)
	if v["top"].(string) != "x" {
		t.Fatalf("top: %q", v["top"])
	}
	if v["other"].(string) != "<b>y</b>" {
		t.Fatalf("other: %q", v["other"])
	}
}

func TestStripHTML_NoSpecialChars_FastPath(t *testing.T) {
	// Input without '<' or '&' should return unchanged via the fast path.
	if got := stripHTML("hello world"); got != "hello world" {
		t.Fatalf("fast path: %q", got)
	}
}

func TestStripHTML_UnterminatedComment(t *testing.T) {
	// "<!-- ..." without "-->" returns whatever was accumulated before.
	if got := stripHTML("ab<!-- never closed"); got != "ab" {
		t.Fatalf("unterminated comment: %q", got)
	}
}

func TestReadAndCap_AtCapBoundary(t *testing.T) {
	// Body of exactly maxBytes — must succeed (return body, false, nil).
	body, err := readAndCap(strings.NewReader("12345"), 5)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "12345" {
		t.Fatalf("body: %q", body)
	}
}

func TestSanitize_ReencodingErrorPath_Unreachable(t *testing.T) {
	// json.Marshal of a successfully-decoded value should never fail
	// in practice; this test just covers the call path. We rely on
	// other tests to assert correctness; here we just confirm round-
	// trip on an empty object.
	n := normalize(Config{StripHTML: true})
	out, ok := n.sanitize([]byte(`{}`))
	if !ok || string(out) != "{}" {
		t.Fatalf("empty object: %q ok=%v", out, ok)
	}
}

func TestStripHTML_UnterminatedTag(t *testing.T) {
	if got := stripHTML("hello <unterminated"); got != "hello " {
		t.Fatalf("unterminated tag handling: %q", got)
	}
}

func TestStripHTML_Comment(t *testing.T) {
	if got := stripHTML("a<!-- skip me -->b"); got != "ab" {
		t.Fatalf("comment handling: %q", got)
	}
}
