package aarv

import (
	"bytes"
	"testing"
)

func TestStdJSONCodecCoverage(t *testing.T) {
	codec := StdJSONCodec{}

	var encoded bytes.Buffer
	if err := codec.Encode(&encoded, map[string]string{"message": "hello"}); err != nil {
		t.Fatalf("encode failed: %v", err)
	}
	if got := encoded.String(); got == "" || got[0] != '{' {
		t.Fatalf("unexpected encoded payload %q", got)
	}

	var decoded map[string]string
	if err := codec.UnmarshalBytes([]byte(`{"message":"hello"}`), &decoded); err != nil {
		t.Fatalf("unmarshal bytes failed: %v", err)
	}
	if decoded["message"] != "hello" {
		t.Fatalf("unexpected decoded value %#v", decoded)
	}
}

func TestStdJSONCodecConstructors(t *testing.T) {
	if codec := NewStdJSONCodec(); codec == nil {
		t.Fatal("expected std codec constructor to return a codec")
	}
	if codec := NewOptimizedJSONCodec(); codec == nil {
		t.Fatal("expected deprecated optimized codec alias constructor to return a codec")
	}
}
