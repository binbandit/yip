# Run `just --list` to see every recipe. Override the embedded version with
# `just version=1.2.3 build` or the VERSION environment variable.

version := env("VERSION", `git describe --tags --always --dirty 2>/dev/null || echo 0.1.0-dev`)
ldflags := "-s -w -X github.com/binbandit/yip/internal/buildinfo.Version=" + version

# build the web client and the yip binary
all: web build

# install this checkout's CLI and refresh existing macOS user services
[positional-arguments]
install *args: web
    sh scripts/install.sh "{{ ldflags }}" "$@"

# verify install builds safely before any installed binary is replaced
test-install:
    node --test scripts/install.test.mjs

# compile the yip binary (embeds web/dist if built)
build:
    go build -trimpath -ldflags "{{ ldflags }}" -o bin/yip ./cmd/yip

# build and run the real hub (optional arguments go to `yip hub`)
[positional-arguments]
start *args: all
    exec ./bin/yip hub "$@"

# run a separate development hub with Go auto-restart and frontend hot updates
dev: web-deps _dev-tools
    node scripts/dev.mjs

# keep the pinned Go watcher local to this checkout
_dev-tools:
    GOBIN="{{ justfile_directory() }}/bin" go install github.com/air-verse/air@v1.67.4

# install the web client's locked dependencies
web-deps:
    cd web && npm ci

# build the Svelte client into web/dist
web: web-deps
    cd web && npm run build

# regenerate JSON schemas and TypeScript types from protocol/*.go
schema:
    go run ./cmd/yip schema

# run the Go unit and integration tests
test:
    go test ./...

# -race also turns on checkptr, which the machine-translated SQLite spends most
# of its time in, so it's off there and stays on for everything else.
# run the Go unit and integration tests under the race detector
test-race:
    go test -race -gcflags='modernc.org/...=-d=checkptr=0' -timeout 15m ./internal/... ./test/integration/

# test dev-server shutdown and reloads against the pinned Go watcher
test-dev: _dev-tools
    node --test scripts/dev.test.mjs scripts/dev-air.test.mjs

# separate test executable, excluded from release builds
browser-harness: all
    go build -o bin/yip-browser-harness ./test/browserharness

# browser journeys (needs Chromium or Chrome)
e2e: browser-harness
    cd web && npm run e2e

# type-check and test the web client
test-web:
    cd web && npm run check && npm run test

# check Go formatting and run go vet
lint:
    gofmt -l . | grep -v -e '^web/' -e '^\.claude/' | (! grep .)
    go vet ./...

# run the hub and web client in Docker on localhost:7420 (arguments go to docker compose, e.g. `just docker down`)
[positional-arguments]
docker *args:
    if [ "$#" -eq 0 ]; then set -- up --build; fi; YIP_VERSION="{{ version }}" YIP_RUNNER_URL="${YIP_RUNNER_URL:-https://$(uname -n):7443}" docker compose -f packaging/container/compose.hub.yml "$@"

# build locally with your Go/npm settings, then package a Linux hub image
docker-build:
    sh scripts/docker-build.sh "{{ version }}"

# run the locally packaged image without rebuilding or pulling (e.g. -d)
[positional-arguments]
docker-run *args:
    YIP_HUB_IMAGE=yip-hub:local just docker up --no-build --pull never "$@"

# cross-compile release binaries and their checksums into dist/
release:
    GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "{{ ldflags }}" -o dist/yip-darwin-arm64 ./cmd/yip
    GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "{{ ldflags }}" -o dist/yip-linux-amd64 ./cmd/yip
    GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "{{ ldflags }}" -o dist/yip-linux-arm64 ./cmd/yip
    cd dist && shasum -a 256 yip-* > SHA256SUMS

# remove build output
clean:
    rm -rf bin dist web/dist/assets web/dist/index.html
