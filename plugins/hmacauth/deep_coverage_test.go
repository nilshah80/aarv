package hmacauth

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/nilshah80/aarv"
)

func TestStdlibSkipperWithBridgeBypassesUnsignedRequest(t *testing.T) {
	cfg, _ := newConfig(t, NewMemoryNonceStore(64))
	cfg.Skipper = func(c *aarv.Context) bool { return c.Path() == "/health" }

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(cfg), stdlibSibling())
	app.Get("/health", func(c *aarv.Context) error { return c.Text(http.StatusOK, "ok") })

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestVerifyStdlibSuccessWithBridgedContextAndObserver(t *testing.T) {
	cfg, client := newConfig(t, NewMemoryNonceStore(64))
	var events []Event
	cfg.Observer = func(c *aarv.Context, e Event) {
		if c == nil {
			t.Error("observer context = nil, want bridged aarv.Context")
		}
		events = append(events, e)
	}

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(cfg), stdlibSibling())
	app.Get("/whoami", func(c *aarv.Context) error {
		got, ok := From(c)
		if !ok || got.ClientID != client.ClientID {
			t.Fatalf("From(c) = (%+v, %v), want client %q", got, ok, client.ClientID)
		}
		gotCtx, ok := FromContext(c.Context())
		if !ok || gotCtx.ClientID != client.ClientID {
			t.Fatalf("FromContext(c.Context()) = (%+v, %v), want client %q", gotCtx, ok, client.ClientID)
		}
		return c.NoContent(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/whoami", nil)
	signRequest(t, req, nil, client, 1735000000, "nonce-stdlib-bridge")
	req.Body = nil

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d body=%s, want 204", rec.Code, rec.Body.String())
	}
	if len(events) != 1 || events[0].Outcome != OutcomeOK {
		t.Fatalf("events = %+v, want one OutcomeOK", events)
	}
}

func TestVerifyNativeNilBodyAccepts(t *testing.T) {
	cfg, client := newConfig(t, NewMemoryNonceStore(64))
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(cfg))
	app.Get("/nil-body", func(c *aarv.Context) error { return c.NoContent(http.StatusNoContent) })

	req := httptest.NewRequest(http.MethodGet, "/nil-body", nil)
	signRequest(t, req, nil, client, 1735000000, "nonce-native-nil-body")
	req.Body = nil

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d body=%s, want 204", rec.Code, rec.Body.String())
	}
}

func TestVerifyNativeObserverOnBodyReadError(t *testing.T) {
	cfg, client := newConfig(t, NewMemoryNonceStore(64))
	var events []Event
	cfg.Observer = func(_ *aarv.Context, e Event) { events = append(events, e) }

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(cfg))
	app.Post("/x", func(c *aarv.Context) error {
		t.Fatal("handler reached after body read failure")
		return c.NoContent(http.StatusOK)
	})

	body := []byte("signed body")
	req := httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader(body))
	signRequest(t, req, body, client, 1735000000, "nonce-native-read-fail")
	req.Body = io.NopCloser(&erroringReader{err: errBodyTransport})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if len(events) != 1 || events[0].Outcome != OutcomeUnauthorized || events[0].Status != http.StatusUnauthorized {
		t.Fatalf("events = %+v, want one unauthorized body-read event", events)
	}
}

func TestVerifyNativeObserverOnVerifyFailure(t *testing.T) {
	cfg, client := newConfig(t, NewMemoryNonceStore(64))
	var events []Event
	cfg.Observer = func(_ *aarv.Context, e Event) { events = append(events, e) }

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(cfg))
	app.Get("/x", func(c *aarv.Context) error {
		t.Fatal("handler reached after bad signature")
		return c.NoContent(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/x", http.NoBody)
	signRequest(t, req, nil, client, 1735000000, "nonce-native-bad-sig")
	req.Header.Set(DefaultSignatureHeader, strings.Repeat("0", 64))

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if len(events) != 1 || events[0].Outcome != OutcomeSignatureInvalid {
		t.Fatalf("events = %+v, want one signature-invalid event", events)
	}
}

func TestVerifyRejectsClientWithoutSecret(t *testing.T) {
	n := normalize(Config{
		Validator: func(id string) (Client, error) {
			return Client{ClientID: id}, nil
		},
		SkewSeconds: 60,
		Now:         stubClock(1735000000),
	})
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	signRequest(t, req, nil, Client{ClientID: "x", Secret: []byte("k")}, 1735000000, "nonce-no-secret")

	res := n.verify(context.Background(),
		req.Header.Get(DefaultClientIDHeader),
		req.Header.Get(DefaultTimestampHeader),
		req.Header.Get(DefaultNonceHeader),
		req.Header.Get(DefaultSignatureHeader),
		req.Method, req.URL.Path, req.URL.Query(), nil)
	if res.ok || res.outcome != OutcomeUnauthorized {
		t.Fatalf("verify result = %+v, want unauthorized rejection", res)
	}
}

func TestErrNativeDefaultStatus(t *testing.T) {
	cfg, _ := newConfig(t, NewMemoryNonceStore(64))
	n := normalize(cfg)
	app := aarv.New(aarv.WithBanner(false))
	app.Get("/x", func(c *aarv.Context) error {
		return n.errNative(c, 0, "denied")
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestErrStdlibDefaultStatus(t *testing.T) {
	cfg, _ := newConfig(t, NewMemoryNonceStore(64))
	n := normalize(cfg)
	rec := httptest.NewRecorder()
	n.errStdlib(rec, httptest.NewRequest(http.MethodGet, "/x", nil), 0, "denied")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestLoadVectorsErrors(t *testing.T) {
	if _, err := loadVectors(fstest.MapFS{}); err == nil || !strings.Contains(err.Error(), "read vectors") {
		t.Fatalf("missing vectors error = %v, want read vectors error", err)
	}
	if _, err := loadVectors(fstest.MapFS{
		"testdata/vectors.json": &fstest.MapFile{Data: []byte("{")},
	}); err == nil || !strings.Contains(err.Error(), "parse vectors") {
		t.Fatalf("bad vectors error = %v, want parse vectors error", err)
	}
}

func TestVectorHelpersErrorBranches(t *testing.T) {
	if b, err := (Vector{}).Body(); err != nil || b != nil {
		t.Fatalf("empty Body() = (%q, %v), want nil, nil", b, err)
	}
	if _, err := (Vector{BodyB64: "not-base64"}).Body(); err == nil {
		t.Fatal("Body() with bad base64 returned nil error")
	}
	if _, err := (Vector{SecretHex: "not-hex"}).Secret(); err == nil {
		t.Fatal("Secret() with bad hex returned nil error")
	}
	if v, err := (Vector{}).Values(); err != nil || len(v) != 0 {
		t.Fatalf("empty Values() = (%v, %v), want empty, nil", v, err)
	}
	if _, err := (Vector{Query: "%zz"}).Values(); err == nil {
		t.Fatal("Values() with malformed query returned nil error")
	}
}
