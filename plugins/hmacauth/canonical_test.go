package hmacauth

import (
	"net/url"
	"strings"
	"testing"
)

func TestCanonicalQueryEmpty(t *testing.T) {
	if got := canonicalQuery(url.Values{}); got != "" {
		t.Fatalf("empty values: got %q want empty", got)
	}
	if got := canonicalQuery(nil); got != "" {
		t.Fatalf("nil values: got %q want empty", got)
	}
}

func TestCanonicalQuerySortsKeysAscending(t *testing.T) {
	v := url.Values{}
	v.Set("zoo", "z")
	v.Set("alpha", "a")
	v.Set("midway", "m")
	got := canonicalQuery(v)
	want := "alpha=a&midway=m&zoo=z"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestCanonicalQuerySortsValuesAscending(t *testing.T) {
	v := url.Values{}
	v["tag"] = []string{"zoo", "alpha", "bravo"}
	got := canonicalQuery(v)
	want := "tag=alpha&tag=bravo&tag=zoo"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestCanonicalQueryRFC3986NotFormUrlEncoded(t *testing.T) {
	// Spaces, '+', '/' must encode as %HH per RFC 3986. url.QueryEscape
	// would emit "+" for spaces — that is the bug this whole codepath
	// guards against.
	v := url.Values{}
	v.Set("q", "hello world")
	v.Set("p", "a+b")
	v.Set("x", "/foo/bar")

	got := canonicalQuery(v)
	wantSubstrs := []string{
		"p=a%2Bb",
		"q=hello%20world",
		"x=%2Ffoo%2Fbar",
	}
	for _, w := range wantSubstrs {
		if !strings.Contains(got, w) {
			t.Fatalf("want %q in canonical %q", w, got)
		}
	}
	if strings.Contains(got, "+") {
		t.Fatalf("canonical must not contain literal '+': %q", got)
	}
}

func TestCanonicalQueryUnreservedSetUnchanged(t *testing.T) {
	v := url.Values{}
	v.Set("x", "ABCabc012-_.~")
	got := canonicalQuery(v)
	want := "x=ABCabc012-_.~"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestCanonicalQueryHexUppercase(t *testing.T) {
	v := url.Values{}
	v.Set("k", "\x7f")
	got := canonicalQuery(v)
	if !strings.Contains(got, "%7F") {
		t.Fatalf("want uppercase hex in %q", got)
	}
}

func TestCanonicalRequestNewlineStructure(t *testing.T) {
	v := url.Values{}
	v.Set("a", "1")
	got := canonicalRequest("GET", "/x", v, []byte("body"), 1700000000, "nonce-1")
	parts := strings.Split(string(got), "\n")
	if len(parts) != 6 {
		t.Fatalf("want 6 newline-separated parts, got %d: %q", len(parts), got)
	}
	if parts[0] != "GET" {
		t.Fatalf("part[0] method: got %q want %q", parts[0], "GET")
	}
	if parts[1] != "/x" {
		t.Fatalf("part[1] path: got %q want %q", parts[1], "/x")
	}
	if parts[2] != "a=1" {
		t.Fatalf("part[2] query: got %q want %q", parts[2], "a=1")
	}
	if len(parts[3]) != 64 {
		t.Fatalf("part[3] body hash: got len %d want 64", len(parts[3]))
	}
	if parts[4] != "1700000000" {
		t.Fatalf("part[4] timestamp: got %q want %q", parts[4], "1700000000")
	}
	if parts[5] != "nonce-1" {
		t.Fatalf("part[5] nonce: got %q want %q", parts[5], "nonce-1")
	}
}

func TestCanonicalRequestUppercasesMethod(t *testing.T) {
	got := canonicalRequest("post", "/x", nil, nil, 1, "n")
	if !strings.HasPrefix(string(got), "POST\n") {
		t.Fatalf("method should uppercase: got %q", got)
	}
}

func TestCanonicalRequestEmptyBodyHash(t *testing.T) {
	got := canonicalRequest("GET", "/x", nil, nil, 1, "n")
	parts := strings.Split(string(got), "\n")
	// SHA-256 of empty input is the well-known constant.
	const empty = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if parts[3] != empty {
		t.Fatalf("empty body hash: got %q want %q", parts[3], empty)
	}
}

func FuzzCanonicalQuery(f *testing.F) {
	f.Add("a=1&b=2&a=3")
	f.Add("")
	f.Add("k=hello%20world")
	f.Add("=")
	f.Add("a=&b=")
	f.Add("a=1&a=1&a=1")
	f.Fuzz(func(t *testing.T, raw string) {
		v, err := url.ParseQuery(raw)
		if err != nil {
			// Invalid query strings are fine — we only check that
			// parsed values are processed deterministically and
			// without panic.
			return
		}
		first := canonicalQuery(v)
		// Determinism: a second call with the same Values must
		// produce identical output.
		second := canonicalQuery(v)
		if first != second {
			t.Fatalf("non-deterministic: %q vs %q", first, second)
		}
		// Output must contain only RFC 3986 unreserved bytes plus
		// '%', '=', '&'.
		for i := 0; i < len(first); i++ {
			c := first[i]
			if isUnreserved(c) || c == '%' || c == '=' || c == '&' {
				continue
			}
			t.Fatalf("unexpected byte %#x in canonical %q", c, first)
		}
	})
}
