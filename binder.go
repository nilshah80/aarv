package aarv

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
)

// CustomBinder is an interface for types that can bind themselves from the request context.
// If a type implements this interface, BindFromContext is called instead of the default binding.
type CustomBinder interface {
	BindFromContext(c *Context) error
}

// ParamParser is an interface for types that can parse themselves from a string parameter.
// This allows custom types to be used as path parameters, query params, etc.
type ParamParser interface {
	ParseParam(value string) error
}

// bindSource identifies where a field value comes from.
type bindSource int

const (
	sourceParam  bindSource = iota // path parameter
	sourceQuery                    // query string
	sourceHeader                   // request header
	sourceCookie                   // cookie
	sourceForm                     // form data
)

// fieldBinding describes how to populate a single struct field from the request.
type fieldBinding struct {
	source         bindSource
	name           string // tag value (e.g., "userId")
	fieldIndex     []int  // reflect field index path
	directIndex    int
	hasDirectIndex bool
	kind           reflect.Kind
	fieldType      reflect.Type
	setter         fieldSetter
	defaultValue   string
	hasDefault     bool
	hasParamParser bool // true if field type implements ParamParser
}

type fieldSetter func(field reflect.Value, raw string) error

// paramParserType is the reflect.Type for the ParamParser interface.
var paramParserType = reflect.TypeOf((*ParamParser)(nil)).Elem()

var (
	binderCache      sync.Map
	queryBinderCache sync.Map
	formBinderCache  sync.Map
)

// fileFieldBinding describes how to populate a file-typed struct field.
type fileFieldBinding struct {
	name       string // tag value (form field name)
	fieldIndex []int  // reflect field index path
	isSlice    bool   // true for []*UploadedFile, false for *UploadedFile
}

// uploadedFileType and uploadedFileSliceType are used for registration-time
// type checking of file-tagged fields.
var (
	uploadedFileType      = reflect.TypeOf((*UploadedFile)(nil))
	uploadedFileSliceType = reflect.TypeOf(([]*UploadedFile)(nil))
)

// structBinder holds pre-computed binding info for a struct type.
type structBinder struct {
	fields        []fieldBinding
	fileFields    []fileFieldBinding
	defaults      []fieldBinding
	hasDefaults   bool
	needBody      bool // true if any field uses json tag (body binding)
	needForm      bool // true if any field uses form tag
	needMultipart bool // true if any field uses file tag
}

