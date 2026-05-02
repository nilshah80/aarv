package openapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// Schema is a subset of the OpenAPI 3.1 / JSON Schema 2020-12 vocabulary.
// Pointer fields (*float64, *int) preserve "0 means unset" by leaving them
// nil when no constraint applies.
//
// MarshalJSON translates Nullable into the JSON Schema 2020-12 union-type
// form (type: ["X", "null"] or a oneOf wrapping for $ref). The OpenAPI 3.0
// "nullable: true" keyword is NOT valid in 3.1 and is never emitted.
type Schema struct {
	Ref                  string             `json:"$ref,omitempty"`
	Type                 string             `json:"type,omitempty"`
	Format               string             `json:"format,omitempty"`
	Properties           map[string]*Schema `json:"properties,omitempty"`
	Required             []string           `json:"required,omitempty"`
	Items                *Schema            `json:"items,omitempty"`
	AdditionalProperties *Schema            `json:"additionalProperties,omitempty"`
	// Nullable is the in-memory marker; the JSON output uses
	// JSON-Schema-2020-12 semantics (see MarshalJSON). Not emitted as
	// "nullable: true" because that keyword is a 3.0-ism rejected by
	// strict 3.1 validators.
	Nullable bool `json:"-"`

	// Numeric constraints (12.6b validation tag mapping).
	Minimum          *float64 `json:"minimum,omitempty"`
	Maximum          *float64 `json:"maximum,omitempty"`
	ExclusiveMinimum *float64 `json:"exclusiveMinimum,omitempty"`
	ExclusiveMaximum *float64 `json:"exclusiveMaximum,omitempty"`

	// String constraints.
	MinLength *int   `json:"minLength,omitempty"`
	MaxLength *int   `json:"maxLength,omitempty"`
	Pattern   string `json:"pattern,omitempty"`

	// Array / object constraints.
	MinItems      *int `json:"minItems,omitempty"`
	MaxItems      *int `json:"maxItems,omitempty"`
	MinProperties *int `json:"minProperties,omitempty"`
	MaxProperties *int `json:"maxProperties,omitempty"`
	UniqueItems   bool `json:"uniqueItems,omitempty"`

	// Enum captures validate:"oneof=a b c". Encoded as a JSON array.
	Enum []any `json:"enum,omitempty"`
}

// schemaJSON is the on-the-wire form of Schema. Type is `any` so it can
// carry either a string ("integer"), a string slice (["integer","null"]
// for nullable primitives), or be omitted. Field set must stay in lockstep
// with Schema.
type schemaJSON struct {
	Ref                  string             `json:"$ref,omitempty"`
	Type                 any                `json:"type,omitempty"`
	Format               string             `json:"format,omitempty"`
	Properties           map[string]*Schema `json:"properties,omitempty"`
	Required             []string           `json:"required,omitempty"`
	Items                *Schema            `json:"items,omitempty"`
	AdditionalProperties *Schema            `json:"additionalProperties,omitempty"`
	Minimum              *float64           `json:"minimum,omitempty"`
	Maximum              *float64           `json:"maximum,omitempty"`
	ExclusiveMinimum     *float64           `json:"exclusiveMinimum,omitempty"`
	ExclusiveMaximum     *float64           `json:"exclusiveMaximum,omitempty"`
	MinLength            *int               `json:"minLength,omitempty"`
	MaxLength            *int               `json:"maxLength,omitempty"`
	Pattern              string             `json:"pattern,omitempty"`
	MinItems             *int               `json:"minItems,omitempty"`
	MaxItems             *int               `json:"maxItems,omitempty"`
	MinProperties        *int               `json:"minProperties,omitempty"`
	MaxProperties        *int               `json:"maxProperties,omitempty"`
	UniqueItems          bool               `json:"uniqueItems,omitempty"`
	Enum                 []any              `json:"enum,omitempty"`
}

