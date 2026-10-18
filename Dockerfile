# syntax=docker/dockerfile:1

# Builds the evrd daemon and the evrctl CLI.
FROM golang:1.26-alpine AS build
WORKDIR /src

# Dependencies first for layer caching.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG VERSION=dev
ENV CGO_ENABLED=0
RUN go build -ldflags "-s -w -X code.ircforever.org/ircforever/evrd/internal/version.Version=${VERSION}" -o /out/evrd ./cmd/evrd && \
    go build -ldflags "-s -w -X code.ircforever.org/ircforever/evrd/internal/version.Version=${VERSION}" -o /out/evrctl ./cmd/evrctl

FROM alpine:3.20
RUN adduser -D -u 1000 evrd && \
    mkdir -p /var/lib/evrd && chown evrd /var/lib/evrd
COPY --from=build /out/evrd /usr/local/bin/evrd
COPY --from=build /out/evrctl /usr/local/bin/evrctl
USER evrd
# IRC (6667 plain / 6697 TLS) and the HTTP/WebSocket API (8098).
EXPOSE 6667 6697 8098
ENTRYPOINT ["evrd"]
