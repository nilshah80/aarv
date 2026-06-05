package hmacauth

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"
)

// Sign attaches the four signed-request headers to req.
//
// Body resolution rules — Sign signs whatever the server will hash,
// not whatever the caller happens to pass:
//
//  1. If body != nil, it is treated as authoritative and used as-is.
//     The caller is asserting "this is the body the server will see".
//     Sign does NOT consume req.Body in this path.
//  2. If body == nil and req.Body is nil/http.NoBody, the empty
//     hash is used (standard for body-less GET/HEAD/DELETE).
//  3. If body == nil and req.Body is non-nil, Sign reads it via
//     req.GetBody so the body bytes flow into the canonical hash
//     and the bytes are still available to the underlying transport.
//     Requests built via http.NewRequest with a bytes/strings
//     reader populate GetBody automatically. If req.Body is set but
//     GetBody is nil (io.Pipe etc.), Sign returns an error rather
//     than silently signing an empty body — that mismatch would
//     produce a request the server immediately rejects with a 401.
//
// now and nonce are dependency-injected for tests. Production code
// passes (time.Now, "") and lets Sign generate a fresh random nonce.
func Sign(req *http.Request, client Client, body []byte, now func() time.Time, nonce string) error {
	if req == nil {
		return errors.New("hmacauth: Sign requires a non-nil request")
	}
	if client.ClientID == "" {
		return errors.New("hmacauth: Sign requires Client.ClientID")
	}
	secret := client.Secret
	if len(secret) == 0 {
		// Fall back to the first non-empty rotation slot so callers
		// can sign with a Client that only populates Secrets.
		for _, s := range client.Secrets {
			if len(s) > 0 {
				secret = s
				break
			}
		}
	}
	if len(secret) == 0 {
		return errors.New("hmacauth: Sign requires at least one non-empty secret on the Client")
	}
	if now == nil {
		now = time.Now
	}
	if nonce == "" {
		var err error
		nonce, err = randomNonce()
		if err != nil {
			return err
		}
	}

	hashBody, err := resolveSignBody(req, body)
	if err != nil {
		return err
	}

	ts := now().Unix()
	canonical := canonicalRequest(req.Method, req.URL.Path, req.URL.Query(), hashBody, ts, nonce)
	mac := hmac.New(sha256.New, secret)
	mac.Write(canonical)
	sig := hex.EncodeToString(mac.Sum(nil))

	req.Header.Set(DefaultClientIDHeader, client.ClientID)
	req.Header.Set(DefaultTimestampHeader, strconv.FormatInt(ts, 10))
	req.Header.Set(DefaultNonceHeader, nonce)
	req.Header.Set(DefaultSignatureHeader, sig)
	return nil
}

// resolveSignBody implements the body resolution rules documented
// on Sign. It either returns the caller-supplied body verbatim
// (when non-nil), the bytes read from req.GetBody (when the caller
// passed nil but the request carries a replayable body), or the
// empty body (when there is no body at all).
//
// It returns an error in the one case Sign refuses to silently
// guess: req.Body is non-nil and unreplayable.
func resolveSignBody(req *http.Request, body []byte) ([]byte, error) {
	if body != nil {
		return body, nil
	}
	if req.Body == nil || req.Body == http.NoBody {
		return nil, nil
	}
	if req.GetBody == nil {
		return nil, errors.New("hmacauth: Sign called with body == nil and req.Body is unreplayable (req.GetBody == nil); supply body bytes explicitly or rebuild req with a typed bytes/strings reader")
	}
	rc, err := req.GetBody()
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	return io.ReadAll(rc)
}

// randomNonce produces 16 bytes from crypto/rand encoded as 32-char
// lowercase hex.
func randomNonce() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// TransportOption configures a Transport.
type TransportOption func(*transport)

// WithTransportNow injects a clock — tests can pass a fixed time.
func WithTransportNow(now func() time.Time) TransportOption {
	return func(t *transport) { t.now = now }
}

// WithTransportNonce injects a nonce source — tests can pass a
// deterministic string or a counter.
func WithTransportNonce(fn func() (string, error)) TransportOption {
	return func(t *transport) { t.nonce = fn }
}

// WithTransportBase replaces the underlying RoundTripper that does
// the actual network hop. Defaults to http.DefaultTransport.
func WithTransportBase(rt http.RoundTripper) TransportOption {
	return func(t *transport) { t.base = rt }
}

// WithTransportHeaders overrides the four header names used during
// signing. Pass an empty string to keep a default.
func WithTransportHeaders(clientID, timestamp, nonce, signature string) TransportOption {
	return func(t *transport) {
		if clientID != "" {
			t.clientIDHeader = clientID
		}
		if timestamp != "" {
			t.timestampHeader = timestamp
		}
		if nonce != "" {
			t.nonceHeader = nonce
		}
		if signature != "" {
			t.signatureHeader = signature
		}
	}
}

