module proxyness/daemon

go 1.25.0

require (
	golang.org/x/sys v0.42.0
	gvisor.dev/gvisor v0.0.0-20250503011706-39ed1f5ac29c
	proxyness/pkg v0.0.0
)

require (
	github.com/google/btree v1.1.2 // indirect
	golang.org/x/crypto v0.49.0 // indirect
	golang.org/x/time v0.7.0 // indirect
)

replace proxyness/pkg => ../pkg
