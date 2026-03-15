package aarv

import (
	"bytes"
	"encoding/json"
	"io"
	"sync"
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

var stdJSONCodecBufferPool = sync.Pool{
	New: func() any {
		return bytes.NewBuffer(make([]byte, 0, 1024))
	},
}

// StdJSONCodec implements the canonical stdlib-backed JSON codec.
// It uses encoding/json and a pooled buffer for MarshalBytes.
type StdJSONCodec struct{}

// Decode reads JSON from r into v using encoding/json.
func (StdJSONCodec) Decode(r io.Reader, v any) error {
	return json.NewDecoder(r).Decode(v)
}

// Encode writes v to w as JSON using encoding/json.
func (StdJSONCodec) Encode(w io.Writer, v any) error {
	return json.NewEncoder(w).Encode(v)
}

// UnmarshalBytes decodes JSON bytes into v using encoding/json.
func (StdJSONCodec) UnmarshalBytes(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

// MarshalBytes encodes v to JSON bytes using a pooled buffer.
func (StdJSONCodec) MarshalBytes(v any) ([]byte, error) {
	buf := stdJSONCodecBufferPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer stdJSONCodecBufferPool.Put(buf)

	if err := json.NewEncoder(buf).Encode(v); err != nil {
		return nil, err
	}
	result := make([]byte, buf.Len())
	copy(result, buf.Bytes())
	return result, nil
}

// ContentType returns the MIME type produced by StdJSONCodec.
func (StdJSONCodec) ContentType() string {
	return "application/json"
}

// NewStdJSONCodec returns the canonical stdlib-backed JSON codec.
func NewStdJSONCodec() *StdJSONCodec {
	return &StdJSONCodec{}
}

// OptimizedJSONCodec is kept as a deprecated alias for backward compatibility.
// Deprecated: use StdJSONCodec or NewStdJSONCodec.
type OptimizedJSONCodec = StdJSONCodec

// NewOptimizedJSONCodec is kept as a deprecated constructor alias.
// Deprecated: use NewStdJSONCodec.
func NewOptimizedJSONCodec() *OptimizedJSONCodec {
	return NewStdJSONCodec()
}
