package aarv

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const maxReusableBodyCache = 16 << 10
const maxReusableStoreKeys = 32

// Context wraps the http.Request and http.ResponseWriter with convenience helpers.
// It is pooled via sync.Pool — do NOT hold references to it beyond the handler lifetime.
type Context struct {
	req               *http.Request
	res               http.ResponseWriter
	app               *App
	store             map[string]any
	requestID         string
	requestIDSet      bool
	query             url.Values // cached parsed query params
	bodyCache         []byte
	bodyRead          bool
	written           bool
	statusCode        int // pending status code for chaining
	cachedLogger      *slog.Logger
	pathParamNames    [16]string
	pathParamValues   [16]string
	pathParamCount    int
	pathParamsApplied bool
}

func (c *Context) reset(w http.ResponseWriter, r *http.Request) {
	c.req = r
	c.res = w
	if c.bodyCache != nil {
		if cap(c.bodyCache) > maxReusableBodyCache {
			c.bodyCache = nil
		} else {
			c.bodyCache = c.bodyCache[:0]
		}
	}
	c.bodyRead = false
	c.written = false
	c.statusCode = http.StatusOK
	c.query = nil
	c.cachedLogger = nil
	if len(c.store) > maxReusableStoreKeys {
		c.store = nil // release oversized maps
	} else if c.store != nil {
		clear(c.store)
	}
	c.requestID = ""
	c.requestIDSet = false
	c.pathParamCount = 0
	c.pathParamsApplied = false
}

// Request returns the underlying *http.Request.
func (c *Context) Request() *http.Request {
	c.materializePathParams()
	return c.req
}

// Response returns the underlying http.ResponseWriter.
func (c *Context) Response() http.ResponseWriter { return c.res }

// Context returns the request's context.Context.
func (c *Context) Context() context.Context { return c.req.Context() }

// SetContext replaces the request's context.
func (c *Context) SetContext(ctx context.Context) {
	prevReq := c.req
	if existing, ok := ctx.Value(ctxKey{}).(*Context); !ok || existing != c {
		ctx = context.WithValue(ctx, ctxKey{}, c)
	}
	req := c.req.WithContext(ctx)
	c.req = req
	c.pathParamsApplied = false
	if prevReq != nil && prevReq != req {
		deleteRequestContext(prevReq)
	}
}

// Method returns the HTTP method.
func (c *Context) Method() string { return c.req.Method }

// Path returns the URL path.
func (c *Context) Path() string { return c.req.URL.Path }

// Host returns the request host.
func (c *Context) Host() string { return c.req.Host }

// Scheme returns "https" or "http".
func (c *Context) Scheme() string {
	if c.IsTLS() {
		return "https"
	}
	if scheme := c.req.Header.Get("X-Forwarded-Proto"); scheme != "" {
		return scheme
	}
	return "http"
}

// IsTLS returns true if the request was served over TLS.
func (c *Context) IsTLS() bool { return c.req.TLS != nil }

// Protocol returns the protocol version string (e.g. "HTTP/2.0").
func (c *Context) Protocol() string { return c.req.Proto }

// RealIP extracts the client IP, respecting X-Real-IP and X-Forwarded-For headers.
// It only trusts proxy headers if the direct client IP is in the TrustedProxies list.
func (c *Context) RealIP() string {
	directIP, _, _ := net.SplitHostPort(c.req.RemoteAddr)
	if directIP == "" {
		directIP = c.req.RemoteAddr
	}

	// Only trust proxy headers if the direct connection is from a trusted proxy
	if c.app != nil && len(c.app.config.TrustedProxies) > 0 {
		if !c.isTrustedProxy(directIP) {
			return directIP
		}
	}

	if ip := c.req.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	if xff := c.req.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i > 0 {
			return strings.TrimSpace(xff[:i])
		}
		return xff
	}
	return directIP
}

// isTrustedProxy checks if the given IP is in the trusted proxies list.
func (c *Context) isTrustedProxy(ip string) bool {
	if c.app == nil || len(c.app.config.TrustedProxies) == 0 {
		return true // trust all if no proxies configured
	}
	parsedIP := net.ParseIP(ip)
	if parsedIP == nil {
		return false
	}
	for _, cidr := range c.app.config.TrustedProxies {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			// Not a CIDR, try as plain IP
			if cidr == ip {
				return true
			}
			continue
		}
		if network.Contains(parsedIP) {
			return true
		}
	}
	return false
}

