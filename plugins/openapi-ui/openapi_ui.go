// Package openapiui mounts the Swagger UI and ReDoc viewers against an
// OpenAPI spec produced by plugins/openapi (or any other source — the
// viewers only require a URL to a JSON or YAML OpenAPI 3.x document).
//
// Use [Mount] for the common case (both viewers + their static assets,
// registered against an *aarv.App). For finer control, [SwaggerHandler]
// and [ReDocHandler] return raw http.Handlers that the caller mounts
// themselves.
//
// VENDORED ASSETS: the embedded files in assets/swagger-ui and
// assets/redoc are real upstream dist bundles pinned to specific
// versions. See ASSETS.md for the pinned versions, source URLs, and
// update procedure. The bundled LICENSE files are tested for existence
// to prevent silent removal during asset updates.
//
// CSP NOTE: the Swagger UI initializer is served as an EXTERNAL script
// (not inlined), so a Content-Security-Policy of `script-src 'self'` is
// sufficient — no per-script-hash maintenance burden when assets are
// updated. ReDoc requires `script-src 'self'` as well.
package openapiui

import (
	"embed"
	"errors"
	"io/fs"
	"net/http"
	"strings"

	"github.com/nilshah80/aarv"
)

// Default mount paths used by [Config] when the corresponding fields are
// left empty. To skip mounting a viewer, set its Path field to the
// sentinel SkipMount ("-") — empty would otherwise be filled with the
// default value by applyDefaults.
const (
	DefaultSwaggerPath = "/docs"
	DefaultReDocPath   = "/redoc"
	DefaultSpecURL     = "/openapi.json"
	DefaultTitle       = "API Docs"

	// SkipMount, when assigned to Config.SwaggerPath or Config.ReDocPath,
	// suppresses mounting of that viewer.
	SkipMount = "-"
)

// Sentinels exposed for errors.Is.
var (
	// ErrNilApp is returned by [Mount] when its app argument is nil.
	ErrNilApp = errors.New("openapi-ui: app is nil")
)

// Config controls which viewers are mounted, where, and what spec they
// point at.
type Config struct {
	// SpecURL is the URL the viewers fetch the OpenAPI document from.
	// Empty uses DefaultSpecURL ("/openapi.json"). Can be relative
	// (served by the same App as the viewers) or absolute (cross-origin
	// — make sure CORS is configured on the spec server).
	SpecURL string

	// Title is the HTML <title> for both viewers. Empty uses
	// DefaultTitle ("API Docs").
	Title string

	// SwaggerPath is the route for the Swagger UI viewer. Empty uses
	// DefaultSwaggerPath ("/docs"). Set to SkipMount ("-") to skip
	// mounting Swagger entirely — there is intentionally no "empty
	// disables" affordance because empty is consumed by applyDefaults.
	SwaggerPath string

	// ReDocPath is the route for the ReDoc viewer. Empty uses
	// DefaultReDocPath ("/redoc"). Set to SkipMount ("-") to skip
	// mounting ReDoc — same rationale as SwaggerPath.
	ReDocPath string
}

//go:embed assets/swagger-ui
var swaggerEmbed embed.FS

//go:embed assets/redoc
var redocEmbed embed.FS

// swaggerStaticFS / redocStaticFS strip the "assets/<name>" prefix so the
// http.FileServer mount serves files at "/<name>.css" rather than
// "/assets/swagger-ui/<name>.css".
//
// fs.Sub only errors on malformed path syntax; both arguments here are
// compile-time constants known to be valid, so the error is discarded.
var (
	swaggerStaticFS, _ = fs.Sub(swaggerEmbed, "assets/swagger-ui")
	redocStaticFS, _   = fs.Sub(redocEmbed, "assets/redoc")
)

// Mount registers the Swagger UI and ReDoc viewer routes (plus their
// embedded static asset routes) on app per cfg. Returns ErrNilApp when
// app is nil.
//
// Each viewer's "static" subtree is mounted at <Path>/static/. The
// generated HTML references those assets via relative URLs so the
// viewer can move under any prefix without rewriting the dist.
func Mount(app *aarv.App, cfg Config) error {
	if app == nil {
		return ErrNilApp
	}
	cfg = applyDefaults(cfg)

	if cfg.SwaggerPath != SkipMount {
		app.Get(cfg.SwaggerPath, swaggerPageHandler(cfg))
		app.Mount(cfg.SwaggerPath+"/static/", http.FileServer(http.FS(swaggerStaticFS)))
	}
	if cfg.ReDocPath != SkipMount {
		app.Get(cfg.ReDocPath, redocPageHandler(cfg))
		app.Mount(cfg.ReDocPath+"/static/", http.FileServer(http.FS(redocStaticFS)))
	}
	return nil
}

