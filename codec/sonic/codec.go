// Package sonic implements the aarv.Codec interface using the
// github.com/bytedance/sonic library, an extremely fast JSON
// serialization library from ByteDance.
package sonic

import (
	"io"
	"reflect"

	"github.com/bytedance/sonic"
)

// SonicCodec implements aarv.Codec using bytedance/sonic.
type SonicCodec struct {
	api sonic.API
}

// New returns a new SonicCodec using sonic.ConfigDefault, which provides full
// validation and standard JSON compliance.
func New() *SonicCodec {
	return &SonicCodec{api: sonic.ConfigDefault}
}

// NewFastest returns a new SonicCodec using sonic.ConfigFastest, which disables
// validation for maximum performance.
func NewFastest() *SonicCodec {
	return &SonicCodec{api: sonic.ConfigFastest}
}

// Decode reads JSON from r and unmarshals it into v.
func (c *SonicCodec) Decode(r io.Reader, v any) error {
	return c.api.NewDecoder(r).Decode(v)
}

// Encode marshals v as JSON and writes it to w.
func (c *SonicCodec) Encode(w io.Writer, v any) error {
	return c.api.NewEncoder(w).Encode(v)
}

// UnmarshalBytes unmarshals JSON bytes into v.
func (c *SonicCodec) UnmarshalBytes(data []byte, v any) error {
	return c.api.Unmarshal(data, v)
}

// MarshalBytes marshals v into JSON bytes.
func (c *SonicCodec) MarshalBytes(v any) ([]byte, error) {
	return c.api.Marshal(v)
}

// ContentType returns the MIME type for JSON content.
func (c *SonicCodec) ContentType() string {
	return "application/json; charset=utf-8"
}

// Pretouch pre-compiles the encoder/decoder for the given types.
// Call this during application startup with your request/response types
// to eliminate JIT compilation overhead during the first request.
// Example: codec.Pretouch(MyRequest{}, MyResponse{}, []MyItem{})
func (c *SonicCodec) Pretouch(types ...any) error {
	for _, t := range types {
		if err := sonic.Pretouch(reflect.TypeOf(t)); err != nil {
			return err
		}
	}
	return nil
}

// Pretouch is a package-level function that pre-compiles types using the default config.
func Pretouch(types ...any) error {
	for _, t := range types {
		if err := sonic.Pretouch(reflect.TypeOf(t)); err != nil {
			return err
		}
	}
	return nil
}
