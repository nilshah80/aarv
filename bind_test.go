package aarv

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"

	"testing"
)

type serveOnlyHandler struct{}

func (serveOnlyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusIMUsed)
}

func TestBind(t *testing.T) {
	type Req struct {
		Name  string `json:"name" validate:"required,min=2"`
		Email string `json:"email" validate:"required,email"`
	}
	type Res struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}

	app := New(WithBanner(false))
	app.Post("/users", Bind(func(c *Context, req Req) (Res, error) {
		return Res{ID: "1", Name: req.Name}, nil
	}))

	tc := NewTestClient(app)

	// Valid request
	resp := tc.Post("/users", map[string]string{"name": "Alice", "email": "alice@test.com"})
	resp.AssertStatus(t, 200)

	var res Res
	if err := resp.JSON(&res); err != nil {
		t.Fatal(err)
	}
	if res.Name != "Alice" {
		t.Errorf("expected 'Alice', got %q", res.Name)
	}
}

func TestBindValidation(t *testing.T) {
	type Req struct {
		Name  string `json:"name" validate:"required,min=2"`
		Email string `json:"email" validate:"required,email"`
	}
	type Res struct {
		ID string `json:"id"`
	}

	app := New(WithBanner(false))
	app.Post("/users", Bind(func(c *Context, req Req) (Res, error) {
		return Res{ID: "1"}, nil
	}))

	tc := NewTestClient(app)

	// Missing required fields
	resp := tc.Post("/users", map[string]string{})
	resp.AssertStatus(t, 422)
}

func TestBindReqWithParams(t *testing.T) {
	type Req struct {
		ID     string `param:"id"`
		Fields string `query:"fields" default:"*"`
	}

	app := New(WithBanner(false))
	app.Get("/users/{id}", BindReq(func(c *Context, req Req) error {
		return c.JSON(200, map[string]string{
			"id":     req.ID,
			"fields": req.Fields,
		})
	}))

	tc := NewTestClient(app)
	resp := tc.WithQuery("fields", "name,email").Get("/users/abc")
	resp.AssertStatus(t, 200)

	var body map[string]string
	if err := resp.JSON(&body); err != nil {
		t.Fatal(err)
	}
	if body["id"] != "abc" {
		t.Errorf("expected 'abc', got %q", body["id"])
	}
	if body["fields"] != "name,email" {
		t.Errorf("expected 'name,email', got %q", body["fields"])
	}
}

func TestBindReqDefaults(t *testing.T) {
	type Req struct {
		ID     string `param:"id"`
		Fields string `query:"fields" default:"*"`
	}

	app := New(WithBanner(false))
	app.Get("/users/{id}", BindReq(func(c *Context, req Req) error {
		return c.JSON(200, map[string]string{"fields": req.Fields})
	}))

	tc := NewTestClient(app)
	resp := tc.Get("/users/abc")
	resp.AssertStatus(t, 200)

	var body map[string]string
	if err := resp.JSON(&body); err != nil {
		t.Fatal(err)
	}
	if body["fields"] != "*" {
		t.Errorf("expected '*', got %q", body["fields"])
	}
}

func TestBindAllSources(t *testing.T) {
	type AdvancedReq struct {
		ID       int      `param:"id"`
		Active   bool     `query:"active"`
		UserGen  string   `header:"User-Agent"`
		Session  string   `cookie:"session"`
		Age      int64    `form:"age"`
		Rating   float64  `form:"rating"`
		Items    []string `query:"items"`
		Payload  string   `json:"payload"`
		DefField string   `query:"def" default:"xyz"`
	}

	app := New(WithBanner(false))
	app.Post("/advanced/{id}", BindReq(func(c *Context, req AdvancedReq) error {
		return c.JSON(200, req)
	}))

	tc := NewTestClient(app)

	reqBody := map[string]string{"payload": "hello"}
	resp := tc.
		WithHeader("User-Agent", "TestClient/1.0").
		WithCookie(&http.Cookie{Name: "session", Value: "abc-123"}).
		Post("/advanced/42?active=true&items=a&items=b", reqBody)

	resp.AssertStatus(t, 200)

	var res AdvancedReq
	if err := resp.JSON(&res); err != nil {
		t.Fatal(err)
	}

	if res.ID != 42 {
		t.Errorf("ID mismatch: %v", res.ID)
	}
	if !res.Active {
		t.Errorf("Active mismatch: %v", res.Active)
	}
	if res.UserGen != "TestClient/1.0" {
		t.Errorf("Header mismatch: %v", res.UserGen)
	}
	if res.Session != "abc-123" {
		t.Errorf("Cookie mismatch: %v", res.Session)
	}
	if len(res.Items) != 1 || res.Items[0] != "a" {
		t.Errorf("Slice mismatch: %v", res.Items)
	}
	if res.Payload != "hello" {
		t.Errorf("JSON mismatch: %v", res.Payload)
	}
	if res.DefField != "xyz" {
		t.Errorf("Default mismatch: %v", res.DefField)
	}
}