// SwaggerHandler returns an http.Handler that serves the Swagger UI
// viewer page. Use this when Mount's path conventions do not fit and the
// caller prefers to wire the routes manually. The caller is responsible
// for separately serving the static assets (see [SwaggerStaticFS]).
func SwaggerHandler(specURL, title, staticBase string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(swaggerHTML(specURL, title, staticBase)))
	})
}

// ReDocHandler returns an http.Handler that serves the ReDoc viewer page.
// Companion to SwaggerHandler; same caveats.
func ReDocHandler(specURL, title, staticBase string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(redocHTML(specURL, title, staticBase)))
	})
}

// SwaggerStaticFS exposes the embedded Swagger UI dist for callers that
// mount it themselves (paired with SwaggerHandler).
func SwaggerStaticFS() fs.FS { return swaggerStaticFS }

// ReDocStaticFS exposes the embedded ReDoc dist for callers that mount
// it themselves (paired with ReDocHandler).
func ReDocStaticFS() fs.FS { return redocStaticFS }

func applyDefaults(cfg Config) Config {
	if cfg.SpecURL == "" {
		cfg.SpecURL = DefaultSpecURL
	}
	if cfg.Title == "" {
		cfg.Title = DefaultTitle
	}
	if cfg.SwaggerPath == "" {
		cfg.SwaggerPath = DefaultSwaggerPath
	}
	if cfg.ReDocPath == "" {
		cfg.ReDocPath = DefaultReDocPath
	}
	return cfg
}

func swaggerPageHandler(cfg Config) aarv.HandlerFunc {
	staticBase := cfg.SwaggerPath + "/static"
	body := swaggerHTML(cfg.SpecURL, cfg.Title, staticBase)
	return func(c *aarv.Context) error {
		c.Response().Header().Set("Content-Type", "text/html; charset=utf-8")
		_, err := c.Response().Write([]byte(body))
		return err
	}
}

func redocPageHandler(cfg Config) aarv.HandlerFunc {
	staticBase := cfg.ReDocPath + "/static"
	body := redocHTML(cfg.SpecURL, cfg.Title, staticBase)
	return func(c *aarv.Context) error {
		c.Response().Header().Set("Content-Type", "text/html; charset=utf-8")
		_, err := c.Response().Write([]byte(body))
		return err
	}
}

const swaggerTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>__TITLE__</title>
  <link rel="stylesheet" type="text/css" href="__BASE__/swagger-ui.css">
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="__BASE__/swagger-ui-bundle.js" charset="UTF-8"></script>
  <script src="__BASE__/swagger-initializer.js" data-spec-url="__SPEC__" charset="UTF-8"></script>
</body>
</html>
`

const redocTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>__TITLE__</title>
  <meta name="viewport" content="width=device-width, initial-scale=1">
</head>
<body style="margin:0">
  <redoc spec-url="__SPEC__"></redoc>
  <script src="__BASE__/redoc.standalone.js" charset="UTF-8"></script>
</body>
</html>
`

func swaggerHTML(specURL, title, staticBase string) string {
	r := strings.NewReplacer(
		"__TITLE__", htmlEscape(title),
		"__BASE__", htmlEscape(staticBase),
		"__SPEC__", htmlEscape(specURL),
	)
	return r.Replace(swaggerTemplate)
}

func redocHTML(specURL, title, staticBase string) string {
	r := strings.NewReplacer(
		"__TITLE__", htmlEscape(title),
		"__BASE__", htmlEscape(staticBase),
		"__SPEC__", htmlEscape(specURL),
	)
	return r.Replace(redocTemplate)
}

// htmlEscape minimally escapes the few characters that have meaning
// inside an HTML attribute or text node. Title / SpecURL / staticBase
// are caller-controlled so we cannot assume they are safe.
func htmlEscape(s string) string {
	r := strings.NewReplacer(
		`&`, "&amp;",
		`<`, "&lt;",
		`>`, "&gt;",
		`"`, "&quot;",
		`'`, "&#39;",
	)
	return r.Replace(s)
}
