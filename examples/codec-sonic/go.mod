module github.com/nilshah80/aarv/examples/codec-sonic

go 1.26.0

require (
	github.com/nilshah80/aarv v0.0.0
	github.com/nilshah80/aarv/codec/sonic v0.0.0
)

require (
	github.com/bytedance/gopkg v0.1.3 // indirect
	github.com/bytedance/sonic v1.15.0 // indirect
	github.com/bytedance/sonic/loader v0.5.0 // indirect
	github.com/cloudwego/base64x v0.1.6 // indirect
	github.com/klauspost/cpuid/v2 v2.3.0 // indirect
	github.com/twitchyliquid64/golang-asm v0.15.1 // indirect
	golang.org/x/arch v0.24.0 // indirect
	golang.org/x/sys v0.41.0 // indirect
)

replace github.com/nilshah80/aarv => ../..

replace github.com/nilshah80/aarv/codec/sonic => ../../codec/sonic
