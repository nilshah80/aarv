module github.com/nilshah80/aarv/examples/microservice

go 1.25.0

require (
	github.com/nilshah80/aarv v0.8.0
	github.com/nilshah80/aarv/plugins/prometheus v0.0.0
)

require (
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/klauspost/compress v1.17.9 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/prometheus/client_golang v1.20.5 // indirect
	github.com/prometheus/client_model v0.6.2 // indirect
	github.com/prometheus/common v0.55.0 // indirect
	github.com/prometheus/procfs v0.16.1 // indirect
	golang.org/x/sys v0.42.0 // indirect
	google.golang.org/protobuf v1.36.8 // indirect
)

replace github.com/nilshah80/aarv => ../..

replace github.com/nilshah80/aarv/plugins/prometheus => ../../plugins/prometheus