func TestBindingAndHandlerAdditionalCoverage(t *testing.T) {
	t.Run("bind response and adapt", func(t *testing.T) {
		app := New(WithBanner(false))
		app.Get("/bindres", BindRes(func(c *Context) (map[string]string, error) {
			return map[string]string{"ok": "true"}, nil
		}))
		app.Get("/adapt", Adapt(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(r.Method))
		}))
		app.Get("/written", BindRes(func(c *Context) (map[string]string, error) {
			_ = c.NoContent(http.StatusNoContent)
			return map[string]string{"ignored": "true"}, nil
		}))

		tc := NewTestClient(app)
		tc.Get("/bindres").AssertStatus(t, http.StatusOK)
		tc.Get("/adapt").AssertStatus(t, http.StatusAccepted)
		tc.Get("/written").AssertStatus(t, http.StatusNoContent)

		app.Get("/bind-written", Bind(func(c *Context, req struct{}) (map[string]string, error) {
			_ = c.NoContent(http.StatusAccepted)
			return map[string]string{"ignored": "true"}, nil
		}))
		tc.Get("/bind-written").AssertStatus(t, http.StatusAccepted)

		app.Get("/bindres-err", BindRes(func(c *Context) (map[string]string, error) {
			return nil, errors.New("bindres failed")
		}))
		tc.Get("/bindres-err").AssertStatus(t, http.StatusInternalServerError)
	})

	t.Run("struct binder and parameter parsing", func(t *testing.T) {
		type EmbeddedBody struct {
			Body string `json:"body"`
		}
		type Embedded struct {
			Role string `header:"X-Role"`
		}
		type payload struct {
			EmbeddedBody
			Embedded
			ID      int         `param:"id"`
			Name    string      `query:"name"`
			Enabled bool        `query:"enabled"`
			Token   string      `cookie:"token"`
			Age     int         `form:"age"`
			Tags    []string    `query:"tags"`
			Parsed  parserValue `query:"parsed"`
			Body    string      `json:"body"`
			Default int         `default:"7"`
		}

		req := httptest.NewRequest(http.MethodPost, "/users/42?name=alice&enabled=true&tags=a,b&parsed=ok", strings.NewReader("age=12"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-Role", "admin")
		req.AddCookie(&http.Cookie{Name: "token", Value: "cookie-token"})
		req.SetPathValue("id", "42")

		app := New(WithBanner(false))
		ctx, _ := newAppContext(app, req)
		defer app.ReleaseContext(ctx)

		sb := buildStructBinder(reflect.TypeOf(payload{}))
		if sb == nil || !sb.needBody {
			t.Fatal("expected struct binder with body metadata")
		}

		var got payload
		if err := sb.bind(ctx, &got); err != nil {
			t.Fatalf("unexpected bind error: %v", err)
		}
		sb.applyDefaults(&got)

		if got.ID != 42 || got.Name != "alice" || !got.Enabled || got.Token != "cookie-token" || got.Age != 12 || got.Role != "admin" || got.Default != 7 || string(got.Parsed) != "OK" {
			t.Fatalf("unexpected bound payload: %+v", got)
		}
		if len(got.Tags) != 2 || got.Tags[0] != "a" || got.Tags[1] != "b" {
			t.Fatalf("unexpected tag values: %#v", got.Tags)
		}

		field := reflect.ValueOf(&got.Parsed).Elem()
		if err := parseWithParamParser(field, "bad"); err == nil {
			t.Fatal("expected parameter parser error")
		}
		if buildStructBinder(reflect.TypeOf(1)) != nil {
			t.Fatal("non-struct binder should be nil")
		}
		if buildStructBinder(reflect.TypeOf(&payload{})) == nil {
			t.Fatal("pointer struct binder should be supported")
		}
		type hiddenOnly struct {
			visible string `query:"hidden"` //nolint:unused // intentionally unexported to test binder skips it
		}
		if hidden := buildStructBinder(reflect.TypeOf(hiddenOnly{})); hidden == nil || len(hidden.fields) != 0 {
			t.Fatalf("expected unexported binding fields to be skipped, got %+v", hidden)
		}
	})

	t.Run("custom binder, query and form helpers, set field value", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/?value=from-query&page=9", strings.NewReader("age=5&name=form-name"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		app := New(WithBanner(false))
		ctx, _ := newAppContext(app, req)
		defer app.ReleaseContext(ctx)

		sb := buildStructBinder(reflect.TypeOf(customBinderPayload{}))
		var custom customBinderPayload
		if err := sb.bind(ctx, &custom); err != nil {
			t.Fatalf("unexpected custom binder error: %v", err)
		}
		if custom.Value != "from-query" {
			t.Fatalf("unexpected custom binder payload: %+v", custom)
		}

		var queryDest struct {
			Page int `query:"page" default:"1"`
		}
		if err := bindQueryParams(ctx, &queryDest); err != nil || queryDest.Page != 9 {
			t.Fatalf("unexpected query bind result: %+v err=%v", queryDest, err)
		}

		var formDest struct {
			Age  int    `form:"age"`
			Name string `json:"name"`
		}
		if err := bindFormValues(ctx, &formDest); err != nil {
			t.Fatalf("unexpected form bind error: %v", err)
		}
		if formDest.Age != 5 || formDest.Name != "form-name" {
			t.Fatalf("unexpected form bind result: %+v", formDest)
		}

		var badInt struct {
			Page int `query:"page"`
		}
		ctx.req.URL.RawQuery = "page=bad"
		ctx.query = nil
		if err := bindQueryParams(ctx, &badInt); err == nil {
			t.Fatal("expected query bind failure")
		}

		type parserFailurePayload struct {
			Value parserValue `query:"value"`
		}
		ctx.req.URL.RawQuery = "value=bad"
		ctx.query = nil
		sb = buildStructBinder(reflect.TypeOf(parserFailurePayload{}))
		var parserFailure parserFailurePayload
		if err := sb.bind(ctx, &parserFailure); err == nil {
			t.Fatal("expected parser-based bind failure")
		}

		type bindFailurePayload struct {
			Page int `query:"page"`
		}
		ctx.req.URL.RawQuery = "page=bad"
		ctx.query = nil
		sb = buildStructBinder(reflect.TypeOf(bindFailurePayload{}))
		var bindFailure bindFailurePayload
		if err := sb.bind(ctx, &bindFailure); err == nil {
			t.Fatal("expected setFieldValue bind failure")
		}

		if err := setFieldValue(reflect.ValueOf(&badInt.Page).Elem(), "bad"); err == nil {
			t.Fatal("expected int parsing error")
		}
		var unsupported struct {
			V map[string]string
		}
		if err := setFieldValue(reflect.ValueOf(&unsupported.V).Elem(), "x"); err == nil {
			t.Fatal("expected unsupported kind error")
		}
	})

	t.Run("bind body failure and handler adaptation", func(t *testing.T) {
		type request struct {
			Name string `json:"name"`
		}
		app := New(WithBanner(false))
		app.Post("/bind", Bind(func(c *Context, req request) (map[string]string, error) {
			return map[string]string{"name": req.Name}, nil
		}))

		req := httptest.NewRequest(http.MethodPost, "/bind", strings.NewReader("{invalid"))
		req.Header.Set("Content-Type", "application/json")
		resp := NewTestClient(app).Do(req)
		resp.AssertStatus(t, http.StatusBadRequest)

		h := adaptHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
		}))
		ctx, rec := newAppContext(app, httptest.NewRequest(http.MethodGet, "/", nil))
		defer app.ReleaseContext(ctx)
		if err := h(ctx); err != nil || rec.Code != http.StatusCreated {
			t.Fatalf("unexpected adapted handler result: code=%d err=%v", rec.Code, err)
		}

		h = adaptHandler(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusAccepted)
		})
		ctx, rec = newAppContext(app, httptest.NewRequest(http.MethodGet, "/", nil))
		if err := h(ctx); err != nil || rec.Code != http.StatusAccepted {
			t.Fatalf("unexpected std func handler result: code=%d err=%v", rec.Code, err)
		}

		h = adaptHandler(func(c *Context) error { return c.NoContent(http.StatusPartialContent) })
		ctx, rec = newAppContext(app, httptest.NewRequest(http.MethodGet, "/", nil))
		if err := h(ctx); err != nil || rec.Code != http.StatusPartialContent {
			t.Fatalf("unexpected context func handler result: code=%d err=%v", rec.Code, err)
		}

		rawHandler := HandlerFunc(func(c *Context) error { return c.NoContent(http.StatusResetContent) })
		h = adaptHandler(rawHandler)
		ctx, rec = newAppContext(app, httptest.NewRequest(http.MethodGet, "/", nil))
		if err := h(ctx); err != nil || rec.Code != http.StatusResetContent {
			t.Fatalf("unexpected raw handler result: code=%d err=%v", rec.Code, err)
		}

		h = adaptHandler(serveOnlyHandler{})
		ctx, rec = newAppContext(app, httptest.NewRequest(http.MethodGet, "/", nil))
		if err := h(ctx); err != nil || rec.Code != http.StatusIMUsed {
			t.Fatalf("unexpected http.Handler adapter result: code=%d err=%v", rec.Code, err)
		}

		defer func() {
			if recover() == nil {
				t.Fatal("expected unsupported handler panic")
			}
		}()
		_ = adaptHandler(123)
	})
}

