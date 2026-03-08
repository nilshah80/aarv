package aarv

import (
	"encoding/json"
	"io"
	"testing"
)

type codecBenchmarkPayload struct {
	ID      int               `json:"id"`
	Name    string            `json:"name"`
	Email   string            `json:"email"`
	Active  bool              `json:"active"`
	Tags    []string          `json:"tags"`
	Meta    map[string]string `json:"meta"`
	Balance float64           `json:"balance"`
}

func benchmarkCodecPayload() codecBenchmarkPayload {
	return codecBenchmarkPayload{
		ID:      42,
		Name:    "alice",
		Email:   "alice@example.com",
		Active:  true,
		Tags:    []string{"admin", "editor", "user"},
		Meta:    map[string]string{"team": "platform", "region": "us"},
		Balance: 1234.56,
	}
}

func BenchmarkCodecEncode(b *testing.B) {
	payload := benchmarkCodecPayload()

	b.Run("StdJSONCodec", func(b *testing.B) {
		codec := StdJSONCodec{}
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if err := codec.Encode(io.Discard, payload); err != nil {
				b.Fatalf("encode failed: %v", err)
			}
		}
	})

	b.Run("OptimizedJSONCodec", func(b *testing.B) {
		codec := NewOptimizedJSONCodec()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if err := codec.Encode(io.Discard, payload); err != nil {
				b.Fatalf("encode failed: %v", err)
			}
		}
	})

	b.Run("RawJSONEncoder", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if err := json.NewEncoder(io.Discard).Encode(payload); err != nil {
				b.Fatalf("encode failed: %v", err)
			}
		}
	})
}
