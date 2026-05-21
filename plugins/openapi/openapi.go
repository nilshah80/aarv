// Package openapi generates OpenAPI 3.1 specifications from an aarv App's
// registered routes and surfaces the spec at configurable HTTP endpoints
// (JSON and YAML).
//
// The plugin reads the metadata that aarv route options attach to RouteInfo
// (WithSchema / WithSchemaTypes / WithResponse / WithRequestContentType,
// plus the universal WithSummary / WithDescription / WithOperationID /
// WithTags / WithDeprecated). For typed handlers registered via
// aarv.BindRoute / aarv.BindGroupRoute, request and response schemas are
// picked up automatically.
//
// LAZY-CACHE CONTRACT: register all routes BEFORE the first request to
// the JSON or YAML endpoint. The spec is generated lazily on that first
// request via sync.Once and cached for the App lifetime; routes added
// after that point are NOT reflected. Calling openapi.New does not by
// itself freeze the route set — the freeze happens at first spec request.
//
// FEATURE SET (12.6b):
//   - OpenAPI 3.1 / JSON Schema 2020-12 output (nullable rendered as
//     type: ["X","null"] union, not the deprecated 3.0 nullable keyword).
//   - Component dedup keyed by reflect.Type identity. Recursive struct
//     types terminate via component-placeholder $ref.
//   - validate:"" tag mapping for required, min/max/gte/lte/gt/lt/len,
//     oneof, email, url, uuid, regex, unique. Unknown rules are ignored
//     with a slog.Debug entry.
//   - YAML output via sigs.k8s.io/yaml.JSONToYAML (preserves JSON's
//     deterministic key ordering).
//   - SecuritySchemes emission into components.securitySchemes.
//   - Custom App codec content type flows into request body media type
//     declarations (no per-route override needed).
//   - Catch-all aarv path patterns "{name...}" normalize to "{name}" in
//     the OpenAPI path; consumers treat them as opaque string parameters.
//
// NON-GOALS (intentionally not implemented):
//   - Polymorphic / discriminator-based schemas.
//   - Full custom MarshalJSON introspection.
//   - Non-string-keyed maps (degraded to bare object schemas).
//   - Automatic operation.security generation from middleware.
package openapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/nilshah80/aarv"
)

// Default route paths used by [Config] when the corresponding fields are
// left empty.
const (
	DefaultJSONPath = "/openapi.json"
	DefaultYAMLPath = "/openapi.yaml"
)

// ErrNilApp is returned by [New] when its app argument is nil.
var ErrNilApp = errors.New("openapi: app is nil")

// Server describes a server entry in the OpenAPI document.
type Server struct {
	URL         string `json:"url"`
	Description string `json:"description,omitempty"`
}

// Contact describes the API contact in the OpenAPI Info object.
type Contact struct {
	Name  string `json:"name,omitempty"`
	URL   string `json:"url,omitempty"`
	Email string `json:"email,omitempty"`
}

// License describes the API license in the OpenAPI Info object.
type License struct {
	Name       string `json:"name,omitempty"`
	URL        string `json:"url,omitempty"`
	Identifier string `json:"identifier,omitempty"`
}

// Config controls the generated OpenAPI document and where it is served.
type Config struct {
	// Title, Version, Description populate the Info object. Title and
	// Version default to "Aarv API" and "0.0.0" respectively.
	Title       string
	Version     string
	Description string

	// Servers lists the public-facing URLs the API is reachable at.
	Servers []Server

	// Contact and License are surfaced in the Info object when set.
	Contact *Contact
	License *License

	// Include, when non-nil, is the SOLE filter applied to routes:
	// Exclude and Tags are ignored. When nil, Tags is applied first
	// (when set) and Exclude is applied to whatever survives.
	Include func(aarv.RouteInfo) bool

	// Tags filters routes by their RouteInfo.Tags (set via aarv.WithTags
	// at route registration). When non-empty, only routes carrying at
	// least one of the listed tags are included in the spec; routes
	// with no tags are excluded. Useful for splitting a multi-section
	// API into per-tag specs (e.g. one spec for "public", another for
	// "internal") without writing a custom Include closure.
	//
	// Tags is ignored when Include is non-nil — Include remains the
	// override hatch for arbitrary filter logic.
	Tags []string

	// Exclude is a list of path-prefixes; any route whose Pattern
	// starts with one of these is omitted from the spec. The defaults
	// hide the documentation endpoints themselves so the spec does
	// not document its own viewer routes.
	Exclude []string

	// JSONPath is the route at which the JSON spec is served. Empty
	// uses DefaultJSONPath ("/openapi.json").
	JSONPath string

	// DisableJSONEndpoint suppresses registration of the JSON route
	// so callers can serve the spec themselves (e.g. behind auth or
	// at a different prefix). The Spec is still buildable via Spec().
	DisableJSONEndpoint bool

	// YAMLPath is the route at which the YAML spec is served. Empty
	// uses DefaultYAMLPath ("/openapi.yaml"). Set DisableYAMLEndpoint
	// to suppress registration entirely.
	YAMLPath string

	// DisableYAMLEndpoint suppresses registration of the YAML route.
	DisableYAMLEndpoint bool

	// SecuritySchemes populates components.securitySchemes in the
	// generated document. The key is the scheme name (referenced from
	// operations via the security field, which 12.6b does not yet
	// generate automatically — set it on routes via security middleware
	// or by post-processing the spec).
	SecuritySchemes map[string]SecurityScheme
}

