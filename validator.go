package aarv

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
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

// ValidationMessageFunc customizes the human-readable message for a failed rule.
type ValidationMessageFunc func(field, param string) string

// customRules stores registered custom validation rules.
var customRules sync.Map

// validationMessageTemplates stores per-rule message template overrides.
var validationMessageTemplates sync.Map

// RegisterRule registers a custom validation rule.
// The rule can then be used in validate tags like: validate:"myrule=param"
func RegisterRule(name string, fn CustomRuleFunc) {
	customRules.Store(name, fn)
}

// SetValidationMessageTemplate overrides the default message for a validation rule.
// Passing a nil function removes the custom template for the rule.
func SetValidationMessageTemplate(tag string, fn ValidationMessageFunc) {
	if fn == nil {
		validationMessageTemplates.Delete(tag)
		return
	}
	validationMessageTemplates.Store(tag, fn)
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
	tag    string
	param  string
	num    float64
	hasNum bool
	oneOf  []string
}

type fieldValidator struct {
	name       string // json/field name for error messages
	fieldIndex []int
	kind       reflect.Kind
	fieldType  reflect.Type
	rules      []validationRule
	nested     *structValidator // for nested structs
	dive       *structValidator // for nested slice/map elements
	hasDive    bool
	diveRules  []validationRule // rules applied to each slice/map element
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
			rules := parseValidateTag(tag)
			if diveIndex := findDiveIndex(rules); diveIndex >= 0 {
				fv.hasDive = true
				fv.rules = append(fv.rules, rules[:diveIndex]...)
				fv.diveRules = append(fv.diveRules, rules[diveIndex+1:]...)
			} else {
				fv.rules = rules
			}
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
		if fv.hasDive && (ft.Kind() == reflect.Slice || ft.Kind() == reflect.Map) {
			elemType := ft.Elem()
			if elemType.Kind() == reflect.Ptr {
				elemType = elemType.Elem()
			}
			if elemType.Kind() == reflect.Struct {
				fv.dive = buildStructValidator(elemType)
			}
			hasRules = true
		}

		sv.fields = append(sv.fields, fv)
	}

	if !hasRules {
		return nil
	}

	validatorCache.Store(t, sv)
	return sv
}

func findDiveIndex(rules []validationRule) int {
	for i, rule := range rules {
		if rule.tag == "dive" {
			return i
		}
	}
	return -1
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
		compileValidationRule(&r)
		rules = append(rules, r)
	}
	return rules
}

