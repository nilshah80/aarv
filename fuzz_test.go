package aarv

import (
	"bytes"
	"math"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

// rulesEqual compares two parsed rule sets with NaN-safe numeric comparison.
// parseValidateTag stores numeric params via strconv.ParseFloat, so an input
// like "min=NaN" yields num == NaN. Plain == (and reflect.DeepEqual) treat
// NaN != NaN, which would make two identical parses of the same tag compare
// unequal and produce false determinism failures.
func rulesEqual(a, b []validationRule) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if x.tag != y.tag || x.param != y.param || x.hasNum != y.hasNum {
			return false
		}
		if x.hasNum {
			if math.IsNaN(x.num) != math.IsNaN(y.num) {
				return false
			}
			if !math.IsNaN(x.num) && x.num != y.num {
				return false
			}
		}
		if !slices.Equal(x.oneOf, y.oneOf) {
			return false
		}
	}
	return true
}

// FuzzParseValidateTag exercises the validate-tag parser with arbitrary input.
// It asserts the parser never panics and is deterministic (same input parses
// to an equal rule set on repeat).
func FuzzParseValidateTag(f *testing.F) {
	seeds := []string{
		"", "required", "required,min=2,max=100",
		"oneof=a b c", "dive,required,min=1", "regex=^[a-z]+$",
		"datetime=2006-01-02", "len=5,unique", "email", "url", "uuid",
		"contains=@,startswith=x,endswith=y,excludes=z",
		"gte=1,lte=10,gt=0,lt=11", "ip,ipv4,ipv6,cidr,json,alphanum",
		// Adversarial / malformed inputs.
		"min=", "min=abc", "min=NaN", "min=Inf", "min=1e999", "min=-0",
		"oneof=", "=", "==", ",,,", "min=1,,max=2", "dive,dive",
		"omitempty,required", "regex=", "regex=[",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, tag string) {
		r1 := parseValidateTag(tag)
		r2 := parseValidateTag(tag)
		if !rulesEqual(r1, r2) {
			t.Fatalf("non-deterministic parse for %q: %#v vs %#v", tag, r1, r2)
		}
	})
}

// FuzzBindQuery exercises query-parameter binding with arbitrary raw query
// strings. The raw bytes are assigned to req.URL.RawQuery directly (rather than
// spliced into a URL string) so the fuzzer tests query binding, not net/url
// string parsing. It asserts binding never panics.
func FuzzBindQuery(f *testing.F) {
	seeds := []string{
		"a=1&n=2&f=3.14&b=true&s=x&s=y",
		"", "a=%ZZ", "n=notanum", "f=notafloat", "b=maybe",
		"s=&s=&s=", "=&=&=", "a", "a=", "&&&", "n=99999999999999999999",
		"f=NaN", "f=Inf", "b=1", "b=0",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	app := New(WithBanner(false))
	f.Fuzz(func(t *testing.T, raw string) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.URL.RawQuery = raw
		ctx, _ := newAppContext(app, req)
		defer app.ReleaseContext(ctx)

		var dst struct {
			A string   `query:"a"`
			N int      `query:"n"`
			F float64  `query:"f"`
			B bool     `query:"b"`
			S []string `query:"s"`
		}
		_ = ctx.BindQuery(&dst) // error is acceptable; a panic is not
	})
}

// FuzzBindJSON exercises JSON body binding with arbitrary bytes. It asserts the
// decode path never panics regardless of malformed, truncated, or hostile JSON.
func FuzzBindJSON(f *testing.F) {
	seeds := [][]byte{
		[]byte(`{}`), []byte(`{"name":"x","age":3,"items":[1,2,3]}`),
		[]byte(``), []byte(`not json`), []byte(`{"name":`), []byte(`[]`),
		[]byte(`null`), []byte(`123`), []byte(`"str"`), []byte(`true`),
		[]byte(`{"nested":{"a":1}}`), []byte(`{"age":"not-an-int"}`),
		[]byte(`{"items":"not-an-array"}`), bytes.Repeat([]byte(`{"a":`), 50),
	}
	for _, s := range seeds {
		f.Add(s)
	}
	app := New(WithBanner(false))
	f.Fuzz(func(t *testing.T, body []byte) {
		req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		ctx, _ := newAppContext(app, req)
		defer app.ReleaseContext(ctx)

		var dst struct {
			Name  string         `json:"name"`
			Age   int            `json:"age"`
			Items []int          `json:"items"`
			Tags  map[string]any `json:"tags"`
		}
		_ = ctx.BindJSON(&dst) // error is acceptable; a panic is not
	})
}
