VERSION ?= dev
# Pure Go (no cgo): the SQLite driver and Pebble both have pure-Go builds.
export CGO_ENABLED := 0

LDFLAGS := -s -w -X github.com/4evrd/evrd/internal/version.Version=$(VERSION)

.PHONY: all build core cli web vet test clean up down up-prod down-prod e2e e2e-setup

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

# Dev stack with hot reload (air for the daemon, vite HMR for the web client).
# web → http://localhost:5173 (proxies the API on :8098) · irc → :6667 / :6697
up:
	docker compose -f docker-compose.dev.yml up --build

down:
	docker compose -f docker-compose.dev.yml down

# Built-image stack (the images CI publishes; nginx-served web on :8080).
up-prod:
	docker compose up --build

down-prod:
	docker compose down

# One-time: install web deps + the Playwright browser.
e2e-setup:
	cd web && pnpm install && pnpm exec playwright install chromium

# End-to-end tests (Playwright): brings up the dev stack, then drives the UI
# against the real daemon. Run `make e2e-setup` once first.
e2e:
	docker compose -f docker-compose.dev.yml up -d --build
	cd web && pnpm exec playwright test

clean:
	rm -rf bin web/dist
