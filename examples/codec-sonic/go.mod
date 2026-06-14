module github.com/nilshah80/aarv/examples/codec-sonic

go 1.26.0

require (
	github.com/nilshah80/aarv v0.9.5
	github.com/nilshah80/aarv/codec/sonic v0.9.5
)

require (
	github.com/bytedance/gopkg v0.1.4 // indirect
	github.com/bytedance/sonic v1.15.0 // indirect
	github.com/bytedance/sonic/loader v0.5.1 // indirect
	github.com/cloudwego/base64x v0.1.6 // indirect
	github.com/klauspost/cpuid/v2 v2.3.0 // indirect
	github.com/twitchyliquid64/golang-asm v0.15.1 // indirect
	golang.org/x/arch v0.26.0 // indirect
	golang.org/x/sys v0.43.0 // indirect
)

replace github.com/nilshah80/aarv => ../..

replace github.com/nilshah80/aarv/codec/sonic => ../../codec/sonic
