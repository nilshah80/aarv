# Migrating to aarv v0.9.0

v0.9.0 removes the global `nativeMiddlewareRegistry` and introduces
`aarv.NativeMiddleware` as a value type carrying both the stdlib path
and the framework-native counterpart. This fixes a long-standing
collision bug where multiple instances of the same wrapper-style
middleware silently overwrote each other's native pair.

The change is **mostly source-compatible**: typical call sites like
`app.Use(plugin.New())` continue to compile because `App.Use` widens
its variadic to `...any`. A handful of patterns do break — this guide
walks through each one with before/after code.

## Quick decision tree

- I just use `app.Use(plugin.New())` — **nothing to change**.
- I assign the plugin's return value to a typed local — see §1.
- I have a helper function returning `aarv.Middleware` that wraps
  `aarv.WrapMiddleware` — see §2.
- I do `app.Use(mws...)` where `mws` is `[]aarv.Middleware` — see §3.
- I use `pprof.Config.AuthMiddleware` — see §4.
- I'm a plugin author with a `New(...) aarv.Middleware` constructor
  that calls `RegisterNativeMiddleware` — see §5.

## §1. Typed variable assignment

**Before:**

```go
var mw aarv.Middleware = compress.New()
app.Use(mw)
```

**After (pick one):**

```go
// Option A — change the type word
var mw aarv.NativeMiddleware = compress.New()
app.Use(mw)

// Option B — type inference (recommended)
mw := compress.New()
app.Use(mw)

// Option C — keep aarv.Middleware by extracting .Stdlib
var mw aarv.Middleware = compress.New().Stdlib
app.Use(mw)  // works but discards the native fast path
```

## §2. Helper functions returning `aarv.Middleware`

If your helper wraps `aarv.WrapMiddleware` or `aarv.RegisterNativeMiddleware`,
its return type must change:

**Before:**

```go
func traceAuth() aarv.Middleware {
    return aarv.WrapMiddleware(func(next aarv.HandlerFunc) aarv.HandlerFunc {
        return func(c *aarv.Context) error {
            // ... emit span ...
            return next(c)
        }
    })
}
```

**After:**

```go
func traceAuth() aarv.NativeMiddleware {
    return aarv.WrapMiddleware(func(next aarv.HandlerFunc) aarv.HandlerFunc {
        return func(c *aarv.Context) error {
            // ... emit span ...
            return next(c)
        }
    })
}
```

Only the return type changes. Callers of `traceAuth()` keep working
because `App.Use(...any)` accepts both `Middleware` and `NativeMiddleware`.

## §3. Spread pattern `app.Use(mws...)` with `[]aarv.Middleware`

`App.Use`'s variadic widened from `...Middleware` to `...any`, so a
typed-slice spread no longer satisfies the call. Two equivalent rewrites:

**Before:**

```go
mws := []aarv.Middleware{aarv.Recovery(), aarv.Logger()}
app.Use(mws...)
```

**After (option A — use `[]any`):**

```go
mws := []any{aarv.Recovery(), aarv.Logger()}
app.Use(mws...)
```

**After (option B — explicit loop):**

```go
mws := []aarv.Middleware{aarv.Recovery(), aarv.Logger()}
for _, mw := range mws {
    app.Use(mw)
}
```

Same applies to `RouteGroup.Use`, `PluginContext.Use`, and
`WithRouteMiddleware`.

## §4. `pprof.Config.AuthMiddleware`

This field is intentionally **not widened** — it's invoked only on the
stdlib path inside the pprof handler chain, so widening it to `any`
would silently discard a `NativeMiddleware.Native` set by the
consumer. The field stays typed `aarv.Middleware`.

**Before:**

```go
jwtMW := jwt.New(cfg) // returned aarv.Middleware in v0.8.x
pprof.New(pprof.Config{AuthMiddleware: jwtMW})
```

**After:**

```go
jwtMW := jwt.New(cfg) // now returns aarv.NativeMiddleware
pprof.New(pprof.Config{AuthMiddleware: jwtMW.Stdlib})
```

You lose the native fast path for the pprof routes specifically, which
is intentional: pprof handlers are not hot-path code, and gating them
behind a native-aware auth middleware would change the semantics of
where authentication runs.

