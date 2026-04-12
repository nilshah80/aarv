package requestid

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nilshah80/aarv"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Header != "X-Request-ID" || cfg.Generator == nil {
		t.Fatalf("unexpected default config: %#v", cfg)
	}
}

func TestGenerateULIDAndFromContext(t *testing.T) {
	id1 := GenerateULID()
	id2 := GenerateULID()

	if len(id1) != 26 || len(id2) != 26 {
		t.Fatalf("expected 26-char ULIDs, got %q and %q", id1, id2)
	}
	for _, r := range id1 + id2 {
		if !strings.ContainsRune(ulidEncoding, r) {
			t.Fatalf("unexpected ulid rune %q", r)
		}
	}
	if id1 == id2 {
		t.Fatal("expected generated ULIDs to differ")
	}

	if got := FromContext(context.Background()); got != "" {
		t.Fatalf("expected empty id from background context, got %q", got)
	}
	ctx := context.WithValue(context.Background(), contextKey{}, "ctx-id")
	if got := FromContext(ctx); got != "ctx-id" {
		t.Fatalf("expected ctx-id, got %q", got)
	}
}

func TestNewAppliesDefaultHeaderAndStoresAarvContextID(t *testing.T) {
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{
		Header: "",
		Generator: func() string {
			return "generated-id"
		},
	}))

	app.Get("/id", func(c *aarv.Context) error {
		return c.Text(http.StatusOK, c.RequestID()+"|"+FromContext(c.Context()))
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/id", nil))

	if got := rec.Header().Get("X-Request-ID"); got != "generated-id" {
		t.Fatalf("expected generated-id header, got %q", got)
	}
	if body := rec.Body.String(); body != "generated-id|generated-id" {
		t.Fatalf("unexpected body %q", body)
	}
}

func TestNewUsesIncomingHeaderWithoutAarvContext(t *testing.T) {
	handler := New(Config{Header: "X-Test-ID"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(FromContext(r.Context())))
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Test-ID", "incoming-id")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Test-ID"); got != "incoming-id" {
		t.Fatalf("expected propagated header, got %q", got)
	}
	if body := rec.Body.String(); body != "incoming-id" {
		t.Fatalf("unexpected body %q", body)
	}
}

func TestNewAdditionalBranches(t *testing.T) {
	t.Run("defaults header and generator when config fields are empty", func(t *testing.T) {
		app := aarv.New(aarv.WithBanner(false))
		app.Use(New(Config{
			Header:    "",
			Generator: nil,
		}))
		app.Get("/id", func(c *aarv.Context) error {
			if got := FromContext(c.Context()); got == "" {
				t.Fatal("expected request id in aarv context")
			}
			return c.NoContent(http.StatusNoContent)
		})

		req := httptest.NewRequest(http.MethodGet, "/id", nil)
		req.Header.Set("X-Request-ID", "provided-id")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)

		if got := rec.Header().Get("X-Request-ID"); got != "provided-id" {
			t.Fatalf("expected default header propagation, got %q", got)
		}
	})

	t.Run("stdlib path generates id when header missing", func(t *testing.T) {
		handler := New(Config{
			Header: "X-Test-ID",
			Generator: func() string {
				return "generated-direct"
			},
		})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(FromContext(r.Context())))
		}))

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

		if got := rec.Header().Get("X-Test-ID"); got != "generated-direct" {
			t.Fatalf("expected generated header, got %q", got)
		}
		if body := rec.Body.String(); body != "generated-direct" {
			t.Fatalf("unexpected generated body %q", body)
		}
	})

	t.Run("native path with FastGenerator", func(t *testing.T) {
		app := aarv.New(aarv.WithBanner(false))
		app.Use(New(FastConfig()))
		app.Get("/id", func(c *aarv.Context) error {
			return c.Text(http.StatusOK, c.RequestID())
		})

		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/id", nil))

		id := rec.Body.String()
		if len(id) != 26 {
			t.Fatalf("expected 26-char ULID from FastGenerator, got %q", id)
		}
		for _, r := range id {
			if !strings.ContainsRune(ulidEncoding, r) {
				t.Fatalf("unexpected ulid rune %q in FastGenerator output", r)
			}
		}
	})

	t.Run("stdlib path updates aarv context when present", func(t *testing.T) {
		app := aarv.New(aarv.WithBanner(false))
		app.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				next.ServeHTTP(w, r)
			})
		})
		app.Use(New(Config{
			Header: "X-Test-ID",
			Generator: func() string {
				return "generated-aarv"
			},
		}))
		app.Get("/id", func(c *aarv.Context) error {
			return c.Text(http.StatusOK, c.RequestID()+"|"+FromContext(c.Context()))
		})

		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/id", nil))

		if got := rec.Header().Get("X-Test-ID"); got != "generated-aarv" {
			t.Fatalf("expected generated aarv header, got %q", got)
		}
		if body := rec.Body.String(); body != "generated-aarv|generated-aarv" {
			t.Fatalf("unexpected aarv body %q", body)
		}
	})
}