// MarshalJSON emits OpenAPI 3.1 / JSON Schema 2020-12 compliant JSON.
// Nullability is rendered as a union type (type: ["X","null"]) for typed
// schemas, "type: null" for typeless schemas (interface{} fields), or a
// oneOf wrapping for $ref-bearing schemas (since "nullable: true" is not
// valid in 3.1, and combining $ref with siblings has historically been
// inconsistently supported by tooling — oneOf is the unambiguous form).
func (s Schema) MarshalJSON() ([]byte, error) {
	if s.Nullable && s.Ref != "" {
		return json.Marshal(map[string]any{
			"oneOf": []map[string]string{
				{"$ref": s.Ref},
				{"type": "null"},
			},
		})
	}

	out := schemaJSON{
		Ref:                  s.Ref,
		Format:               s.Format,
		Properties:           s.Properties,
		Required:             s.Required,
		Items:                s.Items,
		AdditionalProperties: s.AdditionalProperties,
		Minimum:              s.Minimum,
		Maximum:              s.Maximum,
		ExclusiveMinimum:     s.ExclusiveMinimum,
		ExclusiveMaximum:     s.ExclusiveMaximum,
		MinLength:            s.MinLength,
		MaxLength:            s.MaxLength,
		Pattern:              s.Pattern,
		MinItems:             s.MinItems,
		MaxItems:             s.MaxItems,
		MinProperties:        s.MinProperties,
		MaxProperties:        s.MaxProperties,
		UniqueItems:          s.UniqueItems,
		Enum:                 s.Enum,
	}
	switch {
	case s.Nullable && s.Type != "":
		out.Type = []string{s.Type, "null"}
	case s.Nullable:
		out.Type = "null"
	case s.Type != "":
		out.Type = s.Type
	}
	return json.Marshal(out)
}

// schemaBuilder is the per-spec walker that emits inline schemas for
// primitives / containers and component refs for named struct types.
// Cycle detection uses the placeholder pattern: a type's component entry
// is registered BEFORE its fields are walked, so any recursive reference
// resolves to a $ref pointing at the in-progress component.
type schemaBuilder struct {
	types      map[reflect.Type]string // reflect.Type → component name
	components map[string]*Schema
	nameUsed   map[string]reflect.Type // for collision detection
	logger     *slog.Logger
}

func newSchemaBuilder(logger *slog.Logger) *schemaBuilder {
	if logger == nil {
		logger = slog.Default()
	}
	return &schemaBuilder{
		types:      make(map[reflect.Type]string),
		components: make(map[string]*Schema),
		nameUsed:   make(map[string]reflect.Type),
		logger:     logger,
	}
}

// timeType is special-cased to render as string/date-time, matching the
// behavior the encoding/json marshaler produces for time.Time.
var timeType = reflect.TypeFor[time.Time]()

// schemaFor returns an inline Schema (for primitives / containers /
// anonymous structs) or a $ref Schema (for named structs).
func (b *schemaBuilder) schemaFor(t reflect.Type) *Schema {
	if t == nil {
		return &Schema{}
	}

	nullable := false
	for t.Kind() == reflect.Ptr {
		nullable = true
		t = t.Elem()
	}

	if t == timeType {
		return &Schema{Type: "string", Format: "date-time", Nullable: nullable}
	}

	switch t.Kind() {
	case reflect.Bool:
		return &Schema{Type: "boolean", Nullable: nullable}

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32:
		return &Schema{Type: "integer", Format: "int32", Nullable: nullable}

	case reflect.Int64, reflect.Uint64:
		return &Schema{Type: "integer", Format: "int64", Nullable: nullable}

	case reflect.Float32:
		return &Schema{Type: "number", Format: "float", Nullable: nullable}

	case reflect.Float64:
		return &Schema{Type: "number", Format: "double", Nullable: nullable}

	case reflect.String:
		return &Schema{Type: "string", Nullable: nullable}

	case reflect.Slice, reflect.Array:
		// []byte → base64 string, matching encoding/json behavior.
		if t.Elem().Kind() == reflect.Uint8 {
			return &Schema{Type: "string", Format: "byte", Nullable: nullable}
		}
		return &Schema{Type: "array", Items: b.schemaFor(t.Elem()), Nullable: nullable}

	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return &Schema{Type: "object", Nullable: nullable}
		}
		return &Schema{
			Type:                 "object",
			AdditionalProperties: b.schemaFor(t.Elem()),
			Nullable:             nullable,
		}

	case reflect.Struct:
		// Anonymous structs (no name) are inlined. Named structs go
		// through the component-ref path so cycles terminate.
		if t.Name() == "" {
			s := &Schema{Type: "object", Properties: map[string]*Schema{}, Nullable: nullable}
			b.collectFields(t, s)
			return s
		}
		ref := b.componentRef(t)
		// Schema.MarshalJSON translates Nullable + Ref into the
		// {"oneOf": [{$ref}, {"type":"null"}]} form mandated by
		// OpenAPI 3.1; the in-memory representation stays compact.
		return &Schema{Ref: ref, Nullable: nullable}

	case reflect.Interface:
		return &Schema{Nullable: nullable}

	default:
		return &Schema{}
	}
}

