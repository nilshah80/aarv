package aarv

import (
	"io"
	"net/http"
	"reflect"
	"testing"
)

type schemaReqDoc struct {
	Name string `json:"name"`
}
type schemaResDoc struct {
	ID string `json:"id"`
}

func TestWithSchemaPanicsOnBothNil(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("WithSchema(nil, nil) must panic at construction time")
		}
	}()
	_ = WithSchema(nil, nil)
}

func TestWithSchemaUnwrapsPointers(t *testing.T) {
	cases := []struct {
		name   string
		req    any
		res    any
		wantRq reflect.Type
		wantRs reflect.Type
	}{
		{"value+value", schemaReqDoc{}, schemaResDoc{}, reflect.TypeFor[schemaReqDoc](), reflect.TypeFor[schemaResDoc]()},
		{"ptr+value", &schemaReqDoc{}, schemaResDoc{}, reflect.TypeFor[schemaReqDoc](), reflect.TypeFor[schemaResDoc]()},
		{"value+ptr", schemaReqDoc{}, &schemaResDoc{}, reflect.TypeFor[schemaReqDoc](), reflect.TypeFor[schemaResDoc]()},
		{"req only", schemaReqDoc{}, nil, reflect.TypeFor[schemaReqDoc](), nil},
		{"res only", nil, schemaResDoc{}, nil, reflect.TypeFor[schemaResDoc]()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rc := &routeConfig{}
			WithSchema(tc.req, tc.res)(rc)
			if rc.schemaReq != tc.wantRq {
				t.Errorf("schemaReq: got %v want %v", rc.schemaReq, tc.wantRq)
			}
			if rc.schemaRes != tc.wantRs {
				t.Errorf("schemaRes: got %v want %v", rc.schemaRes, tc.wantRs)
			}
		})
	}
}

func TestWithSchemaTypesNilOnSide(t *testing.T) {
	rc := &routeConfig{}
	WithSchemaTypes(reflect.TypeFor[schemaReqDoc](), nil)(rc)
	if rc.schemaReq != reflect.TypeFor[schemaReqDoc]() {
		t.Fatalf("schemaReq: got %v", rc.schemaReq)
	}
	if rc.schemaRes != nil {
		t.Fatalf("schemaRes should be nil, got %v", rc.schemaRes)
	}
}

func TestWithResponsePanicsOnInvalidStatus(t *testing.T) {
	for _, status := range []int{0, 99, 600, -1, 999} {
		func() {
			defer func() {
				if r := recover(); r == nil {
					t.Errorf("WithResponse(%d) must panic", status)
				}
			}()
			_ = WithResponse(status, "x")
		}()
	}
}

func TestWithResponseAccumulatesAndOverrides(t *testing.T) {
	rc := &routeConfig{}
	WithResponse(200, "ok")(rc)
	WithResponse(404, "missing")(rc)
	WithResponse(200, "all good")(rc) // overrides prior 200
	if got := rc.responses[200]; got != "all good" {
		t.Errorf("200 description: got %q want %q", got, "all good")
	}
	if got := rc.responses[404]; got != "missing" {
		t.Errorf("404 description: got %q", got)
	}
	if len(rc.responses) != 2 {
		t.Errorf("expected exactly 2 entries, got %d", len(rc.responses))
	}
}

func TestWithRequestContentType(t *testing.T) {
	rc := &routeConfig{}
	WithRequestContentType("application/x-www-form-urlencoded")(rc)
	if rc.requestContentType != "application/x-www-form-urlencoded" {
		t.Errorf("requestContentType: got %q", rc.requestContentType)
	}
}

