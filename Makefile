VERSION ?= dev
LDFLAGS := -s -w -X code.ircforever.org/ircforever/evrd/internal/version.Version=$(VERSION)

.PHONY: all build core cli web vet test clean up down

all: build web

build: core cli

core:
	go build -ldflags "$(LDFLAGS)" -o bin/evrd ./cmd/evrd

cli:
	go build -ldflags "$(LDFLAGS)" -o bin/evrctl ./cmd/evrctl

vet:
	go vet ./...

test:
	go test ./...

web:
	cd web && pnpm install && pnpm build

# Whole stack via Docker Compose: upstream ircd + evrd + web client.
up:
	docker compose up --build

down:
	docker compose down

clean:
	rm -rf bin web/dist
