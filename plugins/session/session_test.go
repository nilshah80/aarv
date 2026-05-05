package session

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestSessionGetSetDelete(t *testing.T) {
	s := newSession("id-1", true, defaultIDLen)
	if v, ok := s.Get("missing"); ok || v != nil {
		t.Fatalf("Get on missing key returned (%v, %v)", v, ok)
	}
	if s.dirty {
		t.Fatal("session should not be dirty before any mutation")
	}
	s.Set("name", "alice")
	if !s.dirty {
		t.Fatal("Set should mark session dirty")
	}
	v, ok := s.Get("name")
	if !ok || v != "alice" {
		t.Fatalf("Get(name) = (%v, %v)", v, ok)
	}
	// Delete on present key flips dirty even when previously clean.
	s2 := newSession("id-2", false, defaultIDLen)
	s2.data["k"] = "v"
	s2.Delete("k")
	if !s2.dirty {
		t.Fatal("Delete on present key should mark dirty")
	}
	// Delete on missing key is a no-op.
	s3 := newSession("id-3", false, defaultIDLen)
	s3.Delete("missing")
	if s3.dirty {
		t.Fatal("Delete on missing key must not flip dirty")
	}
}

func TestSessionFlashLifecycle(t *testing.T) {
	// Round 1: write a flash.
	s1 := newSession("id-1", true, defaultIDLen)
	s1.Flash("notice", "saved")
	stored := s1.toStored()
	if stored.Flash["notice"] != "saved" {
		t.Fatal("flash not in stored snapshot")
	}

	// Round 2: load from stored, consume.
	s2 := sessionFromStored("id-1", stored, defaultIDLen)
	if v, ok := s2.ConsumeFlash("notice"); !ok || v != "saved" {
		t.Fatalf("ConsumeFlash = (%v, %v)", v, ok)
	}
	if len(s2.consumed) != 1 {
		t.Fatal("consumed must be tracked so save knows to rewrite")
	}
	if !shouldSave(s2) {
		t.Fatal("consumed flash must force save")
	}

	// Round 3: stored after save no longer carries the consumed key.
	stored2 := s2.toStored()
	if _, ok := stored2.Flash["notice"]; ok {
		t.Fatal("consumed flash should not persist into next round")
	}

	// Round 4: missing flash returns (nil, false).
	s3 := sessionFromStored("id-1", stored2, defaultIDLen)
	if v, ok := s3.ConsumeFlash("notice"); ok || v != nil {
		t.Fatalf("missing flash returned (%v, %v)", v, ok)
	}
}

func TestSessionRegenerate(t *testing.T) {
	s := newSession("orig-id", false, defaultIDLen)
	s.Set("user", "alice")
	if err := s.Regenerate(); err != nil {
		t.Fatalf("Regenerate: %v", err)
	}
	if s.id == "orig-id" {
		t.Fatal("Regenerate must change ID")
	}
	if s.oldID != "orig-id" {
		t.Fatalf("oldID = %q; want %q", s.oldID, "orig-id")
	}
	if v, _ := s.Get("user"); v != "alice" {
		t.Fatal("Regenerate must preserve data")
	}
	// Multiple Regenerate in one request should keep the *original* oldID.
	prevNewID := s.id
	if err := s.Regenerate(); err != nil {
		t.Fatalf("second Regenerate: %v", err)
	}
	if s.oldID != "orig-id" {
		t.Fatalf("oldID after 2x Regenerate = %q; want %q", s.oldID, "orig-id")
	}
	if s.id == prevNewID {
		t.Fatal("second Regenerate must produce a new ID")
	}
	if !s.regenerated {
		t.Fatal("regenerated flag must be set")
	}
}