// SecurityScheme is the OpenAPI 3.1 Security Scheme object. The minimum
// useful field set: Type ("apiKey", "http", "oauth2", "openIdConnect"),
// Scheme (for http: "bearer", "basic"), BearerFormat ("JWT" etc.),
// In ("header"/"query"/"cookie" for apiKey), Name (header/query name
// for apiKey).
type SecurityScheme struct {
	Type             string `json:"type"`
	Description      string `json:"description,omitempty"`
	Name             string `json:"name,omitempty"`
	In               string `json:"in,omitempty"`
	Scheme           string `json:"scheme,omitempty"`
	BearerFormat     string `json:"bearerFormat,omitempty"`
	OpenIDConnectURL string `json:"openIdConnectUrl,omitempty"`
}

// DefaultExclude is the default value for Config.Exclude. It hides the
// documentation endpoints so they are not self-documented unless the
// caller opts in via Config.Include.
var DefaultExclude = []string{
	"/openapi.json",
	"/openapi.yaml",
	"/docs",
	"/redoc",
}

// generator is the lazy, cached spec builder bound to one App + Config.
// Exported via the Plugin returned by [New] so callers can request the
// spec out-of-band (e.g. for testing or for serving over a custom route).
type generator struct {
	app *aarv.App
	cfg Config

	once sync.Once
	spec *Spec

	yamlOnceFlag sync.Once
	yamlBytes    []byte
	yamlErr      error
}

// Spec is the in-memory representation of an OpenAPI 3.1 document. It is
// JSON-serializable directly. Fields are tagged with the OpenAPI key names.
type Spec struct {
	OpenAPI    string              `json:"openapi"`
	Info       Info                `json:"info"`
	Servers    []Server            `json:"servers,omitempty"`
	Paths      map[string]PathItem `json:"paths"`
	Components *Components         `json:"components,omitempty"`
}

// Info is the OpenAPI Info object.
type Info struct {
	Title       string   `json:"title"`
	Version     string   `json:"version"`
	Description string   `json:"description,omitempty"`
	Contact     *Contact `json:"contact,omitempty"`
	License     *License `json:"license,omitempty"`
}

// PathItem maps lowercase HTTP method → Operation.
type PathItem map[string]*Operation

// Operation is the OpenAPI Operation object.
type Operation struct {
	Tags        []string            `json:"tags,omitempty"`
	Summary     string              `json:"summary,omitempty"`
	Description string              `json:"description,omitempty"`
	OperationID string              `json:"operationId,omitempty"`
	Deprecated  bool                `json:"deprecated,omitempty"`
	Parameters  []Parameter         `json:"parameters,omitempty"`
	RequestBody *RequestBody        `json:"requestBody,omitempty"`
	Responses   map[string]Response `json:"responses"`
}

// Parameter is the OpenAPI Parameter object (path / query / header).
type Parameter struct {
	Name     string  `json:"name"`
	In       string  `json:"in"` // "path" / "query" / "header"
	Required bool    `json:"required,omitempty"`
	Schema   *Schema `json:"schema,omitempty"`
}

// RequestBody is the OpenAPI Request Body object.
type RequestBody struct {
	Required bool                 `json:"required,omitempty"`
	Content  map[string]MediaType `json:"content"`
}

