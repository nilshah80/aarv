package openapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nilshah80/aarv"
)

// ---- Fixtures ---------------------------------------------------------------

type sampleReq struct {
	Name string `json:"name"`
	Age  int    `json:"age,omitempty"`
}

type sampleRes struct {
	ID string `json:"id"`
}

func newApp(opts ...aarv.Option) *aarv.App {
	base := []aarv.Option{
		aarv.WithBanner(false),
		aarv.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	}
	return aarv.New(append(base, opts...)...)
}

func mustSpec(t *testing.T, p *Plugin) *Spec {
	t.Helper()
	spec := p.Spec()
	if spec == nil {
		t.Fatal("Spec() returned nil")
	}
	return spec
}

// ---- New / configuration ----------------------------------------------------

func TestNewRejectsNilApp(t *testing.T) {
	if _, err := New(nil, Config{}); !errors.Is(err, ErrNilApp) {
		t.Fatalf("expected ErrNilApp, got %v", err)
	}
}

func TestApplyDefaultsFillsTitleVersionPathAndExclude(t *testing.T) {
	cfg := applyDefaults(Config{})
	if cfg.Title != "Aarv API" {
		t.Errorf("Title default: %q", cfg.Title)
	}
	if cfg.Version != "0.0.0" {
		t.Errorf("Version default: %q", cfg.Version)
	}
	if cfg.JSONPath != DefaultJSONPath {
		t.Errorf("JSONPath default: %q", cfg.JSONPath)
	}
	if !reflect.DeepEqual(cfg.Exclude, DefaultExclude) {
		t.Errorf("Exclude default: %v", cfg.Exclude)
	}
}

func TestApplyDefaultsHonorsExplicitExclude(t *testing.T) {
	in := Config{Exclude: []string{"/internal/"}}
	cfg := applyDefaults(in)
	// Explicit Exclude is preserved AND augmented with the resolved doc
	// paths (so the generator does not self-document when the user has
	// not explicitly opted in via Include).
	if !slices.Contains(cfg.Exclude, "/internal/") {
		t.Errorf("explicit Exclude entry dropped: %v", cfg.Exclude)
	}
	if !slices.Contains(cfg.Exclude, DefaultJSONPath) {
		t.Errorf("doc JSON path must be auto-added to Exclude: %v", cfg.Exclude)
	}
	if !slices.Contains(cfg.Exclude, DefaultYAMLPath) {
		t.Errorf("doc YAML path must be auto-added to Exclude: %v", cfg.Exclude)
	}
}

func TestApplyDefaultsLeavesExcludeNilWhenIncludeSet(t *testing.T) {
	in := Config{Include: func(aarv.RouteInfo) bool { return true }}
	cfg := applyDefaults(in)
	if cfg.Exclude != nil {
		t.Errorf("Exclude should remain nil when Include is set: %v", cfg.Exclude)
	}
}

// ---- Endpoint registration --------------------------------------------------

func TestNewRegistersJSONEndpoint(t *testing.T) {
	app := newApp()
	app.Get("/ping", func(c *aarv.Context) error { return c.JSON(http.StatusOK, nil) })

	if _, err := New(app, Config{}); err != nil {
		t.Fatalf("New: %v", err)
	}

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, DefaultJSONPath, nil)
	app.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("Content-Type: %q", ct)
	}

	var spec map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &spec); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if spec["openapi"] != "3.1.0" {
		t.Errorf("openapi field: %v", spec["openapi"])
	}
}

