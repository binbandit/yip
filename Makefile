VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.1.0-dev)
LDFLAGS := -s -w -X github.com/binbandit/yip/internal/buildinfo.Version=$(VERSION)

.PHONY: all build web web-deps schema test test-web e2e e2e-webkit lint demo lan clean release

all: web build

## build: compile the yip binary (embeds web/dist if built)
build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/yip ./cmd/yip

web-deps:
	cd web && npm ci

## web: build the Svelte client into web/dist
web: web-deps
	cd web && npm run build

## schema: regenerate JSON schemas and TypeScript types from protocol/*.go
schema:
	go run ./cmd/yip schema

test:
	go test ./...

test-web:
	cd web && npm run check && npm run test

## e2e: browser journeys against a demo hub (requires built binary)
e2e: all
	cd web && npm run e2e

## e2e-webkit: the same journeys, plus layout and zoom sweeps, in the system WebKit (macOS; downloads nothing)
e2e-webkit: all
	scripts/e2e/run-webkit.sh

lint:
	gofmt -l . | grep -v -e '^web/' -e '^\.claude/' | (! grep .)
	go vet ./...

demo: all
	./bin/yip demo

## lan: demo on your local network (built app :7721, live-reload client :5173)
lan: all
	./scripts/lan-dev.sh

release:
	GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/yip-darwin-arm64 ./cmd/yip
	GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/yip-linux-amd64 ./cmd/yip
	GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/yip-linux-arm64 ./cmd/yip
	cd dist && shasum -a 256 yip-* > SHA256SUMS

clean:
	rm -rf bin dist web/dist/assets web/dist/index.html