func TestRouteInfoSurfacesMetadata(t *testing.T) {
	app := New(WithBanner(false))
	app.Get("/users/{id}",
		func(c *Context) error { return c.JSON(http.StatusOK, nil) },
		WithName("getUser"),
		WithSummary("Fetch user"),
		WithDescription("Returns one user."),
		WithOperationID("getUserById"),
		WithTags("users", "v1"),
		WithDeprecated(),
		WithSchema(schemaReqDoc{}, schemaResDoc{}),
		WithResponse(200, "OK"),
		WithResponse(404, "Not found"),
		WithRequestContentType("application/json"),
	)

	routes := app.Routes()
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}
	r := routes[0]
	if r.Name != "getUser" || r.Summary != "Fetch user" || r.OperationID != "getUserById" || r.Description != "Returns one user." {
		t.Errorf("scalar metadata not surfaced: %#v", r)
	}
	if !r.Deprecated {
		t.Error("Deprecated flag not surfaced")
	}
	if !reflect.DeepEqual(r.Tags, []string{"users", "v1"}) {
		t.Errorf("Tags: %v", r.Tags)
	}
	if r.RequestType != reflect.TypeFor[schemaReqDoc]() || r.ResponseType != reflect.TypeFor[schemaResDoc]() {
		t.Errorf("schema types not surfaced: req=%v res=%v", r.RequestType, r.ResponseType)
	}
	if r.Responses[200] != "OK" || r.Responses[404] != "Not found" {
		t.Errorf("Responses map: %v", r.Responses)
	}
	if r.RequestContentType != "application/json" {
		t.Errorf("RequestContentType: %q", r.RequestContentType)
	}
}

func TestRouteInfoDefaultRequestContentType(t *testing.T) {
	app := New(WithBanner(false))
	app.Post("/foo",
		func(c *Context) error { return nil },
		WithSchema(schemaReqDoc{}, nil),
	)
	r := app.Routes()[0]
	if r.RequestContentType != "application/json" {
		t.Fatalf("expected default application/json when schema is set, got %q", r.RequestContentType)
	}
}

// codecYAMLStub fakes a non-JSON codec by reporting a custom ContentType.
// We don't need its encode/decode methods to do anything sensible — only
// ContentType is consulted by the route metadata defaulting path.
type codecYAMLStub struct{}

func (codecYAMLStub) Decode(_ io.Reader, _ any) error    { return nil }
func (codecYAMLStub) Encode(_ io.Writer, _ any) error    { return nil }
func (codecYAMLStub) MarshalBytes(_ any) ([]byte, error) { return nil, nil }
func (codecYAMLStub) UnmarshalBytes(_ []byte, _ any) error {
	return nil
}
func (codecYAMLStub) ContentType() string { return "application/yaml" }

func TestRouteInfoRequestContentTypeFollowsCodec(t *testing.T) {
	app := New(WithBanner(false), WithCodec(codecYAMLStub{}))
	app.Post("/yaml",
		func(c *Context) error { return nil },
		WithSchema(schemaReqDoc{}, nil),
	)
	r := app.Routes()[0]
	if r.RequestContentType != "application/yaml" {
		t.Fatalf("expected codec content type to flow into RouteInfo, got %q", r.RequestContentType)
	}
}

func TestCodecContentTypeAccessor(t *testing.T) {
	def := New(WithBanner(false))
	if got := def.CodecContentType(); got != "application/json" {
		t.Errorf("default codec content type: %q", got)
	}
	custom := New(WithBanner(false), WithCodec(codecYAMLStub{}))
	if got := custom.CodecContentType(); got != "application/yaml" {
		t.Errorf("custom codec content type: %q", got)
	}
}

func TestRouteInfoNoContentTypeWithoutSchema(t *testing.T) {
	app := New(WithBanner(false))
	app.Get("/ping", func(c *Context) error { return nil })
	r := app.Routes()[0]
	if r.RequestContentType != "" {
		t.Fatalf("expected empty RequestContentType when no schema, got %q", r.RequestContentType)
	}
}