// --- Path Parameters ---

// Param returns a path parameter by name.
func (c *Context) Param(name string) string {
	for i := 0; i < c.pathParamCount; i++ {
		if c.pathParamNames[i] == name {
			return c.pathParamValues[i]
		}
	}
	return c.req.PathValue(name)
}

// ParamInt returns a path parameter parsed as int.
func (c *Context) ParamInt(name string) (int, error) {
	return strconv.Atoi(c.Param(name))
}

// ParamInt64 returns a path parameter parsed as int64.
func (c *Context) ParamInt64(name string) (int64, error) {
	return strconv.ParseInt(c.Param(name), 10, 64)
}

// ParamUUID returns a path parameter validated as UUID format.
func (c *Context) ParamUUID(name string) (string, error) {
	v := c.Param(name)
	if !isValidUUID(v) {
		return "", fmt.Errorf("invalid UUID: %s", v)
	}
	return v, nil
}

func (c *Context) setDirectPathParam(name, value string) {
	if c.pathParamCount < len(c.pathParamNames) {
		c.pathParamNames[c.pathParamCount] = name
		c.pathParamValues[c.pathParamCount] = value
		c.pathParamCount++
		return
	}
	c.materializePathParams()
	c.req.SetPathValue(name, value)
}

func (c *Context) materializePathParams() {
	if c.pathParamsApplied || c.pathParamCount == 0 {
		return
	}
	for i := 0; i < c.pathParamCount; i++ {
		c.req.SetPathValue(c.pathParamNames[i], c.pathParamValues[i])
	}
	c.pathParamsApplied = true
}

// --- Query Parameters ---

// queryValues returns cached parsed query parameters.
func (c *Context) queryValues() url.Values {
	if c.query == nil {
		c.query = c.req.URL.Query()
	}
	return c.query
}

// Query returns a query parameter by name.
func (c *Context) Query(name string) string {
	return c.queryValues().Get(name)
}

// QueryDefault returns a query parameter or a fallback value.
func (c *Context) QueryDefault(name, fallback string) string {
	v := c.queryValues().Get(name)
	if v == "" {
		return fallback
	}
	return v
}

