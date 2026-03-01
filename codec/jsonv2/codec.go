// Package jsonv2 implements the aarv.Codec interface using the
// github.com/go-json-experiment/json library, the experimental v2 JSON
// package for Go.
package jsonv2

import (
	"io"

	"github.com/go-json-experiment/json"
)

// JSONv2Codec implements aarv.Codec using go-json-experiment/json.
type JSONv2Codec struct{}

// New returns a new JSONv2Codec.
func New() *JSONv2Codec {
	return &JSONv2Codec{}
}

// Decode reads JSON from r and unmarshals it into v.
func (c *JSONv2Codec) Decode(r io.Reader, v any) error {
	return json.UnmarshalRead(r, v)
}

// Encode marshals v as JSON and writes it to w.
func (c *JSONv2Codec) Encode(w io.Writer, v any) error {
	return json.MarshalWrite(w, v)
}

// UnmarshalBytes unmarshals JSON bytes into v.
func (c *JSONv2Codec) UnmarshalBytes(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

// MarshalBytes marshals v into JSON bytes.
func (c *JSONv2Codec) MarshalBytes(v any) ([]byte, error) {
	return json.Marshal(v)
}

// ContentType returns the MIME type for JSON content.
func (c *JSONv2Codec) ContentType() string {
	return "application/json; charset=utf-8"
}