func TestRoutesReturnsDeepCopy(t *testing.T) {
	app := New(WithBanner(false))
	app.Get("/items",
		func(c *Context) error { return nil },
		WithTags("a", "b"),
		WithResponse(200, "ok"),
		WithResponse(500, "boom"),
	)

	first := app.Routes()
	if len(first) != 1 {
		t.Fatalf("expected 1 route, got %d", len(first))
	}

	// Mutate the returned slice and per-element collections.
	first[0].Tags[0] = "MUTATED"
	first[0].Tags = append(first[0].Tags, "extra")
	first[0].Responses[200] = "MUTATED"
	first[0].Responses[201] = "added"

	second := app.Routes()
	if !reflect.DeepEqual(second[0].Tags, []string{"a", "b"}) {
		t.Errorf("internal Tags corrupted by caller mutation: %v", second[0].Tags)
	}
	if second[0].Responses[200] != "ok" {
		t.Errorf("internal Responses[200] corrupted: %q", second[0].Responses[200])
	}
	if _, exists := second[0].Responses[201]; exists {
		t.Error("internal Responses gained a key from caller mutation")
	}
}

type bindRouteReq struct {
	Name string `json:"name"`
}
type bindRouteRes struct {
	Echo string `json:"echo"`
}

func TestBindRouteAttachesSchemaAutomatically(t *testing.T) {
	app := New(WithBanner(false))
	BindRoute(app, "POST", "/echo",
		func(c *Context, req bindRouteReq) (bindRouteRes, error) {
			return bindRouteRes{Echo: req.Name}, nil
		},
		WithSummary("Echo"),
	)

	routes := app.Routes()
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}
	r := routes[0]
	if r.RequestType != reflect.TypeFor[bindRouteReq]() {
		t.Errorf("RequestType: got %v want %v", r.RequestType, reflect.TypeFor[bindRouteReq]())
	}
	if r.ResponseType != reflect.TypeFor[bindRouteRes]() {
		t.Errorf("ResponseType: got %v want %v", r.ResponseType, reflect.TypeFor[bindRouteRes]())
	}
	if r.Summary != "Echo" {
		t.Errorf("user-supplied Summary not applied: %q", r.Summary)
	}
	if r.RequestContentType != "application/json" {
		t.Errorf("RequestContentType default: %q", r.RequestContentType)
	}
}

func TestBindRouteRespectsCallerSchemaOverride(t *testing.T) {
	type explicitReq struct {
		Custom string `json:"custom"`
	}
	app := New(WithBanner(false))
	BindRoute(app, "POST", "/override",
		func(c *Context, req bindRouteReq) (bindRouteRes, error) {
			return bindRouteRes{}, nil
		},
		WithSchema(explicitReq{}, nil),
	)
	r := app.Routes()[0]
	if r.RequestType != reflect.TypeFor[explicitReq]() {
		t.Fatalf("caller WithSchema must override auto-schema; got %v", r.RequestType)
	}
}

func TestBindGroupRouteAttachesSchema(t *testing.T) {
	app := New(WithBanner(false))
	app.Group("/v1", func(g *RouteGroup) {
		BindGroupRoute(g, "POST", "/echo",
			func(c *Context, req bindRouteReq) (bindRouteRes, error) {
				return bindRouteRes{}, nil
			},
		)
	})

	routes := app.Routes()
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}
	r := routes[0]
	if r.Pattern != "/v1/echo" {
		t.Errorf("Pattern: %q", r.Pattern)
	}
	if r.RequestType != reflect.TypeFor[bindRouteReq]() {
		t.Errorf("RequestType: got %v", r.RequestType)
	}
}

type ptrReq struct{ X int }
type ptrRes struct{ Y int }

func TestBindRouteUnwrapsPointerTypeParams(t *testing.T) {
	app := New(WithBanner(false))
	BindRoute(app, "POST", "/ptr",
		func(c *Context, req *ptrReq) (*ptrRes, error) {
			return &ptrRes{Y: 1}, nil
		},
	)
	r := app.Routes()[0]
	if r.RequestType != reflect.TypeFor[ptrReq]() {
		t.Errorf("RequestType (pointer Req): got %v want %v", r.RequestType, reflect.TypeFor[ptrReq]())
	}
	if r.ResponseType != reflect.TypeFor[ptrRes]() {
		t.Errorf("ResponseType (pointer Res): got %v want %v", r.ResponseType, reflect.TypeFor[ptrRes]())
	}
}
