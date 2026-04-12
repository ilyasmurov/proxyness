FROM golang:1.26-alpine AS build
RUN apk add --no-cache gcc musl-dev
WORKDIR /app
COPY config/ config/
ENV GOWORK=off
RUN cd config && go build -ldflags="-s -w" -o /proxyness-config ./cmd

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /proxyness-config /proxyness-config
EXPOSE 8443
ENTRYPOINT ["/proxyness-config"]
