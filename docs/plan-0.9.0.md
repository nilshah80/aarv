# aarv v0.9.0 release plan

> **Status:** Phases 0–8 complete; awaiting **Checkpoint B** approval before release ceremony (Phases 9–14).
> **Target tag:** `v0.9.0` (minor).
> **Owner:** maintainer to approve at each checkpoint; release work executed end-to-end after Checkpoint B.
>
> **Implementation summary (as of 2026-06-05):**
> - 22 root composition sites migrated to `[]middlewareSlot`
> - `nativeMiddlewareRegistry` deleted entirely
> - 35 plugin constructors migrated (in-root + submodule)
> - 2 breaking examples fixed
> - `plugins/hmacauth-otel/` companion module created (10 tests pass)
> - `nativemiddleware_test.go` regression suite (13 tests)
> - `CHANGELOG.md [0.9.0]`, `docs/MIGRATION_v0.9.md`, `skippaths.go`, `docs/middleware.md`, `docs/tasks-md.md §12.6.10` all updated
> - All 31 root packages + 11 submodules + 30 examples build/test clean with `-race`; `golangci-lint run` reports 0 issues
> - `go.work` and `go.work.sum` created for cross-module dev; both gitignored, removed before tagging

---

## Table of contents

1. [Decision summary](#decision-summary)
2. [Revision history](#revision-history)
3. [Audit-derived inventory](#audit-derived-inventory)
4. [Phase 0 — Pre-flight verification](#phase-0--pre-flight-verification)
5. [Phase 1 — Design lock-in](#phase-1--design-lock-in)
6. [Checkpoint A](#-checkpoint-a)
7. [Phase 2 — Root implementation](#phase-2--root-implementation)
8. [Phase 3 — In-root plugin migration](#phase-3--in-root-plugin-migration)
9. [Phase 4 — Create and implement plugins/hmacauth-otel](#phase-4--create-and-implement-pluginshmacauth-otel)
10. [Phase 5 — Submodule migration via go.work](#phase-5--submodule-migration-via-gowork)
11. [Phase 6 — SkipPaths lift](#phase-6--skippaths-lift)
12. [Phase 7 — Documentation](#phase-7--documentation)
13. [Phase 8 — Quality gates](#phase-8--quality-gates)
14. [Checkpoint B](#-checkpoint-b)
15. [Phase 9 — Release-prep commit + push](#phase-9--release-prep-commit--push)
16. [Phase 10 — Root tag v0.9.0](#phase-10--root-tag-v090)
17. [Phase 11 — Submodule pin bumps](#phase-11--submodule-pin-bumps)
18. [Phase 12 — Submodule tags](#phase-12--submodule-tags)
19. [Phase 13 — Go module verification](#phase-13--go-module-verification)
20. [Phase 14 — GitHub releases](#phase-14--github-releases)
21. [Phase 15 — alp follow-up](#phase-15--alp-follow-up)
22. [Phase 16 — Rollback](#phase-16--rollback)
23. [Complete file inventory](#complete-file-inventory)
24. [Approvals needed](#approvals-needed)
25. [Timeline](#timeline)

---

## Decision summary

- **Tag:** `v0.9.0` (minor bump; deliberate breaking changes accepted)
- **Scope:** full registry removal + every plugin migrated per audit + `plugins/hmacauth-otel` companion + Prometheus/OTel recorder migration to `StatusRecorder` + all additive items previously planned for v0.8.1
- **No interim v0.8.1.** ALP and any other consumer adopts v0.9.0 directly.
- **No `Co-Authored-By` lines** in commits (saved memory rule).
- **No tag push before Checkpoint B explicit approval.**
- **CI wait points** after every commit push, before any tag push.
- **Go module verification before GitHub release creation** so broken tags are diagnosed quietly.

---

## Revision history

| Rev | Resolved |
|---|---|
| Rev 1 → 2 | Scope honesty: was v0.8.1 with stdlib-only SkipPaths caveat. Moved to v0.9.0 minor with breaking changes acknowledged; CHANGELOG `### Breaking Changes` + migration guide; full plugin coverage. |
| Rev 2 → 3 | `nativeMiddlewareRegistry` **deleted entirely** (no legacy fallback). `go.work` replaces `replace` directives. Examples sweep added. 22 composition sites enumerated. Robust CI wait loop. Audit-driven scope (35 items). Tuple constructors named explicitly. Nil-validation panics at `Use`-time. `WrapMiddleware` migration in guide. Legacy OTel attrs removed in **v0.10.0**. Tool checks. `hmacauth-otel/go.mod` corrected. `GOPROXY=direct` + retry. |
| Rev 3 final | Phase ordering: hmacauth-otel created **before** `go.work`. `tracetest` imported, not required as module. `SkipPaths(paths []string, mw any)` accepts any type. Phase 13 smoke avoids `SkipPaths(nil, nil)`. **`jq`** + **Go 1.25 version gate** in Phase 0. Examples sweep uses **`GOWORK=off`**. `pprof.Config.AuthMiddleware` explicit decision (keep as `Middleware`; document `.Stdlib` extraction). Migration counts canonical from audit. |
| Rev 3 final fixes | `SkipPaths(nil)` now panics directly. `docs/tasks-md.md` update scope narrowed to only actually completed items. `plugins/prometheus` and `plugins/otel` internal `recordingWriter` migrations added to v0.9.0 scope. Phase 13 smoke uses native-capable inners. Memory cleanup marked local-only and excluded from the release commit. |

---

## Audit-derived inventory

All counts come from the workflow audit (`v0.9.0-revision-audits`). The audit table is the canonical scope; hand counts are avoided.

| Surface | Source | Count | Notes |
|---|---|---|---|
| Tag scheme | `git tag --sort=v:refname` | 1 root + 10 existing submodules + 1 new = **12** | All existing submodules have v0.8.0; bump in lockstep |
| Constructor migrations | `needsMigration` list | **35 items** | 33 free constructors + 2 `*Authorizer` methods (rbac) |
| Tuple constructors | `returnsTuple == true` | **2** | `ratelimit.NewWithCleanup`, `encrypt.New` |
| Stdlib-only outlier | `stdlibOnly` | **1** | `plugins/timeout.New(d)` — its sibling `Context(d)` migrates |
| Public Middleware-typed config fields | grep audit | **1** | `pprof.Config.AuthMiddleware` only |
| Examples breakage | `breakageRisk == "breaks"` | **2 files** | `examples/route-groups/main.go:10`, `examples/auth/main.go:202` |
| Composition sites in root | `compositionSites` | **22 sites** | Across 6 files |
| Plugins audited | `pluginCoverage` | **37** | 27 in-root + 10 submodule |

---

## Phase 0 — Pre-flight verification

**Duration:** ~30 min. **Output:** report at end.

```bash
cd /Users/nilayshah/Documents/PoC/aarv

# Tool availability — Phase 4/8 (govulncheck), Phase 8 (lint), Phase 14 (gh)
echo "=== Tool availability ==="
command -v go             >/dev/null || { echo "FATAL: go missing"; exit 1; }
command -v gh             >/dev/null || { echo "FATAL: gh missing"; exit 1; }
command -v govulncheck    >/dev/null || { echo "FATAL: govulncheck missing"; exit 1; }
command -v golangci-lint  >/dev/null || { echo "FATAL: golangci-lint missing"; exit 1; }
command -v jq             >/dev/null || { echo "FATAL: jq missing (CI wait loop depends on it)"; exit 1; }

echo "=== Tool versions ==="
go version
gh --version | head -1
govulncheck -version 2>&1 | head -1
golangci-lint --version | head -1
jq --version

# Go version gate — go.work uses 'go 1.25'
go_v=$(go env GOVERSION | sed 's/^go//')
maj=$(echo $go_v | cut -d. -f1)
min=$(echo $go_v | cut -d. -f2)
if [ "$maj" -lt 1 ] || ([ "$maj" -eq 1 ] && [ "$min" -lt 25 ]); then
  echo "FATAL: Go $go_v < 1.25; v0.9.0 dev flow uses go.work (go 1.25)"
  exit 1
fi
echo "Go $go_v >= 1.25 ✓"

# gh auth — gates Phase 14
gh auth status

echo "=== Tree state ==="
git status --short
git diff --stat HEAD

echo "=== Tag state (portable; no sort -V) ==="
git tag --sort=v:refname --list 'v*' 'plugins/*'

echo "=== Remote state ==="
git fetch origin
[ "$(git rev-parse HEAD)" = "$(git rev-parse origin/main)" ] \
  && echo "HEAD == origin/main" \
  || echo "HEAD differs — commit pending"

echo "=== Discovered plugin inventory ==="
echo "-- Submodules:"
find plugins -mindepth 2 -maxdepth 2 -name go.mod -not -path '*/.*' -exec dirname {} \;
echo "-- In-root:"
find plugins -mindepth 1 -maxdepth 1 -type d -not -name '.*' | \
  while read d; do [ -f "$d/go.mod" ] || echo "$d"; done

echo "=== Submodule pins ==="
for sub in $(find plugins -mindepth 2 -maxdepth 2 -name go.mod -not -path '*/.*' -exec dirname {} \;); do
  echo "-- $sub"
  grep "github.com/nilshah80/aarv " $sub/go.mod 2>/dev/null || echo "  (no aarv dep)"
done
```

---

## Phase 1 — Design lock-in

**Duration:** ~½ day. **Output:** finalized design (presented at Checkpoint A).

### Final `middleware.go` shape (no registry remains)

```go
type Middleware     func(http.Handler) http.Handler
type MiddlewareFunc func(next HandlerFunc) HandlerFunc

// NativeMiddleware bundles a stdlib path with its native counterpart.
// Each value carries its own identity — multiple instances of the same
// wrapper helper do not interfere.
type NativeMiddleware struct {
    Stdlib Middleware     // required; nil panics at Use-time
    Native MiddlewareFunc // optional; nil downgrades that slot to stdlib
}

// RegisterNativeMiddleware bundles m + fn into a NativeMiddleware value.
// No global registry; each call returns an independent value.
func RegisterNativeMiddleware(m Middleware, fn MiddlewareFunc) NativeMiddleware {
    return NativeMiddleware{Stdlib: m, Native: fn}
}

func WrapMiddleware(fn MiddlewareFunc) NativeMiddleware { /* ... */ }

// middlewareSlot is the internal storage shape. Private.
type middlewareSlot struct {
    stdlib Middleware
    native MiddlewareFunc // nil → no native variant for this slot
}

// coerceSlot is the type-switch helper shared by App.Use, RouteGroup.Use,
// PluginContext.Use, WithRouteMiddleware, and SkipPaths.
func coerceSlot(arg any, call string, index int) middlewareSlot {
    switch v := arg.(type) {
    case nil:
        panic(fmt.Sprintf("aarv: %s: argument %d is nil", call, index))
    case Middleware:
        if v == nil {
            panic(fmt.Sprintf("aarv: %s: argument %d is a nil Middleware", call, index))
        }
        return middlewareSlot{stdlib: v}
    case func(http.Handler) http.Handler:
        if v == nil {
            panic(fmt.Sprintf("aarv: %s: argument %d is a nil middleware function", call, index))
        }
        return middlewareSlot{stdlib: Middleware(v)}
    case NativeMiddleware:
        if v.Stdlib == nil {
            panic(fmt.Sprintf("aarv: %s: argument %d is NativeMiddleware with nil Stdlib", call, index))
        }
        return middlewareSlot{stdlib: v.Stdlib, native: v.Native}
    default:
        panic(fmt.Sprintf("aarv: %s: argument %d has unsupported type %T", call, index, arg))
    }
}

// buildChain / buildNativeChain take []middlewareSlot.
// buildNativeChain bails on the first slot with nil native.
func buildChain(handler http.Handler, slots []middlewareSlot) http.Handler { /* ... */ }
func buildNativeChain(handler HandlerFunc, slots []middlewareSlot) (HandlerFunc, bool) { /* ... */ }
```

### `SkipPaths` final signature

```go
// SkipPaths returns mw wrapped so requests matching paths bypass it.
// mw accepts aarv.Middleware, aarv.NativeMiddleware, or
// func(http.Handler) http.Handler. Other types panic via coerceSlot.
func SkipPaths(paths []string, mw any) NativeMiddleware {
    if mw == nil {
        panic("aarv.SkipPaths: mw must not be nil")
    }
    slot := coerceSlot(mw, "SkipPaths", 0)
    if len(paths) == 0 {
        return NativeMiddleware{Stdlib: slot.stdlib, Native: slot.native}
    }
    // Build skip-aware stdlib + native; native present only when slot.native != nil
}
```

### `pprof.Config.AuthMiddleware` decision

**Keep as `aarv.Middleware`.** Reasoning:
- Invoked only at `plugins/pprof/pprof.go:98` as `h = cfg.AuthMiddleware(h)` on an `http.Handler` — pure stdlib path.
- Widening to `any` would silently discard `NativeMiddleware.Native` and add runtime panic risk.
- Consumers with a `NativeMiddleware` extract `.Stdlib`:
  ```go
  cfg.AuthMiddleware = jwtMW.Stdlib
  ```
- Document the extraction pattern in `docs/MIGRATION_v0.9.md`.

---

## ✋ Checkpoint A

I present the design above. You approve, redirect, or scope-cut.
**No production code is written before this checkpoint.**

---

## Phase 2 — Root implementation

**Duration:** ~1.5 days. **Output:** root tests pass with `-race`; new regression test passes.

### Composition site checklist (22 sites)

| # | File:line | Site | Action |
|---|---|---|---|
| 1 | `aarv.go:37` | `App.globalMiddleware` field | type → `[]middlewareSlot` |
| 2 | `router.go:69` | `routeConfig.middleware` field | type → `[]middlewareSlot` |
| 3 | `router.go:248` | `RouteGroup.middleware` field | type → `[]middlewareSlot` |
| 4 | `router.go:100-102` | `WithRouteMiddleware(...any)` | type-switch via `coerceSlot`; defensive clone |
| 5 | `router.go:265-271` | `RouteGroup.addRoute` combined slice | allocate `[]middlewareSlot`; append `g.middleware...` then `rc.middleware...` |
| 6 | `router.go:315` | RouteGroup native build | pass slots to `buildNativeChain` |
| 7 | `router.go:324` | RouteGroup stdlib build | pass slots to `buildChain` |
| 8 | `router.go:418-421` | `RouteGroup.Use(...any)` | `coerceSlot` |
| 9 | `router.go:430` | **Nested group inheritance** | preserve one-shot snapshot; document |
| 10 | `app_routes.go:82` | `addRoute` non-group stdlib | works unchanged (now slots) |
| 11 | `app_routes.go:178-181` | `App.Use(...any)` | `coerceSlot` |
| 12 | `dispatch.go:22` | global stdlib build | pass slots |
| 13 | `dispatch.go:27` | global-native pre-check | `if slot.native == nil { globalNative = false; break }` |
| 14 | `dispatch.go:49` | route fast native build | pass slots |
| 15 | `dispatch.go:67` | route fast stdlib fallback | pass slots |
| 16 | `dispatch.go:83` | group fast stdlib over global | pass slots |
| 17 | `dispatch.go:106` | group fast native over global | pass slots |
| 18 | `dispatch.go:130` | group dynamic over global stdlib | pass slots |
| 19 | `dispatch.go:154` | group dynamic over global native | pass slots |
| 20 | `middleware.go:66` | `buildChain` | takes `[]middlewareSlot` |
| 21 | `middleware.go:73` | `buildNativeChain` | takes `[]middlewareSlot`; bails on nil native |
| 22 | `plugin.go:86-89` | `PluginContext.Use(...any)` | forwards to `pc.group.Use(...)` |

### New test file: `nativemiddleware_test.go`

Covers:
- Distinct registrations don't collide (the regression guard)
- Slot-based chain build correctness
- Nil panics: `app.Use(nil)`, `app.Use(NativeMiddleware{Stdlib: nil})`
- Unsupported-type panic: `app.Use(42)`
- Untyped `func(http.Handler) http.Handler` literal path
- Typed `Middleware` value path
- `NativeMiddleware` value path
- `SkipPaths` multi-instance native preservation
- Same matrix for `RouteGroup.Use`, `PluginContext.Use`, `WithRouteMiddleware`

---

## Phase 3 — In-root plugin migration

**Duration:** ~1.5 days. **Scope:** audit's `needsMigration` list filtered to in-root.

### Migration table (26 in-root plugins, 27 entries)

| Plugin | File:line | Action | Notes |
|---|---|---|---|
| apikey | `apikey.go:75` | `New` → NativeMiddleware | |
| basicauth | `basicauth.go:89` | `New` → NativeMiddleware | |
| bearer | `bearer.go:136` | `New` → NativeMiddleware | |
| bodylimit | `bodylimit.go:34` | `New(maxBytes int64)` → NativeMiddleware | |
| bodylimit | `bodylimit.go:82` | `NewWithResponse` → NativeMiddleware | |
| compress | `compress.go:511` | `New` → NativeMiddleware | |
| cors | `cors.go:59` | `New` → NativeMiddleware | |
| csrf | `csrf.go:124` | `New` → NativeMiddleware | |
| encrypt | `encrypt.go:301` | `New(key, ...cfg) (Middleware, error)` → `(NativeMiddleware, error)` | **TUPLE**; indirect via `MustNew` |
| encrypt | `encrypt.go:476` | `MustNew` → NativeMiddleware | |
| etag | `etag.go:98` | `New` → NativeMiddleware | |
| health | `health.go:66` | `New` → NativeMiddleware | |
| hmacauth | `hmacauth.go:252` | `New` → NativeMiddleware | |
| idempotency | `idempotency.go:204` | `New` → NativeMiddleware | |
| ipfilter | `ipfilter.go:96` | `New` → NativeMiddleware | |
| jwt | `jwt.go:275` | `New` → NativeMiddleware | |
| logger | `logger.go:81` | `New` → NativeMiddleware | |
| pprof | `pprof.go:133` | `New` → NativeMiddleware | `Config.AuthMiddleware` field stays `aarv.Middleware` |
| ratelimit | `ratelimit.go:124` | `New` → NativeMiddleware | indirect via `(*rateLimiter).middleware()` |
| ratelimit | `ratelimit.go:136` | `NewWithCleanup` → `(NativeMiddleware, func() error)` | **TUPLE** |
| rbac | `rbac.go:111` | `(*Authorizer).RequireRoles` → NativeMiddleware | **METHOD**; indirect via `(*Authorizer).middleware` |
| rbac | `rbac.go:130` | `(*Authorizer).RequireAnyRole` → NativeMiddleware | **METHOD** |
| recover | `recover.go:131` | `New` → NativeMiddleware | |
| requestid | `requestid.go:225` | `New` → NativeMiddleware | |
| secure | `secure.go:95` | `New` → NativeMiddleware | |
| session | `middleware.go:149` | `New` → NativeMiddleware | indirect via `buildMiddleware` |
| session | `middleware.go:162` | `NewCookie` → NativeMiddleware | indirect via `buildMiddleware` |
| static | `static.go:103` | `New` → NativeMiddleware | |
| throttle | `throttle.go:70` | `New` → NativeMiddleware | |
| timeout | `timeout.go:199` | `Context(d)` → NativeMiddleware | sibling `New(d)` at `timeout.go:126` **stays `aarv.Middleware`** (stdlib-only outlier) |
| verboselog | `verboselog.go:538` | `New` → NativeMiddleware | |

### Examples fixed alongside

| File:line | Function | Action |
|---|---|---|
| `examples/route-groups/main.go:10` | `apiVersion` | return type → `aarv.NativeMiddleware` |
| `examples/auth/main.go:202` | `SecureSessionMiddleware` | return type → `aarv.NativeMiddleware` |

### Acceptance

Every in-root plugin's `go test -race ./plugins/<name>/...` passes; root sweep passes.

---

## Phase 4 — Create and implement `plugins/hmacauth-otel`

**Duration:** ~½ day. **Important:** runs **before** Phase 5 so `go.work` can reference the directory.

### Files created

| Path | Content |
|---|---|
| `plugins/hmacauth-otel/go.mod` | `module github.com/nilshah80/aarv/plugins/hmacauth-otel`; `go 1.25.0`; requires `github.com/nilshah80/aarv v0.9.0`, `go.opentelemetry.io/otel v1.43.0`, `go.opentelemetry.io/otel/trace v1.43.0`, `go.opentelemetry.io/otel/sdk v1.43.0`. **Does NOT require `tracetest` as a separate module** — that's a sub-package under `go.opentelemetry.io/otel/sdk`, imported in tests as `go.opentelemetry.io/otel/sdk/trace/tracetest`. |
| `plugins/hmacauth-otel/observer.go` | `NewObserver(opts ...Option) hmacauth.Observer` — imports `github.com/nilshah80/aarv/plugins/hmacauth` package |
| `plugins/hmacauth-otel/options.go` | `Option`, `WithTracerProvider`, `WithSpanName` (default `"auth.HMAC.verify"`) |
| `plugins/hmacauth-otel/observer_test.go` | 10 tests (matrix below) |
| `plugins/hmacauth-otel/README.md` | Mirrors `plugins/otel/README.md`'s "Bring your own Provider" pattern |
| `plugins/hmacauth-otel/doc.go` | Package doc |

### Span lifecycle

The Observer fires AFTER verification, so a synchronous `Start/End` would measure 0ms. Backdate the span:

```go
endTime := time.Now()
startTime := endTime.Add(-event.Duration)
ctx, span := tracer.Start(parentCtx, spanName, trace.WithTimestamp(startTime))
defer span.End(trace.WithTimestamp(endTime))
```

Parent context: `c.Context()` when `c != nil`, else `context.Background()`.

### Test matrix (10 tests)

1. `TestObserver_SpanNameDefault` — default name is `"auth.HMAC.verify"`
2. `TestObserver_SpanNameOverride` — `WithSpanName(...)` honored
3. `TestObserver_AttributesAllOutcomes` — each outcome produces correct attribute set
4. `TestObserver_StatusErrorOnNonOK` — `codes.Error` set iff outcome ≠ OK
5. `TestObserver_StatusUnsetOnOK` — OK leaves span status Unset
6. `TestObserver_OmitsZeroStatus` — `auth.response_status` absent when `Event.Status == 0`
7. `TestObserver_OmitsSkewWhenNotClockSkew` — `auth.skew_seconds` absent for non-skew outcomes
8. `TestObserver_DefaultProviderFallback` — nil TracerProvider option → uses `otel.GetTracerProvider()`
9. `TestObserver_NilContextParenting` — `c == nil` → span parented under `context.Background()`; no panic
10. `TestObserver_SpanIsBackdated` — span start time is `endTime - Event.Duration`

---

## Phase 5 — Submodule migration via `go.work`

**Duration:** ~1 day.

### Workspace setup (after Phase 4)

```bash
cat > go.work <<'EOF'
go 1.25

use (
    .
    ./plugins/autocert
    ./plugins/h2c
    ./plugins/hmacauth-otel
    ./plugins/hmacauth-redis
    ./plugins/idempotency-redis
    ./plugins/openapi
    ./plugins/openapi-ui
    ./plugins/otel
    ./plugins/prometheus
    ./plugins/ratelimit-redis
    ./plugins/sanitize
)
EOF

# Ensure gitignored
grep -q '^go\.work$'     .gitignore || echo 'go.work'     >> .gitignore
grep -q '^go\.work\.sum$' .gitignore || echo 'go.work.sum' >> .gitignore
```

### Per-submodule migration (audit-driven)

| Submodule | Constructor migration |
|---|---|
| `plugins/otel` | `New` (`otel.go:234`) → NativeMiddleware |
| `plugins/prometheus` | `New` (`prometheus.go:227`) → NativeMiddleware |
| `plugins/ratelimit-redis` | `New` (`ratelimit.go:150`) → NativeMiddleware |
| `plugins/sanitize` | `New` (`sanitize.go:97`) → NativeMiddleware |
| `plugins/autocert` | no constructor migration; pin bump only (Phase 11) |
| `plugins/h2c` | no constructor migration; pin bump only |
| `plugins/hmacauth-redis` | no constructor migration; pin bump only |
| `plugins/idempotency-redis` | no constructor migration; pin bump only |
| `plugins/openapi` | no constructor migration; pin bump only |
| `plugins/openapi-ui` | no constructor migration; pin bump only |

### Additional submodule cleanup now in scope

Because `plugins/otel` and `plugins/prometheus` are already touched in this phase for constructor migration, also complete the §12.6.10 `StatusRecorder` follow-up:

| Submodule | Extra migration |
|---|---|
| `plugins/prometheus` | Replace internal `recordingWriter` with pooled `aarv.StatusRecorder` plus any thin local wrapper still needed for plugin-specific behavior. Preserve status, bytes, `Unwrap`, panic cleanup, and existing metric semantics. |
| `plugins/otel` | Replace internal `recordingWriter` with pooled `aarv.StatusRecorder` plus any thin local wrapper still needed for plugin-specific behavior. Preserve span status, metric response-size, `Unwrap`, panic cleanup, and existing trace/metric semantics. |

### Test loop

```bash
set -e
for sub in $(find plugins -mindepth 2 -maxdepth 2 -name go.mod -not -path '*/.*' -exec dirname {} \;); do
  echo "::group::Testing $sub"
  (cd $sub && go test -race ./...)
  echo "::endgroup::"
done
```

---

## Phase 6 — SkipPaths lift

**Duration:** ~½ day.

### `skippaths.go` final

```go
func SkipPaths(paths []string, mw any) NativeMiddleware {
    // Uses coerceSlot for type discipline.
    // Returns NativeMiddleware{Stdlib, Native} per slot semantics.
}
```

### `skippaths_test.go` updates

- DROP `TestSkipPaths_DoesNotRegisterNativePair`
- ADD `TestSkipPaths_PreservesNativeFastPathAcrossInstances` (regression guard)
- ADD `TestSkipPaths_AcceptsMiddleware`
- ADD `TestSkipPaths_AcceptsNativeMiddleware`
- ADD `TestSkipPaths_AcceptsFuncLiteral`
- ADD `TestSkipPaths_PanicsOnNilMiddleware`
- ADD `TestSkipPaths_PanicsOnUnsupportedType`
- ADD `TestSkipPaths_PanicsOnNilStdlibInNativeMiddleware`

---

## Phase 7 — Documentation

**Duration:** ~1 day.

### `CHANGELOG.md` `[0.9.0] - 2026-06-03`

Sections:
- `### Breaking Changes` — return type changes, signature widening, spread pattern break, registry removal
- `### Fixed` — multi-instance collision, SkipPaths native preservation
- `### Added` — StatusRecorder, NativeMiddleware, SkipPaths, Observer hook, hmacauth-otel companion, openapi Tags, prometheus SubMillisecondBuckets, docs/middleware.md recipe, docs/MIGRATION_v0.9.md
- `### Changed` — otel semconv v1.37.0 (dual-emit; **legacy keys removed in v0.10.0**); logger/prometheus/otel internal recorder cleanup via `aarv.StatusRecorder`
- `### Migration` — points at `docs/MIGRATION_v0.9.md`

Insert fresh empty `## [Unreleased]` above the v0.9.0 heading.

### `docs/MIGRATION_v0.9.md` (new file)

Sections:
1. **Consumer code** — `app.Use(plugin.New())` works unchanged
2. **Spread pattern** — `mws := []any{...}` or explicit loop
3. **Typed variable assignment** — `var m aarv.Middleware = plugin.New()` → `var m aarv.NativeMiddleware = plugin.New()`
4. **`WrapMiddleware` helper** — `func myMW() aarv.Middleware { return aarv.WrapMiddleware(fn) }` → `aarv.NativeMiddleware`
5. **`pprof.Config.AuthMiddleware`** — `cfg.AuthMiddleware = jwtMW.Stdlib` extraction
6. **Tuple constructors** — `mw, err := encrypt.New(key)` only the type word changes
7. **Custom helper functions** — when to change vs not
8. **Plugin authors** — how to migrate `New(...)` constructors

### `docs/tasks-md.md`

- Do **not** blanket-tick entire subsections. Update only items actually completed by this release.
- §12.6.7: leave the optional Prometheus safe-label follow-up open unless separately implemented; release checklist items should be ticked only when the corresponding release phase actually completes.
- §12.6.9: rewrite the `SkipPaths` shipped entry to remove the stdlib-only caveat; mark the registry-keying limitation item resolved by v0.9.0.
- §12.6.10: tick the completed root-release, submodule-pin, `plugins/prometheus`/`plugins/otel` `StatusRecorder`, `plugins/hmacauth-otel`, and registry-redesign items. Leave unrelated/deferred future work open.
- Add §12.6.11 summarizing v0.9.0 (registry redesign, plugin migration, hmacauth-otel)

### `docs/middleware.md`

- Remove "stdlib-only" / "pending registry fix" caveat from the "Wrapping a middleware to add observability" recipe
- Update SkipPaths recommendation to "native fast path preserved"

### `skippaths.go` doc comment

- Drop the "Native fast path: not preserved (yet)" section
- Replace with concise "preserves native fast path when inner has a native pair"

### `plugins/hmacauth/hmacauth.go` package doc

- Update "Observability" section: replace "a future `plugins/hmacauth-otel` will register itself via this hook" with "the `plugins/hmacauth-otel` companion module ships a ready-made adapter — see its README"

### `plugins/hmacauth/README.md` (if exists; else doc.go)

- Add "Tracing" subsection pointing to `plugins/hmacauth-otel`

### Memory cleanup

- Local-only cleanup, **not part of the release commit**: if writable in the current Codex environment, delete `~/.claude/projects/-Users-nilayshah-Documents-PoC-aarv/memory/feedback_native_middleware_registry_keying.md` and remove its index line from `MEMORY.md`. If not writable, report the manual cleanup note instead of blocking the release.

---

## Phase 8 — Quality gates

**Duration:** ~½ day.

```bash
set -e

# Root tests (CI mirror)
go test -race -coverprofile=coverage.txt -covermode=atomic \
  $(go list ./... | grep -v /examples/ | grep -v /tests/benchmark/)

# Lint
golangci-lint run

# Submodule tests via go.work (exact CI discovery)
for mod in $(find plugins -name go.mod -not -path '*/.*'); do
  dir=$(dirname "$mod")
  (cd "$dir" && go test -race ./...)
done

# Examples sweep — GOWORK=off so example modules aren't required in go.work
echo "=== Examples compile sweep (GOWORK=off) ==="
GOWORK=off go build ./examples/...
GOWORK=off go vet ./examples/...
for mod in $(find examples -name go.mod 2>/dev/null); do
  dir=$(dirname "$mod")
  echo "  -> $dir"
  (cd "$dir" && GOWORK=off go build ./... && GOWORK=off go vet ./...)
done

# govulncheck
govulncheck ./...
for mod in $(find plugins -name go.mod -not -path '*/.*'); do
  (cd $(dirname "$mod") && govulncheck ./...)
done

# tidy diff — go.mod files unchanged (go.work isn't pulled into go.mod)
go mod tidy && git diff --exit-code go.mod go.sum
for mod in $(find plugins -name go.mod -not -path '*/.*'); do
  (cd $(dirname "$mod") && go mod tidy && git diff --exit-code go.mod go.sum)
done

# gh auth
gh auth status
```

**Acceptance:** all green; zero tidy diffs; examples compile clean under `GOWORK=off`; gh authenticated.

---

## ✋ Checkpoint B

Report:
- Phase 0–8 results (full)
- Files changed since `0361a5d` + uncommitted file list
- Verified tag versions (12 tags planned)
- Final commit message
- Tag messages for `v0.9.0` root + 11 submodule tags
- CHANGELOG `[0.9.0]` rendered
- Audit migration table re-confirmed: 35 needsMigration entries, all tests pass
- `pprof.Config.AuthMiddleware` decision re-confirmed
- Post-tag verification script ready

You: **proceed** / **wait** / **abort**. **No tag push before this.**

---

## Phase 9 — Release-prep commit + push

```bash
git add <specific-file-list-from-Checkpoint-B>
git diff --cached --stat   # verify

git commit -m "$(cat <<'EOF'
feat(v0.9.0): full registry redesign + hmacauth-otel + ALP batch closeouts

BREAKING:
  - RegisterNativeMiddleware / WrapMiddleware return NativeMiddleware
  - Every plugin constructor in the audit's needsMigration list now
    returns NativeMiddleware (33 constructors + 2 *Authorizer methods);
    timeout.New is the only stdlib-only outlier and stays Middleware.
  - App.Use, RouteGroup.Use, PluginContext.Use, WithRouteMiddleware,
    aarv.SkipPaths widened from typed variadic to ...any with runtime
    panics on nil/invalid types.
  - Spread pattern app.Use(mws...) with mws []aarv.Middleware no
    longer compiles. See docs/MIGRATION_v0.9.md.
  - nativeMiddlewareRegistry and nativeMiddlewareFunc deleted entirely.

FIXED:
  - Multi-instance wrapper collisions on the code-pointer registry.
  - aarv.SkipPaths preserves the native fast path under multiple
    instances.

ADDED:
  - aarv.StatusRecorder, aarv.NativeMiddleware, aarv.SkipPaths
    (native-preserving), plugins/hmacauth Observer hook,
    plugins/hmacauth-otel companion module, plugins/openapi Config.Tags,
    plugins/prometheus SubMillisecondBuckets, docs/middleware.md
    wrapping recipe, docs/MIGRATION_v0.9.md.

CHANGED:
  - plugins/otel HTTP semconv v1.37.0 (dual-emit; legacy keys removed
    in v0.10.0 no earlier than 4 weeks after v0.9.0).
  - plugins/logger, plugins/prometheus, and plugins/otel internal
    response recorders migrated to aarv.StatusRecorder.

DECISION:
  - pprof.Config.AuthMiddleware stays typed aarv.Middleware (used only
    on stdlib path at pprof.go:98). Consumers with NativeMiddleware
    extract .Stdlib.

See CHANGELOG.md [0.9.0] for the complete entry.
EOF
)"

git push origin main
```

### Phase 9.5 — CI wait loop

```bash
wait_for_ci() {
  local sha=$1
  local min_runs=${2:-2}
  local timeout_s=1800
  local start=$(date +%s)
  echo "Waiting for ≥$min_runs CI runs on $sha..."
  while true; do
    [ $(( $(date +%s) - start )) -gt $timeout_s ] && { echo "TIMEOUT"; return 1; }
    local runs=$(gh run list --commit=$sha --limit=20 --json status,conclusion 2>/dev/null)
    local total=$(echo "$runs" | jq '. | length')
    local incomplete=$(echo "$runs" | jq '[.[] | select(.status != "completed")] | length')
    if [ "$total" -lt "$min_runs" ]; then echo "  $total/$min_runs visible…"; sleep 15; continue; fi
    if [ "$incomplete" -gt 0 ]; then echo "  $incomplete still running…"; sleep 20; continue; fi
    break
  done
  local fails=$(gh run list --commit=$sha --limit=20 --json conclusion \
    -q '[.[] | select(.conclusion != "success" and .conclusion != "skipped")] | length')
  if [ "$fails" -gt 0 ]; then
    echo "FAIL: $fails CI run(s) failed on $sha"
    gh run list --commit=$sha --limit=20
    return 1
  fi
  echo "All CI runs green on $sha"
}

HEAD_SHA=$(git rev-parse HEAD)
wait_for_ci $HEAD_SHA 2 || exit 1
```

---

## Phase 10 — Root tag `v0.9.0`

```bash
git tag -a v0.9.0 -m "v0.9.0 — full registry redesign + ALP feedback batch

BREAKING: RegisterNativeMiddleware / WrapMiddleware return
NativeMiddleware. All plugin constructors per audit migrate.
App.Use family widened to ...any with nil-panics. See CHANGELOG.

FIXED: nativeMiddlewareRegistry removed; SkipPaths native-preserving.

ADDED: StatusRecorder, NativeMiddleware, Observer hook,
hmacauth-otel companion, openapi Tags, prometheus
SubMillisecondBuckets, wrapping recipe.

CHANGED: otel semconv v1.37.0 (legacy keys removed in v0.10.0).
logger/prometheus/otel response recorders now use StatusRecorder."

git push origin v0.9.0
git ls-remote --refs --tags origin v0.9.0
```

---

## Phase 11 — Submodule pin bumps

```bash
# Drop dev-time workspace before any submodule edits
rm -f go.work go.work.sum

# Bump pin in each submodule against tagged root
for sub in $(find plugins -mindepth 2 -maxdepth 2 -name go.mod -not -path '*/.*' -exec dirname {} \;); do
  echo "=== $sub ==="
  (cd $sub && go get github.com/nilshah80/aarv@v0.9.0 && go mod tidy && go test -race ./...) || exit 1
done

git add plugins/*/go.mod plugins/*/go.sum
git diff --cached --stat   # verify
git commit -m "chore: bump aarv pin to v0.9.0 in all submodules"
git push origin main
```

### Phase 11.5 — CI wait on submodule pin-bump commit

Same `wait_for_ci` invocation as Phase 9.5.

---

## Phase 12 — Submodule tags

```bash
for sub in autocert h2c hmacauth-redis idempotency-redis openapi openapi-ui otel prometheus ratelimit-redis sanitize; do
  git tag -a plugins/$sub/v0.9.0 \
    -m "plugins/$sub v0.9.0 — aarv v0.9.0 pin; NativeMiddleware-shaped public API where applicable"
done
git tag -a plugins/hmacauth-otel/v0.1.0 \
  -m "plugins/hmacauth-otel v0.1.0 — OTel adapter for hmacauth.Config.Observer"

git push origin \
  plugins/autocert/v0.9.0 \
  plugins/h2c/v0.9.0 \
  plugins/hmacauth-redis/v0.9.0 \
  plugins/idempotency-redis/v0.9.0 \
  plugins/openapi/v0.9.0 \
  plugins/openapi-ui/v0.9.0 \
  plugins/otel/v0.9.0 \
  plugins/prometheus/v0.9.0 \
  plugins/ratelimit-redis/v0.9.0 \
  plugins/sanitize/v0.9.0 \
  plugins/hmacauth-otel/v0.1.0

git ls-remote --refs --tags origin 'plugins/*/v0.9.0' 'plugins/hmacauth-otel/v0.1.0'
```

---

## Phase 13 — Go module verification

**Before GitHub release creation** (so broken tags are caught quietly).

```bash
verify_module() {
  local mod=$1
  for attempt in 1 2 3 4 5; do
    if GOPROXY=direct go list -m $mod 2>/dev/null; then return 0; fi
    echo "  attempt $attempt failed; sleeping $((attempt * 10))s…"
    sleep $((attempt * 10))
  done
  echo "FAIL: $mod did not resolve after 5 attempts"
  return 1
}

TMPDIR=$(mktemp -d) && cd $TMPDIR && go mod init verify

for mod in \
  "github.com/nilshah80/aarv@v0.9.0" \
  "github.com/nilshah80/aarv/plugins/prometheus@v0.9.0" \
  "github.com/nilshah80/aarv/plugins/otel@v0.9.0" \
  "github.com/nilshah80/aarv/plugins/openapi@v0.9.0" \
  "github.com/nilshah80/aarv/plugins/sanitize@v0.9.0" \
  "github.com/nilshah80/aarv/plugins/ratelimit-redis@v0.9.0" \
  "github.com/nilshah80/aarv/plugins/hmacauth-redis@v0.9.0" \
  "github.com/nilshah80/aarv/plugins/idempotency-redis@v0.9.0" \
  "github.com/nilshah80/aarv/plugins/autocert@v0.9.0" \
  "github.com/nilshah80/aarv/plugins/h2c@v0.9.0" \
  "github.com/nilshah80/aarv/plugins/openapi-ui@v0.9.0" \
  "github.com/nilshah80/aarv/plugins/hmacauth-otel@v0.1.0"; do
  verify_module $mod || exit 1
done

# Compile smoke — exercises the registry-fix payoff AND avoids
# SkipPaths(nil,nil) which now panics directly in SkipPaths.
cat > main.go <<'EOF'
package main

import (
    "fmt"

    "github.com/nilshah80/aarv"
    hmacotel "github.com/nilshah80/aarv/plugins/hmacauth-otel"
    "github.com/nilshah80/aarv/plugins/hmacauth"
    "github.com/nilshah80/aarv/plugins/prometheus"
)

func main() {
    app := aarv.New()

    // Two distinct SkipPaths instances with native-capable inners,
    // exercising the registry-fix payoff.
    innerA := aarv.Logger()
    innerB := aarv.Recovery()
    sp1 := aarv.SkipPaths([]string{"/a"}, innerA)
    sp2 := aarv.SkipPaths([]string{"/b"}, innerB)
    app.Use(sp1, sp2)  // App.Use(...any) accepting NativeMiddleware

    _ = aarv.NewStatusRecorder(nil)
    _ = prometheus.SubMillisecondBuckets
    _ = hmacauth.OutcomeOK
    _ = hmacotel.NewObserver()
    fmt.Println("ok")
}
EOF
GOPROXY=direct go mod tidy
go run .   # expect: ok

cd - && rm -rf $TMPDIR
```

**On failure:** STOP. No GitHub releases. Activate Phase 16 rollback to v0.9.1.

---

## Phase 14 — GitHub releases

```bash
awk '/^## \[0.9.0\]/{flag=1; next} /^## \[/{flag=0} flag' CHANGELOG.md > /tmp/notes_v0.9.0.md
cat /tmp/notes_v0.9.0.md   # sanity check

gh release create v0.9.0 \
  --title "v0.9.0 — full registry redesign + ALP feedback batch" \
  --notes-file /tmp/notes_v0.9.0.md

gh release create plugins/hmacauth-otel/v0.1.0 \
  --title "plugins/hmacauth-otel v0.1.0 — initial release" \
  --notes "OTel adapter for hmacauth.Config.Observer (shipped with aarv v0.9.0)."

for sub in autocert h2c hmacauth-redis idempotency-redis openapi openapi-ui otel prometheus ratelimit-redis sanitize; do
  gh release create plugins/$sub/v0.9.0 \
    --title "plugins/$sub v0.9.0 — aarv v0.9.0 pin" \
    --notes "Pinned to aarv v0.9.0. See aarv v0.9.0 release notes for framework changes (RegisterNativeMiddleware return type, App.Use signature)."
done
```

---

## Phase 15 — alp follow-up

Update `~/Documents/PoC/alp/tasks.md` §10.12 noting:
- aarv v0.9.0 ships the registry fix
- alp adopts §10.2 **Path B** (full ~−118 LOC cleanup; `plugins/hmacauth-otel` available)
- alp adopts §10.5 **Path A** (SkipPaths now native-preserving; no caveat)
- alp's spread-pattern uses of `app.Use(mws...)` with `mws []aarv.Middleware` need migration per `docs/MIGRATION_v0.9.md`

Show draft for review before applying.

---

## Phase 16 — Rollback

**Forward-only policy:** never delete tags, supersede with v0.9.1.

If Phase 13 fails:
1. Stop immediately. Do NOT create GitHub releases.
2. Diagnose (likely a missing file in tag, broken pin, etc.)
3. Prepare a `v0.9.1` patch with the fix
4. Re-run Phases 7 → 13 for v0.9.1
5. Update the GitHub release for `v0.9.0` (if it was created before Phase 13) to add a prominent "DO NOT USE — use v0.9.1 instead" warning at the top

---

## Complete file inventory

Every file that will be added, modified, or deleted across the release.

### Root module — core Go files

| Path | Action | Notes |
|---|---|---|
| `middleware.go` | MODIFY | Add `NativeMiddleware` type; delete `nativeMiddlewareRegistry`, `registerNativeMiddleware`, `nativeMiddlewareFunc`; refactor `RegisterNativeMiddleware` and `WrapMiddleware` to return `NativeMiddleware`; refactor `buildChain` / `buildNativeChain` to take `[]middlewareSlot`; add `middlewareSlot` type and `coerceSlot` helper; update `Recovery()` and `Logger()` return types |
| `aarv.go` | MODIFY | `App.globalMiddleware` field type → `[]middlewareSlot` |
| `app_routes.go` | MODIFY | `App.Use(middlewares ...any)` signature change; type-switch via `coerceSlot` |
| `router.go` | MODIFY | `RouteGroup.middleware` and `routeConfig.middleware` field types → `[]middlewareSlot`; `RouteGroup.Use(...any)`; `WithRouteMiddleware(...any)`; `RouteGroup.addRoute` combined slice rebuilt as `[]middlewareSlot`; `RouteGroup.Group` nested inheritance copy updated (one-shot snapshot preserved) |
| `dispatch.go` | MODIFY | 9 composition sites consuming `a.globalMiddleware` updated; native pre-check at line 27 changes to `slot.native == nil` check |
| `plugin.go` | MODIFY | `PluginContext.Use(...any)` signature change |
| `skippaths.go` | MODIFY | Signature change to `SkipPaths(paths []string, mw any) NativeMiddleware`; uses `coerceSlot`; preserves native fast path; drop stdlib-only doc comment |

### Root module — new files

| Path | Notes |
|---|---|
| `nativemiddleware_test.go` | New regression suite: distinct registrations don't collide; slot-based chain build; nil/invalid-type panics; SkipPaths multi-instance native preservation |

### Root module — test files modified

| Path | Notes |
|---|---|
| `middleware_test.go` | Update tests that pass `[]Middleware` directly to internal builders (if any); update tests touching `RegisterNativeMiddleware` return type |
| `skippaths_test.go` | DROP `TestSkipPaths_DoesNotRegisterNativePair`; ADD multi-instance regression + coerceSlot acceptance/panic tests |
| Other `*_test.go` | Touch only if a test asserts middleware-slice types directly (likely none) |

### In-root plugin source files (26 directories, 27 entries)

| Plugin | File:line | Change |
|---|---|---|
| `plugins/apikey/apikey.go` | line 75 | `New` return type → `aarv.NativeMiddleware` |
| `plugins/basicauth/basicauth.go` | line 89 | `New` → NativeMiddleware |
| `plugins/bearer/bearer.go` | line 136 | `New` → NativeMiddleware |
| `plugins/bodylimit/bodylimit.go` | lines 34, 82 | `New(maxBytes int64)` and `NewWithResponse` → NativeMiddleware |
| `plugins/compress/compress.go` | line 511 | `New` → NativeMiddleware |
| `plugins/cors/cors.go` | line 59 | `New` → NativeMiddleware |
| `plugins/csrf/csrf.go` | line 124 | `New` → NativeMiddleware |
| `plugins/encrypt/encrypt.go` | lines 301, 476 | `New(key, ...cfg) (NativeMiddleware, error)` TUPLE; `MustNew` → NativeMiddleware |
| `plugins/etag/etag.go` | line 98 | `New` → NativeMiddleware |
| `plugins/health/health.go` | line 66 | `New` → NativeMiddleware |
| `plugins/hmacauth/hmacauth.go` | line 252 | `New` → NativeMiddleware; **also** update package doc "Observability" section to point to `plugins/hmacauth-otel` companion |
| `plugins/idempotency/idempotency.go` | line 204 | `New` → NativeMiddleware |
| `plugins/ipfilter/ipfilter.go` | line 96 | `New` → NativeMiddleware |
| `plugins/jwt/jwt.go` | line 275 | `New` → NativeMiddleware |
| `plugins/logger/logger.go` | line 81 | `New` → NativeMiddleware |
| `plugins/pprof/pprof.go` | line 133 | `New` → NativeMiddleware; **`Config.AuthMiddleware` field stays `aarv.Middleware`** (no change to that field) |
| `plugins/ratelimit/ratelimit.go` | lines 124, 136 | `New` → NativeMiddleware; `NewWithCleanup` → `(NativeMiddleware, func() error)` TUPLE |
| `plugins/rbac/rbac.go` | lines 111, 130 | `(*Authorizer).RequireRoles` and `RequireAnyRole` METHODS → NativeMiddleware |
| `plugins/recover/recover.go` | line 131 | `New` → NativeMiddleware |
| `plugins/requestid/requestid.go` | line 225 | `New` → NativeMiddleware |
| `plugins/secure/secure.go` | line 95 | `New` → NativeMiddleware |
| `plugins/session/middleware.go` | lines 149, 162 | `New` and `NewCookie` → NativeMiddleware (indirect via `buildMiddleware`) |
| `plugins/static/static.go` | line 103 | `New` → NativeMiddleware |
| `plugins/throttle/throttle.go` | line 70 | `New` → NativeMiddleware |
| `plugins/timeout/timeout.go` | line 199 | `Context(d)` → NativeMiddleware. **`New(d)` at line 126 STAYS `aarv.Middleware`** (stdlib-only outlier) |
| `plugins/verboselog/verboselog.go` | line 538 | `New` → NativeMiddleware |

### In-root plugin test files

Test files for the 26 plugins above need updates wherever they capture the return value as `aarv.Middleware`:

| Pattern to search | Action |
|---|---|
| `var mw aarv.Middleware = <plugin>.New(...)` | rewrite as `var mw aarv.NativeMiddleware = ...` |
| `mw := <plugin>.New(...)` (`:=`) | unchanged (type inference picks up new type) |
| `app.Use(<plugin>.New(...))` | unchanged |

### Submodule plugin source files

| Submodule | File:line | Change |
|---|---|---|
| `plugins/otel/otel.go` | line 234 + recording writer section | `New` → NativeMiddleware; migrate internal `recordingWriter` to pooled `aarv.StatusRecorder` while preserving trace/metric semantics |
| `plugins/prometheus/prometheus.go` | line 227 + recording writer section | `New` → NativeMiddleware; migrate internal `recordingWriter` to pooled `aarv.StatusRecorder` while preserving metric semantics |
| `plugins/ratelimit-redis/ratelimit.go` | line 150 | `New` → NativeMiddleware |
| `plugins/sanitize/sanitize.go` | line 97 | `New` → NativeMiddleware |
| `plugins/autocert/` | (none) | pin bump only (Phase 11) |
| `plugins/h2c/` | (none) | pin bump only |
| `plugins/hmacauth-redis/` | (none) | pin bump only |
| `plugins/idempotency-redis/` | (none) | pin bump only |
| `plugins/openapi/` | (none) | pin bump only |
| `plugins/openapi-ui/` | (none) | pin bump only |

### Submodule `go.mod` and `go.sum` files (Phase 11 pin bumps)

All 10 existing submodule `go.mod` files get `require github.com/nilshah80/aarv v0.9.0`:
- `plugins/autocert/go.mod` + `go.sum`
- `plugins/h2c/go.mod` + `go.sum`
- `plugins/hmacauth-redis/go.mod` + `go.sum`
- `plugins/idempotency-redis/go.mod` + `go.sum`
- `plugins/openapi/go.mod` + `go.sum`
- `plugins/openapi-ui/go.mod` + `go.sum`
- `plugins/otel/go.mod` + `go.sum`
- `plugins/prometheus/go.mod` + `go.sum`
- `plugins/ratelimit-redis/go.mod` + `go.sum`
- `plugins/sanitize/go.mod` + `go.sum`

### New plugin module: `plugins/hmacauth-otel/`

| Path | Action |
|---|---|
| `plugins/hmacauth-otel/go.mod` | NEW — `go 1.25.0`; requires `github.com/nilshah80/aarv v0.9.0`, `go.opentelemetry.io/otel v1.43.0`, `go.opentelemetry.io/otel/trace v1.43.0`, `go.opentelemetry.io/otel/sdk v1.43.0` |
| `plugins/hmacauth-otel/go.sum` | NEW — auto-generated |
| `plugins/hmacauth-otel/doc.go` | NEW — package doc |
| `plugins/hmacauth-otel/observer.go` | NEW — `NewObserver(opts ...Option) hmacauth.Observer` |
| `plugins/hmacauth-otel/options.go` | NEW — `Option`, `WithTracerProvider`, `WithSpanName` |
| `plugins/hmacauth-otel/observer_test.go` | NEW — 10 tests (matrix in Phase 4) |
| `plugins/hmacauth-otel/README.md` | NEW — mirrors `plugins/otel/README.md` BYO-Provider pattern |

### Documentation files

| Path | Action | Notes |
|---|---|---|
| `CHANGELOG.md` | MODIFY | Promote `[Unreleased]` → `## [0.9.0] - 2026-06-03`; full `Breaking/Fixed/Added/Changed/Migration` sections; insert fresh empty `## [Unreleased]` above |
| `docs/MIGRATION_v0.9.md` | NEW | Consumer + plugin-author migration guide (8 sections per Phase 7) |
| `docs/middleware.md` | MODIFY | Drop stdlib-only caveat from "Skipping the wrap on observability paths" subsection; update "Wrapping a middleware to add observability" recipe |
| `docs/openapi.md` | (unchanged) | Already updated with `Config.Tags` row |
| `docs/tasks-md.md` | MODIFY | Update only items completed by this release; keep optional/deferred tasks open; add §12.6.11 "v0.9.0 release — full registry redesign" |
| `docs/plan-0.9.0.md` | NEW | This file |
| `skippaths.go` doc comment | MODIFY | Drop "Native fast path: not preserved (yet)" section; replace with native-preserving language |
| `plugins/hmacauth/hmacauth.go` package doc | MODIFY | Update "Observability" subsection: replace "a future `plugins/hmacauth-otel` will…" with concrete pointer to shipped companion module |
| `plugins/otel/README.md` | (already updated) | Modern attribute keys + dual-emit caveat |
| `plugins/prometheus/README.md` | (already updated) | SubMillisecondBuckets preset section |

### Examples files

| Path | Action | Notes |
|---|---|---|
| `examples/route-groups/main.go` | MODIFY line 10 | `apiVersion` return type → `aarv.NativeMiddleware` (the function `return aarv.WrapMiddleware(...)`s) |
| `examples/auth/main.go` | MODIFY line 202 | `SecureSessionMiddleware` return type → `aarv.NativeMiddleware` |
| `examples/custom-middleware/main.go` | (audit-confirmed compatible) | `dualRequestID := aarv.RegisterNativeMiddleware(...)` uses `:=`; works through type inference. Verify in Phase 8 sweep. |
| `examples/custom-plugin/main.go` | (audit-confirmed compatible) | `pc.Use(aarv.RegisterNativeMiddleware(...))` inline — works because `pc.Use` accepts `any` |
| `examples/plugin-custom/main.go` | (audit-confirmed compatible) | `pc.Use(aarv.WrapMiddleware(...))` inline — works because `pc.Use` accepts `any` |
| `examples/middleware-chain/main.go` | (audit-confirmed compatible) | `app.Use(aarv.WrapMiddleware(...), ...)` — works because `app.Use` accepts `any` |
| Other 30 example files | (audit-confirmed compatible) | No `aarv.Middleware`-returning helper functions that call `RegisterNativeMiddleware` |

### Memory files (Claude Code metadata; local-only, not release commit)

| Path | Action |
|---|---|
| `~/.claude/projects/-Users-nilayshah-Documents-PoC-aarv/memory/feedback_native_middleware_registry_keying.md` | Local-only cleanup if writable; otherwise report manual cleanup note |
| `~/.claude/projects/-Users-nilayshah-Documents-PoC-aarv/memory/MEMORY.md` | Local-only index cleanup if writable; otherwise report manual cleanup note |

### Temporary / build files

| Path | Action |
|---|---|
| `go.work` | CREATE in Phase 5, DELETE in Phase 11. **Gitignored.** |
| `go.work.sum` | Auto-generated, gitignored |
| `.gitignore` | ENSURE contains `go.work` and `go.work.sum` (add if missing) |

### CI / workflow files

| Path | Action |
|---|---|
| `.github/workflows/test.yml` | (no changes) — already auto-discovers all `plugins/*/go.mod` via `find` |
| `.github/workflows/lint.yml` | (no changes) |

### Files NOT touched (worth noting for review)

- `aarv_test.go` and other root tests — modify only if they assert middleware-slice types directly
- `codec/segmentio/`, `codec/sonic/`, `codec/jsonv2/` codec submodules — not affected; not touched
- `tests/benchmark/` — gitignored; not part of release flow
- `mach_style_bench/` — gitignored
- `CONTRIBUTING.md`, `LICENSE`, `README.md` — no changes
- `cmd/` directory — not affected
- `internal/headerbuffer/` — not affected

---

## Approvals needed

Five explicit yes/no items before Phase 0 starts:

1. **Tag v0.9.0** (minor; accepts breaking changes documented in CHANGELOG)
2. **Migration scope = audit's 35 `needsMigration` items** (timeout.New stays Middleware; pprof.Config.AuthMiddleware stays Middleware)
3. **`App.Use(...any)` widening + spread-pattern break** documented in `docs/MIGRATION_v0.9.md`
4. **`SkipPaths(paths []string, mw any) NativeMiddleware`** signature
5. **Legacy OTel attribute removal in v0.10.0** (not v0.9.x)

---

## Timeline

| Block | Duration |
|---|---|
| Phase 0 verify + tool/Go gate | 30 min |
| Phase 1 design lock-in | ½ day |
| Phase 2 root impl (22 sites) | 1.5 days |
| Phase 3 in-root plugins (per audit) | 1.5 days |
| Phase 4 hmacauth-otel module | ½ day |
| Phase 5 submodules via go.work | 1 day |
| Phase 6 SkipPaths lift | ½ day |
| Phase 7 docs incl. MIGRATION_v0.9 | 1 day |
| Phase 8 quality gates incl. examples sweep | ½ day |
| Phases 9–14 release ceremony | 2 hours active + CI waits |
| Phase 15 alp draft | 30 min |
| **Total** | **~8–10 focused days; ~2 calendar weeks** |