func TestSessionDestroy(t *testing.T) {
	s := newSession("id", false, defaultIDLen)
	s.Set("k", "v")
	s.Destroy()
	if !s.destroyed {
		t.Fatal("Destroy must set destroyed")
	}
	// Mutations after Destroy are no-ops; Get returns zero.
	s.Set("k2", "v2")
	if _, ok := s.Get("k"); ok {
		t.Fatal("Get must return false after Destroy")
	}
	if _, ok := s.Get("k2"); ok {
		t.Fatal("Set after Destroy must be a no-op")
	}
	// Regenerate / CSRFToken after Destroy report ErrSessionDestroyed.
	if err := s.Regenerate(); !errors.Is(err, ErrSessionDestroyed) {
		t.Fatalf("Regenerate after Destroy = %v; want ErrSessionDestroyed", err)
	}
	if _, err := s.CSRFToken(); !errors.Is(err, ErrSessionDestroyed) {
		t.Fatalf("CSRFToken after Destroy = %v", err)
	}
}

func TestSessionCSRFToken(t *testing.T) {
	s := newSession("id", true, defaultIDLen)
	tok1, err := s.CSRFToken()
	if err != nil {
		t.Fatalf("CSRFToken: %v", err)
	}
	if tok1 == "" {
		t.Fatal("CSRFToken returned empty string")
	}
	if !s.dirty {
		t.Fatal("CSRFToken first issuance must dirty session")
	}
	// Stable across calls in one request.
	tok2, _ := s.CSRFToken()
	if tok1 != tok2 {
		t.Fatal("CSRFToken must be stable within a request")
	}
	// Survives a save/load round-trip via Stored.CSRF.
	stored := s.toStored()
	s2 := sessionFromStored(s.id, stored, defaultIDLen)
	tok3, err := s2.CSRFToken()
	if err != nil {
		t.Fatalf("CSRFToken on loaded session: %v", err)
	}
	if tok3 != tok1 {
		t.Fatal("CSRFToken must persist across round-trip")
	}
	// CSRF is not exposed via Get.
	if v, ok := s.Get("_csrf"); ok || v != nil {
		t.Fatal("CSRF must not be reachable via Get")
	}
}

func TestGenerateIDPropagatesError(t *testing.T) {
	old := randReader
	t.Cleanup(func() { randReader = old })
	randReader = io.NopCloser(bytes.NewReader(nil))

	s := newSession("orig", false, defaultIDLen)
	if err := s.Regenerate(); err == nil {
		t.Fatal("Regenerate must report rand failure")
	}
	if _, err := s.CSRFToken(); err == nil {
		t.Fatal("CSRFToken must report rand failure")
	}
}

func TestShouldSavePredicate(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Session)
		want bool
	}{
		{"clean read", func(s *Session) {}, false},
		{"new only", func(s *Session) { s.isNew = true }, false},
		{"after Set", func(s *Session) { s.Set("k", "v") }, true},
		{"after consumed flash", func(s *Session) {
			s.flash["k"] = "v"
			_, _ = s.ConsumeFlash("k")
		}, true},
		{"after regenerate", func(s *Session) { _ = s.Regenerate() }, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newSession("id", false, defaultIDLen)
			tc.mut(s)
			if got := shouldSave(s); got != tc.want {
				t.Fatalf("shouldSave = %v; want %v", got, tc.want)
			}
		})
	}
}

func TestSessionIsNew(t *testing.T) {
	if !newSession("id", true, defaultIDLen).IsNew() {
		t.Fatal("IsNew should be true for fresh session")
	}
	if newSession("id", false, defaultIDLen).IsNew() {
		t.Fatal("IsNew should be false for loaded session")
	}
}

func TestStoredShape(t *testing.T) {
	st := &Stored{
		Data:  map[string]any{"k": "v"},
		Flash: map[string]any{"f": "x"},
		CSRF:  "tok",
	}
	clone := cloneStored(st)
	clone.Data["k"] = "mutated"
	if st.Data["k"] != "v" {
		t.Fatal("cloneStored must not alias Data map")
	}
	clone.Flash["f"] = "mutated"
	if st.Flash["f"] != "x" {
		t.Fatal("cloneStored must not alias Flash map")
	}
}

// quick sanity: session ID encoding looks like base64url.
func TestGenerateIDEncoding(t *testing.T) {
	id, err := generateID(32)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(id, "+/=") {
		t.Fatalf("session ID %q should be base64url (no +/=)", id)
	}
}
