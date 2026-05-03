package idempotencyredis

import (
	"strings"
	"testing"
	"time"

	"github.com/nilshah80/aarv/plugins/idempotency"
)

// TestSave_RejectsNonPositiveTTL guards against the go-redis quirk
// where Set(..., 0) creates a non-expiring key. A non-positive ttl
// reaching this Store is always a misconfiguration — the
// idempotency middleware never calls Save with TTL <= 0 (the
// per-route opt-out path short-circuits before Save), so direct
// callers misusing the public Store cannot create undeletable
// entries by mistake.
func TestSave_RejectsNonPositiveTTL(t *testing.T) {
	s, _, _ := newStore(t)
	resp := &idempotency.Response{StatusCode: 200, Body: []byte("x")}

	cases := []struct {
		name string
		ttl  time.Duration
	}{
		{"zero", 0},
		{"negative", -time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := s.Save("k", resp, tc.ttl)
			if err == nil {
				t.Fatalf("expected error for ttl=%v", tc.ttl)
			}
			if !strings.Contains(err.Error(), "ttl") {
				t.Fatalf("error should mention ttl, got %v", err)
			}
		})
	}
}