// componentRef registers t as a component (if not already) and returns
// its "#/components/schemas/{Name}" ref. Cycle-safe: the component entry
// is staked out BEFORE field walking, so a recursive type encountered
// during walk resolves to the same ref.
func (b *schemaBuilder) componentRef(t reflect.Type) string {
	if name, ok := b.types[t]; ok {
		return "#/components/schemas/" + name
	}
	name := b.assignName(t)
	b.types[t] = name
	// Stake the placeholder so recursion terminates.
	b.components[name] = nil

	s := &Schema{Type: "object", Properties: map[string]*Schema{}}
	b.collectFields(t, s)
	b.components[name] = s
	return "#/components/schemas/" + name
}

// assignName picks a component name for t. The first occurrence of a name
// gets the bare TypeName; collisions append the sanitized package path so
// distinct types from different packages do not overwrite each other.
//
// Anonymous types (which today are routed away from this path; future
// recursive-anonymous-named structures could land here) get a stable
// hash-derived name.
func (b *schemaBuilder) assignName(t reflect.Type) string {
	bare := t.Name()
	if bare == "" {
		// Defensive — current callers route anonymous structs to inline
		// schemas. If a future caller asks for a component name for an
		// anonymous type, derive a stable one from the field signature.
		sum := sha256.Sum256([]byte(t.String()))
		bare = "anon_" + hex.EncodeToString(sum[:6])
	}

	if existing, taken := b.nameUsed[bare]; !taken || existing == t {
		b.nameUsed[bare] = t
		return bare
	}

	// Collision with a different type: prefix with sanitized package
	// path. Replace any non-[A-Za-z0-9_] character with "_", collapse
	// runs, trim leading/trailing underscores, then prepend.
	prefix := sanitizeName(t.PkgPath())
	qualified := prefix + "_" + bare
	for c := 1; ; c++ {
		// Defensive numeric suffix in case the same package contains
		// two types with the same simple name (rare, but observed in
		// generated code).
		candidate := qualified
		if c > 1 {
			candidate = qualified + "_" + strconv.Itoa(c)
		}
		if existing, taken := b.nameUsed[candidate]; !taken || existing == t {
			b.nameUsed[candidate] = t
			return candidate
		}
	}
}

// sanitizeName replaces non-[A-Za-z0-9_] runes with "_", collapses runs
// of "_", and trims leading/trailing "_".
func sanitizeName(s string) string {
	if s == "" {
		return "anon"
	}
	var b strings.Builder
	prevUnderscore := false
	for _, r := range s {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
			prevUnderscore = false
			continue
		}
		if !prevUnderscore {
			b.WriteByte('_')
			prevUnderscore = true
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "anon"
	}
	return out
}

