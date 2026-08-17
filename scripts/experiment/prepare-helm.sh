#!/bin/sh

set -eu

umask 077

pass() {
    printf '[PASS] %s\n' "$1"
}

die() {
    printf '[FAIL] %s\n' "$1" >&2
    exit 1
}

require_command() {
    if ! command -v "$1" >/dev/null 2>&1; then
        die "Required command not found: $1"
    fi
}

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repository_root=$(CDPATH= cd -- "$script_dir/../.." && pwd)
lock_env="$repository_root/experiment/phase3.lock.env"
tools_root="$repository_root/.cache/experiment/tools"
helm_path="$tools_root/helm"

require_command curl
require_command shasum
require_command tar

if [ ! -r "$lock_env" ]; then
    die "Phase 3 lock is missing: $lock_env"
fi
# This tracked file contains reviewed assignments only.
. "$lock_env"

if [ "$(uname -s)" != Darwin ] || [ "$(uname -m)" != arm64 ]; then
    die "The locked Phase 3 Helm artifact currently supports only Darwin arm64"
fi

archive_name="helm-v${PHASE3_HELM_VERSION}-darwin-arm64.tar.gz"
archive_path="$tools_root/$archive_name"
download_path="$tools_root/.${archive_name}.download"
staging_root="$tools_root/.helm-staging"
archive_url="https://get.helm.sh/$archive_name"

case "$tools_root" in
    "$repository_root"/.cache/experiment/tools)
        ;;
    *)
        die "Helm tool cache is outside the Ethquake-owned path"
        ;;
esac

mkdir -p -- "$tools_root"
if [ ! -f "$archive_path" ]; then
    rm -f -- "$download_path"
    if ! curl --fail --location --proto '=https' --tlsv1.2 \
        --output "$download_path" "$archive_url"; then
        rm -f -- "$download_path"
        die "Official Helm archive download failed"
    fi
    downloaded_sha=$(shasum -a 256 "$download_path" | awk '{ print $1 }')
    if [ "$downloaded_sha" != "$PHASE3_HELM_DARWIN_ARM64_SHA256" ]; then
        rm -f -- "$download_path"
        die "Downloaded Helm archive checksum mismatch"
    fi
    mv -- "$download_path" "$archive_path"
fi

archive_sha=$(shasum -a 256 "$archive_path" | awk '{ print $1 }')
if [ "$archive_sha" != "$PHASE3_HELM_DARWIN_ARM64_SHA256" ]; then
    die "Cached Helm archive checksum mismatch: $archive_path"
fi
if ! tar -tzf "$archive_path" | awk '$0 == "darwin-arm64/helm" { found = 1 } END { exit !found }'; then
    die "Helm archive does not contain darwin-arm64/helm"
fi

rm -rf -- "$staging_root"
mkdir -p -- "$staging_root"
tar -xzf "$archive_path" -C "$staging_root" darwin-arm64/helm
mv -- "$staging_root/darwin-arm64/helm" "$helm_path"
rm -rf -- "$staging_root"
chmod 0500 "$helm_path"

actual_version=$("$helm_path" version --template '{{.Version}}')
if [ "$actual_version" != "v$PHASE3_HELM_VERSION" ]; then
    die "Prepared Helm version $actual_version does not match v$PHASE3_HELM_VERSION"
fi
pass "Helm archive SHA-256: $archive_sha"
pass "Repository-local Helm version: $actual_version"
