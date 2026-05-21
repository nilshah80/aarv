package hmacauth

import (
	"time"

	"github.com/nilshah80/aarv"
)

// Outcome classifies the result of one HMAC verification attempt. It is
// the value observers (loggers, tracers, metrics) should switch on,
// rather than the response status — multiple outcomes collapse to the
// same status (most failures are 401) and the response status alone
// hides the operationally interesting distinction between, say, a clock
// skew failure and a malformed signature.
//
// The set is closed: future outcomes will only be added in a backward-
// compatible way. Observers should treat unknown outcomes as a
// "verification did not pass" signal rather than panicking.
type Outcome string

const (
	// OutcomeOK is set when the request signature, timestamp, body
	// hash, and nonce all verified successfully. The Event's Status is
	// 0 — the auth middleware wrote nothing; the handler that runs
	// after auth chooses the actual response status.
	OutcomeOK Outcome = "ok"

	// OutcomeUnauthorized is the catch-all bucket for verification
	// failures that aren't more specifically classified: missing
	// required headers, unparseable timestamp, unknown client (the
	// Validator returned an error or an empty Client), missing secret
	// material on a known client, or a generic 401 from the response
	// path.
	OutcomeUnauthorized Outcome = "unauthorized"

	// OutcomeClockSkew is set when the signed timestamp is outside the
	// configured SkewSeconds window. The Event's SkewSeconds field is
	// populated with the absolute drift in seconds so observers can
	// alert on "many clients drifting by ~30s" patterns.
	OutcomeClockSkew Outcome = "clock_skew"

	// OutcomeSignatureInvalid is set when the X-Signature header is
	// malformed (bad hex, wrong length) OR when the constant-time
	// compare against every candidate secret failed. These two cases
	// are merged because separating them would tell an attacker which
	// branch of the verifier rejected their request.
	OutcomeSignatureInvalid Outcome = "signature_invalid"

	// OutcomeReplayDetected is set when the NonceStore.SetNX call
	// returned !fresh — the (clientID, nonce) pair has been seen
	// within the nonce TTL. Distinct from OutcomeUnauthorized so
	// dashboards can alert on replay attempts specifically.
	OutcomeReplayDetected Outcome = "replay_detected"

	// OutcomeBodyTooLarge is set when the request body exceeded
	// MaxBodyBytes. The verifier never reaches the signature check in
	// this case; the response is 413, not 401.
	OutcomeBodyTooLarge Outcome = "body_too_large"
)

// Event is the payload an Observer receives after each verification
// attempt. The struct is value-typed and self-contained so observers
// can defer it, send it to a channel, or accumulate it for batching
// without thinking about lifetime.
//
// Status is the HTTP status the middleware itself wrote on a failure
// (401 for auth failures, 413 for body overflow). On OutcomeOK the
// middleware wrote nothing — the handler chooses the response status —
// so Status is 0. Observers that want the terminal response status of
// the entire request should instrument with a separate response-writer
// recorder on top of the auth observer.
type Event struct {
	// Outcome classifies the verification result.
	Outcome Outcome

	// ClientID is the value of the X-Client-Id header on the request.
	// Populated even on most failure paths (the header is read before
	// validation). Empty when the header was missing.
	ClientID string

	// Status is the HTTP status the middleware would write for this
	// outcome. 401 for auth failures, 413 for body overflow, 0 for
	// OutcomeOK (the handler picks the success status).
	Status int

	// SkewSeconds carries the absolute timestamp drift in seconds, but
	// only when Outcome == OutcomeClockSkew. Zero for every other
	// outcome — observers should not interpret a zero value as
	// "drift was zero" outside the clock-skew case.
	SkewSeconds int64

	// Duration is wall-clock time spent in verification: header reads,
	// body read+hash, signature compare, and nonce-store SetNX. Useful
	// for operators tracking the cost of HMAC auth — particularly the
	// SetNX round-trip when NonceStore is a remote Redis.
	Duration time.Duration
}

// Observer is invoked exactly once per verification attempt that the
// middleware actually runs (Skipper-bypassed paths emit nothing). The
// Observer runs synchronously on the request hot path; do not block.
//
// nil is the zero-overhead default — an unset Observer adds no calls
// and no allocations. Setting an Observer adds one struct copy and one
// indirect call per request.
//
// Observers must not import OpenTelemetry or any other observability
// dependency *into the root aarv module* — hmacauth lives in the root
// module and the root contract is zero-dep. Tracing-aware adapters
// belong in a separate companion module (e.g.
// plugins/hmacauth-otel/) that depends on this package and on its own
// observability stack.
type Observer func(c *aarv.Context, e Event)