func TestBindErrorHandlingCases(t *testing.T) {
	type req struct {
		Name string `json:"name" validate:"required"`
	}

	app := New(WithBanner(false))
	app.Post("/bind", BindReq(func(c *Context, payload req) error {
		return c.JSON(http.StatusOK, payload)
	}))

	t.Run("malformed json small payload", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/bind", strings.NewReader(`{"name":`))
		req.Header.Set("Content-Type", "application/json")
		req.ContentLength = int64(len(`{"name":`))

		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for malformed json, got %d", rec.Code)
		}
	})

	t.Run("malformed json large payload", func(t *testing.T) {
		bad := `{"name":"` + strings.Repeat("x", 11000)
		req := httptest.NewRequest(http.MethodPost, "/bind", strings.NewReader(bad))
		req.Header.Set("Content-Type", "application/json")
		req.ContentLength = int64(len(bad))

		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for large malformed json, got %d", rec.Code)
		}
	})

	t.Run("missing required field", func(t *testing.T) {
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/bind", strings.NewReader(`{}`)))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("expected 422 for missing required fields, got %d", rec.Code)
		}
	})
}

func TestBindNilDestinationCodecFallback(t *testing.T) {
	app := New(WithBanner(false), WithCodec(nil))
	app.Post("/decode", BindReq(func(c *Context, payload struct {
		Name string `json:"name"`
	}) error {
		return c.Text(http.StatusOK, payload.Name)
	}))

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/decode", strings.NewReader(`{"name":"ok"}`)))
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "ok" {
		t.Fatalf("expected default codec to decode successfully, got status=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestBinderBranchCoverage(t *testing.T) {
	app := New(WithBanner(false))

	req := httptest.NewRequest(http.MethodGet, "/?value=xff", nil)
	req.RemoteAddr = "9.9.9.9:1234"
	req.Header.Set("X-Forwarded-For", "3.3.3.3, 4.4.4.4")
	ctx, _ := newAppContext(app, req)
	if ctx.Scheme() != "http" || ctx.RealIP() != "3.3.3.3" {
		t.Fatalf("unexpected scheme or forwarded IP: %s %s", ctx.Scheme(), ctx.RealIP())
	}
	app.ReleaseContext(ctx)

	valueHandler := adaptHandler(func(c *Context) error { return c.NoContent(http.StatusOK) })
	handlerCtx, rec := newAppContext(app, httptest.NewRequest(http.MethodGet, "/", nil))
	if err := valueHandler(handlerCtx); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("unexpected framework handler result: code=%d err=%v", rec.Code, err)
	}
	app.ReleaseContext(handlerCtx)

	stdHandler := adaptHandler(http.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})))
	handlerCtx, rec = newAppContext(app, httptest.NewRequest(http.MethodGet, "/", nil))
	if err := stdHandler(handlerCtx); err != nil || rec.Code != http.StatusAccepted {
		t.Fatalf("unexpected std handler result: code=%d err=%v", rec.Code, err)
	}
	app.ReleaseContext(handlerCtx)

	type request struct {
		Name string `json:"name"`
	}
	app.Post("/bindreq", BindReq(func(c *Context, req request) error {
		if req.Name == "err" {
			return errors.New("handler error")
		}
		return c.NoContent(http.StatusCreated)
	}))
	req = httptest.NewRequest(http.MethodPost, "/bindreq", strings.NewReader(`{"name":"err"}`))
	req.Header.Set("Content-Type", "application/json")
	resp := NewTestClient(app).Do(req)
	resp.AssertStatus(t, http.StatusInternalServerError)

	req = httptest.NewRequest(http.MethodPost, "/bindreq", strings.NewReader(`{"name":"ok"}`))
	req.Header.Set("Content-Type", "application/json")
	resp = NewTestClient(app).Do(req)
	resp.AssertStatus(t, http.StatusCreated)

	field := reflect.New(reflect.TypeOf(parserValue(""))).Elem()
	ptrField := reflect.New(field.Type())
	if err := parseWithParamParser(ptrField, "ok"); err != nil {
		t.Fatalf("unexpected pointer parser error: %v", err)
	}
	var nilParser *parserValue
	if err := parseWithParamParser(reflect.ValueOf(&nilParser).Elem(), "next"); err != nil {
		t.Fatalf("unexpected nil pointer parser init error: %v", err)
	}

	var u uint64
	if err := setFieldValue(reflect.ValueOf(&u).Elem(), "5"); err != nil || u != 5 {
		t.Fatalf("unexpected uint value: %d err=%v", u, err)
	}
	var f float64
	if err := setFieldValue(reflect.ValueOf(&f).Elem(), "2.5"); err != nil || f != 2.5 {
		t.Fatalf("unexpected float value: %f err=%v", f, err)
	}
	var b bool
	if err := setFieldValue(reflect.ValueOf(&b).Elem(), "true"); err != nil || !b {
		t.Fatalf("unexpected bool value: %v err=%v", b, err)
	}
	if err := setFieldValue(reflect.ValueOf(&u).Elem(), "bad"); err == nil {
		t.Fatal("expected uint parse failure")
	}
	if err := setFieldValue(reflect.ValueOf(&f).Elem(), "bad"); err == nil {
		t.Fatal("expected float parse failure")
	}
	if err := setFieldValue(reflect.ValueOf(&b).Elem(), "bad"); err == nil {
		t.Fatal("expected bool parse failure")
	}

	qreq := httptest.NewRequest(http.MethodGet, "/", nil)
	qctx, _ := newAppContext(app, qreq)
	var queryDefault struct {
		Page int `query:"page" default:"4"`
	}
	if err := bindQueryParams(qctx, &queryDefault); err != nil || queryDefault.Page != 4 {
		t.Fatalf("unexpected default query bind result: %+v err=%v", queryDefault, err)
	}
	var skippedQuery struct {
		Page int `query:"page"`
	}
	if err := bindQueryParams(qctx, &skippedQuery); err != nil {
		t.Fatalf("unexpected skipped query bind error: %v", err)
	}
	var noTagQuery struct {
		Page int
	}
	if err := bindQueryParams(qctx, &noTagQuery); err != nil {
		t.Fatalf("unexpected no-tag query bind error: %v", err)
	}
	var unexportedQuery struct {
		page int `query:"page"`
	}
	if err := bindQueryParams(qctx, &unexportedQuery); err != nil {
		t.Fatalf("unexpected unexported query bind error: %v", err)
	}
	app.ReleaseContext(qctx)

	formReq := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(""))
	formReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	formCtx, _ := newAppContext(app, formReq)
	var skipped struct {
		Skip string `json:"-"`
	}
	if err := bindFormValues(formCtx, &skipped); err != nil {
		t.Fatalf("unexpected skipped form bind error: %v", err)
	}
	var unexported struct {
		skip string `form:"skip"`
	}
	if err := bindFormValues(formCtx, &unexported); err != nil {
		t.Fatalf("unexpected unexported form bind error: %v", err)
	}
	var emptyForm struct {
		Name string `form:"name"`
	}
	if err := bindFormValues(formCtx, &emptyForm); err != nil {
		t.Fatalf("unexpected empty form bind error: %v", err)
	}
	badFormReq := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("age=bad"))
	badFormReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	badFormCtx, _ := newAppContext(app, badFormReq)
	var badForm struct {
		Age int `form:"age"`
	}
	if err := bindFormValues(badFormCtx, &badForm); err == nil {
		t.Fatal("expected form bind parse failure")
	}
	app.ReleaseContext(badFormCtx)
	app.ReleaseContext(formCtx)
}

func TestBindAdditionalErrorBranches(t *testing.T) {
	app := New(WithBanner(false))
	app.Get("/bind-error", Bind(func(c *Context, req struct {
		Page int `query:"page"`
	}) (map[string]int, error) {
		return map[string]int{"page": req.Page}, nil
	}))
	app.Get("/bind-handler-error", Bind(func(c *Context, req struct{}) (map[string]string, error) {
		return nil, errors.New("bind handler failed")
	}))
	app.Get("/bindreq-error", BindReq(func(c *Context, req struct {
		Page int `query:"page"`
	}) error {
		return c.NoContent(http.StatusNoContent)
	}))

	tc := NewTestClient(app)
	tc.Get("/bind-error?page=bad").AssertStatus(t, http.StatusBadRequest)
	tc.Get("/bind-handler-error").AssertStatus(t, http.StatusInternalServerError)
	tc.Get("/bindreq-error?page=bad").AssertStatus(t, http.StatusBadRequest)
}
