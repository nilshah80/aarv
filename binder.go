package aarv

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// bindSource identifies where a field value comes from.
type bindSource int

const (
	sourceParam       bindSource = iota // path parameter
	sourceQuery                         // query string
	sourceHeader                        // request header
	sourceCookie                        // cookie
	sourceForm                          // form data
	sourceDefaultOnly                   // no binding source, only default value
)

// fieldBinding describes how to populate a single struct field from the request.
type fieldBinding struct {
	source       bindSource
	name         string // tag value (e.g., "userId")
	fieldIndex   []int  // reflect field index path
	kind         reflect.Kind
	defaultValue string
	hasDefault   bool
}

// structBinder holds pre-computed binding info for a struct type.
type structBinder struct {
	fields   []fieldBinding
	needBody bool // true if any field uses json tag (body binding)
}

// buildStructBinder inspects a struct type at registration time and returns a binder.
func buildStructBinder(t reflect.Type) *structBinder {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
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
					sb.fields = append(sb.fields, ef)
				}
				if embedded.needBody {
					sb.needBody = true
				}
			}
			continue
		}

		fb := fieldBinding{
			fieldIndex: f.Index,
			kind:       f.Type.Kind(),
		}

		if tag := f.Tag.Get("default"); tag != "" {
			fb.defaultValue = tag
			fb.hasDefault = true
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
			sb.fields = append(sb.fields, fb)
		} else if fb.hasDefault {
			// Field has a default but no binding source (e.g., JSON-only fields).
			// Track it so applyDefaults can set the default for zero-value fields.
			fb.source = sourceDefaultOnly
			fb.name = f.Name
			sb.fields = append(sb.fields, fb)
		}

		if tag := f.Tag.Get("json"); tag != "" && tag != "-" {
			sb.needBody = true
		}
	}

	return sb
}

// bind populates the struct from the request context.
func (sb *structBinder) bind(c *Context, dest any) error {
	v := reflect.ValueOf(dest)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}

	for _, fb := range sb.fields {
		var raw string
		var found bool

		switch fb.source {
		case sourceParam:
			raw = c.Param(fb.name)
			found = raw != ""
		case sourceQuery:
			raw = c.Query(fb.name)
			found = raw != ""
		case sourceHeader:
			raw = c.Header(fb.name)
			found = raw != ""
		case sourceCookie:
			cookie, err := c.Cookie(fb.name)
			if err == nil {
				raw = cookie.Value
				found = true
			}
		case sourceForm:
			if err := c.req.ParseForm(); err == nil {
				raw = c.req.FormValue(fb.name)
				found = raw != ""
			}
		case sourceDefaultOnly:
			// No binding source — defaults are applied by applyDefaults.
			continue
		}

		if !found && fb.hasDefault {
			raw = fb.defaultValue
			found = true
		}

		if !found {
			continue
		}

		field := v.FieldByIndex(fb.fieldIndex)
		if err := setFieldValue(field, raw); err != nil {
			return fmt.Errorf("field %s: %w", fb.name, err)
		}
	}

	return nil
}

// applyDefaults sets default values for zero-value fields.
func (sb *structBinder) applyDefaults(dest any) {
	v := reflect.ValueOf(dest)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	for _, fb := range sb.fields {
		if !fb.hasDefault {
			continue
		}
		field := v.FieldByIndex(fb.fieldIndex)
		if field.IsZero() {
			_ = setFieldValue(field, fb.defaultValue)
		}
	}
}

func setFieldValue(field reflect.Value, raw string) error {
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
			parts := strings.Split(raw, ",")
			field.Set(reflect.ValueOf(parts))
		}
	default:
		return fmt.Errorf("unsupported field kind: %s", field.Kind())
	}
	return nil
}

// bindQueryParams binds query parameters to a struct (used by Context.BindQuery).
func bindQueryParams(c *Context, dest any) error {
	t := reflect.TypeOf(dest)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	v := reflect.ValueOf(dest)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}

	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		tag := f.Tag.Get("query")
		if tag == "" {
			continue
		}
		raw := c.Query(tag)
		if raw == "" {
			if def := f.Tag.Get("default"); def != "" {
				raw = def
			} else {
				continue
			}
		}
		if err := setFieldValue(v.Field(i), raw); err != nil {
			return fmt.Errorf("query field %s: %w", tag, err)
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
	v := reflect.ValueOf(dest)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}

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
		raw := c.req.FormValue(tag)
		if raw == "" {
			continue
		}
		if err := setFieldValue(v.Field(i), raw); err != nil {
			return fmt.Errorf("form field %s: %w", tag, err)
		}
	}
	return nil
}
