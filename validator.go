package aarv

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
)

// SelfValidator can be implemented by types to provide custom validation.
type SelfValidator interface {
	Validate() []ValidationError
}

// StructLevelValidator can be implemented by types to provide struct-level validation
// that runs after field validation.
type StructLevelValidator interface {
	ValidateStruct() []ValidationError
}

// CustomRuleFunc is a function signature for custom validation rules.
// The function receives the field value and the rule parameter, and returns true if valid.
type CustomRuleFunc func(field reflect.Value, param string) bool

// customRules stores registered custom validation rules.
var customRules sync.Map

// RegisterRule registers a custom validation rule.
// The rule can then be used in validate tags like: validate:"myrule=param"
func RegisterRule(name string, fn CustomRuleFunc) {
	customRules.Store(name, fn)
}

// StructLevelFunc is a function that performs struct-level validation.
type StructLevelFunc func(v any) []ValidationError

// structLevelValidators stores registered struct-level validators.
var structLevelValidators sync.Map

// RegisterStructValidation registers a struct-level validation function for a specific type.
func RegisterStructValidation(t reflect.Type, fn StructLevelFunc) {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	structLevelValidators.Store(t, fn)
}

type validationRule struct {
	tag   string
	param string
}

type fieldValidator struct {
	name       string // json/field name for error messages
	fieldIndex []int
	kind       reflect.Kind
	fieldType  reflect.Type
	rules      []validationRule
	nested     *structValidator // for nested structs
	dive       *structValidator // for slice elements
	diveRules  []validationRule // rules for individual elements after dive
}

type structValidator struct {
	fields []fieldValidator
}

var (
	validatorCache sync.Map
	regexCache     sync.Map
	emailRegex     = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)
	uuidRegex      = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

func buildStructValidator(t reflect.Type) *structValidator {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}

	if cached, ok := validatorCache.Load(t); ok {
		return cached.(*structValidator)
	}

	sv := &structValidator{}
	hasRules := false

	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}

		tag := f.Tag.Get("validate")
		if tag == "" && f.Type.Kind() != reflect.Struct {
			continue
		}

		name := f.Tag.Get("json")
		if name == "" || name == "-" {
			name = f.Tag.Get("param")
		}
		if name == "" {
			name = f.Tag.Get("query")
		}
		if name == "" {
			name = strings.ToLower(f.Name)
		}
		// Strip json options like "name,omitempty"
		if idx := strings.IndexByte(name, ','); idx >= 0 {
			name = name[:idx]
		}

		fv := fieldValidator{
			name:       name,
			fieldIndex: f.Index,
			kind:       f.Type.Kind(),
			fieldType:  f.Type,
		}

		if tag != "" {
			fv.rules = parseValidateTag(tag)
			hasRules = true
		}

		// Handle nested structs
		ft := f.Type
		if ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
		}
		if ft.Kind() == reflect.Struct && ft != reflect.TypeOf(time.Time{}) {
			fv.nested = buildStructValidator(ft)
			if fv.nested != nil {
				hasRules = true
			}
		}

		// Handle dive for slices
		if ft.Kind() == reflect.Slice {
			for _, r := range fv.rules {
				if r.tag == "dive" {
					elemType := ft.Elem()
					if elemType.Kind() == reflect.Ptr {
						elemType = elemType.Elem()
					}
					if elemType.Kind() == reflect.Struct {
						fv.dive = buildStructValidator(elemType)
					}
					hasRules = true
					break
				}
			}
		}

		sv.fields = append(sv.fields, fv)
	}

	if !hasRules {
		return nil
	}

	validatorCache.Store(t, sv)
	return sv
}

func parseValidateTag(tag string) []validationRule {
	parts := strings.Split(tag, ",")
	rules := make([]validationRule, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		r := validationRule{tag: p}
		if idx := strings.IndexByte(p, '='); idx >= 0 {
			r.tag = p[:idx]
			r.param = p[idx+1:]
		}
		rules = append(rules, r)
	}
	return rules
}

