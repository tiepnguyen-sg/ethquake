#!/bin/sh

set -eu

umask 077

die() {
    printf '[FAIL] %s\n' "$1" >&2
    exit 1
}

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
test_root=$(mktemp -d "${TMPDIR:-/tmp}/ethquake-phase3-test.XXXXXX")
trap 'rm -rf "$test_root"' EXIT HUP INT TERM
fake_bin="$test_root/bin"
cloud_marker="$test_root/cloud-api-called"
first_output="$test_root/first.out"
second_output="$test_root/second.out"
mkdir -p "$fake_bin"

for cloud_command in aws; do
    command_path="$fake_bin/$cloud_command"
    {
        printf '#!/bin/sh\n'
        printf '%s\n' ': >"${ETHQUAKE_CLOUD_MARKER:?}"'
        printf 'exit 97\n'
    } >"$command_path"
    chmod 700 "$command_path"
done

PATH="$fake_bin:$PATH" ETHQUAKE_CLOUD_MARKER="$cloud_marker" \
    ETHQUAKE_SESSION_ID=local-check \
    "$script_dir/phase3.sh" dry-run >"$first_output"
PATH="$fake_bin:$PATH" ETHQUAKE_CLOUD_MARKER="$cloud_marker" \
    ETHQUAKE_SESSION_ID=local-check \
    "$script_dir/phase3.sh" dry-run >"$second_output"

if [ -e "$cloud_marker" ]; then
    die "dry-run called a cloud CLI"
fi
if ! cmp -s "$first_output" "$second_output"; then
    die "dry-run output is not deterministic"
fi
for expected in \
    '[PLAN] provider=aws' \
    '[PLAN] participants=4' \
    '[PLAN] required_vcpus=16' \
    '[PLAN] cloud_api_calls=false' \
    '[PLAN] resource_creation=false' \
    '[PLAN] compute_substrate=unselected'; do
    if ! grep -F "$expected" "$first_output" >/dev/null; then
        die "dry-run output is missing: $expected"
    fi
done

if PATH="$fake_bin:$PATH" ETHQUAKE_CLOUD_MARKER="$cloud_marker" \
    ETHQUAKE_SESSION_ID='INVALID' \
    "$script_dir/phase3.sh" dry-run >"$test_root/invalid.out" 2>&1; then
    die "dry-run accepted an invalid session ID"
fi
if PATH="$fake_bin:$PATH" ETHQUAKE_CLOUD_MARKER="$cloud_marker" \
    "$script_dir/phase3.sh" run \
    >"$test_root/run.out" 2>&1; then
    die "real AWS run was not blocked"
fi
if ! grep -F 'AWS account preflight' "$test_root/run.out" >/dev/null; then
    die "real-run refusal did not identify the missing AWS preflight"
fi
if [ -e "$cloud_marker" ]; then
    die "blocked run called a cloud CLI"
fi

printf '[PASS] Phase 3 AWS dry-run is deterministic and cloud-free\n'
printf '[PASS] Phase 3 AWS real-run is fail-safe\n'
