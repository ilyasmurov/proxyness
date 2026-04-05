module smurov-proxy/daemon

go 1.24.0

require (
	gvisor.dev/gvisor v0.0.0-20250503011706-39ed1f5ac29c
	smurov-proxy/pkg v0.0.0
)

require (
	github.com/google/btree v1.1.2 // indirect
	golang.org/x/sys v0.42.0 // indirect
	golang.org/x/time v0.7.0 // indirect
)

replace smurov-proxy/pkg => ../pkg
