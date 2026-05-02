package aarv

import "reflect"

// BindRoute is a typed convenience wrapper that registers method+pattern with
// Bind(fn) and an auto-applied schema option, so the OpenAPI plugin (and any
// other RouteInfo consumer) sees the request and response types without the
// caller having to repeat them via WithSchema.
//
// The auto-applied schema unwraps pointer types to their value types, matching
// WithSchema's behavior. Additional opts are applied after the auto-schema, so
// a caller-provided WithSchema, WithSchemaTypes, or WithRequestContentType
// will override the defaults.
//
// Example:
//
//	type CreateUserReq struct{ Name string `json:"name"` }
//	type CreateUserRes struct{ ID string `json:"id"` }
//
//	aarv.BindRoute(app, "POST", "/users",
//	    func(c *aarv.Context, req CreateUserReq) (CreateUserRes, error) {
//	        return CreateUserRes{ID: "u_1"}, nil
//	    },
//	    aarv.WithSummary("Create user"),
//	    aarv.WithResponse(201, "Created"),
//	)
//
// For a handler that already has its body and validator built (i.e. you are
// using Bind directly), prefer the explicit App.Get/Post/... + WithSchema
// pair so the schema source is unambiguous.
func BindRoute[Req, Res any](
	app *App,
	method, pattern string,
	fn func(*Context, Req) (Res, error),
	opts ...RouteOption,
) *App {
	prepended := append(autoSchemaOpts[Req, Res](), opts...)
	return app.addRoute(method, pattern, Bind(fn), prepended...)
}

// BindGroupRoute is the RouteGroup analogue of BindRoute. It exists as a free
// function because Go does not support generic methods on a non-generic
// receiver.
func BindGroupRoute[Req, Res any](
	g *RouteGroup,
	method, pattern string,
	fn func(*Context, Req) (Res, error),
	opts ...RouteOption,
) *RouteGroup {
	prepended := append(autoSchemaOpts[Req, Res](), opts...)
	g.addRoute(method, pattern, Bind(fn), prepended...)
	return g
}

// autoSchemaOpts builds the implicit RouteOption applied by BindRoute /
// BindGroupRoute. It does NOT call WithSchema (which panics on nil/nil)
// because the generic type parameters give us the types directly via
// reflect.TypeFor — no untyped-nil ambiguity even when Req/Res are
// pointer or interface types.
func autoSchemaOpts[Req, Res any]() []RouteOption {
	return []RouteOption{WithSchemaTypes(unwrapPtr(reflect.TypeFor[Req]()), unwrapPtr(reflect.TypeFor[Res]()))}
}
