// Package jwt — algorithm registry and family-specific signers/verifiers.
//
// The "none" algorithm is intentionally never registered: a token whose
// header carries "alg":"none" is rejected at parse time before any key
// resolution happens. The header alg is also checked against the caller's
// allow-list before the key-type assertion runs, which together close the
// classic alg-confusion attack (an HS256 token cannot be verified against
// an RSA public key).

package jwt

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"hash"
	"io"
	"math/big"
)

// ecdsaRand is the entropy source for ecdsa.Sign. Held as a package
// variable so tests can swap in a failing reader to exercise the rand
// failure branch without inventing a curve that ecdsa.Sign rejects.
var ecdsaRand io.Reader = rand.Reader

// Algorithm is the JOSE "alg" header value.
type Algorithm string

// Supported algorithms. "none" is intentionally absent and unregistered.
const (
	HS256 Algorithm = "HS256"
	HS384 Algorithm = "HS384"
	HS512 Algorithm = "HS512"
	RS256 Algorithm = "RS256"
	RS384 Algorithm = "RS384"
	RS512 Algorithm = "RS512"
	ES256 Algorithm = "ES256"
	ES384 Algorithm = "ES384"
	ES512 Algorithm = "ES512"
	EdDSA Algorithm = "EdDSA"
)

// algSpec describes how a single algorithm signs and verifies. signKey and
// verifyKey are concrete Go types so callers cannot accidentally pass a
// wrong key shape; mismatch produces ErrKeyTypeMismatch.
type algSpec struct {
	hash      crypto.Hash // 0 for EdDSA
	hmacSize  int         // minimum HMAC key length per RFC 7518 §3.2; 0 for non-HMAC
	curve     elliptic.Curve
	curveSize int // bytes per coordinate; 0 for non-ECDSA
	sign      func(key any, msg []byte) ([]byte, error)
	verify    func(key any, msg, sig []byte) error
}

var algRegistry = map[Algorithm]*algSpec{
	HS256: hmacSpec(crypto.SHA256, 32),
	HS384: hmacSpec(crypto.SHA384, 48),
	HS512: hmacSpec(crypto.SHA512, 64),
	RS256: rsaSpec(crypto.SHA256),
	RS384: rsaSpec(crypto.SHA384),
	RS512: rsaSpec(crypto.SHA512),
	ES256: ecdsaSpec(crypto.SHA256, elliptic.P256(), 32),
	ES384: ecdsaSpec(crypto.SHA384, elliptic.P384(), 48),
	ES512: ecdsaSpec(crypto.SHA512, elliptic.P521(), 66),
	EdDSA: ed25519Spec(),
}

// lookupAlg returns the spec for alg, or (nil, false) if unrecognized. The
// "none" string is rejected here too because it is not a registered key.
func lookupAlg(alg Algorithm) (*algSpec, bool) {
	s, ok := algRegistry[alg]
	return s, ok
}

// isHMAC reports whether alg belongs to the HS* family. Used during config
// validation to enforce that HMACSecret is paired only with HS* algorithms.
func isHMAC(alg Algorithm) bool {
	switch alg {
	case HS256, HS384, HS512:
		return true
	}
	return false
}

// --- HMAC ---

func hmacSpec(h crypto.Hash, minKey int) *algSpec {
	return &algSpec{
		hash:     h,
		hmacSize: minKey,
		sign: func(key any, msg []byte) ([]byte, error) {
			k, ok := key.([]byte)
			if !ok {
				return nil, ErrKeyTypeMismatch
			}
			if len(k) < minKey {
				return nil, ErrWeakKey
			}
			mac := hmac.New(hashFunc(h), k)
			mac.Write(msg)
			return mac.Sum(nil), nil
		},
		verify: func(key any, msg, sig []byte) error {
			k, ok := key.([]byte)
			if !ok {
				return ErrKeyTypeMismatch
			}
			mac := hmac.New(hashFunc(h), k)
			mac.Write(msg)
			expected := mac.Sum(nil)
			if !hmac.Equal(expected, sig) {
				return ErrInvalidSignature
			}
			return nil
		},
	}
}