// buildStructBinder inspects a struct type at registration time and returns a binder.
func buildStructBinder(t reflect.Type) *structBinder {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}

	if cached, ok := binderCache.Load(t); ok {
		return cached.(*structBinder)
	}

	sb := &structBinder{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}

		// Check for embedded struct
		if f.Anonymous && f.Type.Kind() == reflect.Struct {
			embedded := buildStructBinder(f.Type)
			if embedded != nil {
				for _, ef := range embedded.fields {
					ef.fieldIndex = append([]int{i}, ef.fieldIndex...)
					ef.hasDirectIndex = false
					sb.fields = append(sb.fields, ef)
				}
				for _, ef := range embedded.defaults {
					ef.fieldIndex = append([]int{i}, ef.fieldIndex...)
					ef.hasDirectIndex = false
					sb.defaults = append(sb.defaults, ef)
				}
				if embedded.hasDefaults {
					sb.hasDefaults = true
				}
				if embedded.needBody {
					sb.needBody = true
				}
				if embedded.needForm {
					sb.needForm = true
				}
				for _, ef := range embedded.fileFields {
					ef.fieldIndex = append([]int{i}, ef.fieldIndex...)
					sb.fileFields = append(sb.fileFields, ef)
				}
				if embedded.needMultipart {
					sb.needMultipart = true
				}
			}
			continue
		}

		fb := fieldBinding{
			fieldIndex:     f.Index,
			kind:           f.Type.Kind(),
			fieldType:      f.Type,
			setter:         buildFieldSetter(f.Type),
			hasDirectIndex: len(f.Index) == 1,
		}
		if fb.hasDirectIndex {
			fb.directIndex = f.Index[0]
		}

		// Check if the field type implements ParamParser
		if f.Type.Implements(paramParserType) || reflect.PointerTo(f.Type).Implements(paramParserType) {
			fb.hasParamParser = true
		}

		if tag := f.Tag.Get("default"); tag != "" {
			fb.defaultValue = tag
			fb.hasDefault = true
			sb.hasDefaults = true
		}

		if tag := f.Tag.Get("param"); tag != "" {
			fb.source = sourceParam
			fb.name = tag
			sb.fields = append(sb.fields, fb)
		} else if tag := f.Tag.Get("query"); tag != "" {
			fb.source = sourceQuery
			fb.name = tag
			sb.fields = append(sb.fields, fb)
		} else if tag := f.Tag.Get("header"); tag != "" {
			fb.source = sourceHeader
			fb.name = tag
			sb.fields = append(sb.fields, fb)
		} else if tag := f.Tag.Get("cookie"); tag != "" {
			fb.source = sourceCookie
			fb.name = tag
			sb.fields = append(sb.fields, fb)
		} else if tag := f.Tag.Get("form"); tag != "" {
			fb.source = sourceForm
			fb.name = tag
			sb.needForm = true
			sb.fields = append(sb.fields, fb)
		} else if tag := f.Tag.Get("file"); tag != "" {
			isSlice := false
			switch f.Type {
			case uploadedFileType:
				// *UploadedFile — single file
			case uploadedFileSliceType:
				isSlice = true
			default:
				panic(fmt.Sprintf("aarv: file tag on field %q requires *UploadedFile or []*UploadedFile, got %s", f.Name, f.Type))
			}
			sb.fileFields = append(sb.fileFields, fileFieldBinding{
				name:       tag,
				fieldIndex: f.Index,
				isSlice:    isSlice,
			})
			sb.needMultipart = true
		}

		if fb.hasDefault {
			sb.defaults = append(sb.defaults, fb)
		}

		if tag := f.Tag.Get("json"); tag != "" && tag != "-" {
			sb.needBody = true
		}
	}

	binderCache.Store(t, sb)
	return sb
}

type simpleFieldBinding struct {
	name       string
	fieldIndex int
	setter     fieldSetter
	defaultVal string
	hasDefault bool
}

type simpleBinder struct {
	fields []simpleFieldBinding
}

func buildQueryBinder(t reflect.Type) *simpleBinder {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	if cached, ok := queryBinderCache.Load(t); ok {
		return cached.(*simpleBinder)
	}

	sb := &simpleBinder{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		tag := f.Tag.Get("query")
		if tag == "" {
			continue
		}
		sb.fields = append(sb.fields, simpleFieldBinding{
			name:       tag,
			fieldIndex: i,
			setter:     buildFieldSetter(f.Type),
			defaultVal: f.Tag.Get("default"),
			hasDefault: f.Tag.Get("default") != "",
		})
	}
	queryBinderCache.Store(t, sb)
	return sb
}

func buildFormBinder(t reflect.Type) *simpleBinder {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	if cached, ok := formBinderCache.Load(t); ok {
		return cached.(*simpleBinder)
	}

	sb := &simpleBinder{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		tag := f.Tag.Get("form")
		if tag == "" {
			tag = f.Tag.Get("json")
		}
		if tag == "" || tag == "-" {
			continue
		}
		if idx := strings.IndexByte(tag, ','); idx >= 0 {
			tag = tag[:idx]
		}
		if tag == "" {
			continue
		}
		sb.fields = append(sb.fields, simpleFieldBinding{
			name:       tag,
			fieldIndex: i,
			setter:     buildFieldSetter(f.Type),
		})
	}
	formBinderCache.Store(t, sb)
	return sb
}

