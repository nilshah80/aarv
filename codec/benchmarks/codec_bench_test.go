// Package benchmarks compares the aarv codec sub-packages against each other
// and the standard library across small, medium, and large JSON payloads.
//
// This directory is its own Go module, so run the suite from *inside* it:
//
//	cd codec/benchmarks
//	go test -bench=. -benchmem ./...
//
// Running `go test ./codec/benchmarks/...` from the repo root will fail with
// "directory prefix codec/benchmarks does not contain main module" because
// the root module does not include this sub-module.
//
// Recorded results for a representative run live in RESULTS.md next to this
// file.
package benchmarks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"testing"

	"github.com/nilshah80/aarv/codec/jsonv2"
	"github.com/nilshah80/aarv/codec/segmentio"
	"github.com/nilshah80/aarv/codec/sonic"
)

// Codec is the minimal interface implemented by every aarv codec.
// It is redeclared locally to avoid a dependency on the root aarv module.
type Codec interface {
	Encode(w io.Writer, v any) error
	Decode(r io.Reader, v any) error
	MarshalBytes(v any) ([]byte, error)
	UnmarshalBytes(data []byte, v any) error
}

// stdlibCodec wraps encoding/json so it can participate in the same benches.
type stdlibCodec struct{}

func (stdlibCodec) Encode(w io.Writer, v any) error    { return json.NewEncoder(w).Encode(v) }
func (stdlibCodec) Decode(r io.Reader, v any) error    { return json.NewDecoder(r).Decode(v) }
func (stdlibCodec) MarshalBytes(v any) ([]byte, error) { return json.Marshal(v) }
func (stdlibCodec) UnmarshalBytes(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

type smallPayload struct {
	ID      int               `json:"id"`
	Name    string            `json:"name"`
	Email   string            `json:"email"`
	Active  bool              `json:"active"`
	Tags    []string          `json:"tags"`
	Meta    map[string]string `json:"meta"`
	Balance float64           `json:"balance"`
}

type mediumPayload struct {
	ID    int            `json:"id"`
	Title string         `json:"title"`
	Body  string         `json:"body"`
	Items []smallPayload `json:"items"`
}

type largePayload struct {
	Items []mediumPayload `json:"items"`
}

func makeSmall() smallPayload {
	return smallPayload{
		ID:      42,
		Name:    "alice",
		Email:   "alice@example.com",
		Active:  true,
		Tags:    []string{"admin", "editor", "user"},
		Meta:    map[string]string{"team": "platform", "region": "us"},
		Balance: 1234.56,
	}
}

func makeMedium() mediumPayload {
	items := make([]smallPayload, 10)
	for i := range items {
		items[i] = makeSmall()
		items[i].ID = i
	}
	return mediumPayload{
		ID:    1,
		Title: "Quarterly performance summary for the platform team",
		Body:  "A medium-sized payload roughly representative of a typical API response with nested records and descriptive text fields.",
		Items: items,
	}
}

func makeLarge() largePayload {
	items := make([]mediumPayload, 50)
	for i := range items {
		items[i] = makeMedium()
		items[i].ID = i
	}
	return largePayload{Items: items}
}

type codecCase struct {
	name  string
	codec Codec
}

func codecs() []codecCase {
	return []codecCase{
		{"stdlib", stdlibCodec{}},
		{"segmentio", segmentio.New()},
		{"sonic", sonic.New()},
		{"sonic-fastest", sonic.NewFastest()},
		{"jsonv2", jsonv2.New()},
	}
}

type payloadCase struct {
	name  string
	value any
	dst   func() any
}

func payloads() []payloadCase {
	return []payloadCase{
		{"small", makeSmall(), func() any { return new(smallPayload) }},
		{"medium", makeMedium(), func() any { return new(mediumPayload) }},
		{"large", makeLarge(), func() any { return new(largePayload) }},
	}
}

func BenchmarkMarshal(b *testing.B) {
	for _, p := range payloads() {
		for _, c := range codecs() {
			b.Run(fmt.Sprintf("%s/%s", p.name, c.name), func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					if _, err := c.codec.MarshalBytes(p.value); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func BenchmarkUnmarshal(b *testing.B) {
	for _, p := range payloads() {
		data, err := json.Marshal(p.value)
		if err != nil {
			b.Fatal(err)
		}
		for _, c := range codecs() {
			b.Run(fmt.Sprintf("%s/%s", p.name, c.name), func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					if err := c.codec.UnmarshalBytes(data, p.dst()); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func BenchmarkEncode(b *testing.B) {
	for _, p := range payloads() {
		for _, c := range codecs() {
			b.Run(fmt.Sprintf("%s/%s", p.name, c.name), func(b *testing.B) {
				b.ReportAllocs()
				var buf bytes.Buffer
				for i := 0; i < b.N; i++ {
					buf.Reset()
					if err := c.codec.Encode(&buf, p.value); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func BenchmarkDecode(b *testing.B) {
	for _, p := range payloads() {
		data, err := json.Marshal(p.value)
		if err != nil {
			b.Fatal(err)
		}
		for _, c := range codecs() {
			b.Run(fmt.Sprintf("%s/%s", p.name, c.name), func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					if err := c.codec.Decode(bytes.NewReader(data), p.dst()); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