// Response is the OpenAPI Response object.
type Response struct {
	Description string               `json:"description"`
	Content     map[string]MediaType `json:"content,omitempty"`
}

// MediaType is the OpenAPI Media Type object.
type MediaType struct {
	Schema *Schema `json:"schema,omitempty"`
}

// Components is the OpenAPI Components object.
type Components struct {
	Schemas         map[string]*Schema        `json:"schemas,omitempty"`
	SecuritySchemes map[string]SecurityScheme `json:"securitySchemes,omitempty"`
}

// Plugin is the consumer-facing handle returned by [New]. The Spec method
// returns the cached, deterministic JSON-ready representation; Handler
// returns an http.Handler that emits the spec as application/json.
type Plugin struct {
	gen *generator
}

// New registers an OpenAPI plugin against app. The JSON and YAML endpoints
// are registered immediately at cfg.JSONPath / cfg.YAMLPath (or their
// defaults); the spec itself is generated lazily on the first request and
// cached for the App lifetime.
//
// Returns ErrNilApp when app is nil. Spec building does not return an
// error in the current generator — validation tag mapping degrades by
// ignoring unknown rules, and cycle detection terminates via component
// refs. YAML serialization can fail in pathological encoder scenarios;
// that error surfaces from the YAML endpoint as a 500 (see
// (*generator).yamlOnce).
func New(app *aarv.App, cfg Config) (*Plugin, error) {
	if app == nil {
		return nil, ErrNilApp
	}
	cfg = applyDefaults(cfg)
	gen := &generator{app: app, cfg: cfg}
	if !cfg.DisableJSONEndpoint {
		app.Get(cfg.JSONPath, func(c *aarv.Context) error {
			return c.JSON(http.StatusOK, gen.specOnce())
		})
	}
	if !cfg.DisableYAMLEndpoint {
		app.Get(cfg.YAMLPath, func(c *aarv.Context) error {
			data, err := gen.yamlOnce()
			if err != nil {
				return err
			}
			c.Response().Header().Set("Content-Type", "application/yaml; charset=utf-8")
			_, werr := c.Response().Write(data)
			return werr
		})
	}
	return &Plugin{gen: gen}, nil
}

// Spec returns the cached OpenAPI document, building it if this is the
// first call. Safe to call concurrently.
func (p *Plugin) Spec() *Spec {
	return p.gen.specOnce()
}

// Handler returns an http.Handler that emits the cached spec as
// application/json. Useful when the caller has DisableJSONEndpoint set
// and wants to mount the spec themselves under custom middleware.
func (p *Plugin) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(p.gen.specOnce())
	})
}

func applyDefaults(cfg Config) Config {
	if cfg.Title == "" {
		cfg.Title = "Aarv API"
	}
	if cfg.Version == "" {
		cfg.Version = "0.0.0"
	}
	if cfg.JSONPath == "" {
		cfg.JSONPath = DefaultJSONPath
	}
	if cfg.YAMLPath == "" {
		cfg.YAMLPath = DefaultYAMLPath
	}
	if cfg.Include == nil {
		if cfg.Exclude == nil {
			cfg.Exclude = append([]string(nil), DefaultExclude...)
		}
		// Make sure the spec endpoints — even when configured to a
		// non-default path — do not document themselves. Skip the merge
		// when the corresponding endpoint is suppressed (the user is
		// likely serving the spec from outside the App).
		if !cfg.DisableJSONEndpoint {
			cfg.Exclude = appendUnique(cfg.Exclude, cfg.JSONPath)
		}
		if !cfg.DisableYAMLEndpoint {
			cfg.Exclude = appendUnique(cfg.Exclude, cfg.YAMLPath)
		}
	}
	return cfg
}

func appendUnique(slice []string, s string) []string {
	for _, v := range slice {
		if v == s {
			return slice
		}
	}
	return append(slice, s)
}

// specOnce builds the spec on first call and returns the cached result on
// subsequent calls. The build itself is infallible; YAML serialization
// (which CAN fail in pathological encoder scenarios) is reported through
// yamlOnce.
func (g *generator) specOnce() *Spec {
	g.once.Do(func() { g.spec = g.build() })
	return g.spec
}

