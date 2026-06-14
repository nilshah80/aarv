module github.com/nilshah80/aarv/examples/openapi-spec

go 1.23.0

require (
	github.com/nilshah80/aarv v0.9.5
	github.com/nilshah80/aarv/plugins/openapi v0.9.5
	github.com/nilshah80/aarv/plugins/openapi-ui v0.9.5
)

require (
	github.com/kr/text v0.2.0 // indirect
	go.yaml.in/yaml/v2 v2.4.2 // indirect
	sigs.k8s.io/yaml v1.6.0 // indirect
)

replace github.com/nilshah80/aarv => ../..

replace github.com/nilshah80/aarv/plugins/openapi => ../../plugins/openapi

replace github.com/nilshah80/aarv/plugins/openapi-ui => ../../plugins/openapi-ui
