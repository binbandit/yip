#!/bin/sh
# An existing service may use this checkout's bin/yip. Build elsewhere so the
# installer can validate and back up that executable before replacing it.
set -eu
cd "$(dirname "$0")/.."
ldflags=$1
shift
install_dir=$(mktemp -d "${TMPDIR:-/tmp}/yip-install.XXXXXX")
trap 'rm -rf "$install_dir"' EXIT
trap 'exit 1' HUP INT TERM
go build -trimpath -ldflags "$ldflags" -o "$install_dir/yip" ./cmd/yip
"$install_dir/yip" install "$@"
