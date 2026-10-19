# syntax=docker/dockerfile:1

# Builds the evrd daemon, the evrctl CLI and evrd-links (the connection
# holder, built without the version stamp so it only changes when its code
# does).
# The build stage runs on the builder's own platform and cross-compiles (pure
# Go, no cgo), so a multi-arch build never emulates the compiler.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
WORKDIR /src

# Dependencies first for layer caching.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
ARG VERSION=dev
ARG TARGETOS TARGETARCH
ENV CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go build -ldflags "-s -w -X github.com/4evrd/evrd/internal/version.Version=${VERSION}" -o /out/evrd ./cmd/evrd && \
    go build -ldflags "-s -w -X github.com/4evrd/evrd/internal/version.Version=${VERSION}" -o /out/evrctl ./cmd/evrctl && \
    go build -trimpath -buildvcs=false -ldflags "-s -w" -o /out/evrd-links ./cmd/evrd-links

FROM alpine:3.20
RUN adduser -D -u 1000 evrd && \
    mkdir -p /var/lib/evrd /run/evrd-links && chown evrd /var/lib/evrd /run/evrd-links
COPY --from=build /out/evrd /usr/local/bin/evrd
COPY --from=build /out/evrctl /usr/local/bin/evrctl
COPY --from=build /out/evrd-links /usr/local/bin/evrd-links
USER evrd
# Relative paths in the defaults (sqlite:evrd.db, pebble:history) land in the
# data volume.
WORKDIR /var/lib/evrd
# IRC (6667 plain / 6697 TLS) and the HTTP/WebSocket API (8098).
EXPOSE 6667 6697 8098
ENTRYPOINT ["evrd"]
