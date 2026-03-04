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

// OptimizedJSONCodec implements Codec with sync.Pool buffering for better performance.
// It reuses byte buffers to reduce allocations during encoding.
type OptimizedJSONCodec struct {
	pool sync.Pool
}

// NewOptimizedJSONCodec creates a new OptimizedJSONCodec with pooled buffers.
func NewOptimizedJSONCodec() *OptimizedJSONCodec {
	return &OptimizedJSONCodec{
		pool: sync.Pool{
			New: func() any {
				return bytes.NewBuffer(make([]byte, 0, 1024))
			},
		},
	}
}

func (c *OptimizedJSONCodec) Decode(r io.Reader, v any) error {
	return json.NewDecoder(r).Decode(v)
}

func (c *OptimizedJSONCodec) Encode(w io.Writer, v any) error {
	buf := c.pool.Get().(*bytes.Buffer)
	buf.Reset()
	defer c.pool.Put(buf)

	if err := json.NewEncoder(buf).Encode(v); err != nil {
		return err
	}
	_, err := w.Write(buf.Bytes())
	return err
}

func (c *OptimizedJSONCodec) UnmarshalBytes(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

func (c *OptimizedJSONCodec) MarshalBytes(v any) ([]byte, error) {
	buf := c.pool.Get().(*bytes.Buffer)
	buf.Reset()
	defer c.pool.Put(buf)

	if err := json.NewEncoder(buf).Encode(v); err != nil {
		return nil, err
	}
	// Return a copy since we're returning the buffer to the pool
	result := make([]byte, buf.Len())
	copy(result, buf.Bytes())
	return result, nil
}

func (c *OptimizedJSONCodec) ContentType() string {
	return "application/json"
}