// bind populates the struct from the request context.
func (sb *structBinder) bind(c *Context, dest any) error {
	// Check if dest implements CustomBinder
	if cb, ok := dest.(CustomBinder); ok {
		return cb.BindFromContext(c)
	}

	v := reflect.ValueOf(dest)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}

	// Parse multipart early if any field needs it — this also populates
	// r.Form with text field values from the multipart body, so form-tagged
	// fields work correctly alongside file-tagged fields.
	if sb.needMultipart {
		if err := c.parseMultipartIfNeeded(); err != nil {
			return &BindError{Err: err, Source: "file"}
		}
	}

	// Parse form once before the loop if any field needs it.
	// net/http caches the result, but calling it per-field still acquires
	// an internal mutex each time.
	var formParsed bool
	var queryValues url.Values
	var headers http.Header
	var formValues url.Values
	if sb.needForm {
		formParsed = c.req.ParseForm() == nil
		if formParsed {
			formValues = c.req.Form
		}
	}
	queryValues = c.queryValues()
	headers = c.req.Header

	for _, fb := range sb.fields {
		var raw string
		var found bool

		switch fb.source {
		case sourceParam:
			raw = c.Param(fb.name)
			found = raw != ""
		case sourceQuery:
			raw = queryValues.Get(fb.name)
			found = raw != ""
		case sourceHeader:
			raw = headers.Get(fb.name)
			found = raw != ""
		case sourceCookie:
			cookie, err := c.Cookie(fb.name)
			if err == nil {
				raw = cookie.Value
				found = true
			}
		case sourceForm:
			if formParsed {
				raw = formValues.Get(fb.name)
				found = raw != ""
			}
		}

		if !found && fb.hasDefault {
			raw = fb.defaultValue
			found = true
		}

		if !found {
			continue
		}

		field := v.FieldByIndex(fb.fieldIndex)
		if fb.hasDirectIndex {
			field = v.Field(fb.directIndex)
		}

		// If field implements ParamParser, use it
		if fb.hasParamParser {
			if err := parseWithParamParser(field, raw); err != nil {
				return fmt.Errorf("field %s: %w", fb.name, err)
			}
			continue
		}

		if err := fb.setter(field, raw); err != nil {
			return fmt.Errorf("field %s: %w", fb.name, err)
		}
	}

	// File field binding — separate from string-value fields.
	// Multipart parsing already happened at the top of bind().
	if sb.needMultipart {
		for _, ff := range sb.fileFields {
			field := v.FieldByIndex(ff.fieldIndex)
			if ff.isSlice {
				files, err := c.FormFiles(ff.name)
				if errors.Is(err, http.ErrMissingFile) {
					continue // missing non-required — leave nil, let validator handle
				}
				if err != nil {
					return &BindError{Err: err, Source: "file"}
				}
				field.Set(reflect.ValueOf(files))
			} else {
				f, err := c.FormFile(ff.name)
				if errors.Is(err, http.ErrMissingFile) {
					continue // missing non-required — leave nil, let validator handle
				}
				if err != nil {
					return &BindError{Err: err, Source: "file"}
				}
				field.Set(reflect.ValueOf(f))
			}
		}
	}

	return nil
}

// parseWithParamParser calls ParseParam on fields that implement ParamParser.
func parseWithParamParser(field reflect.Value, raw string) error {
	var parser ParamParser
	if field.Kind() == reflect.Ptr {
		if field.IsNil() {
			field.Set(reflect.New(field.Type().Elem()))
		}
		parser = field.Interface().(ParamParser)
	} else {
		parser = field.Addr().Interface().(ParamParser)
	}
	return parser.ParseParam(raw)
}

// applyDefaults sets default values for zero-value fields.
func (sb *structBinder) applyDefaults(dest any) {
	v := reflect.ValueOf(dest)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	for _, fb := range sb.defaults {
		field := v.FieldByIndex(fb.fieldIndex)
		if fb.hasDirectIndex {
			field = v.Field(fb.directIndex)
		}
		if field.IsZero() {
			_ = setFieldValue(field, fb.defaultValue)
		}
	}
}

func setFieldValue(field reflect.Value, raw string) error {
	return buildFieldSetter(field.Type())(field, raw)
}

