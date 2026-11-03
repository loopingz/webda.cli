#!/bin/sh
# Builds the Secure Enclave bridge (enclave/libsebridge_<arch>.a), then, given an output path, the darwin binary
# with it (`-tags sebridge`, cgo), for macOS 12 and later.
# Usage: scripts/build-sebridge.sh <arm64|amd64> [output [go build args...]]
set -eu
root="$(cd "$(dirname "$0")/.." && pwd)"
arch="${1:-$(go env GOARCH)}"
case "$arch" in
arm64) target=arm64-apple-macos12 ;;
amd64) target=x86_64-apple-macos12 ;;
*) echo "unsupported arch $arch" >&2; exit 1 ;;
esac
swiftc -parse-as-library -emit-library -static -O -module-name sebridge -target "$target" \
    "$root/enclave/bridge.swift" -o "$root/enclave/libsebridge_${arch}.a"
[ "$#" -ge 2 ] || exit 0
out="$2"
shift 2
# the Swift back-deployment shims of macOS 12 come with the toolchain
compat="$(dirname "$(dirname "$(xcrun --find swiftc)")")/lib/swift/macosx"
cd "$root"
CGO_ENABLED=1 GOOS=darwin GOARCH="$arch" \
    CGO_CFLAGS="-mmacosx-version-min=12.0" CGO_LDFLAGS="-mmacosx-version-min=12.0 -L$compat" \
    go build -tags sebridge "$@" -o "$out" .
# the bridge must be linked in, or --require-presence would fall back to the CLI check
otool -L "$out" | grep -q CryptoKit