func compileValidationRule(rule *validationRule) {
	switch rule.tag {
	case "min", "max", "gte", "lte", "gt", "lt", "len":
		rule.num, rule.hasNum = parseNumValue(rule.param)
	case "oneof":
		rule.oneOf = strings.Fields(rule.param)
	}
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

		errs = append(errs, validateDiveRules(fv, field)...)
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

func validateDiveRules(fv fieldValidator, field reflect.Value) []ValidationError {
	if !fv.hasDive {
		return nil
	}
	switch field.Kind() {
	case reflect.Slice:
		var errs []ValidationError
		for i := 0; i < field.Len(); i++ {
			errs = append(errs, validateDiveElement(fv, field.Index(i), fmt.Sprintf("%s[%d]", fv.name, i))...)
		}
		return errs
	case reflect.Map:
		var errs []ValidationError
		iter := field.MapRange()
		for iter.Next() {
			key := iter.Key()
			errs = append(errs, validateDiveElement(fv, iter.Value(), fmt.Sprintf("%s[%v]", fv.name, key.Interface()))...)
		}
		return errs
	default:
		return nil
	}
}

func validateDiveElement(fv fieldValidator, elem reflect.Value, path string) []ValidationError {
	var errs []ValidationError
	elemValue := elem
	elemKind := elem.Kind()

	if hasOmitEmptyRule(fv.diveRules) && isZero(elemValue) {
		return nil
	}
	if elemValue.Kind() == reflect.Ptr && !elemValue.IsNil() {
		elemValue = elemValue.Elem()
		elemKind = elemValue.Kind()
	}

	for _, rule := range fv.diveRules {
		if rule.tag == "omitempty" {
			continue
		}
		if !checkRule(elemValue, elemKind, rule) {
			errs = append(errs, ValidationError{
				Field:   path,
				Tag:     rule.tag,
				Param:   rule.param,
				Value:   fieldValue(elemValue),
				Message: formatMessage(path, rule),
			})
		}
	}

	if fv.dive == nil {
		return errs
	}

	nestedTarget := elem
	if nestedTarget.Kind() == reflect.Ptr {
		if nestedTarget.IsNil() {
			return errs
		}
		nestedTarget = nestedTarget.Elem()
	}
	if !nestedTarget.IsValid() || nestedTarget.Kind() != reflect.Struct {
		return errs
	}

	var nested any
	if nestedTarget.CanAddr() {
		nested = nestedTarget.Addr().Interface()
	} else {
		copyPtr := reflect.New(nestedTarget.Type())
		copyPtr.Elem().Set(nestedTarget)
		nested = copyPtr.Interface()
	}
	nestedErrs := fv.dive.validate(nested)
	for i := range nestedErrs {
		nestedErrs[i].Field = path + "." + nestedErrs[i].Field
	}
	return append(errs, nestedErrs...)
}

func hasOmitEmptyRule(rules []validationRule) bool {
	for _, rule := range rules {
		if rule.tag == "omitempty" {
			return true
		}
	}
	return false
}

func checkRule(field reflect.Value, kind reflect.Kind, rule validationRule) bool {
	switch rule.tag {
	case "required":
		return !isZero(field)
	case "min":
		return checkMinRule(field, kind, rule)
	case "max":
		return checkMaxRule(field, kind, rule)
	case "gte":
		return checkGteRule(field, kind, rule)
	case "lte":
		return checkLteRule(field, kind, rule)
	case "gt":
		return checkGtRule(field, kind, rule)
	case "lt":
		return checkLtRule(field, kind, rule)
	case "len":
		return checkLenRule(field, kind, rule)
	case "oneof":
		return checkOneOfRule(field, rule)
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
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

func parseNumValue(s string) (float64, bool) {
	f, err := strconv.ParseFloat(s, 64)
	return f, err == nil
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

func checkMinRule(field reflect.Value, kind reflect.Kind, rule validationRule) bool {
	return fieldLen(field, kind) >= numericRuleValue(rule)
}

func checkMaxRule(field reflect.Value, kind reflect.Kind, rule validationRule) bool {
	return fieldLen(field, kind) <= numericRuleValue(rule)
}

func checkGteRule(field reflect.Value, kind reflect.Kind, rule validationRule) bool {
	return fieldLen(field, kind) >= numericRuleValue(rule)
}

func checkLteRule(field reflect.Value, kind reflect.Kind, rule validationRule) bool {
	return fieldLen(field, kind) <= numericRuleValue(rule)
}

func checkGtRule(field reflect.Value, kind reflect.Kind, rule validationRule) bool {
	return fieldLen(field, kind) > numericRuleValue(rule)
}

func checkLtRule(field reflect.Value, kind reflect.Kind, rule validationRule) bool {
	return fieldLen(field, kind) < numericRuleValue(rule)
}

func checkLenRule(field reflect.Value, kind reflect.Kind, rule validationRule) bool {
	value := numericRuleValue(rule)
	switch kind {
	case reflect.String:
		return float64(len(field.String())) == value
	case reflect.Slice, reflect.Map, reflect.Array:
		return float64(field.Len()) == value
	}
	return true
}

func numericRuleValue(rule validationRule) float64 {
	if rule.hasNum {
		return rule.num
	}
	return parseNum(rule.param)
}

func checkOneOfRule(field reflect.Value, rule validationRule) bool {
	val := stringifyValue(field)
	options := rule.oneOf
	if options == nil {
		options = strings.Fields(rule.param)
	}
	for _, opt := range options {
		if val == opt {
			return true
		}
	}
	return false
}

func stringifyValue(field reflect.Value) string {
	switch field.Kind() {
	case reflect.String:
		return field.String()
	case reflect.Bool:
		return strconv.FormatBool(field.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(field.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return strconv.FormatUint(field.Uint(), 10)
	case reflect.Float32:
		return strconv.FormatFloat(field.Float(), 'f', -1, 32)
	case reflect.Float64:
		return strconv.FormatFloat(field.Float(), 'f', -1, 64)
	default:
		return fmt.Sprintf("%v", field.Interface())
	}
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
	if tmpl, ok := validationMessageTemplates.Load(rule.tag); ok {
		if msg := tmpl.(ValidationMessageFunc)(field, rule.param); msg != "" {
			return msg
		}
	}
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