// yamlOnce serializes the spec to YAML on first call and caches the
// bytes for subsequent calls. Round-trips through JSON for stable
// key ordering.
func (g *generator) yamlOnce() ([]byte, error) {
	g.yamlOnceFlag.Do(func() {
		spec := g.specOnce()
		// json.Marshal cannot fail on *Spec: every Schema field is
		// either a primitive, slice, map[string]X, or another *Schema.
		// No funcs/channels/complex values reach here.
		jsonBytes, _ := json.Marshal(spec)
		g.yamlBytes, g.yamlErr = jsonToYAML(jsonBytes)
	})
	return g.yamlBytes, g.yamlErr
}

// build assembles the spec from the App's current routes. Called exactly
// once per generator (under sync.Once). Schemas are routed through a
// per-spec builder so named struct types resolve to a single component
// entry shared across all routes that mention them, and recursive types
// terminate via the component placeholder pattern.
func (g *generator) build() *Spec {
	routes := g.app.Routes()
	filtered := filterRoutes(routes, g.cfg)

	// Stable order: sort by Pattern, then Method. encoding/json sorts
	// string-keyed maps, so the final byte stream is deterministic.
	sort.SliceStable(filtered, func(i, j int) bool {
		if filtered[i].Pattern != filtered[j].Pattern {
			return filtered[i].Pattern < filtered[j].Pattern
		}
		return filtered[i].Method < filtered[j].Method
	})

	// nil logger → slog.Default. App's logger is unexported and we don't
	// want to depend on the framework adding an accessor just for this.
	builder := newSchemaBuilder(nil)

	paths := make(map[string]PathItem)
	for _, r := range filtered {
		path := normalizePath(r.Pattern)
		item, ok := paths[path]
		if !ok {
			item = make(PathItem)
			paths[path] = item
		}
		item[strings.ToLower(r.Method)] = buildOperation(r, builder, g.app.CodecContentType())
	}

	spec := &Spec{
		OpenAPI: "3.1.0",
		Info: Info{
			Title:       g.cfg.Title,
			Version:     g.cfg.Version,
			Description: g.cfg.Description,
			Contact:     g.cfg.Contact,
			License:     g.cfg.License,
		},
		Servers: g.cfg.Servers,
		Paths:   paths,
	}

	if len(builder.components) > 0 || len(g.cfg.SecuritySchemes) > 0 {
		spec.Components = &Components{}
		if len(builder.components) > 0 {
			spec.Components.Schemas = builder.components
		}
		if len(g.cfg.SecuritySchemes) > 0 {
			spec.Components.SecuritySchemes = g.cfg.SecuritySchemes
		}
	}
	return spec
}

// filterRoutes applies the Include/Tags/Exclude rules. Include, when
// set, is the sole filter (Tags and Exclude are ignored). When Include
// is nil: Tags is applied first (when set) — only routes carrying at
// least one of the listed tags survive; Exclude is then applied to the
// remainder as a path-prefix match.
func filterRoutes(routes []aarv.RouteInfo, cfg Config) []aarv.RouteInfo {
	out := routes[:0:0]
	if cfg.Include != nil {
		for _, r := range routes {
			if cfg.Include(r) {
				out = append(out, r)
			}
		}
		return out
	}
	tagFilter := buildTagSet(cfg.Tags)
	for _, r := range routes {
		if tagFilter != nil && !routeHasAnyTag(r, tagFilter) {
			continue
		}
		if matchesAnyPrefix(r.Pattern, cfg.Exclude) {
			continue
		}
		out = append(out, r)
	}
	return out
}

// buildTagSet returns a lookup set for the configured tag filter, or
// nil when no tag filter is configured. nil signals "do not filter by
// tag" so a missing Tags field passes everything through.
func buildTagSet(tags []string) map[string]struct{} {
	if len(tags) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(tags))
	for _, t := range tags {
		if t == "" {
			continue
		}
		set[t] = struct{}{}
	}
	if len(set) == 0 {
		return nil
	}
	return set
}

// routeHasAnyTag reports whether r carries at least one tag from set.
// Routes with no tags never match — empty intersects nothing.
func routeHasAnyTag(r aarv.RouteInfo, set map[string]struct{}) bool {
	for _, t := range r.Tags {
		if _, ok := set[t]; ok {
			return true
		}
	}
	return false
}

