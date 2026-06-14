package sonic

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
)

type samplePayload struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func TestSonicCodecRoundTrip(t *testing.T) {
	for name, c := range map[string]*SonicCodec{
		"default": New(),
		"fastest": NewFastest(),
	} {
		t.Run(name, func(t *testing.T) {
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
		})
	}
}

func TestSonicCodecRejectsInvalidJSON(t *testing.T) {
	c := New()
	var out samplePayload
	if err := c.UnmarshalBytes([]byte("{bad"), &out); err == nil {
		t.Fatal("expected UnmarshalBytes error")
	}
	if err := c.Decode(strings.NewReader("{bad"), &out); err == nil {
		t.Fatal("expected Decode error")
	}
}

func TestSonicPretouch(t *testing.T) {
	c := New()
	if err := c.Pretouch(samplePayload{}); err != nil {
		t.Fatalf("method Pretouch: %v", err)
	}
	if err := Pretouch(samplePayload{}); err != nil {
		t.Fatalf("package Pretouch: %v", err)
	}
	if err := c.Pretouch(); err != nil {
		t.Fatalf("method Pretouch without types: %v", err)
	}
	if err := Pretouch(); err != nil {
		t.Fatalf("package Pretouch without types: %v", err)
	}
}

func TestSonicPretouchWithReturnsFirstError(t *testing.T) {
	want := errors.New("pretouch failed")
	calls := 0
	err := pretouchWith(func(rt reflect.Type) error {
		calls++
		if rt != reflect.TypeOf(samplePayload{}) {
			t.Fatalf("type = %v", rt)
		}
		return want
	}, samplePayload{})
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}
