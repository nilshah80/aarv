package session

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/nilshah80/aarv"
)

// maxCookieEncodedLen is the upper bound enforced on the encoded
// cookie payload at Save time. Browsers commonly cap individual
// cookies at ~4 KiB; 3.5 KiB leaves headroom for the cookie name,
// attributes (Path, Domain, Max-Age, etc.), and Set-Cookie header
// framing.
const maxCookieEncodedLen = 3584

// ErrCookiePayloadTooLarge is reported via SaveErrorHandler when the
// encrypted Stored payload would exceed the cookie size guard.
var ErrCookiePayloadTooLarge = errors.New("session: cookie payload exceeds 3.5 KiB browser-safe limit")

// CookieStore is the stateless backend: session state is JSON-marshaled
// and AES-256-GCM-encrypted into the cookie value itself. There is no
// server-side state, so Regenerate cannot revoke the previous payload
// (the old cookie remains valid until it expires or is overwritten by
// the client).
//
// CookieStore intentionally does not satisfy the Store interface — see
// package docs for value-shape constraints and the Store / CookieStore
// trade-off.
//
// Session.ID() under CookieStore returns the encrypted cookie value
// and is unstable across saves (a fresh AES-GCM nonce is generated on
// every encryption). Use it as an opaque per-request identifier only,
// never as a cross-request correlation key, log identifier, or join
// key against external systems.
type CookieStore struct {
	gcm cipher.AEAD
}

// NewCookieStore returns a CookieStore using key (must be exactly 32
// bytes, AES-256). Callers should source key from an HKDF or KMS, not
// hard-code it.
func NewCookieStore(key []byte) (*CookieStore, error) {
	if len(key) != 32 {
		return nil, ErrInvalidKey
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		// AES with a valid 32-byte key cannot fail; defensive only.
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &CookieStore{gcm: gcm}, nil
}

// cookieEnvelope is the on-the-wire JSON shape inside the encrypted
// cookie. The unix timestamp lives in the encrypted payload (not the
// HTTP Max-Age, which the client can spoof) and drives server-side
// expiry on read.
type cookieEnvelope struct {
	IssuedAt int64   `json:"t"`
	Stored   *Stored `json:"s"`
}

// load implements sessionBackend.
func (cs *CookieStore) load(c *aarv.Context, r *http.Request, cfg *normalized) (*Session, error) {
	cookie, err := r.Cookie(cfg.cookieName)
	if err != nil || cookie == nil || cookie.Value == "" {
		return cs.freshSession(cfg)
	}
	env, err := cs.decode(cookie.Value, cfg)
	if err != nil {
		// Tampered, expired, or stale-key cookie. Treat as no session;
		// not an error, so the request continues with a fresh one.
		return cs.freshSession(cfg)
	}
	st := env.Stored
	if st == nil {
		st = &Stored{}
	}
	// The id of a CookieStore session is the cookie value itself; it
	// has no semantic content beyond uniqueness. Regenerate sets oldID
	// for parity with server stores but cannot actually revoke the old
	// cookie — documented in CookieStore godoc.
	return sessionFromStored(cookie.Value, st, cfg.idLength), nil
}

// save implements sessionBackend.
func (cs *CookieStore) save(c *aarv.Context, w http.ResponseWriter, sess *Session, cfg *normalized) error {
	env := cookieEnvelope{
		IssuedAt: time.Now().Unix(),
		Stored:   sess.toStored(),
	}
	encoded, err := cs.encode(env, cfg)
	if err != nil {
		return err
	}
	if len(encoded) > maxCookieEncodedLen {
		return ErrCookiePayloadTooLarge
	}
	http.SetCookie(w, buildSessionCookie(cfg, encoded, false))
	return nil
}

// destroy implements sessionBackend by emitting an expired cookie. No
// server-side state to clear.
func (cs *CookieStore) destroy(c *aarv.Context, w http.ResponseWriter, sess *Session, cfg *normalized) error {
	http.SetCookie(w, buildSessionCookie(cfg, "", true))
	return nil
}

func (cs *CookieStore) freshSession(cfg *normalized) (*Session, error) {
	idLen := cfg.idLength
	if idLen <= 0 {
		idLen = defaultIDLen
	}
	id, err := generateID(idLen)
	if err != nil {
		return nil, err
	}
	return newSession(id, true, idLen), nil
}

// aadFor returns the AES-GCM additional authenticated data binding the
// ciphertext to the configured cookie name. A deployer who reuses the
// same master key for multiple cookies (or for some other AES-GCM
// purpose) cannot have a value sealed under cookie name A accepted
// when presented under cookie name B — Open will fail because the
// authentication tag was computed over a different AAD.
//
// The "aarv/session:" prefix is a domain-separation tag so a non-
// session AES-GCM use of the same key cannot collide with a session
// AAD of "x" by coincidence.
func aadFor(cfg *normalized) []byte {
	return []byte("aarv/session:" + cfg.cookieName)
}

// encode marshals env to JSON, AES-256-GCM-encrypts it (with AAD bound
// to the cookie name via aadFor), and base64url-encodes the
// nonce || ciphertext || tag blob.
func (cs *CookieStore) encode(env cookieEnvelope, cfg *normalized) (string, error) {
	plain, err := json.Marshal(env)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, cs.gcm.NonceSize())
	if _, err := readRand(nonce); err != nil {
		return "", err
	}
	sealed := cs.gcm.Seal(nonce, nonce, plain, aadFor(cfg))
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

// decode reverses encode. Returns an error for tampered, malformed,
// expired (per cfg.maxAge), or cross-cookie-name replayed payloads.
func (cs *CookieStore) decode(encoded string, cfg *normalized) (cookieEnvelope, error) {
	var env cookieEnvelope
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return env, err
	}
	ns := cs.gcm.NonceSize()
	if len(raw) < ns {
		return env, errors.New("session: cookie too short")
	}
	nonce, ct := raw[:ns], raw[ns:]
	plain, err := cs.gcm.Open(nil, nonce, ct, aadFor(cfg))
	if err != nil {
		return env, err
	}
	if err := json.Unmarshal(plain, &env); err != nil {
		return env, err
	}
	if cfg.maxAge > 0 && env.IssuedAt > 0 {
		age := time.Since(time.Unix(env.IssuedAt, 0))
		if age > cfg.maxAge {
			return env, errors.New("session: cookie expired")
		}
	}
	return env, nil
}

// readRand is indirected so tests can simulate randomness exhaustion.
var readRand = func(b []byte) (int, error) {
	return randReader.Read(b)
}

// Compile-time guarantee that CookieStore is the internal backend
// (and explicitly NOT a Store).
var _ sessionBackend = (*CookieStore)(nil)