func matchesAnyPrefix(pattern string, prefixes []string) bool {
	for _, p := range prefixes {
		if p == "" {
			continue
		}
		if strings.HasPrefix(pattern, p) {
			return true
		}
	}
	return false
}

// normalizePath converts aarv path patterns to OpenAPI 3.1 path strings.
// The only aarv-specific syntax that needs translation is the catch-all
// segment "{name...}" — OpenAPI has no native catch-all concept, so we
// emit "{name}" and let the spec consumer treat it as an opaque string
// path parameter. Documented here and in docs/openapi.md.
func normalizePath(pattern string) string {
	return catchAllRe.ReplaceAllString(pattern, "{$1}")
}

var (
	pathParamRe = regexp.MustCompile(`\{([^}/]+)\}`)
	catchAllRe  = regexp.MustCompile(`\{([^}/]+)\.\.\.\}`)
)

// buildOperation builds an OpenAPI Operation from a RouteInfo, routing
// schema lookups through builder so named struct types resolve to shared
// component refs. responseCT is the App's codec content type — used as
// the default media type for the response body so a non-JSON codec (e.g.
// YAML) flows into the spec without per-route overrides. "application/json"
// is the defensive fallback when responseCT is empty.
func buildOperation(r aarv.RouteInfo, builder *schemaBuilder, responseCT string) *Operation {
	if responseCT == "" {
		responseCT = "application/json"
	}
	op := &Operation{
		Tags:        r.Tags,
		Summary:     r.Summary,
		Description: r.Description,
		OperationID: r.OperationID,
		Deprecated:  r.Deprecated,
		Parameters:  pathParameters(r.Pattern),
	}

	if r.RequestType != nil {
		schema := builder.schemaFor(r.RequestType)
		ct := r.RequestContentType
		if ct == "" {
			ct = "application/json"
		}
		op.RequestBody = &RequestBody{
			Required: true,
			Content:  map[string]MediaType{ct: {Schema: schema}},
		}
	}

	op.Responses = buildResponses(r, builder, responseCT)
	return op
}

// pathParameters extracts {name} (and {name...} catch-all) segments and
// emits a path Parameter for each one. All path parameters are required
// by OpenAPI definition. Catch-all parameters drop the trailing "..."
// from the parameter name so consumers see "path" rather than "path..."
// (which is not a valid OpenAPI parameter name shape).
func pathParameters(pattern string) []Parameter {
	matches := pathParamRe.FindAllStringSubmatch(pattern, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(matches))
	out := make([]Parameter, 0, len(matches))
	for _, m := range matches {
		name := strings.TrimSuffix(m[1], "...")
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, Parameter{
			Name:     name,
			In:       "path",
			Required: true,
			Schema:   &Schema{Type: "string"},
		})
	}
	return out
}

// buildResponses constructs the responses map. Documented codes from
// WithResponse are emitted verbatim; the response with a body schema
// is the lowest 2xx code present, defaulting to "200" if no 2xx code
// was documented and a ResponseType is set. The response body schema
// is resolved through builder so it can share components, and the media
// type is responseCT (typically the App's codec content type) so a
// non-JSON codec (e.g. YAML) is reflected in the spec.
func buildResponses(r aarv.RouteInfo, builder *schemaBuilder, responseCT string) map[string]Response {
	resp := make(map[string]Response)
	for code, desc := range r.Responses {
		resp[strconv.Itoa(code)] = Response{Description: desc}
	}

	if r.ResponseType != nil {
		bodyCode := pickBodyCode(r.Responses)
		existing, ok := resp[bodyCode]
		if !ok {
			existing = Response{Description: "OK"}
		}
		existing.Content = map[string]MediaType{
			responseCT: {Schema: builder.schemaFor(r.ResponseType)},
		}
		resp[bodyCode] = existing
	}

	if len(resp) == 0 {
		resp["200"] = Response{Description: "OK"}
	}
	return resp
}

// pickBodyCode returns the code under which the response body schema is
// emitted. Picks the lowest 2xx code from WithResponse if any; otherwise
// "200". Lets callers document additional non-success codes without
// confusing the schema attachment.
func pickBodyCode(responses map[int]string) string {
	best := -1
	for code := range responses {
		if code >= 200 && code < 300 {
			if best < 0 || code < best {
				best = code
			}
		}
	}
	if best < 0 {
		return "200"
	}
	return strconv.Itoa(best)
}
