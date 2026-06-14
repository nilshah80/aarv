module github.com/nilshah80/aarv/examples/autocert-letsencrypt

go 1.25.0

require (
	github.com/nilshah80/aarv v0.9.5
	github.com/nilshah80/aarv/plugins/autocert v0.9.5
	golang.org/x/crypto v0.51.0
)

require (
	golang.org/x/net v0.55.0 // indirect
	golang.org/x/text v0.37.0 // indirect
)

replace github.com/nilshah80/aarv => ../..

replace github.com/nilshah80/aarv/plugins/autocert => ../../plugins/autocert