func hashFunc(h crypto.Hash) func() hash.Hash {
	switch h {
	case crypto.SHA256:
		return sha256.New
	case crypto.SHA384:
		return sha512.New384
	case crypto.SHA512:
		return sha512.New
	default:
		panic("jwt: unsupported hash") // unreachable: registry only uses these three
	}
}

// --- RSA ---

func rsaSpec(h crypto.Hash) *algSpec {
	return &algSpec{
		hash: h,
		sign: func(key any, msg []byte) ([]byte, error) {
			priv, ok := key.(*rsa.PrivateKey)
			if !ok {
				return nil, ErrKeyTypeMismatch
			}
			sum := hashSum(h, msg)
			return rsa.SignPKCS1v15(rand.Reader, priv, h, sum)
		},
		verify: func(key any, msg, sig []byte) error {
			pub, ok := key.(*rsa.PublicKey)
			if !ok {
				return ErrKeyTypeMismatch
			}
			sum := hashSum(h, msg)
			if err := rsa.VerifyPKCS1v15(pub, h, sum, sig); err != nil {
				return ErrInvalidSignature
			}
			return nil
		},
	}
}

// --- ECDSA ---

func ecdsaSpec(h crypto.Hash, curve elliptic.Curve, curveSize int) *algSpec {
	return &algSpec{
		hash:      h,
		curve:     curve,
		curveSize: curveSize,
		sign: func(key any, msg []byte) ([]byte, error) {
			priv, ok := key.(*ecdsa.PrivateKey)
			if !ok {
				return nil, ErrKeyTypeMismatch
			}
			if priv.Curve != curve {
				return nil, ErrKeyTypeMismatch
			}
			sum := hashSum(h, msg)
			r, s, err := ecdsa.Sign(ecdsaRand, priv, sum)
			if err != nil {
				return nil, err
			}
			// Encode as fixed-length R||S per RFC 7518 §3.4.
			sig := make([]byte, 2*curveSize)
			rBytes := r.Bytes()
			sBytes := s.Bytes()
			copy(sig[curveSize-len(rBytes):curveSize], rBytes)
			copy(sig[2*curveSize-len(sBytes):], sBytes)
			return sig, nil
		},
		verify: func(key any, msg, sig []byte) error {
			pub, ok := key.(*ecdsa.PublicKey)
			if !ok {
				return ErrKeyTypeMismatch
			}
			if pub.Curve != curve {
				return ErrKeyTypeMismatch
			}
			if len(sig) != 2*curveSize {
				return ErrInvalidSignature
			}
			r := new(big.Int).SetBytes(sig[:curveSize])
			s := new(big.Int).SetBytes(sig[curveSize:])
			sum := hashSum(h, msg)
			if !ecdsa.Verify(pub, sum, r, s) {
				return ErrInvalidSignature
			}
			return nil
		},
	}
}

// --- EdDSA (Ed25519) ---

func ed25519Spec() *algSpec {
	return &algSpec{
		sign: func(key any, msg []byte) ([]byte, error) {
			priv, ok := key.(ed25519.PrivateKey)
			if !ok {
				return nil, ErrKeyTypeMismatch
			}
			if len(priv) != ed25519.PrivateKeySize {
				return nil, ErrKeyTypeMismatch
			}
			return ed25519.Sign(priv, msg), nil
		},
		verify: func(key any, msg, sig []byte) error {
			pub, ok := key.(ed25519.PublicKey)
			if !ok {
				return ErrKeyTypeMismatch
			}
			if len(pub) != ed25519.PublicKeySize {
				return ErrKeyTypeMismatch
			}
			if !ed25519.Verify(pub, msg, sig) {
				return ErrInvalidSignature
			}
			return nil
		},
	}
}

// hashSum computes h(msg) using the registry's allowed hashes.
func hashSum(h crypto.Hash, msg []byte) []byte {
	hh := hashFunc(h)()
	hh.Write(msg)
	return hh.Sum(nil)
}
