module github.com/nilshah80/aarv/examples/h2c-internal

go 1.25.0

require (
	github.com/nilshah80/aarv v0.9.5
	github.com/nilshah80/aarv/plugins/h2c v0.9.5
	golang.org/x/net v0.55.0
)

require golang.org/x/text v0.37.0 // indirect

replace github.com/nilshah80/aarv => ../..

replace github.com/nilshah80/aarv/plugins/h2c => ../../plugins/h2c