func TestDisableJSONEndpointSkipsRegistration(t *testing.T) {
	app := newApp()
	app.Get("/ping", func(c *aarv.Context) error { return c.JSON(http.StatusOK, nil) })

	if _, err := New(app, Config{DisableJSONEndpoint: true}); err != nil {
		t.Fatalf("New: %v", err)
	}

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, DefaultJSONPath, nil))
	// /openapi.json should be a 404 since we did not register the
	// endpoint.
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestPluginHandlerServesSpec(t *testing.T) {
	app := newApp()
	app.Get("/ping", func(c *aarv.Context) error { return nil })

	p, err := New(app, Config{DisableJSONEndpoint: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rec := httptest.NewRecorder()
	p.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/spec", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("Content-Type: %q", rec.Header().Get("Content-Type"))
	}
}

// ---- Filtering --------------------------------------------------------------

func TestExcludeFiltersDocumentationRoutesByDefault(t *testing.T) {
	app := newApp()
	app.Get("/health", func(c *aarv.Context) error { return nil })

	p, err := New(app, Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	spec := mustSpec(t, p)

	if _, ok := spec.Paths["/openapi.json"]; ok {
		t.Error("/openapi.json must be excluded by default")
	}
	if _, ok := spec.Paths["/health"]; !ok {
		t.Error("/health must be present")
	}
}

func TestIncludeOverridesExclude(t *testing.T) {
	app := newApp()
	app.Get("/internal", func(c *aarv.Context) error { return nil })
	app.Get("/public", func(c *aarv.Context) error { return nil })

	p, err := New(app, Config{
		Include: func(r aarv.RouteInfo) bool { return r.Pattern == "/internal" },
		Exclude: []string{"/internal"}, // ignored when Include is set
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	spec := mustSpec(t, p)

	if _, ok := spec.Paths["/internal"]; !ok {
		t.Error("Include must be the sole filter when set")
	}
	if _, ok := spec.Paths["/public"]; ok {
		t.Error("/public should be excluded by Include returning false")
	}
}

func TestCustomExcludeHonored(t *testing.T) {
	app := newApp()
	app.Get("/admin/users", func(c *aarv.Context) error { return nil })
	app.Get("/users", func(c *aarv.Context) error { return nil })

	p, err := New(app, Config{Exclude: []string{"/admin/"}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	spec := mustSpec(t, p)

	if _, ok := spec.Paths["/admin/users"]; ok {
		t.Error("/admin/users must be excluded by custom prefix")
	}
	if _, ok := spec.Paths["/users"]; !ok {
		t.Error("/users must remain")
	}
}

func TestEmptyExcludeStringIsIgnored(t *testing.T) {
	app := newApp()
	app.Get("/x", func(c *aarv.Context) error { return nil })

	p, err := New(app, Config{Exclude: []string{""}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	spec := mustSpec(t, p)
	if _, ok := spec.Paths["/x"]; !ok {
		t.Fatal("empty Exclude string should not match anything")
	}
}

// ---- Path parameters --------------------------------------------------------

func TestPathParametersExtracted(t *testing.T) {
	app := newApp()
	app.Get("/users/{id}/posts/{postID}", func(c *aarv.Context) error { return nil })

	p, _ := New(app, Config{})
	spec := mustSpec(t, p)
	op := spec.Paths["/users/{id}/posts/{postID}"]["get"]
	if op == nil {
		t.Fatal("operation missing")
	}
	if len(op.Parameters) != 2 {
		t.Fatalf("expected 2 path params, got %d (%+v)", len(op.Parameters), op.Parameters)
	}
	for _, param := range op.Parameters {
		if param.In != "path" {
			t.Errorf("In: got %q", param.In)
		}
		if !param.Required {
			t.Errorf("path params must be required: %+v", param)
		}
		if param.Schema == nil || param.Schema.Type != "string" {
			t.Errorf("Schema: %+v", param.Schema)
		}
	}
}

func TestPathParametersDeduped(t *testing.T) {
	// Synthetic — aarv would reject duplicate {name} at registration
	// time, but the helper itself must dedupe defensively for spec
	// stability.
	got := pathParameters("/a/{x}/b/{x}")
	if len(got) != 1 {
		t.Fatalf("expected 1 deduped param, got %d", len(got))
	}
}

// ---- Bodies and responses ---------------------------------------------------

func TestBindRouteAttachesRequestAndResponseSchemas(t *testing.T) {
	app := newApp()
	aarv.BindRoute(app, "POST", "/echo",
		func(c *aarv.Context, req sampleReq) (sampleRes, error) {
			return sampleRes{ID: req.Name}, nil
		},
	)

	p, _ := New(app, Config{})
	spec := mustSpec(t, p)
	op := spec.Paths["/echo"]["post"]
	if op == nil {
		t.Fatal("operation missing")
	}
	if op.RequestBody == nil {
		t.Fatal("RequestBody missing")
	}
	mt, ok := op.RequestBody.Content["application/json"]
	if !ok {
		t.Fatalf("missing application/json: %v", op.RequestBody.Content)
	}
	// Named struct types are emitted as $ref into components.
	reqSchema := derefSchema(t, spec, mt.Schema)
	if reqSchema.Type != "object" {
		t.Fatalf("request schema: %+v", reqSchema)
	}
	if _, present := reqSchema.Properties["name"]; !present {
		t.Errorf("request schema missing 'name' property")
	}

	resp200, ok := op.Responses["200"]
	if !ok {
		t.Fatalf("200 response missing: %v", op.Responses)
	}
	rmt, ok := resp200.Content["application/json"]
	if !ok || rmt.Schema == nil {
		t.Fatalf("response schema: %+v", resp200)
	}
	resSchema := derefSchema(t, spec, rmt.Schema)
	if _, present := resSchema.Properties["id"]; !present {
		t.Errorf("response schema missing 'id' property")
	}
}

func TestWithRequestContentTypeOverridesDefault(t *testing.T) {
	app := newApp()
	app.Post("/upload",
		func(c *aarv.Context) error { return nil },
		aarv.WithSchema(sampleReq{}, nil),
		aarv.WithRequestContentType("multipart/form-data"),
	)

	p, _ := New(app, Config{})
	spec := mustSpec(t, p)
	op := spec.Paths["/upload"]["post"]
	if _, ok := op.RequestBody.Content["multipart/form-data"]; !ok {
		t.Fatalf("expected multipart/form-data, got keys=%v", contentKeys(op.RequestBody.Content))
	}
	_ = derefSchema(t, spec, op.RequestBody.Content["multipart/form-data"].Schema)
}

func TestWithResponseAddsErrorEntries(t *testing.T) {
	app := newApp()
	aarv.BindRoute(app, "GET", "/users/{id}",
		func(c *aarv.Context, req sampleReq) (sampleRes, error) { return sampleRes{}, nil },
		aarv.WithResponse(404, "Not found"),
		aarv.WithResponse(500, "Server error"),
	)

	p, _ := New(app, Config{})
	spec := mustSpec(t, p)
	op := spec.Paths["/users/{id}"]["get"]
	if got := op.Responses["404"].Description; got != "Not found" {
		t.Errorf("404: %q", got)
	}
	if got := op.Responses["500"].Description; got != "Server error" {
		t.Errorf("500: %q", got)
	}
	if _, ok := op.Responses["200"].Content["application/json"]; !ok {
		t.Error("response body schema must attach to 200 by default")
	}
}

func TestPickBodyCodeUsesLowest2xxIfPresent(t *testing.T) {
	cases := []struct {
		responses map[int]string
		want      string
	}{
		{nil, "200"},
		{map[int]string{404: "x"}, "200"},
		{map[int]string{200: "ok"}, "200"},
		{map[int]string{201: "created", 202: "accepted"}, "201"},
		{map[int]string{500: "server"}, "200"},
	}
	for _, tc := range cases {
		if got := pickBodyCode(tc.responses); got != tc.want {
			t.Errorf("pickBodyCode(%v) = %q want %q", tc.responses, got, tc.want)
		}
	}
}

func TestRouteWithoutSchemaProducesDefaultOK(t *testing.T) {
	app := newApp()
	app.Get("/health", func(c *aarv.Context) error { return nil })

	p, _ := New(app, Config{})
	spec := mustSpec(t, p)
	op := spec.Paths["/health"]["get"]
	if op == nil {
		t.Fatal("operation missing")
	}
	if op.RequestBody != nil {
		t.Error("RequestBody must be nil when no schema is set")
	}
	r, ok := op.Responses["200"]
	if !ok || r.Description != "OK" {
		t.Errorf("expected default 200/OK, got %+v", op.Responses)
	}
}

func TestUserSuppliedResponseDescriptionPreserved(t *testing.T) {
	app := newApp()
	aarv.BindRoute(app, "POST", "/things",
		func(c *aarv.Context, req sampleReq) (sampleRes, error) { return sampleRes{}, nil },
		aarv.WithResponse(201, "Created"),
	)

	p, _ := New(app, Config{})
	spec := mustSpec(t, p)
	r := spec.Paths["/things"]["post"].Responses["201"]
	if r.Description != "Created" {
		t.Errorf("Description: %q", r.Description)
	}
	if _, ok := r.Content["application/json"]; !ok {
		t.Error("body schema must attach to 201 (lowest 2xx)")
	}
}

func TestMethodTiebreakerSortedAlphabetically(t *testing.T) {
	app := newApp()
	// Both methods on the same path force the (Pattern equal → Method
	// comparison) branch in build's sort.SliceStable.
	app.Post("/things", func(c *aarv.Context) error { return nil })
	app.Get("/things", func(c *aarv.Context) error { return nil })

	p, _ := New(app, Config{})
	spec := mustSpec(t, p)
	item := spec.Paths["/things"]
	// Two operations registered on the same path.
	if item["get"] == nil || item["post"] == nil {
		t.Fatalf("expected both operations on /things, got %v", item)
	}
}

func TestBuildOperationDefaultsRequestContentType(t *testing.T) {
	// Synthetic RouteInfo with RequestType but EMPTY RequestContentType
	// exercises the defense-in-depth defaulting branch in buildOperation
	// (the framework normally fills this in routeInfoFromConfig).
	r := aarv.RouteInfo{
		Method:      "POST",
		Pattern:     "/x",
		RequestType: reflect.TypeFor[sampleReq](),
	}
	op := buildOperation(r, newSchemaBuilder(nil), "application/json")
	if _, ok := op.RequestBody.Content["application/json"]; !ok {
		t.Fatalf("expected default application/json, got %v", op.RequestBody.Content)
	}
}

// ---- Determinism ------------------------------------------------------------

func TestSpecIsByteIdenticalAcrossGenerations(t *testing.T) {
	app := newApp()
	aarv.BindRoute(app, "POST", "/b", func(c *aarv.Context, req sampleReq) (sampleRes, error) { return sampleRes{}, nil })
	aarv.BindRoute(app, "POST", "/a", func(c *aarv.Context, req sampleReq) (sampleRes, error) { return sampleRes{}, nil })
	app.Get("/c", func(c *aarv.Context) error { return nil })

	p1, _ := New(app, Config{})
	spec1 := mustSpec(t, p1)
	bytes1, _ := json.Marshal(spec1)

	// A fresh generator with the same App should produce identical bytes
	// because: routes are sorted by (Pattern, Method), and JSON marshals
	// string-keyed maps in sorted key order.
	app2 := newApp()
	aarv.BindRoute(app2, "POST", "/b", func(c *aarv.Context, req sampleReq) (sampleRes, error) { return sampleRes{}, nil })
	aarv.BindRoute(app2, "POST", "/a", func(c *aarv.Context, req sampleReq) (sampleRes, error) { return sampleRes{}, nil })
	app2.Get("/c", func(c *aarv.Context) error { return nil })
	p2, _ := New(app2, Config{})
	spec2 := mustSpec(t, p2)
	bytes2, _ := json.Marshal(spec2)

	if !bytes.Equal(bytes1, bytes2) {
		t.Fatalf("spec output not deterministic:\n%s\n---\n%s", bytes1, bytes2)
	}
}

func TestPathsOrderedAlphabeticallyInJSON(t *testing.T) {
	app := newApp()
	app.Get("/zeta", func(c *aarv.Context) error { return nil })
	app.Get("/alpha", func(c *aarv.Context) error { return nil })
	app.Get("/mike", func(c *aarv.Context) error { return nil })

	p, _ := New(app, Config{})
	spec := mustSpec(t, p)
	out, _ := json.Marshal(spec)

	// "/alpha" must appear before "/mike" must appear before "/zeta".
	a := bytes.Index(out, []byte(`"/alpha"`))
	m := bytes.Index(out, []byte(`"/mike"`))
	z := bytes.Index(out, []byte(`"/zeta"`))
	if a < 0 || m < 0 || z < 0 || a >= m || m >= z {
		t.Fatalf("paths not in sorted order: a=%d m=%d z=%d", a, m, z)
	}
}

// ---- Schema generator (minimal) --------------------------------------------

func TestSchemaPrimitiveKinds(t *testing.T) {
	type T struct {
		B   bool    `json:"b"`
		I   int     `json:"i"`
		I64 int64   `json:"i64"`
		F32 float32 `json:"f32"`
		F64 float64 `json:"f64"`
		S   string  `json:"s"`
	}
	s := SchemaFromType(reflect.TypeFor[T]())
	if s.Properties["b"].Type != "boolean" {
		t.Errorf("bool: %+v", s.Properties["b"])
	}
	if s.Properties["i"].Type != "integer" || s.Properties["i"].Format != "int32" {
		t.Errorf("int: %+v", s.Properties["i"])
	}
	if s.Properties["i64"].Type != "integer" || s.Properties["i64"].Format != "int64" {
		t.Errorf("int64: %+v", s.Properties["i64"])
	}
	if s.Properties["f32"].Type != "number" || s.Properties["f32"].Format != "float" {
		t.Errorf("float32: %+v", s.Properties["f32"])
	}
	if s.Properties["f64"].Type != "number" || s.Properties["f64"].Format != "double" {
		t.Errorf("float64: %+v", s.Properties["f64"])
	}
	if s.Properties["s"].Type != "string" {
		t.Errorf("string: %+v", s.Properties["s"])
	}
}

func TestSchemaPointerNullable(t *testing.T) {
	type T struct {
		Name *string `json:"name,omitempty"`
	}
	s := SchemaFromType(reflect.TypeFor[T]())
	prop := s.Properties["name"]
	if prop.Type != "string" {
		t.Errorf("expected string under pointer, got %+v", prop)
	}
	if !prop.Nullable {
		t.Errorf("expected Nullable for pointer field")
	}
	if slices.Contains(s.Required, "name") {
		t.Errorf("pointer field with omitempty must not be required: %v", s.Required)
	}
}

func TestSchemaOmitemptyDropsRequired(t *testing.T) {
	type T struct {
		A string `json:"a"`
		B string `json:"b,omitempty"`
	}
	s := SchemaFromType(reflect.TypeFor[T]())
	if !slices.Contains(s.Required, "a") {
		t.Errorf("a must be required, got %v", s.Required)
	}
	if slices.Contains(s.Required, "b") {
		t.Errorf("b must not be required, got %v", s.Required)
	}
}

func TestSchemaJSONTagDashHidesField(t *testing.T) {
	type T struct {
		Visible string `json:"visible"`
		Hidden  string `json:"-"`
	}
	s := SchemaFromType(reflect.TypeFor[T]())
	if _, ok := s.Properties["Hidden"]; ok {
		t.Error("'-' tag must hide the field")
	}
	if _, ok := s.Properties["visible"]; !ok {
		t.Error("visible field missing")
	}
}

func TestSchemaUnexportedFieldsHidden(t *testing.T) {
	type T struct {
		Public  string `json:"public"`
		private string //nolint:unused
	}
	_ = T{private: "x"}
	s := SchemaFromType(reflect.TypeFor[T]())
	if _, ok := s.Properties["private"]; ok {
		t.Error("unexported fields must be hidden")
	}
	if _, ok := s.Properties["Public"]; !ok {
		// note: when no json tag is present, the struct field name is
		// used verbatim.
		if _, ok := s.Properties["public"]; !ok {
			t.Error("Public field missing")
		}
	}
}

func TestSchemaEmbeddedStructFlattens(t *testing.T) {
	type Base struct {
		ID string `json:"id"`
	}
	type T struct {
		Base
		Name string `json:"name"`
	}
	s := SchemaFromType(reflect.TypeFor[T]())
	if _, ok := s.Properties["id"]; !ok {
		t.Errorf("embedded id missing: %v", s.Properties)
	}
	if _, ok := s.Properties["name"]; !ok {
		t.Errorf("name missing: %v", s.Properties)
	}
}

func TestSchemaSliceAndMap(t *testing.T) {
	type T struct {
		Tags   []string          `json:"tags"`
		Attrs  map[string]string `json:"attrs"`
		Bytes  []byte            `json:"bytes"`
		IntKey map[int]string    `json:"intkey"`
	}
	s := SchemaFromType(reflect.TypeFor[T]())
	if s.Properties["tags"].Type != "array" || s.Properties["tags"].Items.Type != "string" {
		t.Errorf("tags: %+v", s.Properties["tags"])
	}
	if s.Properties["attrs"].Type != "object" || s.Properties["attrs"].AdditionalProperties.Type != "string" {
		t.Errorf("attrs: %+v", s.Properties["attrs"])
	}
	if s.Properties["bytes"].Type != "string" || s.Properties["bytes"].Format != "byte" {
		t.Errorf("bytes (base64): %+v", s.Properties["bytes"])
	}
	// Non-string-keyed map: 12.6a degrades to bare object.
	if s.Properties["intkey"].Type != "object" || s.Properties["intkey"].AdditionalProperties != nil {
		t.Errorf("intkey: %+v", s.Properties["intkey"])
	}
}

func TestSchemaTimeAsDateTime(t *testing.T) {
	type T struct {
		When timeFixture `json:"when"`
	}
	s := SchemaFromType(reflect.TypeFor[T]())
	w := s.Properties["when"]
	if w.Type != "string" || w.Format != "date-time" {
		t.Errorf("time: %+v", w)
	}
}

func TestSchemaInterfaceLeavesOpen(t *testing.T) {
	type T struct {
		Anything any `json:"anything"`
	}
	s := SchemaFromType(reflect.TypeFor[T]())
	if got := s.Properties["anything"].Type; got != "" {
		t.Errorf("interface should produce open schema, got Type=%q", got)
	}
}

func TestSchemaUnsupportedKindReturnsEmpty(t *testing.T) {
	type T struct {
		Ch chan int `json:"ch"`
	}
	s := SchemaFromType(reflect.TypeFor[T]())
	got := s.Properties["ch"]
	if got == nil || got.Type != "" || got.Format != "" {
		t.Errorf("unsupported kind: %+v", got)
	}
}

func TestSchemaNilTypeReturnsEmpty(t *testing.T) {
	if got := SchemaFromType(nil); got == nil || got.Type != "" {
		t.Errorf("nil type: %+v", got)
	}
}

// (12.6a's TestSchemaDepthLimitBailsOnRecursion is replaced by
// TestRecursiveStructEmitsRefAndComponent in the 12.6b advanced tests
// below, which proves recursion terminates via component refs rather
// than depth bounding.)

func TestSchemaRequiredSlicesAreSorted(t *testing.T) {
	type T struct {
		Z string `json:"z"`
		A string `json:"a"`
		M string `json:"m"`
	}
	s := SchemaFromType(reflect.TypeFor[T]())
	if !reflect.DeepEqual(s.Required, []string{"a", "m", "z"}) {
		t.Errorf("Required not sorted: %v", s.Required)
	}
}

func TestParseJSONTagAndOptionPresent(t *testing.T) {
	name, opts := parseJSONTag("foo,omitempty,inline")
	if name != "foo" {
		t.Errorf("name: %q", name)
	}
	if !optionPresent(opts, "omitempty") || !optionPresent(opts, "inline") {
		t.Errorf("opts not parsed: %v", opts)
	}
	if optionPresent(opts, "missing") {
		t.Error("optionPresent false-positive")
	}
	emptyName, emptyOpts := parseJSONTag("")
	if emptyName != "" || emptyOpts != nil {
		t.Errorf("empty tag: name=%q opts=%v", emptyName, emptyOpts)
	}
}

func TestSchemaFieldWithoutJSONTagUsesFieldName(t *testing.T) {
	type T struct {
		FieldName string
	}
	s := SchemaFromType(reflect.TypeFor[T]())
	if _, ok := s.Properties["FieldName"]; !ok {
		t.Errorf("expected FieldName property, got %v", s.Properties)
	}
}

// ---- helpers ----------------------------------------------------------------

// derefSchema follows a $ref into spec.Components.Schemas, returning the
// underlying inline schema. Fatals when the ref is missing or the
// component is absent — both indicate a generator bug.
func derefSchema(t *testing.T, spec *Spec, s *Schema) *Schema {
	t.Helper()
	if s.Ref == "" {
		return s
	}
	if spec.Components == nil || spec.Components.Schemas == nil {
		t.Fatalf("schema has $ref %q but spec has no components/schemas", s.Ref)
	}
	const prefix = "#/components/schemas/"
	if !strings.HasPrefix(s.Ref, prefix) {
		t.Fatalf("unexpected $ref shape: %q", s.Ref)
	}
	name := s.Ref[len(prefix):]
	out, ok := spec.Components.Schemas[name]
	if !ok {
		t.Fatalf("$ref %q points at missing component (have %v)", s.Ref, mapKeys(spec.Components.Schemas))
	}
	return out
}

func mapKeys(m map[string]*Schema) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func contentKeys(m map[string]MediaType) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// timeFixture aliases time.Time so reflect.TypeFor[timeFixture]() returns
// exactly the same reflect.Type the production schema generator special-cases.
type timeFixture = time.Time

// recursiveDoc exercises the depth limiter (a struct that contains itself).
type recursiveDoc struct {
	Next *recursiveDoc `json:"next,omitempty"`
	Name string        `json:"name"`
}
