// Package segmentio implements the aarv.Codec interface using the
// github.com/segmentio/encoding/json library, a high-performance drop-in
// replacement for encoding/json.
package segmentio

import (
	"io"

	"github.com/segmentio/encoding/json"
)

// SegmentioCodec implements aarv.Codec using segmentio/encoding/json.
type SegmentioCodec struct{}

// New returns a new SegmentioCodec.
func New() *SegmentioCodec {
	return &SegmentioCodec{}
}

// Decode reads JSON from r and unmarshals it into v.
func (c *SegmentioCodec) Decode(r io.Reader, v any) error {
	return json.NewDecoder(r).Decode(v)
}

// Encode marshals v as JSON and writes it to w.
func (c *SegmentioCodec) Encode(w io.Writer, v any) error {
	return json.NewEncoder(w).Encode(v)
}

// UnmarshalBytes unmarshals JSON bytes into v.
func (c *SegmentioCodec) UnmarshalBytes(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

// MarshalBytes marshals v into JSON bytes.
func (c *SegmentioCodec) MarshalBytes(v any) ([]byte, error) {
	return json.Marshal(v)
}

// ContentType returns the MIME type for JSON content.
func (c *SegmentioCodec) ContentType() string {
	return "application/json; charset=utf-8"
}