func TestNewPrefixAppliedToGeneratedIDs(t *testing.T) {
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{
		Prefix: "svc-",
		Generator: func() string {
			return "abc123"
		},
	}))
	app.Get("/id", func(c *aarv.Context) error {
		return c.Text(http.StatusOK, c.RequestID())
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/id", nil))

	if got := rec.Body.String(); got != "svc-abc123" {
		t.Fatalf("expected prefixed id, got %q", got)
	}
	if got := rec.Header().Get("X-Request-ID"); got != "svc-abc123" {
		t.Fatalf("expected prefixed header, got %q", got)
	}
}

func TestNewPrefixNotAppliedToIncomingIDs(t *testing.T) {
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{Prefix: "svc-"}))
	app.Get("/id", func(c *aarv.Context) error {
		return c.Text(http.StatusOK, c.RequestID())
	})

	req := httptest.NewRequest("GET", "/id", nil)
	req.Header.Set("X-Request-ID", "incoming-123")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if got := rec.Body.String(); got != "incoming-123" {
		t.Fatalf("expected unprefixed incoming id, got %q", got)
	}
}

func TestNewPrefixStdlibPath(t *testing.T) {
	handler := New(Config{
		Prefix: "api-",
		Generator: func() string {
			return "gen456"
		},
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(FromContext(r.Context())))
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	if got := rec.Header().Get("X-Request-ID"); got != "api-gen456" {
		t.Fatalf("expected prefixed header in stdlib path, got %q", got)
	}
	if got := rec.Body.String(); got != "api-gen456" {
		t.Fatalf("expected prefixed body in stdlib path, got %q", got)
	}
}

func TestNewPrefixNotAppliedToIncomingIDsStdlibPath(t *testing.T) {
	handler := New(Config{
		Prefix: "svc-",
		Generator: func() string {
			return "should-not-appear"
		},
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(FromContext(r.Context())))
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Request-ID", "external-999")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Request-ID"); got != "external-999" {
		t.Fatalf("expected unprefixed incoming header in stdlib path, got %q", got)
	}
	if got := rec.Body.String(); got != "external-999" {
		t.Fatalf("expected unprefixed incoming body in stdlib path, got %q", got)
	}
}

func TestFastGeneratorUniqueness(t *testing.T) {
	ids := make(map[string]struct{}, 10000)
	for i := 0; i < 10000; i++ {
		id := FastGenerator()
		if _, dup := ids[id]; dup {
			t.Fatalf("duplicate FastGenerator ID at iteration %d: %q", i, id)
		}
		ids[id] = struct{}{}
	}
}

func TestFastConfig(t *testing.T) {
	cfg := FastConfig()
	if cfg.Header != "X-Request-ID" {
		t.Fatalf("expected default header, got %q", cfg.Header)
	}
	if cfg.Generator == nil {
		t.Fatal("expected fast generator to be configured")
	}
	id := cfg.Generator()
	if len(id) != 26 {
		t.Fatalf("expected 26-char ULID from fast config, got %q", id)
	}
}

func BenchmarkGenerateULID(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		GenerateULID()
	}
}

func BenchmarkFastGenerator(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		FastGenerator()
	}
}

func BenchmarkGenerateULID_Parallel(b *testing.B) {
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			GenerateULID()
		}
	})
}

func BenchmarkFastGenerator_Parallel(b *testing.B) {
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			FastGenerator()
		}
	})
}

func BenchmarkMiddleware_CryptoULID(b *testing.B) {
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New()) // default: GenerateULID with crypto/rand
	app.Get("/", func(c *aarv.Context) error {
		return c.NoContent(200)
	})

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	}
}

func BenchmarkMiddleware_FastGenerator(b *testing.B) {
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(FastConfig()))
	app.Get("/", func(c *aarv.Context) error {
		return c.NoContent(200)
	})

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	}
}
