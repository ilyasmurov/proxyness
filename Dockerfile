FROM golang:1.22-alpine AS builder

WORKDIR /build
COPY go.work go.work
COPY pkg/ pkg/
COPY server/ server/

WORKDIR /build/server
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /server ./cmd

FROM alpine:3.20
RUN apk add --no-cache ca-certificates

COPY --from=builder /server /usr/local/bin/server

EXPOSE 443

ENTRYPOINT ["server"]
