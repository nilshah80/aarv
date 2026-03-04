module github.com/nilshah80/aarv/examples/codec-jsonv2

go 1.26.0

require (
	github.com/nilshah80/aarv v0.0.0
	github.com/nilshah80/aarv/codec/jsonv2 v0.0.0
)

require github.com/go-json-experiment/json v0.0.0-20250223041408-d3c622f1b874 // indirect

replace github.com/nilshah80/aarv => ../..

replace github.com/nilshah80/aarv/codec/jsonv2 => ../../codec/jsonv2
