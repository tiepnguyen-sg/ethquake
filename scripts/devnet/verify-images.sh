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
network_params="$repository_root/devnet/network_params.yaml"

if ! command -v docker >/dev/null 2>&1; then
    die "Required command not found: docker"
fi
if ! docker buildx version >/dev/null 2>&1; then
    die "Docker buildx is required for OCI manifest inspection"
fi
if [ ! -r "$network_params" ]; then
    die "Cannot read devnet configuration: $network_params"
fi

images=$(awk '
    /^[[:space:]]+(el_image|cl_image|vc_image|image):[[:space:]]+/ {
        sub(/^[^:]+:[[:space:]]+/, "")
        print
    }
' "$network_params" | sort -u)

if [ -z "$images" ]; then
    die "No devnet images were found"
fi

printf '%s\n' "$images" | while IFS= read -r image; do
    digest=${image##*@sha256:}
    if [ "$digest" = "$image" ]; then
        die "Devnet image is not digest-pinned: $image"
    fi
    case "$digest" in
        *[!0-9a-f]*|'')
            die "Devnet image has an invalid SHA-256 digest: $image"
            ;;
    esac
    if [ "${#digest}" -ne 64 ]; then
        die "Devnet image digest must contain 64 characters: $image"
    fi

    inspect_output=$(docker buildx imagetools inspect "$image" 2>&1) || {
        printf '%s\n' "$inspect_output" >&2
        die "OCI manifest inspection failed: $image"
    }
    if ! printf '%s\n' "$inspect_output" |
        awk '$1 == "Platform:" && $2 == "linux/arm64" { found = 1 }
             END { exit !found }'; then
        die "Devnet image has no linux/arm64 manifest: $image"
    fi
    pass "linux/arm64 image: $image"
done

pass "All configured devnet images are immutable and Apple-silicon compatible"