// QueryInt returns a query parameter parsed as int, with a fallback.
func (c *Context) QueryInt(name string, fallback int) int {
	v := c.queryValues().Get(name)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

// QueryInt64 returns a query parameter parsed as int64, with a fallback.
func (c *Context) QueryInt64(name string, fallback int64) int64 {
	v := c.queryValues().Get(name)
	if v == "" {
		return fallback
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return fallback
	}
	return n
}

// QueryFloat64 returns a query parameter parsed as float64, with a fallback.
func (c *Context) QueryFloat64(name string, fallback float64) float64 {
	v := c.queryValues().Get(name)
	if v == "" {
		return fallback
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return fallback
	}
	return f
}

// QueryBool returns a query parameter parsed as bool, with a fallback.
func (c *Context) QueryBool(name string, fallback bool) bool {
	v := c.queryValues().Get(name)
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}

// QuerySlice returns all values for a query parameter.
func (c *Context) QuerySlice(name string) []string {
	return c.queryValues()[name]
}

// QueryParams returns all query parameters.
func (c *Context) QueryParams() url.Values {
	return c.req.URL.Query()
}

// --- Headers ---

// Header returns a request header value.
func (c *Context) Header(name string) string {
	return c.req.Header.Get(name)
}

// SetHeader sets a response header.
func (c *Context) SetHeader(name, value string) {
	c.res.Header().Set(name, value)
}

func (c *Context) setContentType(value string) {
	c.res.Header()["Content-Type"] = []string{value}
}

// AddHeader adds a response header value.
func (c *Context) AddHeader(name, value string) {
	c.res.Header().Add(name, value)
}

// HeaderValues returns all values for a request header.
func (c *Context) HeaderValues(name string) []string {
	return c.req.Header.Values(name)
}

// --- Cookies ---

// Cookie returns a request cookie by name.
func (c *Context) Cookie(name string) (*http.Cookie, error) {
	return c.req.Cookie(name)
}

// SetCookie sets a response cookie.
func (c *Context) SetCookie(cookie *http.Cookie) {
	http.SetCookie(c.res, cookie)
}

// --- Body ---

// Body reads and caches the request body bytes. Subsequent calls return cached data.
func (c *Context) Body() ([]byte, error) {
	if c.bodyRead {
		return c.bodyCache, nil
	}
	data, err := readBodyInto(c.req.Body, c.bodyCache, c.req.ContentLength)
	if err != nil {
		return nil, err
	}
	c.bodyCache = data
	c.bodyRead = true
	return data, nil
}

func readBodyInto(r io.Reader, buf []byte, contentLength int64) ([]byte, error) {
	if contentLength >= 0 && contentLength <= int64(^uint(0)>>1) {
		size := int(contentLength)
		if cap(buf) < size {
			buf = make([]byte, size)
		} else {
			buf = buf[:size]
		}
		n, err := io.ReadFull(r, buf)
		if err == nil {
			return buf, nil
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return buf[:n], nil
		}
		return nil, err
	}

	var chunk [512]byte
	buf = buf[:0]
	for {
		n, err := r.Read(chunk[:])
		if n > 0 {
			buf = append(buf, chunk[:n]...)
		}
		if err == io.EOF {
			return buf, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

// Bind decodes the request body into dest using the configured codec.
func (c *Context) Bind(dest any) error {
	return c.BindJSON(dest)
}

// BindJSON decodes the JSON request body into dest.
func (c *Context) BindJSON(dest any) error {
	if c.req.ContentLength > 0 && c.req.ContentLength < 10240 {
		body, err := c.Body()
		if err != nil {
			return err
		}
		return c.app.codecUnmarshal(body, dest)
	}
	return c.app.codecDecode(c.req.Body, dest)
}

// BindQuery decodes query parameters into a struct using the query struct tag.
func (c *Context) BindQuery(dest any) error {
	// Delegated to binder — this is a convenience alias
	return bindQueryParams(c, dest)
}

// BindForm decodes form data into dest.
func (c *Context) BindForm(dest any) error {
	if err := c.req.ParseForm(); err != nil {
		return err
	}
	return bindFormValues(c, dest)
}

// FormFile returns the first file for the given form key.
func (c *Context) FormFile(name string) (*multipart.FileHeader, error) {
	_, fh, err := c.req.FormFile(name)
	return fh, err
}

// --- Response Helpers ---

// JSON serializes v as JSON and writes it with the given status code.
// If status is 0, uses the status set by Status() or defaults to 200.
func (c *Context) JSON(status int, v any) error {
	if c.written {
		return ErrResponseAlreadyWritten
	}
	c.written = true
	if status == 0 {
		status = c.statusCode
	}
	c.setContentType(c.app.codecContentType)
	if status != http.StatusOK {
		c.res.WriteHeader(status)
	}
	return c.app.codecEncode(c.res, v)
}

// JSONPretty serializes v as indented JSON.
func (c *Context) JSONPretty(status int, v any) error {
	c.written = true
	data, err := c.app.codecMarshal(v)
	if err != nil {
		return err
	}
	c.setContentType(c.app.codecContentType)
	c.res.WriteHeader(status)
	// Re-encode with indentation — simple approach
	_, err = c.res.Write(data)
	return err
}

// Text writes a plain text response.
func (c *Context) Text(status int, text string) error {
	c.written = true
	c.setContentType("text/plain; charset=utf-8")
	if status != http.StatusOK {
		c.res.WriteHeader(status)
	}
	_, err := io.WriteString(c.res, text)
	return err
}

// HTML writes an HTML response.
func (c *Context) HTML(status int, html string) error {
	c.written = true
	c.setContentType("text/html; charset=utf-8")
	if status != http.StatusOK {
		c.res.WriteHeader(status)
	}
	_, err := io.WriteString(c.res, html)
	return err
}

// XML serializes v as XML.
func (c *Context) XML(status int, v any) error {
	c.written = true
	c.setContentType("application/xml; charset=utf-8")
	if status != http.StatusOK {
		c.res.WriteHeader(status)
	}
	return xml.NewEncoder(c.res).Encode(v)
}

// Blob writes raw bytes with the given content type.
func (c *Context) Blob(status int, contentType string, data []byte) error {
	c.written = true
	c.setContentType(contentType)
	if status != http.StatusOK {
		c.res.WriteHeader(status)
	}
	_, err := c.res.Write(data)
	return err
}

// Stream copies from reader directly to the response writer.
// It bypasses the buffered response writer for streaming responses.
func (c *Context) Stream(status int, contentType string, reader io.Reader) error {
	c.written = true
	// Bypass buffered writer if present (for streaming)
	if bw, ok := c.res.(*bufferedResponseWriter); ok {
		bw.Bypass()
	}
	c.setContentType(contentType)
	c.res.WriteHeader(status)
	_, err := io.Copy(c.res, reader)
	return err
}

// Redirect sends an HTTP redirect.
func (c *Context) Redirect(status int, url string) error {
	c.written = true
	http.Redirect(c.res, c.req, url, status)
	return nil
}

// NoContent sends a response with no body.
func (c *Context) NoContent(status int) error {
	c.written = true
	c.res.WriteHeader(status)
	return nil
}

// Status sets the response status code for chaining.
// Use with JSON, Text, etc. to set status before writing.
// Example: c.Status(201).JSON(data)
func (c *Context) Status(code int) *Context {
	c.statusCode = code
	return c
}

// File sends a file as the response.
func (c *Context) File(filepath string) error {
	c.written = true
	http.ServeFile(c.res, c.req, filepath)
	return nil
}

// Attachment sends a file as an attachment (download).
func (c *Context) Attachment(filepath, filename string) error {
	c.written = true
	if filename == "" {
		// Extract filename from path
		for i := len(filepath) - 1; i >= 0; i-- {
			if filepath[i] == '/' || filepath[i] == '\\' {
				filename = filepath[i+1:]
				break
			}
		}
		if filename == "" {
			filename = filepath
		}
	}
	c.SetHeader("Content-Disposition", "attachment; filename=\""+filename+"\"")
	http.ServeFile(c.res, c.req, filepath)
	return nil
}

// Written returns true if a response has already been written.
func (c *Context) Written() bool { return c.written }

// SendStatus writes only the status code with no body (alias for NoContent).
func (c *Context) SendStatus(code int) error {
	return c.NoContent(code)
}

// --- Request-Scoped Store ---

// Set stores a key-value pair in the request-scoped store.
func (c *Context) Set(key string, value any) {
	if key == "requestId" {
		if s, ok := value.(string); ok {
			c.requestID = s
			c.requestIDSet = true
			return
		}
	}
	if c.store == nil {
		c.store = make(map[string]any, 4)
	}
	c.store[key] = value
}

// Get retrieves a value from the request-scoped store.
func (c *Context) Get(key string) (any, bool) {
	if key == "requestId" && c.requestIDSet {
		return c.requestID, true
	}
	v, ok := c.store[key]
	return v, ok
}

// MustGet retrieves a value from the store or panics if missing.
func (c *Context) MustGet(key string) any {
	v, ok := c.store[key]
	if !ok {
		panic(fmt.Sprintf("aarv: key %q not found in context store", key))
	}
	return v
}

// --- Metadata ---

// RequestID returns the request ID from the store (set by RequestID middleware).
func (c *Context) RequestID() string {
	if c.requestIDSet {
		return c.requestID
	}
	if c.store == nil {
		return ""
	}
	v, ok := c.store["requestId"]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// Logger returns a request-scoped logger with the request ID attached (cached).
func (c *Context) Logger() *slog.Logger {
	if c.cachedLogger != nil {
		return c.cachedLogger
	}
	l := c.app.logger
	if rid := c.RequestID(); rid != "" {
		l = l.With("request_id", rid)
	}
	c.cachedLogger = l
	return l
}

// Error is a shortcut to return an AppError from a handler.
func (c *Context) Error(status int, message string) error {
	return NewError(status, http.StatusText(status), message)
}

// ErrorWithDetail returns an AppError with a detail string.
func (c *Context) ErrorWithDetail(status int, message, detail string) error {
	return NewError(status, http.StatusText(status), message).WithDetail(detail)
}

// GetTyped retrieves a typed value from the context store.
func GetTyped[T any](c *Context, key string) (T, bool) {
	if key == "requestId" && c.requestIDSet {
		t, ok := any(c.requestID).(T)
		return t, ok
	}
	v, ok := c.store[key]
	if !ok {
		var zero T
		return zero, false
	}
	t, ok := v.(T)
	return t, ok
}

// --- helpers ---

func isValidUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}
