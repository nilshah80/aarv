package aarv

import (
	"encoding/json"
	"reflect"
	"strings"
	"time"

	"testing"
)

func TestValidationRules(t *testing.T) {
	type ValidateReq struct {
		ReqStr    string   `json:"req_str" validate:"required"`
		MinStr    string   `json:"min_str" validate:"min=3"`
		MaxStr    string   `json:"max_str" validate:"max=5"`
		Email     string   `json:"email" validate:"email"`
		URL       string   `json:"url" validate:"url"`
		UUID      string   `json:"uuid" validate:"uuid"`
		AlphaNum  string   `json:"alpha_num" validate:"alphanum"`
		Items     []string `json:"items" validate:"min=1,max=2"`
		NumItems  int      `json:"num_items" validate:"min=10,max=20"`
	}

	app := New(WithBanner(false))

	app.Post("/val", BindReq(func(c *Context, req ValidateReq) error {
		return c.JSON(200, "ok")
	}))

	tc := NewTestClient(app)

	// Test 1: Valid payload
	validPayload := `{"req_str":"a","min_str":"abc","max_str":"a","email":"test@test.com","url":"http://test.com","uuid":"123e4567-e89b-12d3-a456-426614174000","alpha_num":"A1","items":["a"],"num_items":15}`
	resp1 := tc.Post("/val", json.RawMessage(validPayload))
	resp1.AssertStatus(t, 200)

	// Test 2: Invalid required
	invalidReq := `{"min_str":"abc","max_str":"a","email":"test@test.com","url":"http://test.com","uuid":"123e4567-e89b-12d3-a456-426614174000","alpha_num":"A1","items":["a"],"num_items":15}`
	resp2 := tc.Post("/val", json.RawMessage(invalidReq))
	resp2.AssertStatus(t, 422)
	assertValidationError(t, resp2.Text(), "req_str", "required")

	// Test 3: Invalid min (string)
	invalidMinStr := `{"req_str":"a","min_str":"ab","max_str":"a","email":"test@test.com","url":"http://test.com","uuid":"123e4567-e89b-12d3-a456-426614174000","alpha_num":"A1","items":["a"],"num_items":15}`
	resp3 := tc.Post("/val", json.RawMessage(invalidMinStr))
	resp3.AssertStatus(t, 422)
	assertValidationError(t, resp3.Text(), "min_str", "min")

	// Test 4: Invalid max (string)
	invalidMaxStr := `{"req_str":"a","min_str":"abc","max_str":"abcdef","email":"test@test.com","url":"http://test.com","uuid":"123e4567-e89b-12d3-a456-426614174000","alpha_num":"A1","items":["a"],"num_items":15}`
	resp4 := tc.Post("/val", json.RawMessage(invalidMaxStr))
	resp4.AssertStatus(t, 422)
	assertValidationError(t, resp4.Text(), "max_str", "max")

	// Test 5: Invalid min (slice)
	invalidSliceMin := `{"req_str":"a","min_str":"abc","max_str":"a","email":"test@test.com","url":"http://test.com","uuid":"123e4567-e89b-12d3-a456-426614174000","alpha_num":"A1","items":[],"num_items":15}`
	resp5 := tc.Post("/val", json.RawMessage(invalidSliceMin))
	resp5.AssertStatus(t, 422)
	assertValidationError(t, resp5.Text(), "items", "min")

	// Test 6: Invalid email
	invalidEmail := `{"req_str":"a","min_str":"abc","max_str":"a","email":"not-an-email","url":"http://test.com","uuid":"123e4567-e89b-12d3-a456-426614174000","alpha_num":"A1","items":["a"],"num_items":15}`
	resp6 := tc.Post("/val", json.RawMessage(invalidEmail))
	resp6.AssertStatus(t, 422)
	assertValidationError(t, resp6.Text(), "email", "email")

	// Test 7: Invalid url
	invalidURL := `{"req_str":"a","min_str":"abc","max_str":"a","email":"test@test.com","url":"://invalid","uuid":"123e4567-e89b-12d3-a456-426614174000","alpha_num":"A1","items":["a"],"num_items":15}`
	resp7 := tc.Post("/val", json.RawMessage(invalidURL))
	resp7.AssertStatus(t, 422)
	assertValidationError(t, resp7.Text(), "url", "url")
}

