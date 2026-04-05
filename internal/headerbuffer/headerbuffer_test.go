package headerbuffer

import "testing"

func TestBufferHeaderLazyAlloc(t *testing.T) {
	var b Buffer
	h := b.Header()
	if h == nil {
		t.Fatal("expected non-nil header on first call")
	}
	h.Set("X-Test", "value")
	if got := b.Header().Get("X-Test"); got != "value" {
		t.Fatalf("expected header to persist, got %q", got)
	}
}

func TestBufferReset(t *testing.T) {
	var b Buffer
	b.Header().Set("X-Keep", "yes")
	b.Reset()
	if got := b.Header().Get("X-Keep"); got != "" {
		t.Fatalf("expected cleared header after Reset, got %q", got)
	}
}

func TestBufferResetBeforeHeader(t *testing.T) {
	var b Buffer
	b.Reset() // should not panic on nil map
}

func TestBufferCopyTo(t *testing.T) {
	var b Buffer
	b.Header().Set("X-Src", "val")
	b.Header().Add("X-Multi", "a")
	b.Header().Add("X-Multi", "b")

	dst := make(map[string][]string)
	b.CopyTo(dst)

	if dst["X-Src"][0] != "val" {
		t.Fatalf("expected X-Src copied, got %v", dst["X-Src"])
	}
	if len(dst["X-Multi"]) != 2 {
		t.Fatalf("expected multi-value copied, got %v", dst["X-Multi"])
	}
}

func TestBufferCopyToEmpty(t *testing.T) {
	var b Buffer
	dst := make(map[string][]string)
	b.CopyTo(dst) // nil map — should not panic
	if len(dst) != 0 {
		t.Fatal("expected empty dst after CopyTo from uninitialized buffer")
	}
}
