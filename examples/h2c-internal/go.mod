module github.com/nilshah80/aarv/examples/h2c-internal

go 1.23.0

require (
	github.com/nilshah80/aarv v0.7.0
	github.com/nilshah80/aarv/plugins/h2c v0.0.0
	golang.org/x/net v0.34.0
)

require golang.org/x/text v0.21.0 // indirect

replace github.com/nilshah80/aarv => ../..

replace github.com/nilshah80/aarv/plugins/h2c => ../../plugins/h2c
