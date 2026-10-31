module github.com/aaaSaZaN/gowave-node

go 1.27.1

// Disable MPTCP by default to mitigate CVE-2026-98432 in unpatched Linux kernels.
godebug multipathtcp=0

require (
	github.com/andybalholm/brotli v1.2.7
	github.com/go-chi/chi/v5 v5.5.0
	github.com/golang-jwt/jwt/v5 v5.3.1
	github.com/klauspost/compress v1.20.3
	github.com/shirou/gopsutil/v3 v3.24.5
	github.com/xtls/xray-core v1.260327.0
	golang.org/x/crypto v0.58.0
	google.golang.org/grpc v1.84.0
	google.golang.org/protobuf v1.36.12
)

require (
	github.com/apernet/quic-go v0.59.1-0.20260217092621-db4786c77a22 // indirect
	github.com/cloudflare/circl v1.6.3 // indirect
	github.com/go-ole/go-ole v1.2.6 // indirect
	github.com/juju/ratelimit v1.0.2 // indirect
	github.com/klauspost/cpuid/v2 v2.3.0 // indirect
	github.com/lufia/plan9stats v0.0.0-20211012122336-39d0f177ccd0 // indirect
	github.com/miekg/dns v1.1.72 // indirect
	github.com/pires/go-proxyproto v0.11.0 // indirect
	github.com/power-devops/perfstat v0.0.0-20210106213030-5aafc221ea8c // indirect
	github.com/refraction-networking/utls v1.8.3-0.20260301010127-aa6edf4b11af // indirect
	github.com/sagernet/sing v0.9.6 // indirect
	github.com/sagernet/sing-shadowsocks v0.2.7 // indirect
	github.com/shoenig/go-m1cpu v0.1.6 // indirect
	github.com/tklauser/go-sysconf v0.3.12 // indirect
	github.com/tklauser/numcpus v0.6.1 // indirect
	github.com/xtls/reality v0.0.0-20260322125925-9234c772ba8f // indirect
	github.com/yusufpapurcu/wmi v1.2.4 // indirect
	golang.org/x/mod v0.41.0 // indirect
	golang.org/x/net v0.60.0 // indirect
	golang.org/x/sync v0.24.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/text v0.43.0 // indirect
	golang.org/x/tools v0.51.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260706201446-f0a921348800 // indirect
	lukechampine.com/blake3 v1.4.1 // indirect
)
