package aarv

import (
	"testing"
	"time"
)

// TestRoutes_DeepCopiesIdempotencyTTLPointer guards against the
// shallow-copy footgun documented on Routes(): a caller that
// mutates *Routes()[i].IdempotencyTTL must not be able to alter
// framework-owned route metadata read on subsequent dispatches.
func TestRoutes_DeepCopiesIdempotencyTTLPointer(t *testing.T) {
	a := New()
	a.Post("/items", func(c *Context) error { return c.NoContent(204) }, WithRouteIdempotencyTTL(7*time.Minute))

	first := a.Routes()
	if len(first) == 0 || first[0].IdempotencyTTL == nil {
		t.Fatalf("setup: expected IdempotencyTTL populated")
	}
	if *first[0].IdempotencyTTL != 7*time.Minute {
		t.Fatalf("setup: got %v want 7m", *first[0].IdempotencyTTL)
	}

	// Mutate the pointer target on the returned snapshot.
	*first[0].IdempotencyTTL = 99 * time.Hour

	// Subsequent Routes() must return the original value, not the
	// caller's mutation.
	second := a.Routes()
	if second[0].IdempotencyTTL == nil {
		t.Fatalf("second snapshot lost IdempotencyTTL")
	}
	if *second[0].IdempotencyTTL != 7*time.Minute {
		t.Fatalf("Routes() shallow-copied IdempotencyTTL: framework state mutated to %v", *second[0].IdempotencyTTL)
	}

	// Pointer identity should also differ across snapshots so
	// callers cannot share state by retaining the pointer across
	// Routes() calls.
	if first[0].IdempotencyTTL == second[0].IdempotencyTTL {
		t.Fatalf("Routes() returned the same *time.Duration across calls — pointer must be independently allocated")
	}
}