// Transport returns an http.RoundTripper that signs every outbound
// request with the given client. The request body must either be nil
// or be replayable via GetBody — Transport reads it once (via
// GetBody) to compute the canonical hash, then leaves the request's
// body intact for the underlying RoundTripper.
//
// Redirect handling lives on http.Client, not on the RoundTripper.
// For "fail on redirect" semantics, set Client.CheckRedirect to
// FailOnRedirect (recommended default — silent redirects can land
// the signed request at an unexpected URL with stale headers).
//
// For "follow and re-sign", use the matching CheckRedirect helper:
//
//   - If you customized header names via WithTransportHeaders, use
//     NewSigningTransport (which returns *SigningTransport) and
//     pass its CheckRedirect method to http.Client.CheckRedirect.
//     That helper closes over the same header names the request
//     was originally signed with.
//   - The package-level ResignOnRedirect(client) wires the default
//     header names only. Use it only when you have not customized
//     the four signed-request header names; otherwise redirects
//     will be re-signed under the wrong headers.
func Transport(client Client, opts ...TransportOption) http.RoundTripper {
	return NewSigningTransport(client, opts...)
}

// SigningTransport is the typed wrapper exposed by NewSigningTransport.
// Use it when you need access to per-Transport helpers like
// CheckRedirect that must share state (header names, clock, nonce
// source) with the signer.
//
// SigningTransport satisfies http.RoundTripper, so it can be dropped
// into &http.Client{Transport: t} verbatim.
type SigningTransport struct {
	t *transport
}

// NewSigningTransport returns a *SigningTransport configured with
// the given client and options.
func NewSigningTransport(client Client, opts ...TransportOption) *SigningTransport {
	t := &transport{
		client:          client,
		base:            http.DefaultTransport,
		now:             time.Now,
		nonce:           randomNonce,
		clientIDHeader:  DefaultClientIDHeader,
		timestampHeader: DefaultTimestampHeader,
		nonceHeader:     DefaultNonceHeader,
		signatureHeader: DefaultSignatureHeader,
	}
	for _, o := range opts {
		o(t)
	}
	return &SigningTransport{t: t}
}

// RoundTrip implements http.RoundTripper. Delegates to the
// underlying transport which signs the request via signWithHeaders
// using the configured header names.
func (s *SigningTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return s.t.RoundTrip(req)
}

// CheckRedirect is a "follow and re-sign" helper that honors the
// header names this Transport was configured with. Wire it via
// http.Client.CheckRedirect when you want the client to follow
// redirects and re-sign each hop:
//
//	t := hmacauth.NewSigningTransport(client, hmacauth.WithTransportHeaders(...))
//	c := &http.Client{Transport: t, CheckRedirect: t.CheckRedirect}
//
// Limitations match ResignOnRedirect: the body must be replayable
// via req.GetBody (typed bytes/strings readers populate this
// automatically), and the stdlib's default 10-redirect cap applies.
func (s *SigningTransport) CheckRedirect(req *http.Request, via []*http.Request) error {
	body, err := readReplayableBody(req)
	if err != nil {
		return err
	}
	if body != nil {
		// Re-bind the body so the destination receives it. The
		// stdlib does this for the original body; on a redirect we
		// have to do it ourselves because reading via GetBody
		// consumes one copy.
		req.Body = io.NopCloser(bytes.NewReader(body))
	}
	nonce, err := s.t.nonce()
	if err != nil {
		return err
	}
	if s.t.now == nil {
		s.t.now = time.Now
	}
	return signWithHeaders(req, s.t.client, body, s.t.now, nonce,
		s.t.clientIDHeader, s.t.timestampHeader, s.t.nonceHeader, s.t.signatureHeader)
}

type transport struct {
	client          Client
	base            http.RoundTripper
	now             func() time.Time
	nonce           func() (string, error)
	clientIDHeader  string
	timestampHeader string
	nonceHeader     string
	signatureHeader string
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	body, err := readReplayableBody(req)
	if err != nil {
		return nil, err
	}
	nonce, err := t.nonce()
	if err != nil {
		return nil, err
	}
	// Sign mutates req.Header in place. Clone the request so we do
	// not surprise callers who hold a reference to the original.
	signed := req.Clone(req.Context())
	if err := signWithHeaders(signed, t.client, body, t.now, nonce, t.clientIDHeader, t.timestampHeader, t.nonceHeader, t.signatureHeader); err != nil {
		return nil, err
	}
	return t.base.RoundTrip(signed)
}

