package jsonv2

import (
	"bytes"
	"strings"
	"testing"
)

type samplePayload struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func TestJSONv2CodecRoundTrip(t *testing.T) {
	c := New()
	if c.ContentType() != "application/json; charset=utf-8" {
		t.Fatalf("content type = %q", c.ContentType())
	}

	in := samplePayload{Name: "aarv", Count: 3}
	data, err := c.MarshalBytes(in)
	if err != nil {
		t.Fatalf("MarshalBytes: %v", err)
	}

	var fromBytes samplePayload
	if err := c.UnmarshalBytes(data, &fromBytes); err != nil {
		t.Fatalf("UnmarshalBytes: %v", err)
	}
	if fromBytes != in {
		t.Fatalf("byte round trip = %+v, want %+v", fromBytes, in)
	}

	var buf bytes.Buffer
	if err := c.Encode(&buf, in); err != nil {
		t.Fatalf("Encode: %v", err)
	}

	var fromReader samplePayload
	if err := c.Decode(&buf, &fromReader); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if fromReader != in {
		t.Fatalf("stream round trip = %+v, want %+v", fromReader, in)
	}
}

func TestJSONv2CodecRejectsInvalidJSON(t *testing.T) {
	c := New()
	var out samplePayload
	if err := c.UnmarshalBytes([]byte("{bad"), &out); err == nil {
		t.Fatal("expected UnmarshalBytes error")
	}
	if err := c.Decode(strings.NewReader("{bad"), &out); err == nil {
		t.Fatal("expected Decode error")
	}
}
