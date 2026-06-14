module github.com/nilshah80/aarv/examples/codec-segmentio

go 1.26.0

require (
	github.com/nilshah80/aarv v0.9.5
	github.com/nilshah80/aarv/codec/segmentio v0.9.5
)

require (
	github.com/segmentio/asm v1.2.1 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	golang.org/x/sys v0.43.0 // indirect
)

replace github.com/nilshah80/aarv => ../..

replace github.com/nilshah80/aarv/codec/segmentio => ../../codec/segmentio