func assertValidationError(t *testing.T, responseText, field, tag string) {
	// Simple struct to catch aarv Validation Error schema
	type valError struct {
		Field string `json:"field"`
		Tag   string `json:"tag"`
	}
	type errResp struct {
		Message string     `json:"message"`
		Details  []valError `json:"details"`
	}

	var parsed errResp
	if err := json.Unmarshal([]byte(responseText), &parsed); err != nil {
		t.Fatalf("Failed to unmarshal validation error response: %s", responseText)
	}

	found := false
	for _, e := range parsed.Details {
		if e.Field == field && e.Tag == tag {
			found = true
			break
		}
	}

	if !found {
		t.Errorf("Validation error for field %s with tag %s missing. Got: %v", field, tag, parsed.Details)
	}
}

func TestValidatorAdditionalCoverage(t *testing.T) {
	RegisterRule("startswithx", func(field reflect.Value, _ string) bool {
		return strings.HasPrefix(field.String(), "x")
	})
	RegisterStructValidation(reflect.TypeOf((*structLevelValidated)(nil)), func(v any) []ValidationError {
		s := v.(*structLevelValidated)
		if s.Value == "registered" {
			return []ValidationError{{Field: "value", Tag: "registered"}}
		}
		return nil
	})

	t.Run("validator builder and validate flow", func(t *testing.T) {
		type child struct {
			Name string `json:"name" validate:"required"`
		}
		type item struct {
			Code string `json:"code" validate:"required"`
		}
		type payload struct {
			Name   string               `json:"name,omitempty" validate:"required,min=2,startswith=ab,endswith=z,contains=b,excludes=x"`
			Choice string               `query:"choice" validate:"oneof=one two"`
			Tags   []item               `json:"tags" validate:"dive"`
			Child  *child               `json:"child"`
			Opt    string               `json:"opt" validate:"omitempty,min=3"`
			Meta   structLevelValidated `json:"meta"`
		}

		sv := buildStructValidator(reflect.TypeOf(payload{}))
		if sv == nil {
			t.Fatal("expected struct validator")
		}
		if cached := buildStructValidator(reflect.TypeOf(payload{})); cached != sv {
			t.Fatal("expected cached struct validator instance")
		}
		if buildStructValidator(reflect.TypeOf(1)) != nil {
			t.Fatal("expected nil validator for non-struct")
		}
		type timeHolder struct {
			When time.Time
		}
		if buildStructValidator(reflect.TypeOf(&timeHolder{})) != nil {
			t.Fatal("expected nil validator for time-only struct without rules")
		}

		got := sv.validate(&payload{
			Name:   "a",
			Choice: "three",
			Tags:   []item{{Code: ""}},
			Child:  &child{},
			Meta:   structLevelValidated{Value: "registered"},
		})
		if len(got) == 0 {
			t.Fatal("expected validation errors")
		}

		type ptrPayload struct {
			Child *child  `json:"child"`
			Tags  []*item `json:"tags" validate:"dive"`
		}
		sv = buildStructValidator(reflect.TypeOf(ptrPayload{}))
		if errs := sv.validate(&ptrPayload{
			Tags:  []*item{{Code: ""}},
		}); len(errs) == 0 {
			t.Fatal("expected nested pointer validation errors")
		}

		type nestedChild struct {
			Name string `json:"name" validate:"min=2"`
		}
		type nestedPayload struct {
			Child nestedChild `json:"child"`
		}
		sv = buildStructValidator(reflect.TypeOf(nestedPayload{}))
		if errs := sv.validate(&nestedPayload{Child: nestedChild{Name: "a"}}); len(errs) == 0 {
			t.Fatal("expected nested struct validation errors")
		}

		type ptrNestedPayload struct {
			Child *child `json:"child" validate:"required"`
		}
		sv = buildStructValidator(reflect.TypeOf(ptrNestedPayload{}))
		if errs := sv.validate(&ptrNestedPayload{Child: &child{}}); len(errs) == 0 {
			t.Fatal("expected pointer nested validation errors")
		}
	})

	t.Run("self validator short circuit", func(t *testing.T) {
		sv := &structValidator{}
		if errs := sv.validate(&selfValidating{}); len(errs) != 1 || errs[0].Field != "self" {
			t.Fatalf("unexpected self validation result: %#v", errs)
		}
	})

	t.Run("struct level validators", func(t *testing.T) {
		sv := &structValidator{}
		if errs := sv.validate(&structLevelValidated{Value: "bad"}); len(errs) != 1 || errs[0].Tag != "struct" {
			t.Fatalf("unexpected struct-level validation result: %#v", errs)
		}
		if errs := sv.validate(&structLevelValidated{Value: "registered"}); len(errs) != 1 || errs[0].Tag != "registered" {
			t.Fatalf("unexpected registered struct validation result: %#v", errs)
		}
	})

	t.Run("rule helpers", func(t *testing.T) {
		cases := []struct {
			rule validationRule
			val  any
			kind reflect.Kind
			want bool
		}{
			{validationRule{tag: "required"}, "x", reflect.String, true},
			{validationRule{tag: "min", param: "2"}, "ab", reflect.String, true},
			{validationRule{tag: "max", param: "2"}, "abc", reflect.String, false},
			{validationRule{tag: "gte", param: "2"}, 2, reflect.Int, true},
			{validationRule{tag: "lte", param: "2"}, 3, reflect.Int, false},
			{validationRule{tag: "gt", param: "2"}, 3, reflect.Int, true},
			{validationRule{tag: "lt", param: "2"}, 3, reflect.Int, false},
			{validationRule{tag: "len", param: "2"}, []int{1, 2}, reflect.Slice, true},
			{validationRule{tag: "oneof", param: "a b"}, "b", reflect.String, true},
			{validationRule{tag: "email"}, "test@example.com", reflect.String, true},
			{validationRule{tag: "url"}, "https://example.com", reflect.String, true},
			{validationRule{tag: "uuid"}, "123e4567-e89b-12d3-a456-426614174000", reflect.String, true},
			{validationRule{tag: "alpha"}, "abc", reflect.String, true},
			{validationRule{tag: "numeric"}, "123", reflect.String, true},
			{validationRule{tag: "alphanum"}, "a1", reflect.String, true},
			{validationRule{tag: "ip"}, "127.0.0.1", reflect.String, true},
			{validationRule{tag: "ipv4"}, "127.0.0.1", reflect.String, true},
			{validationRule{tag: "ipv6"}, "::1", reflect.String, true},
			{validationRule{tag: "cidr"}, "127.0.0.0/24", reflect.String, true},
			{validationRule{tag: "json"}, `{"ok":true}`, reflect.String, true},
			{validationRule{tag: "datetime", param: time.RFC3339}, time.Now().UTC().Format(time.RFC3339), reflect.String, true},
			{validationRule{tag: "regex", param: "^a+$"}, "aaa", reflect.String, true},
			{validationRule{tag: "contains", param: "b"}, "abc", reflect.String, true},
			{validationRule{tag: "startswith", param: "a"}, "abc", reflect.String, true},
			{validationRule{tag: "endswith", param: "c"}, "abc", reflect.String, true},
			{validationRule{tag: "excludes", param: "z"}, "abc", reflect.String, true},
			{validationRule{tag: "unique"}, []int{1, 2, 3}, reflect.Slice, true},
			{validationRule{tag: "startswithx"}, "xyz", reflect.String, true},
			{validationRule{tag: "unknown"}, "ignored", reflect.String, true},
			{validationRule{tag: "url"}, 1, reflect.Int, true},
			{validationRule{tag: "ipv4"}, 1, reflect.Int, true},
			{validationRule{tag: "ipv6"}, 1, reflect.Int, true},
			{validationRule{tag: "cidr"}, 1, reflect.Int, true},
			{validationRule{tag: "json"}, 1, reflect.Int, true},
			{validationRule{tag: "datetime", param: time.RFC3339}, 1, reflect.Int, true},
			{validationRule{tag: "regex", param: "^a+$"}, 1, reflect.Int, true},
		}

		for _, tc := range cases {
			if got := checkRule(reflect.ValueOf(tc.val), tc.kind, tc.rule); got != tc.want {
				t.Fatalf("rule %+v on %#v: got %v want %v", tc.rule, tc.val, got, tc.want)
			}
		}

		if checkUnique(reflect.ValueOf([]int{1, 1})) {
			t.Fatal("expected duplicate unique check failure")
		}
		if checkOneOf(reflect.ValueOf("x"), "a b") {
			t.Fatal("expected oneof mismatch")
		}
		if !matchRegex("^a+$", "aa") || !matchRegex("^a+$", "aa") || matchRegex("(", "aa") {
			t.Fatal("unexpected regex helper result")
		}
	})

	t.Run("utility helpers", func(t *testing.T) {
		type hidden struct {
			value int
		}

		if parseNum("2.5") != 2.5 {
			t.Fatal("unexpected parsed number")
		}
		if fieldLen(reflect.ValueOf(map[string]int{"a": 1}), reflect.Map) != 1 {
			t.Fatal("unexpected map field length")
		}
		if fieldLen(reflect.ValueOf([2]int{1, 2}), reflect.Array) != 2 {
			t.Fatal("unexpected array field length")
		}
		if fieldLen(reflect.ValueOf("ab"), reflect.String) != 2 || fieldLen(reflect.ValueOf(3), reflect.Int) != 3 || fieldLen(reflect.ValueOf(uint(4)), reflect.Uint) != 4 || fieldLen(reflect.ValueOf(1.5), reflect.Float64) != 1.5 {
			t.Fatal("unexpected scalar field lengths")
		}
		if fieldLen(reflect.ValueOf(true), reflect.Bool) != 0 {
			t.Fatal("unexpected bool field length")
		}
		if !checkGte(reflect.ValueOf(3), reflect.Int, "2") || !checkLte(reflect.ValueOf(2), reflect.Int, "2") || !checkGt(reflect.ValueOf(3), reflect.Int, "2") || !checkLt(reflect.ValueOf(1), reflect.Int, "2") {
			t.Fatal("unexpected comparison helper result")
		}
		if !checkLen(reflect.ValueOf("ab"), reflect.String, "2") || checkLen(reflect.ValueOf("ab"), reflect.String, "3") {
			t.Fatal("unexpected length helper result")
		}
		if !checkLen(reflect.ValueOf(map[string]int{"a": 1}), reflect.Map, "1") {
			t.Fatal("expected map length helper to pass")
		}
		if !checkLen(reflect.ValueOf(1), reflect.Int, "2") {
			t.Fatal("expected default length helper branch to return true")
		}
		if isAllFunc("", func(r rune) bool { return true }) {
			t.Fatal("expected empty string all-func failure")
		}
		if isAllFunc("ab1", func(r rune) bool { return r != '1' }) {
			t.Fatal("expected isAllFunc mismatch to fail")
		}
		if !isZero(reflect.ValueOf(map[string]int{})) || !isZero(reflect.ValueOf((*int)(nil))) || !isZero(reflect.ValueOf(struct{}{})) {
			t.Fatal("unexpected zero-value helper result")
		}
		if !isZero(reflect.ValueOf("")) || !isZero(reflect.ValueOf(0)) || !isZero(reflect.ValueOf(uint(0))) || !isZero(reflect.ValueOf(0.0)) || !isZero(reflect.ValueOf(false)) {
			t.Fatal("expected primitive zero checks to pass")
		}
		if !isZero(reflect.ValueOf(struct{ V any }{V: nil}).Field(0)) {
			t.Fatal("expected interface zero check to pass")
		}
		if fieldValue(reflect.ValueOf(hidden{value: 1}).Field(0)) != nil {
			t.Fatal("expected nil fieldValue for non-interfaceable field")
		}
		if parseValidateTag(" required , min=2 ") == nil {
			t.Fatal("expected parsed validation rules")
		}
		if got := parseValidateTag("required,,min=2"); len(got) != 2 {
			t.Fatalf("unexpected parsed rule count: %d", len(got))
		}
		if !checkUnique(reflect.ValueOf(123)) {
			t.Fatal("expected non-slice unique check to pass")
		}

		msgs := []string{
			formatMessage("name", validationRule{tag: "required"}),
			formatMessage("name", validationRule{tag: "min", param: "2"}),
			formatMessage("name", validationRule{tag: "max", param: "2"}),
			formatMessage("name", validationRule{tag: "gte", param: "2"}),
			formatMessage("name", validationRule{tag: "lte", param: "2"}),
			formatMessage("name", validationRule{tag: "gt", param: "2"}),
			formatMessage("name", validationRule{tag: "lt", param: "2"}),
			formatMessage("name", validationRule{tag: "len", param: "2"}),
			formatMessage("name", validationRule{tag: "oneof", param: "a b"}),
			formatMessage("name", validationRule{tag: "email"}),
			formatMessage("name", validationRule{tag: "url"}),
			formatMessage("name", validationRule{tag: "uuid"}),
			formatMessage("name", validationRule{tag: "regex", param: "^a+$"}),
			formatMessage("name", validationRule{tag: "custom"}),
		}
		for _, msg := range msgs {
			if msg == "" {
				t.Fatal("expected non-empty validation message")
			}
		}
	})
}
