# Build Go server
FROM golang:1.26-alpine AS builder
WORKDIR /build
COPY pkg/ pkg/
COPY server/ server/

# Use replace directive instead of workspace
RUN cd server && go mod edit -replace proxyness/pkg=../pkg && go mod tidy
WORKDIR /build/server
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /server ./cmd

# Runtime
FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /server /usr/local/bin/server
COPY changelog.json /changelog.json
EXPOSE 443/tcp 443/udp
ENTRYPOINT ["server"]
