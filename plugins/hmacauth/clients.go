package hmacauth

import "errors"

// Client carries the metadata needed to verify a signed request.
// The HMAC verifier needs the raw secret bytes — there is no way to
// verify a digest against a hash, so secrets MUST live in memory in
// plaintext for the lifetime of the process.
//
// Operational consequences:
//
//   - Treat the loaded Client struct as a credential. Do not log it,
//     do not include it in error messages, do not embed it in
//     telemetry payloads. The plugin itself never logs Secret or
//     Secrets.
//   - Source secrets from a secrets manager at startup (or via a
//     periodic refresh hook). Storing them in environment variables is
//     acceptable; in code or in plain config files is not.
//   - During key rotation, populate Secrets with the new + old bytes
//     and remove the old one only after every signing client has
//     migrated. The verifier checks every entry without short-
//     circuiting (see Identity rotation in package doc).
type Client struct {
	// ClientID is the public identifier echoed in the X-Client-Id
	// header. Used for nonce isolation, telemetry labels, and
	// rate-limit keys.
	ClientID string

	// Secret is the primary HMAC key. Either Secret or Secrets must
	// be populated; supplying both is permitted, the verifier checks
	// every candidate.
	Secret []byte

	// Secrets is the rotation set. Populate alongside Secret while a
	// rotation is in progress. Empty/nil entries are skipped; a
	// rotation set containing only nil entries is treated identically
	// to a missing client.
	Secrets [][]byte

	// Identity is opaque caller-supplied metadata stored on the
	// request context on successful verification. Recover via From or
	// FromContext.
	Identity any
}

// Validator returns the Client matching clientID.
//
// Returning a non-nil error rejects the request with a generic 401 —
// the middleware does not pass the error through to the response so a
// detailed validator error cannot leak information to the caller.
//
// Returning (Client{}, nil) is treated as "unknown client" → 401. A
// validator should NOT manufacture a placeholder client for unknown
// IDs, even one with a random secret, because the timing of the
// resulting HMAC compare can leak the answer to "is this client ID
// real?". Returning the zero value lets the middleware short-circuit
// without doing the compare.
type Validator func(clientID string) (Client, error)

// StaticClients returns a Validator that authenticates against an
// in-memory clientID → Client map. The map is snapshotted at
// construction; subsequent changes to the source map do not affect
// authentication decisions.
//
// ClientID handling:
//
//   - The map key is the authoritative client ID. Empty keys panic
//     at construction — an empty client ID is meaningless and would
//     collapse the per-client nonce namespace ("nonce:" + ClientID +
//     ":" + nonce).
//   - When the Client's ClientID field is empty, it is normalized
//     to the map key. This is the common case — callers usually
//     don't repeat the ID in both places.
//   - When the Client's ClientID field is non-empty, it MUST match
//     the map key. A mismatch panics at construction. Allowing
//     drift would silently mis-key downstream rate-limit / audit
//     identifiers built from hmacauth.From(c).ClientID.
//
// The validator returns an unknown-client sentinel error for any
// clientID not in the map — the caller does not need to distinguish
// "unknown" from "found" because the middleware treats both error
// and zero-Client identically.
func StaticClients(clients map[string]Client) Validator {
	snapshot := make(map[string]Client, len(clients))
	for id, c := range clients {
		if id == "" {
			panic("hmacauth: StaticClients map cannot contain an empty client ID")
		}
		switch c.ClientID {
		case "":
			c.ClientID = id
		case id:
			// already consistent
		default:
			panic("hmacauth: StaticClients Client.ClientID does not match map key " + id)
		}
		// Defensive copy of the secret slices so a later mutation of
		// the caller's source map cannot change verification behavior
		// once StaticClients has returned.
		c.Secret = append([]byte(nil), c.Secret...)
		if c.Secrets != nil {
			cp := make([][]byte, len(c.Secrets))
			for i, s := range c.Secrets {
				cp[i] = append([]byte(nil), s...)
			}
			c.Secrets = cp
		}
		snapshot[id] = c
	}
	return func(id string) (Client, error) {
		c, ok := snapshot[id]
		if !ok {
			return Client{}, errUnknownClient
		}
		return c, nil
	}
}

var errUnknownClient = errors.New("hmacauth: unknown client")