func buildFieldSetter(t reflect.Type) fieldSetter {
	switch t.Kind() {
	case reflect.String:
		return func(field reflect.Value, raw string) error {
			field.SetString(raw)
			return nil
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return func(field reflect.Value, raw string) error {
			n, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				return fmt.Errorf("cannot parse %q as int: %w", raw, err)
			}
			field.SetInt(n)
			return nil
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return func(field reflect.Value, raw string) error {
			n, err := strconv.ParseUint(raw, 10, 64)
			if err != nil {
				return fmt.Errorf("cannot parse %q as uint: %w", raw, err)
			}
			field.SetUint(n)
			return nil
		}
	case reflect.Float32, reflect.Float64:
		return func(field reflect.Value, raw string) error {
			f, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				return fmt.Errorf("cannot parse %q as float: %w", raw, err)
			}
			field.SetFloat(f)
			return nil
		}
	case reflect.Bool:
		return func(field reflect.Value, raw string) error {
			b, err := strconv.ParseBool(raw)
			if err != nil {
				return fmt.Errorf("cannot parse %q as bool: %w", raw, err)
			}
			field.SetBool(b)
			return nil
		}
	case reflect.Slice:
		if t.Elem().Kind() == reflect.String {
			return func(field reflect.Value, raw string) error {
				field.Set(reflect.ValueOf(strings.Split(raw, ",")))
				return nil
			}
		}
	}

	return func(field reflect.Value, raw string) error {
		switch field.Kind() {
		case reflect.String:
			field.SetString(raw)
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			n, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				return fmt.Errorf("cannot parse %q as int: %w", raw, err)
			}
			field.SetInt(n)
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			n, err := strconv.ParseUint(raw, 10, 64)
			if err != nil {
				return fmt.Errorf("cannot parse %q as uint: %w", raw, err)
			}
			field.SetUint(n)
		case reflect.Float32, reflect.Float64:
			f, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				return fmt.Errorf("cannot parse %q as float: %w", raw, err)
			}
			field.SetFloat(f)
		case reflect.Bool:
			b, err := strconv.ParseBool(raw)
			if err != nil {
				return fmt.Errorf("cannot parse %q as bool: %w", raw, err)
			}
			field.SetBool(b)
		case reflect.Slice:
			if field.Type().Elem().Kind() == reflect.String {
				field.Set(reflect.ValueOf(strings.Split(raw, ",")))
				return nil
			}
			fallthrough
		default:
			return fmt.Errorf("unsupported field kind: %s", field.Kind())
		}
		return nil
	}
}

// bindQueryParams binds query parameters to a struct (used by Context.BindQuery).
func bindQueryParams(c *Context, dest any) error {
	t := reflect.TypeOf(dest)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	binder := buildQueryBinder(t)
	if binder == nil {
		return nil
	}
	v := reflect.ValueOf(dest)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}

	query := c.queryValues()
	for _, fb := range binder.fields {
		raw := query.Get(fb.name)
		if raw == "" {
			if fb.hasDefault {
				raw = fb.defaultVal
			} else {
				continue
			}
		}
		if err := fb.setter(v.Field(fb.fieldIndex), raw); err != nil {
			return fmt.Errorf("query field %s: %w", fb.name, err)
		}
	}
	return nil
}

// bindFormValues binds form values to a struct (used by Context.BindForm).
func bindFormValues(c *Context, dest any) error {
	t := reflect.TypeOf(dest)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	binder := buildFormBinder(t)
	if binder == nil {
		return nil
	}
	v := reflect.ValueOf(dest)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	if err := c.req.ParseForm(); err != nil {
		return err
	}
	formValues := c.req.Form

	for _, fb := range binder.fields {
		raw := formValues.Get(fb.name)
		if raw == "" {
			continue
		}
		if err := fb.setter(v.Field(fb.fieldIndex), raw); err != nil {
			return fmt.Errorf("form field %s: %w", fb.name, err)
		}
	}
	return nil
}
