FROM golang:1.26-alpine AS build
RUN apk add --no-cache gcc musl-dev
WORKDIR /app
COPY go.work go.work.sum ./
COPY pkg/ pkg/
COPY config/ config/
RUN cd config && go build -ldflags="-s -w" -o /smurov-config ./cmd

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /smurov-config /smurov-config
EXPOSE 8443
ENTRYPOINT ["/smurov-config"]
