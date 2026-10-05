#!/bin/sh
# Compile on the caller's machine; send only the binary to the Docker builder.
set -eu
cd "$(dirname "$0")/.."

# Docker may be remote, or run a different architecture from the local shell.
platform=${DOCKER_DEFAULT_PLATFORM:-}
if [ -z "$platform" ]; then
    platform=$(docker info --format '{{.OSType}}/{{.Architecture}}')
fi
case "$platform" in
    linux/amd64|linux/x86_64) arch=amd64 ;;
    linux/arm64|linux/aarch64|linux/arm64/v8) arch=arm64 ;;
    *) echo 'Local Docker builds require linux/amd64 or linux/arm64; set DOCKER_DEFAULT_PLATFORM to select one.' >&2; exit 1 ;;
esac
platform=linux/$arch

context=$(mktemp -d "${TMPDIR:-/tmp}/yip-docker.XXXXXX")
trap 'rm -rf "$context"' EXIT
trap 'exit 1' HUP INT TERM

echo "Building the hub locally for $platform"
(cd web && npm ci && npm run build)
# Leave proxy, module authentication and GOENV settings with the local Go tool.
# CGO is disabled so a Mac build produces a self-contained Linux executable.
CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -trimpath \
    -ldflags "-s -w -X github.com/binbandit/yip/internal/buildinfo.Version=${1:-0.1.0-dev}" \
    -o "$context/yip" ./cmd/yip
cp packaging/container/Dockerfile.hub-local "$context/Dockerfile"
docker build --platform "$platform" --tag yip-hub:local "$context"
echo 'Image ready. Run it with: just docker-run (or just docker-run -d)'
