package autocert

import (
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Conservative defaults for the redirect listener, which is expected to
// run on public :80 and is therefore slowloris-prone if left untimed.
// These can be overridden per-deployment via RedirectConfig.
const (
	defaultRedirectReadHeaderTimeout = 5 * time.Second
	defaultRedirectReadTimeout       = 10 * time.Second
	defaultRedirectIdleTimeout       = 60 * time.Second
)

// ACMEChallengeHandler is satisfied by *autocert.Manager and exists so
// RedirectConfig.ACMEHandler can accept a fake during testing without
// pulling in the concrete manager. Anything matching this signature works.
type ACMEChallengeHandler interface {
	HTTPHandler(fallback http.Handler) http.Handler
}

// RedirectConfig configures the HTTP-to-HTTPS redirect handler that
// typically pairs with [Listen] on :80 to serve the ACME HTTP-01 challenge
// and bounce ordinary traffic to HTTPS.
//
// HSTS is intentionally absent — Strict-Transport-Security over plain HTTP
// is meaningless. Configure HSTS on your HTTPS responses (e.g. via the
// secure plugin).
type RedirectConfig struct {
	// TargetHost overrides the destination host. Empty uses r.Host as
	// received. When empty, you are responsible for any X-Forwarded-Host /
	// trusted-proxy normalization upstream of this handler.
	TargetHost string

	// HTTPSPort optionally adds ":<port>" to the redirect Location. Zero
	// or 443 omits the port. Use only for non-standard HTTPS ports.
	HTTPSPort int

	// Code is the HTTP status code. Zero defaults to 308 (Permanent
	// Redirect, preserves method).
	Code int

	// ACMEHandler, if non-nil, wraps the redirect so requests to
	// /.well-known/acme-challenge/* are served by the ACME manager. Set
	// this to your *autocert.Manager when running TLS-ALPN-01 alongside
	// HTTP-01.
	ACMEHandler ACMEChallengeHandler

	// ReadHeaderTimeout bounds header-read time on the *http.Server
	// returned by RedirectServer (and the listener run by ListenRedirect).
	// Zero applies the conservative default (5s). Set to a negative
	// duration to disable the cap.
	//
	// The redirect listener is expected to bind public :80 and is
	// therefore slowloris-prone without a header timeout; the default
	// is intentionally tight.
	ReadHeaderTimeout time.Duration

	// ReadTimeout bounds full-request read time. Zero applies the
	// conservative default (10s). Set to a negative duration to disable.
	ReadTimeout time.Duration

	// IdleTimeout bounds idle keep-alive lifetime. Zero applies the
	// conservative default (60s). Set to a negative duration to disable.
	IdleTimeout time.Duration
}

// RedirectHandler returns an http.Handler that redirects every request to
// the equivalent https:// URL. See [RedirectConfig] for behavior knobs.
func RedirectHandler(cfg RedirectConfig) http.Handler {
	code := cfg.Code
	if code == 0 {
		code = http.StatusPermanentRedirect
	}

	base := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := cfg.TargetHost
		if host == "" {
			host = r.Host
		}
		// Reject before constructing url.URL: an empty Host renders as
		// "https:///path" which is a malformed Location. HTTP/1.0 clients
		// without a Host header reach this branch.
		if host == "" {
			http.Error(w, "missing host", http.StatusBadRequest)
			return
		}
		if hasControlChars(host) {
			http.Error(w, "invalid host", http.StatusBadRequest)
			return
		}

		hostOnly, inboundPort := splitHostPortBestEffort(host)
		if hostOnly == "" && inboundPort != "" {
			http.Error(w, "invalid host", http.StatusBadRequest)
			return
		}
		switch {
		case cfg.HTTPSPort > 0 && cfg.HTTPSPort != 443:
			host = withPort(hostOnly, cfg.HTTPSPort)
		case cfg.HTTPSPort == 0 || cfg.HTTPSPort == 443:
			// Omit default ports in HTTPS redirects. Most inbound HTTP
			// requests arrive as Host: example.com or example.com:80;
			// redirecting to https://example.com:80 is almost never right.
			if inboundPort == "80" || inboundPort == "443" {
				host = hostOnly
			}
		}

		// Bracket bare IPv6 literals before they reach url.URL.Host.
		// url.URL.String() does NOT auto-bracket; without this guard,
		// TargetHost: "::1" with no port would emit Location:
		// "https://::1/..." (multiple colons in the authority — invalid).
		host = bracketBareIPv6(host)

		target := (&url.URL{
			Scheme:   "https",
			Host:     host,
			Path:     r.URL.Path,
			RawPath:  r.URL.RawPath,
			RawQuery: r.URL.RawQuery,
		}).String()
		http.Redirect(w, r, target, code)
	})

	if cfg.ACMEHandler != nil {
		return cfg.ACMEHandler.HTTPHandler(base)
	}
	return base
}

