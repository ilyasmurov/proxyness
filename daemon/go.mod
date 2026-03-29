module smurov-proxy/daemon

go 1.23.1

require (
	golang.zx2c4.com/wireguard v0.0.0-20250521234502-f333402bd9cb
	gvisor.dev/gvisor v0.0.0-20250503011706-39ed1f5ac29c
	smurov-proxy/pkg v0.0.0
)

replace smurov-proxy/pkg => ../pkg
