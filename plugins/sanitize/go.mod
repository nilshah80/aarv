module github.com/nilshah80/aarv/plugins/sanitize

go 1.22.0

require (
	github.com/nilshah80/aarv v0.6.0
	golang.org/x/text v0.18.0
)

// Local development: resolve aarv from the working tree rather than the
// proxy. Lifted at release time so the tagged module bytes can be fetched
// via the Go proxy with a published aarv version.
replace github.com/nilshah80/aarv => ../..