// RedirectServer returns an *http.Server pre-configured with the redirect
// handler and slowloris-resistant timeout defaults, ready for the caller
// to manage its lifecycle (Shutdown via signal.NotifyContext, etc.). For
// a one-shot blocker, use [ListenRedirect].
//
// The redirect listener is intentionally NOT routed through aarv.App.ListenServer
// because an App tracks one server pointer; sharing it between the main
// HTTPS listener and this redirect listener would race app.Shutdown(ctx).
func RedirectServer(addr string, cfg RedirectConfig) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           RedirectHandler(cfg),
		ReadHeaderTimeout: redirectTimeout(cfg.ReadHeaderTimeout, defaultRedirectReadHeaderTimeout),
		ReadTimeout:       redirectTimeout(cfg.ReadTimeout, defaultRedirectReadTimeout),
		IdleTimeout:       redirectTimeout(cfg.IdleTimeout, defaultRedirectIdleTimeout),
	}
}

// redirectTimeout normalizes a configured duration: zero substitutes the
// supplied default; negative disables the cap (returns zero, the net/http
// "no timeout" sentinel); positive values pass through verbatim.
func redirectTimeout(configured, fallback time.Duration) time.Duration {
	switch {
	case configured < 0:
		return 0
	case configured == 0:
		return fallback
	default:
		return configured
	}
}

// ListenRedirect runs a redirect server until ListenAndServe returns.
// Caller manages graceful shutdown by closing the returned context (via
// Shutdown on a server reference) — typically wired to signal.NotifyContext
// in main.
func ListenRedirect(addr string, cfg RedirectConfig) error {
	return RedirectServer(addr, cfg).ListenAndServe()
}

func splitHostPortBestEffort(host string) (hostOnly, port string) {
	if h, p, err := net.SplitHostPort(host); err == nil {
		return h, p
	}
	return host, ""
}

func withPort(host string, port int) string {
	return bracketBareIPv6(host) + ":" + strconv.Itoa(port)
}

// bracketBareIPv6 wraps host in [] if it looks like an unbracketed IPv6
// literal (contains ':', is not already bracketed, and is not a host:port
// pair — i.e. has more than one colon). Hostnames and IPv4 literals pass
// through unchanged.
//
// url.URL.String() does not auto-bracket Host, so any bare IPv6 we hand it
// would emit a Location header with multiple colons in the authority,
// which browsers reject.
func bracketBareIPv6(host string) string {
	if host == "" || strings.HasPrefix(host, "[") {
		return host
	}
	// More than one colon means it cannot be host:port; treat as bare IPv6.
	if strings.Count(host, ":") < 2 {
		return host
	}
	return "[" + host + "]"
}

// hasControlChars rejects any byte in the C0 controls range (\x00-\x1F)
// or DEL (\x7F). This is the security floor — header injection
// (\r, \n, \x00) and malformed Location values cannot escape the redirect.
// Other shape correctness (hostname / IPv4 / IPv6 syntax) is delegated to
// tests rather than parsed here, on the principle that strict parsing is a
// frequent source of false rejections (punycode, IDN, custom split-horizon
// names) without proportional security benefit.
func hasControlChars(s string) bool {
	for i := 0; i < len(s); i++ {
		b := s[i]
		if b < 0x20 || b == 0x7f {
			return true
		}
	}
	return false
}
