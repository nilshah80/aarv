package aarv

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestClient provides a fluent API for testing aarv handlers without starting a server.
type TestClient struct {
	app     *App
	headers http.Header
	cookies []*http.Cookie
	query   map[string]string
}

// NewTestClient creates a TestClient for the given App.
func NewTestClient(app *App) *TestClient {
	return &TestClient{
		app:     app,
		headers: make(http.Header),
		query:   make(map[string]string),
	}
}

// WithHeader adds a header to the test request.
func (tc *TestClient) WithHeader(key, value string) *TestClient {
	clone := tc.clone()
	clone.headers.Set(key, value)
	return clone
}

// WithCookie adds a cookie to the test request.
func (tc *TestClient) WithCookie(cookie *http.Cookie) *TestClient {
	clone := tc.clone()
	clone.cookies = append(clone.cookies, cookie)
	return clone
}

// WithQuery adds a query parameter.
func (tc *TestClient) WithQuery(key, value string) *TestClient {
	clone := tc.clone()
	clone.query[key] = value
	return clone
}

// WithBearer adds an Authorization: Bearer header.
func (tc *TestClient) WithBearer(token string) *TestClient {
	return tc.WithHeader("Authorization", "Bearer "+token)
}

func (tc *TestClient) clone() *TestClient {
	c := &TestClient{
		app:     tc.app,
		headers: tc.headers.Clone(),
		cookies: append([]*http.Cookie{}, tc.cookies...),
		query:   make(map[string]string),
	}
	for k, v := range tc.query {
		c.query[k] = v
	}
	return c
}

// Get sends a GET request.
func (tc *TestClient) Get(path string) *TestResponse {
	return tc.doRequest("GET", path, nil)
}

// Post sends a POST request with a JSON body.
func (tc *TestClient) Post(path string, body any) *TestResponse {
	return tc.doRequest("POST", path, body)
}

// Put sends a PUT request with a JSON body.
func (tc *TestClient) Put(path string, body any) *TestResponse {
	return tc.doRequest("PUT", path, body)
}

// Delete sends a DELETE request.
func (tc *TestClient) Delete(path string) *TestResponse {
	return tc.doRequest("DELETE", path, nil)
}

// Patch sends a PATCH request with a JSON body.
func (tc *TestClient) Patch(path string, body any) *TestResponse {
	return tc.doRequest("PATCH", path, body)
}

// Do sends a custom http.Request.
func (tc *TestClient) Do(req *http.Request) *TestResponse {
	rec := httptest.NewRecorder()
	tc.app.ServeHTTP(rec, req)
	return newTestResponse(rec)
}

func (tc *TestClient) doRequest(method, path string, body any) *TestResponse {
	var bodyReader *bytes.Buffer
	if body != nil {
		data, _ := json.Marshal(body)
		bodyReader = bytes.NewBuffer(data)
	} else {
		bodyReader = &bytes.Buffer{}
	}

	req := httptest.NewRequest(method, path, bodyReader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	for k, vals := range tc.headers {
		for _, v := range vals {
			req.Header.Add(k, v)
		}
	}
	for _, c := range tc.cookies {
		req.AddCookie(c)
	}
	q := req.URL.Query()
	for k, v := range tc.query {
		q.Set(k, v)
	}
	req.URL.RawQuery = q.Encode()

	rec := httptest.NewRecorder()
	tc.app.ServeHTTP(rec, req)
	return newTestResponse(rec)
}

// TestResponse wraps an httptest.ResponseRecorder result.
type TestResponse struct {
	Status  int
	Headers http.Header
	Body    []byte
}

func newTestResponse(rec *httptest.ResponseRecorder) *TestResponse {
	return &TestResponse{
		Status:  rec.Code,
		Headers: rec.Header(),
		Body:    rec.Body.Bytes(),
	}
}

// JSON unmarshals the response body into dest.
func (tr *TestResponse) JSON(dest any) error {
	return json.Unmarshal(tr.Body, dest)
}

// Text returns the body as a string.
func (tr *TestResponse) Text() string {
	return string(tr.Body)
}

// AssertStatus checks that the response status matches expected.
func (tr *TestResponse) AssertStatus(t *testing.T, expected int) {
	t.Helper()
	if tr.Status != expected {
		t.Errorf("expected status %d, got %d. Body: %s", expected, tr.Status, string(tr.Body))
	}
}
