#!/bin/bash
# Cross-compiles dictated for macOS and Linux (amd64 + arm64) into dist/.
#
# The binary has no cgo dependencies (see platform.go), so a plain Go
# toolchain can cross-compile every target from any host -- no Docker,
# no Zig, no per-platform build machine needed.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")"

VERSION="${1:-dev}"
DIST=dist
BINARY=dictated

rm -rf "$DIST"
mkdir -p "$DIST"

targets=(
  "darwin amd64"
  "darwin arm64"
  "linux amd64"
  "linux arm64"
)

for target in "${targets[@]}"; do
  read -r goos goarch <<<"$target"
  out="$DIST/${BINARY}-${goos}-${goarch}"
  echo "building $out"
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build \
    -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o "$out" .
done

# Convenience symlink for the host platform, e.g. `./dist/dictated`.
host_goos="$(go env GOOS)"
host_goarch="$(go env GOARCH)"
ln -sf "${BINARY}-${host_goos}-${host_goarch}" "$DIST/$BINARY"

echo
echo "done. artifacts:"
ls -la "$DIST"
