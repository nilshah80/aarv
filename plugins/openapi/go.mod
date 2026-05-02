module github.com/nilshah80/aarv/plugins/openapi

go 1.23.0

require (
	github.com/nilshah80/aarv v0.7.0
	sigs.k8s.io/yaml v1.6.0
)

require go.yaml.in/yaml/v2 v2.4.2 // indirect

// Local development: resolve aarv from the working tree rather than the
// proxy. Lifted at release time so the tagged module bytes can be fetched
// via the Go proxy with a published aarv version.
replace github.com/nilshah80/aarv => ../..
