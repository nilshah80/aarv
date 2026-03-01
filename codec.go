package aarv

import (
	"encoding/json"
	"io"
)

// Codec defines the interface for encoding/decoding request and response bodies.
type Codec interface {
	// Decode reads from r and unmarshals into v (stream-based, for request bodies).
	Decode(r io.Reader, v any) error
	// Encode marshals v and writes to w (stream-based, for response bodies).
	Encode(w io.Writer, v any) error
	// UnmarshalBytes unmarshals pre-read bytes into v (for cached/small payloads).
	UnmarshalBytes(data []byte, v any) error
	// MarshalBytes marshals v into bytes (for pre-serialization).
	MarshalBytes(v any) ([]byte, error)
	// ContentType returns the MIME type for this codec.
	ContentType() string
}

// StdJSONCodec implements Codec using encoding/json from the standard library.
type StdJSONCodec struct{}

func (StdJSONCodec) Decode(r io.Reader, v any) error {
	return json.NewDecoder(r).Decode(v)
}

func (StdJSONCodec) Encode(w io.Writer, v any) error {
	return json.NewEncoder(w).Encode(v)
}

func (StdJSONCodec) UnmarshalBytes(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

func (StdJSONCodec) MarshalBytes(v any) ([]byte, error) {
	return json.Marshal(v)
}

func (StdJSONCodec) ContentType() string {
	return "application/json"
}
