module github.com/nilshah80/aarv/examples/codec-segmentio

go 1.26.0

require (
	github.com/nilshah80/aarv v0.0.0
	github.com/nilshah80/aarv/codec/segmentio v0.0.0
)

require (
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.4.1 // indirect
	golang.org/x/sys v0.0.0-20211110154304-99a53858aa08 // indirect
)

replace github.com/nilshah80/aarv => ../..

replace github.com/nilshah80/aarv/codec/segmentio => ../../codec/segmentio
