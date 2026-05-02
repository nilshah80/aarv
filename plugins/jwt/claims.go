// Package jwt — claim validation.
//
// NumericDate handling is intentionally stricter than RFC 7519 §2: only
// integer Unix seconds in [0, maxNumericDate] are accepted. JSON strings,
// fractional values, negative values, and out-of-range values (which catches
// accidental millisecond-scale timestamps cleanly) all fail with
// ErrInvalidNumericDate. RFC 7519 permits non-integer NumericDates; this
// plugin does not, by design, and that decision is documented in the
// CHANGELOG so callers writing tokens with fractional exp know they will
// be rejected.

package jwt

import (
	"crypto/subtle"
	"time"
)

// maxNumericDate is the upper bound for any NumericDate claim. It corresponds
// to 9999-12-31T23:59:59Z, comfortably below 2^53 so float64 round-trips
// exactly through encoding/json's number decoding.
const maxNumericDate int64 = 253402300799

// validateNumericDate enforces the strict NumericDate rules. The claim
// must be a JSON number (decoded as float64), a non-negative integer, and
// no greater than maxNumericDate.
//
// The boolean reports whether the claim was present at all; absent claims
// are not an error.
func validateNumericDate(claims map[string]any, name string) (sec int64, present bool, err error) {
	raw, ok := claims[name]
	if !ok {
		return 0, false, nil
	}
	f, ok := raw.(float64)
	if !ok {
		return 0, true, ErrInvalidNumericDate
	}
	// Reject fractional and negative values. f != math.Trunc(f) catches
	// fractions; f < 0 catches negatives; f > max catches ms-scale and
	// other huge values without auto-coercion.
	if f < 0 || f > float64(maxNumericDate) {
		return 0, true, ErrInvalidNumericDate
	}
	if f != float64(int64(f)) {
		return 0, true, ErrInvalidNumericDate
	}
	return int64(f), true, nil
}

// validateStandardClaims runs the registered-claim checks: exp / nbf / iat
// shape and time bounds, iss equality, aud match. Custom claim hooks run
// in jwt.go after this returns.
//
// now and leeway are passed in so tests can fix the clock; in production
// New closes over time.Now.
func validateStandardClaims(claims map[string]any, issuer, audience string, leeway time.Duration, now time.Time) error {
	// exp
	if exp, present, err := validateNumericDate(claims, "exp"); err != nil {
		return err
	} else if present {
		expTime := time.Unix(exp, 0)
		if now.After(expTime.Add(leeway)) {
			return ErrExpired
		}
	}

	// nbf
	if nbf, present, err := validateNumericDate(claims, "nbf"); err != nil {
		return err
	} else if present {
		nbfTime := time.Unix(nbf, 0)
		if now.Add(leeway).Before(nbfTime) {
			return ErrNotYetValid
		}
	}

	// iat — shape only
	if _, _, err := validateNumericDate(claims, "iat"); err != nil {
		return err
	}

	// iss
	if issuer != "" {
		raw, ok := claims["iss"]
		if !ok {
			return ErrInvalidIssuer
		}
		s, ok := raw.(string)
		if !ok {
			return ErrInvalidIssuer
		}
		if subtle.ConstantTimeCompare([]byte(s), []byte(issuer)) != 1 {
			return ErrInvalidIssuer
		}
	}

	// aud — string or []any of strings
	if audience != "" {
		raw, ok := claims["aud"]
		if !ok {
			return ErrInvalidAudience
		}
		switch v := raw.(type) {
		case string:
			if subtle.ConstantTimeCompare([]byte(v), []byte(audience)) != 1 {
				return ErrInvalidAudience
			}
		case []any:
			matched := false
			for _, elem := range v {
				s, ok := elem.(string)
				if !ok {
					// Non-string element: reject cleanly, no silent ignore.
					return ErrInvalidAudience
				}
				if subtle.ConstantTimeCompare([]byte(s), []byte(audience)) == 1 {
					matched = true
					// Continue iterating so a later non-string element
					// still rejects the token rather than depending on
					// ordering.
				}
			}
			if !matched {
				return ErrInvalidAudience
			}
		default:
			return ErrInvalidAudience
		}
	}

	return nil
}