// collectFields walks a struct's exported fields, applying json visibility
// and validate-tag constraints, and updates s.Properties / s.Required.
// Embedded anonymous structs are flattened.
func (b *schemaBuilder) collectFields(t reflect.Type, s *Schema) {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}

		jsonTag := f.Tag.Get("json")
		if jsonTag == "-" {
			continue
		}

		name, opts := parseJSONTag(jsonTag)
		omitEmpty := optionPresent(opts, "omitempty")

		if f.Anonymous && name == "" && f.Type.Kind() == reflect.Struct {
			b.collectFields(f.Type, s)
			continue
		}
		if name == "" {
			name = f.Name
		}

		fieldSchema := b.schemaFor(f.Type)
		validateTag := f.Tag.Get("validate")
		isRequired, _ := applyValidateTag(fieldSchema, validateTag, f.Type, b.logger)

		s.Properties[name] = fieldSchema

		// Required-field precedence (12.6b): explicit validate:"required"
		// wins. Otherwise fall back to "required unless omitempty or
		// pointer".
		if isRequired || (!omitEmpty && f.Type.Kind() != reflect.Ptr) {
			s.Required = append(s.Required, name)
		}
	}
	if len(s.Required) > 0 {
		sortStrings(s.Required)
	}
}

// applyValidateTag interprets aarv's validate:"" tag and writes
// constraints onto schema. Returns whether "required" was found and
// (for symmetry / future extension) whether at least one rule was
// recognized.
//
// Unrecognized rules are logged at Debug and ignored — keeps the spec
// generator resilient to custom validators registered via aarv.RegisterRule.
// validateCtx carries the per-field kind hints that the rule appliers
// need to disambiguate length-vs-numeric semantics.
type validateCtx struct {
	stringKind    bool
	containerKind bool
}

// validateRuleAppliers maps a recognized validate-tag rule name to its
// schema-mutation function. Splitting the original switch into a table
// of small functions keeps each per-rule applier well under the
// gocyclo threshold so the OpenAPI plugin scores cleanly on Go Report
// Card without sacrificing the per-tag readability.
//
// "required" is handled inline by applyValidateTag because it mutates
// the parent struct's required slice, not the field's own schema.
var validateRuleAppliers = map[string]func(s *Schema, param string, ctx validateCtx){
	"min":    applyMinRule,
	"max":    applyMaxRule,
	"gte":    applyGteRule,
	"lte":    applyLteRule,
	"gt":     applyGtRule,
	"lt":     applyLtRule,
	"len":    applyLenRule,
	"oneof":  applyOneOfRule,
	"email":  applyFormatRule("email"),
	"url":    applyFormatRule("uri"),
	"uuid":   applyFormatRule("uuid"),
	"regex":  applyRegexRule,
	"unique": applyUniqueRule,
}

func applyMinRule(s *Schema, param string, ctx validateCtx) {
	n, ok := parseRuleNum(param)
	if !ok {
		return
	}
	switch {
	case ctx.stringKind:
		s.MinLength = intPtr(int(n))
	case ctx.containerKind:
		s.MinItems = intPtr(int(n))
	default:
		s.Minimum = float64Ptr(n)
	}
}

func applyMaxRule(s *Schema, param string, ctx validateCtx) {
	n, ok := parseRuleNum(param)
	if !ok {
		return
	}
	switch {
	case ctx.stringKind:
		s.MaxLength = intPtr(int(n))
	case ctx.containerKind:
		s.MaxItems = intPtr(int(n))
	default:
		s.Maximum = float64Ptr(n)
	}
}

func applyGteRule(s *Schema, param string, _ validateCtx) {
	if n, ok := parseRuleNum(param); ok {
		s.Minimum = float64Ptr(n)
	}
}

func applyLteRule(s *Schema, param string, _ validateCtx) {
	if n, ok := parseRuleNum(param); ok {
		s.Maximum = float64Ptr(n)
	}
}

func applyGtRule(s *Schema, param string, _ validateCtx) {
	if n, ok := parseRuleNum(param); ok {
		s.ExclusiveMinimum = float64Ptr(n)
	}
}

func applyLtRule(s *Schema, param string, _ validateCtx) {
	if n, ok := parseRuleNum(param); ok {
		s.ExclusiveMaximum = float64Ptr(n)
	}
}

