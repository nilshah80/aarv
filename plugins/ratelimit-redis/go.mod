module github.com/nilshah80/aarv/plugins/ratelimit-redis

go 1.23.0

require (
	github.com/alicebob/miniredis/v2 v2.37.0
	github.com/nilshah80/aarv v0.7.5
	github.com/redis/go-redis/v9 v9.7.3
)

require (
	github.com/cespare/xxhash/v2 v2.2.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/yuin/gopher-lua v1.1.1 // indirect
)

// Local development: resolve aarv from the working tree rather than the
// proxy. Lifted at release time (Phase 12.6 ships under v0.7.6) so the
// tagged module bytes can be fetched via the Go proxy with a published
// aarv version.
replace github.com/nilshah80/aarv => ../..
