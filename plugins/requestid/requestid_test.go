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