## §5. Plugin authors — migrating `New(...)`

If your plugin's constructor calls `RegisterNativeMiddleware` (or
`WrapMiddleware`), change its return type:

**Before:**

```go
func New(cfg Config) aarv.Middleware {
    // ... build stdlib + native variants ...
    return aarv.RegisterNativeMiddleware(stdlib, native)
}
```

**After:**

```go
func New(cfg Config) aarv.NativeMiddleware {
    // ... build stdlib + native variants ...
    return aarv.RegisterNativeMiddleware(stdlib, native)
}
```

Body unchanged; only the return type. Callers' `app.Use(yourplugin.New())`
keeps working because `App.Use(...any)` accepts the new type.

### Tuple constructors

If your constructor returns a tuple like `(aarv.Middleware, error)`:

**Before:**

```go
func New(key []byte) (aarv.Middleware, error) {
    if !validKey(key) {
        return nil, ErrInvalidKey
    }
    return aarv.RegisterNativeMiddleware(stdlib, native), nil
}
```

**After:**

```go
func New(key []byte) (aarv.NativeMiddleware, error) {
    if !validKey(key) {
        return aarv.NativeMiddleware{}, ErrInvalidKey  // not `nil`
    }
    return aarv.RegisterNativeMiddleware(stdlib, native), nil
}
```

The error-path return value is `aarv.NativeMiddleware{}` (zero value),
not `nil` — `NativeMiddleware` is a struct, not a pointer.

### Stdlib-only middleware (no native variant)

If your plugin only has a stdlib middleware (no `RegisterNativeMiddleware`
or `WrapMiddleware` involvement), **keep returning `aarv.Middleware`**.
The only in-tree example is `plugins/timeout.New(d)` — its
implementation uses a per-request goroutine that has no native-path
analog. Its sibling `timeout.Context(d)` does have a native pair and
returns `NativeMiddleware`.

## Why this change

aarv's pre-v0.9.0 `nativeMiddlewareRegistry` keyed native-pair
registrations on `reflect.ValueOf(m).Pointer()`. For `func` values
this returns the underlying *code* pointer, which is shared across
closures of the same source function literal. Two distinct instances
of `aarv.SkipPaths` (or `aarv.WrapMiddleware`, or any other wrapper
helper) registered under the same key — the second silently
overwrote the first's native fn.

In practice this didn't bite single-plugin usage because each plugin's
`New(...)` returned a closure with a unique source-level inner
function. But the moment a wrapper helper produced two
behaviorally-distinct middlewares from the same code path (different
skip sets, different inner middleware, etc.), the registry collapsed
them. The native fast path silently ran the wrong logic for the wrong
routes.

v0.9.0 deletes the registry. `NativeMiddleware` carries both paths
inline; the chain builder reads the slot directly. Each value has its
own identity by virtue of being a struct, so two distinct calls
produce two distinct, non-colliding registrations.

## What stays the same

- `aarv.Middleware` and `aarv.MiddlewareFunc` type signatures are
  unchanged.
- `RegisterNativeMiddleware`'s **parameter types** are unchanged
  (`(Middleware, MiddlewareFunc)`); only the return type changed.
- `aarv.Recovery()` and `aarv.Logger()` work as before from the
  caller's perspective; they now return `NativeMiddleware` but
  `app.Use(aarv.Recovery())` still compiles.
- All public type signatures inside the `Context`, `RouteGroup`
  routing API, hooks, lifecycle, codec, and OpenAPI surface are
  unchanged.
- Per-plugin `SkipPaths` config fields (`prometheus.Config.SkipPaths`,
  `compress.Config.SkipPaths`, etc.) work as before — the registry
  redesign is orthogonal to these per-plugin skip lists.

## How to verify your code

```bash
# In your project root, after bumping the aarv pin:
go build ./...   # surfaces every type-mismatch site
go vet ./...
go test ./...
```

If you hit a build error not covered above, search the [aarv CHANGELOG
v0.9.0 entry](../CHANGELOG.md) — the per-plugin migration table lists
every constructor whose return type changed.
