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
cache_root="$repository_root/.cache/experiment"
checkout="$cache_root/chaos-mesh"
values_file="$repository_root/experiment/chaos-mesh-values.yaml"
repository=https://github.com/chaos-mesh/chaos-mesh.git

for command_name in git jq rg; do
    require_command "$command_name"
done
if [ ! -r "$lock_file" ]; then
    die "Dependency lock is missing: $lock_file"
fi
revision=$(jq -er '.fault_runtime.chaos_mesh_revision' "$lock_file")
case "$revision" in
    *[!0-9a-f]*|'')
        die "Chaos Mesh revision is not a lowercase commit SHA"
        ;;
esac
if [ "${#revision}" -ne 40 ]; then
    die "Chaos Mesh revision must contain 40 hexadecimal characters"
fi

case "$checkout" in
    "$repository_root"/.cache/experiment/chaos-mesh)
        ;;
    *)
        die "Refusing to replace unexpected checkout path: $checkout"
        ;;
esac
if [ -e "$checkout" ]; then
    rm -rf -- "$checkout"
fi
mkdir -p -- "$cache_root"

git clone --quiet --filter=blob:none --no-checkout "$repository" "$checkout"
git -C "$checkout" checkout --quiet --detach "$revision"
actual_revision=$(git -C "$checkout" rev-parse HEAD)
if [ "$actual_revision" != "$revision" ]; then
    die "Chaos Mesh checkout resolved to $actual_revision; expected $revision"
fi
if [ -n "$(git -C "$checkout" status --porcelain)" ]; then
    die "Prepared Chaos Mesh checkout is dirty"
fi
if [ ! -r "$checkout/helm/chaos-mesh/Chart.yaml" ]; then
    die "Prepared Chaos Mesh checkout has no Helm chart"
fi
controller_image=$(jq -er '.fault_runtime.controller_image' "$lock_file")
daemon_image=$(jq -er '.fault_runtime.daemon_image' "$lock_file")
for image in "$controller_image" "$daemon_image"; do
    repository_and_tag=${image#ghcr.io/}
    image_repository=${repository_and_tag%%:*}
    image_tag=${repository_and_tag#*:}
    if ! rg -F "repository: $image_repository" "$values_file" >/dev/null || \
        ! rg -F "tag: $image_tag" "$values_file" >/dev/null; then
        die "Chaos Mesh values do not reference locked image: $image"
    fi
done

pass "Chaos Mesh revision: $actual_revision"
pass "Chaos Mesh chart: $checkout/helm/chaos-mesh"
