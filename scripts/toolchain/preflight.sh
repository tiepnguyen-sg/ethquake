#!/bin/sh

set -eu

pass() {
    printf '[PASS] %s\n' "$1"
}

die() {
    printf '[FAIL] %s\n' "$1" >&2
    exit 1
}

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repository_root=$(CDPATH= cd -- "$script_dir/../.." && pwd)
versions_file="$repository_root/toolchain/versions.env"

if [ ! -r "$versions_file" ]; then
    die "Cannot read toolchain version locks: $versions_file"
fi

# This tracked file contains reviewed assignments only.
. "$versions_file"

if [ -z "${GO_VERSION:-}" ] || [ -z "${GO_DARWIN_ARM64_ARCHIVE_SHA256:-}" ]; then
    die "Go toolchain version locks are incomplete"
fi

go_binary=${GO:-go}
if ! command -v "$go_binary" >/dev/null 2>&1; then
    die "Go binary not found: $go_binary"
fi

actual_version=$(GOTOOLCHAIN=local "$go_binary" env GOVERSION 2>/dev/null || true)
expected_version="go$GO_VERSION"
if [ "$actual_version" != "$expected_version" ]; then
    die "Go version is $actual_version; expected $expected_version"
fi

pass "Go version: $actual_version"
