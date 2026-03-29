.PHONY: build-server build-daemon build-client test clean

# Server (Linux for VPS)
build-server:
	cd server && GOOS=linux GOARCH=amd64 go build -o ../dist/server-linux ./cmd

# Go daemon (all platforms, output to client/resources for Electron bundling)
build-daemon:
	mkdir -p client/resources
	cd daemon && GOOS=darwin GOARCH=arm64 go build -o ../client/resources/daemon-darwin-arm64 ./cmd
	cd daemon && GOOS=darwin GOARCH=amd64 go build -o ../client/resources/daemon-darwin-amd64 ./cmd
	cd daemon && GOOS=windows GOARCH=amd64 go build -o ../client/resources/daemon-windows.exe ./cmd

# Electron GUI
build-client: build-daemon
	cd client && npm run build && npx electron-builder

# Run all Go tests
test:
	cd pkg && go test ./...
	cd daemon && go test ./...
	cd test && go test -v -timeout 30s

# Dev: run Electron in dev mode (daemon must be started separately)
dev:
	cd client && npm run dev

clean:
	rm -rf dist/ client/dist/ client/dist-electron/ client/release/ client/resources/daemon-*