// signWithHeaders is Sign with overridable header names. Kept
// internal — callers wanting custom headers configure the Transport
// once and let RoundTrip pick them up. Body resolution mirrors Sign
// exactly via resolveSignBody.
func signWithHeaders(req *http.Request, client Client, body []byte, now func() time.Time, nonce, hClient, hTs, hNonce, hSig string) error {
	if client.ClientID == "" {
		return errors.New("hmacauth: signWithHeaders requires Client.ClientID")
	}
	secret := client.Secret
	if len(secret) == 0 {
		for _, s := range client.Secrets {
			if len(s) > 0 {
				secret = s
				break
			}
		}
	}
	if len(secret) == 0 {
		return errors.New("hmacauth: signWithHeaders requires at least one non-empty secret")
	}
	if now == nil {
		now = time.Now
	}
	hashBody, err := resolveSignBody(req, body)
	if err != nil {
		return err
	}
	ts := now().Unix()
	canonical := canonicalRequest(req.Method, req.URL.Path, req.URL.Query(), hashBody, ts, nonce)
	mac := hmac.New(sha256.New, secret)
	mac.Write(canonical)
	sig := hex.EncodeToString(mac.Sum(nil))

	req.Header.Set(hClient, client.ClientID)
	req.Header.Set(hTs, strconv.FormatInt(ts, 10))
	req.Header.Set(hNonce, nonce)
	req.Header.Set(hSig, sig)
	return nil
}

// readReplayableBody returns the body bytes and leaves req.Body
// usable. If the body is nil it returns (nil, nil). Otherwise it
// requires req.GetBody so the body can be reconstructed for the
// underlying transport — io.Pipe-backed requests cannot be signed.
func readReplayableBody(req *http.Request) ([]byte, error) {
	if req.Body == nil || req.Body == http.NoBody {
		return nil, nil
	}
	if req.GetBody != nil {
		rc, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		defer func() { _ = rc.Close() }()
		return io.ReadAll(rc)
	}
	// GetBody is nil but Body is set. Only safe to read+rebuffer when
	// the body has a known positive length — i.e. it came from a typed
	// reader the stdlib could measure (*strings.Reader, *bytes.Reader,
	// *bytes.Buffer). This covers the Go 1.22 redirect case (http.Client
	// populates newReq.Body from oldReq.GetBody() but doesn't propagate
	// GetBody itself; fixed in Go 1.23) while still rejecting truly
	// unreplayable streams (io.Pipe and friends have ContentLength == 0
	// or -1 — reading those would block or read arbitrary upstream data).
	if req.ContentLength <= 0 {
		return nil, errors.New("hmacauth: req.GetBody is nil — request body is not replayable; use http.NewRequest with bytes.NewReader/strings.NewReader/bytes.Buffer")
	}
	b, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	_ = req.Body.Close()
	req.Body = io.NopCloser(bytes.NewReader(b))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(b)), nil
	}
	return b, nil
}

// FailOnRedirect is a CheckRedirect implementation that aborts the
// request on any 3xx response. Use it as the default for signed
// clients — silently following a redirect can mean the signed
// headers (computed for the original URL) end up at an unexpected
// host, which is at best a debugging headache and at worst a
// security issue.
//
// Wire it via http.Client{CheckRedirect: hmacauth.FailOnRedirect}.
func FailOnRedirect(req *http.Request, via []*http.Request) error {
	return http.ErrUseLastResponse
}

// ResignOnRedirect returns a CheckRedirect implementation that
// re-signs the request for the redirect target. The new request URL
// (host, path, query) is reflected in the canonical request, so the
// signature remains valid at the destination.
//
// **Header names**: ResignOnRedirect signs with the four
// Default*Header constants. If you configured a SigningTransport
// with WithTransportHeaders, use SigningTransport.CheckRedirect
// instead — that variant closes over the same header names the
// original request was signed with.
//
// Limitations:
//
//   - The body must be replayable via GetBody; otherwise the
//     redirected request reaches the destination without a body and
//     fails verification. http.Client populates GetBody automatically
//     for typed body readers (bytes/strings); custom readers must
//     supply it.
//   - The maximum redirect chain length is the stdlib default (10);
//     callers needing tighter limits should wrap the function.
//   - The Client passed in is captured by reference. Rotating the
//     secret on the source struct invalidates in-flight redirects
//     started before the rotation.
func ResignOnRedirect(client Client) func(*http.Request, []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		// readReplayableBody handles both the GetBody path and the
		// Go-1.22 redirect quirk where GetBody is stripped but Body
		// is populated. On either path it returns the bytes and
		// leaves req.Body in a re-readable state for the redirect
		// target.
		body, err := readReplayableBody(req)
		if err != nil {
			return err
		}
		if body != nil {
			// Rebind the body so the destination receives it.
			// readReplayableBody consumed one copy via GetBody.
			req.Body = io.NopCloser(bytes.NewReader(body))
		}
		nonce, err := randomNonce()
		if err != nil {
			return err
		}
		return Sign(req, client, body, time.Now, nonce)
	}
}