func (sv *structValidator) validate(dest any) []ValidationError {
	v := reflect.ValueOf(dest)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}

	// Check SelfValidator
	if selfVal, ok := dest.(SelfValidator); ok {
		if errs := selfVal.Validate(); len(errs) > 0 {
			return errs
		}
	}

	var errs []ValidationError

	for _, fv := range sv.fields {
		field := v.FieldByIndex(fv.fieldIndex)

		// Check for omitempty: skip all validation if field is zero
		skipRules := false
		for _, rule := range fv.rules {
			if rule.tag == "omitempty" {
				if isZero(field) {
					skipRules = true
				}
				break
			}
		}

		if !skipRules {
			for _, rule := range fv.rules {
				if rule.tag == "dive" || rule.tag == "omitempty" {
					continue
				}
				if !checkRule(field, fv.kind, rule) {
					errs = append(errs, ValidationError{
						Field:   fv.name,
						Tag:     rule.tag,
						Param:   rule.param,
						Value:   fieldValue(field),
						Message: formatMessage(fv.name, rule),
					})
				}
			}
		}

		// Nested struct validation
		if fv.nested != nil && !field.IsZero() {
			fld := field
			if fld.Kind() == reflect.Ptr {
				fld = fld.Elem()
			}
			nested := fv.nested.validate(fld.Addr().Interface())
			for i := range nested {
				nested[i].Field = fv.name + "." + nested[i].Field
			}
			errs = append(errs, nested...)
		}

		// Dive validation for slices
		if fv.dive != nil && field.Kind() == reflect.Slice {
			for j := 0; j < field.Len(); j++ {
				elem := field.Index(j)
				if elem.Kind() == reflect.Ptr {
					elem = elem.Elem()
				}
				elemErrs := fv.dive.validate(elem.Addr().Interface())
				for k := range elemErrs {
					elemErrs[k].Field = fmt.Sprintf("%s[%d].%s", fv.name, j, elemErrs[k].Field)
				}
				errs = append(errs, elemErrs...)
			}
		}
	}

	// Check StructLevelValidator interface
	if slv, ok := dest.(StructLevelValidator); ok {
		errs = append(errs, slv.ValidateStruct()...)
	}

	// Check registered struct-level validators
	t := v.Type()
	if fn, ok := structLevelValidators.Load(t); ok {
		errs = append(errs, fn.(StructLevelFunc)(dest)...)
	}

	return errs
}

func checkRule(field reflect.Value, kind reflect.Kind, rule validationRule) bool {
	switch rule.tag {
	case "required":
		return !isZero(field)
	case "min":
		return checkMin(field, kind, rule.param)
	case "max":
		return checkMax(field, kind, rule.param)
	case "gte":
		return checkGte(field, kind, rule.param)
	case "lte":
		return checkLte(field, kind, rule.param)
	case "gt":
		return checkGt(field, kind, rule.param)
	case "lt":
		return checkLt(field, kind, rule.param)
	case "len":
		return checkLen(field, kind, rule.param)
	case "oneof":
		return checkOneOf(field, rule.param)
	case "email":
		return field.Kind() != reflect.String || emailRegex.MatchString(field.String())
	case "url":
		if field.Kind() != reflect.String {
			return true
		}
		_, err := url.ParseRequestURI(field.String())
		return err == nil
	case "uuid":
		return field.Kind() != reflect.String || uuidRegex.MatchString(field.String())
	case "alpha":
		return field.Kind() != reflect.String || isAllFunc(field.String(), unicode.IsLetter)
	case "numeric":
		return field.Kind() != reflect.String || isAllFunc(field.String(), unicode.IsDigit)
	case "alphanum":
		return field.Kind() != reflect.String || isAllFunc(field.String(), func(r rune) bool {
			return unicode.IsLetter(r) || unicode.IsDigit(r)
		})
	case "ip":
		return field.Kind() != reflect.String || net.ParseIP(field.String()) != nil
	case "ipv4":
		if field.Kind() != reflect.String {
			return true
		}
		ip := net.ParseIP(field.String())
		return ip != nil && ip.To4() != nil
	case "ipv6":
		if field.Kind() != reflect.String {
			return true
		}
		ip := net.ParseIP(field.String())
		return ip != nil && ip.To4() == nil
	case "cidr":
		if field.Kind() != reflect.String {
			return true
		}
		_, _, err := net.ParseCIDR(field.String())
		return err == nil
	case "json":
		if field.Kind() != reflect.String {
			return true
		}
		return json.Valid([]byte(field.String()))
	case "datetime":
		if field.Kind() != reflect.String {
			return true
		}
		_, err := time.Parse(rule.param, field.String())
		return err == nil
	case "regex":
		if field.Kind() != reflect.String {
			return true
		}
		return matchRegex(rule.param, field.String())
	case "contains":
		return field.Kind() != reflect.String || strings.Contains(field.String(), rule.param)
	case "startswith":
		return field.Kind() != reflect.String || strings.HasPrefix(field.String(), rule.param)
	case "endswith":
		return field.Kind() != reflect.String || strings.HasSuffix(field.String(), rule.param)
	case "excludes":
		return field.Kind() != reflect.String || !strings.Contains(field.String(), rule.param)
	case "unique":
		return checkUnique(field)
	default:
		// Check for custom registered rules
		if fn, ok := customRules.Load(rule.tag); ok {
			return fn.(CustomRuleFunc)(field, rule.param)
		}
	}
	return true
}

