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
lock_file="$repository_root/experiment/dependencies.lock.json"
overlay_file="$repository_root/experiment/ethereum-package.patch"
network_params="$repository_root/experiment/network_params.yaml"
output_root=${ETHQUAKE_DEPENDENCY_ROOT:-"$repository_root/.cache/experiment/dependencies"}
package_path="$output_root/ethereum-package"

for command_name in git jq patch rg shasum; do
    require_command "$command_name"
done

if [ ! -r "$lock_file" ] || [ ! -r "$overlay_file" ]; then
    die "Dependency lock or overlay is missing"
fi

case "$output_root" in
    "$repository_root"/.cache/experiment/*)
        ;;
    *)
        die "Dependency output must remain under the repository .cache/experiment directory"
        ;;
esac

repository=$(jq -er '.ethereum_package.repository' "$lock_file")
revision=$(jq -er '.ethereum_package.revision' "$lock_file")
expected_overlay_sha=$(jq -er '.ethereum_package.overlay_sha256' "$lock_file")
actual_overlay_sha=$(shasum -a 256 "$overlay_file" | awk '{ print $1 }')
if [ "$actual_overlay_sha" != "$expected_overlay_sha" ]; then
    die "ethereum-package overlay checksum mismatch"
fi

if [ -e "$output_root" ]; then
    rm -rf -- "$output_root"
fi
mkdir -p -- "$package_path"

git -C "$package_path" init --quiet
git -C "$package_path" remote add origin "$repository"
git -C "$package_path" fetch --quiet --depth=1 origin "$revision"
git -C "$package_path" checkout --quiet --detach FETCH_HEAD
actual_revision=$(git -C "$package_path" rev-parse HEAD)
if [ "$actual_revision" != "$revision" ]; then
    die "ethereum-package revision mismatch: $actual_revision"
fi

git -C "$package_path" apply --check "$overlay_file"
git -C "$package_path" apply "$overlay_file"
if ! git -C "$package_path" diff --check; then
    die "ethereum-package overlay introduced invalid whitespace"
fi

prometheus_revision=$(jq -er '.imported_packages["github.com/kurtosis-tech/prometheus-package"]' "$lock_file")
if ! rg -F "github.com/kurtosis-tech/prometheus-package@$prometheus_revision" "$package_path/kurtosis.yml" >/dev/null; then
    die "Prometheus package replacement is not pinned to the lock"
fi
if rg -F 'protolambda/eth2-val-tools:latest' "$package_path/src/prelaunch_data_generator/validator_keystores/validator_keystore_generator.star" >/dev/null; then
    die "Mutable validator helper image remains in the prepared dependency path"
fi
if rg -F 'DEFAULT_YQ_IMAGE = "linuxserver/yq"' "$package_path/src/package_io/constants.star" >/dev/null; then
    die "Mutable yq helper image remains in the prepared dependency path"
fi

jq -er '.runtime_images[]' "$lock_file" |
    while IFS= read -r image; do
        if ! rg -F "$image" "$package_path" "$network_params" >/dev/null; then
            die "Locked runtime image is not referenced by the prepared experiment: $image"
        fi
    done

cp -- "$lock_file" "$output_root/dependencies.lock.json"
chmod -R u=rwX,go= "$output_root"
pass "ethereum-package revision: $revision"
pass "Dependency overlay SHA-256: $actual_overlay_sha"
pass "Prepared immutable package: $package_path"
