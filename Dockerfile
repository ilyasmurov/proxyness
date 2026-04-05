# Stage 1: Build admin UI
FROM node:22-alpine AS ui-builder
WORKDIR /ui
COPY server/admin-ui/package*.json ./
RUN npm ci
COPY server/admin-ui/ ./
RUN npm run build

# Stage 2: Build Go server
FROM golang:1.25-alpine AS builder
WORKDIR /build
COPY pkg/ pkg/
COPY server/ server/

# Copy built UI into embed location
RUN mkdir -p server/internal/admin/static
COPY --from=ui-builder /ui/dist/ server/internal/admin/static/

# Use replace directive instead of workspace
RUN cd server && go mod edit -replace smurov-proxy/pkg=../pkg && go mod tidy
WORKDIR /build/server
RUN CGO_ENABLED=0 GOEXPERIMENT=nospinbitmutex GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /server ./cmd

# Stage 3: Runtime
FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /server /usr/local/bin/server
COPY changelog.json /changelog.json
EXPOSE 443/tcp 443/udp
ENTRYPOINT ["server"]