func applyLenRule(s *Schema, param string, ctx validateCtx) {
	n, ok := parseRuleNum(param)
	if !ok {
		return
	}
	switch {
	case ctx.stringKind:
		s.MinLength = intPtr(int(n))
		s.MaxLength = intPtr(int(n))
	case ctx.containerKind:
		s.MinItems = intPtr(int(n))
		s.MaxItems = intPtr(int(n))
	}
}

func applyOneOfRule(s *Schema, param string, _ validateCtx) {
	values := strings.Fields(param)
	if len(values) == 0 {
		return
	}
	s.Enum = make([]any, len(values))
	for i, v := range values {
		s.Enum[i] = v
	}
}

// applyFormatRule returns an applier that sets schema.Format to the
// given OpenAPI format string. Used for the email/url/uuid rules whose
// only effect is the format keyword.
func applyFormatRule(format string) func(s *Schema, param string, ctx validateCtx) {
	return func(s *Schema, _ string, _ validateCtx) {
		s.Format = format
	}
}

func applyRegexRule(s *Schema, param string, _ validateCtx) {
	s.Pattern = param
}

func applyUniqueRule(s *Schema, _ string, _ validateCtx) {
	s.UniqueItems = true
}

func applyValidateTag(schema *Schema, tag string, fieldType reflect.Type, logger *slog.Logger) (required, anyRecognized bool) {
	if tag == "" || tag == "-" {
		return false, false
	}
	if logger == nil {
		logger = slog.Default()
	}
	ctx := validateCtx{
		stringKind:    isStringKind(fieldType),
		containerKind: isContainerKind(fieldType),
	}
	for _, raw := range strings.Split(tag, ",") {
		rule := strings.TrimSpace(raw)
		if rule == "" {
			continue
		}
		name, param := splitRule(rule)
		if name == "required" {
			required, anyRecognized = true, true
			continue
		}
		applier, ok := validateRuleAppliers[name]
		if !ok {
			logger.Debug("openapi: unknown validate rule; ignored", "rule", name)
			continue
		}
		applier(schema, param, ctx)
		anyRecognized = true
	}
	return required, anyRecognized
}

func splitRule(rule string) (name, param string) {
	if i := strings.IndexByte(rule, '='); i >= 0 {
		return rule[:i], rule[i+1:]
	}
	return rule, ""
}

func parseRuleNum(s string) (float64, bool) {
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

func isStringKind(t reflect.Type) bool {
	for t != nil && t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return t != nil && t.Kind() == reflect.String
}

func isContainerKind(t reflect.Type) bool {
	for t != nil && t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t == nil {
		return false
	}
	switch t.Kind() {
	case reflect.Slice, reflect.Array, reflect.Map:
		return true
	}
	return false
}

func intPtr(v int) *int             { return &v }
func float64Ptr(v float64) *float64 { return &v }

func parseJSONTag(tag string) (name string, opts []string) {
	if tag == "" {
		return "", nil
	}
	parts := strings.Split(tag, ",")
	return parts[0], parts[1:]
}

func optionPresent(opts []string, want string) bool {
	for _, o := range opts {
		if o == want {
			return true
		}
	}
	return false
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// SchemaFromType is a one-off helper for callers that want a standalone
// inline schema (no containing spec / no shared components). For named
// struct types, schemaFor would normally return a $ref into the builder's
// components map; here we resolve the ref into its inline body so callers
// see a self-contained schema.
//
// Recursive named struct types are NOT supported through this helper —
// they would loop trying to inline themselves. Use Plugin.Spec() for
// recursive types so the per-spec builder can hand out shared component
// refs.
//
// Production code in this package routes through schemaBuilder directly.
func SchemaFromType(t reflect.Type) *Schema {
	b := newSchemaBuilder(nil)
	s := b.schemaFor(t)
	if name, ok := refToName(s.Ref); ok {
		if comp := b.components[name]; comp != nil {
			return comp
		}
	}
	return s
}

const componentRefPrefix = "#/components/schemas/"

func refToName(ref string) (string, bool) {
	if !strings.HasPrefix(ref, componentRefPrefix) {
		return "", false
	}
	return ref[len(componentRefPrefix):], true
}
