module github.com/nilshah80/aarv/plugins/h2c

go 1.23.0

require (
	github.com/nilshah80/aarv v0.7.5
	golang.org/x/net v0.34.0
)

// Local development: resolve aarv from the working tree rather than the
// proxy. Lifted at release time so the tagged module bytes can be fetched
// via the Go proxy with a published aarv version.
replace github.com/nilshah80/aarv => ../..

require golang.org/x/text v0.21.0 // indirect