func isZero(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.String:
		return v.String() == ""
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.Bool:
		return !v.Bool()
	case reflect.Slice, reflect.Map:
		return v.IsNil() || v.Len() == 0
	case reflect.Ptr, reflect.Interface:
		return v.IsNil()
	default:
		return v.IsZero()
	}
}

func parseNum(s string) float64 {
	var f float64
	fmt.Sscanf(s, "%f", &f)
	return f
}

func fieldLen(field reflect.Value, kind reflect.Kind) float64 {
	switch kind {
	case reflect.String:
		return float64(len(field.String()))
	case reflect.Slice, reflect.Map, reflect.Array:
		return float64(field.Len())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(field.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(field.Uint())
	case reflect.Float32, reflect.Float64:
		return field.Float()
	}
	return 0
}

func checkMin(field reflect.Value, kind reflect.Kind, param string) bool {
	return fieldLen(field, kind) >= parseNum(param)
}

func checkMax(field reflect.Value, kind reflect.Kind, param string) bool {
	return fieldLen(field, kind) <= parseNum(param)
}

func checkGte(field reflect.Value, kind reflect.Kind, param string) bool {
	return fieldLen(field, kind) >= parseNum(param)
}

func checkLte(field reflect.Value, kind reflect.Kind, param string) bool {
	return fieldLen(field, kind) <= parseNum(param)
}

func checkGt(field reflect.Value, kind reflect.Kind, param string) bool {
	return fieldLen(field, kind) > parseNum(param)
}

func checkLt(field reflect.Value, kind reflect.Kind, param string) bool {
	return fieldLen(field, kind) < parseNum(param)
}

func checkLen(field reflect.Value, kind reflect.Kind, param string) bool {
	switch kind {
	case reflect.String:
		return float64(len(field.String())) == parseNum(param)
	case reflect.Slice, reflect.Map, reflect.Array:
		return float64(field.Len()) == parseNum(param)
	}
	return true
}

func checkOneOf(field reflect.Value, param string) bool {
	val := fmt.Sprintf("%v", field.Interface())
	for _, opt := range strings.Fields(param) {
		if val == opt {
			return true
		}
	}
	return false
}

func checkUnique(field reflect.Value) bool {
	if field.Kind() != reflect.Slice {
		return true
	}
	seen := make(map[any]struct{}, field.Len())
	for i := 0; i < field.Len(); i++ {
		v := field.Index(i).Interface()
		if _, exists := seen[v]; exists {
			return false
		}
		seen[v] = struct{}{}
	}
	return true
}

func isAllFunc(s string, fn func(rune) bool) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !fn(r) {
			return false
		}
	}
	return true
}

func matchRegex(pattern, value string) bool {
	cached, ok := regexCache.Load(pattern)
	if ok {
		return cached.(*regexp.Regexp).MatchString(value)
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return false
	}
	regexCache.Store(pattern, re)
	return re.MatchString(value)
}

func fieldValue(v reflect.Value) any {
	if v.CanInterface() {
		return v.Interface()
	}
	return nil
}

func formatMessage(field string, rule validationRule) string {
	switch rule.tag {
	case "required":
		return field + " is required"
	case "min":
		return fmt.Sprintf("%s must be at least %s", field, rule.param)
	case "max":
		return fmt.Sprintf("%s must be at most %s", field, rule.param)
	case "gte":
		return fmt.Sprintf("%s must be >= %s", field, rule.param)
	case "lte":
		return fmt.Sprintf("%s must be <= %s", field, rule.param)
	case "gt":
		return fmt.Sprintf("%s must be > %s", field, rule.param)
	case "lt":
		return fmt.Sprintf("%s must be < %s", field, rule.param)
	case "len":
		return fmt.Sprintf("%s must have length %s", field, rule.param)
	case "oneof":
		return fmt.Sprintf("%s must be one of [%s]", field, rule.param)
	case "email":
		return field + " must be a valid email"
	case "url":
		return field + " must be a valid URL"
	case "uuid":
		return field + " must be a valid UUID"
	case "regex":
		return fmt.Sprintf("%s must match pattern %s", field, rule.param)
	default:
		return fmt.Sprintf("%s failed %s validation", field, rule.tag)
	}
}
