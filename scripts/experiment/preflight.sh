#!/bin/sh

set -eu

umask 077

pass() {
    printf '[PASS] %s\n' "$1"
}

die() {
    printf 'REFUSING per §0.8: %s. To override, say "override §0.8" and I will comply and log it in docs/overrides.md.\n' "$1" >&2
    exit 1
}

require_command() {
    if ! command -v "$1" >/dev/null 2>&1; then
        die "Required command not found: $1"
    fi
    pass "Command: $1"
}

sha256_file() {
    if command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$1" | awk '{ print $1 }'
    elif command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{ print $1 }'
    else
        die "Neither shasum nor sha256sum is available"
    fi
}

verify_sha256() {
    path=$1
    expected=$2
    actual=$(sha256_file "$path")
    if [ "$actual" != "$expected" ]; then
        die "Checksum mismatch for $path: $actual"
    fi
    pass "Checksum: ${path#$repository_root/}"
}

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repository_root=$(CDPATH= cd -- "$script_dir/../.." && pwd)
lock_env="$repository_root/experiment/phase3.lock.env"
scenario_file="$repository_root/scenarios/cl-p2p-partition.yaml"
network_params="$repository_root/experiment/network_params.yaml"
chaos_values="$repository_root/experiment/chaos-mesh-values.yaml"
dependency_lock="$repository_root/experiment/dependencies.lock.json"
ethquake_bin=${ETHQUAKE_BIN:-"$repository_root/bin/ethquake"}
helm_bin=${ETHQUAKE_HELM_BIN:-"$repository_root/.cache/experiment/tools/helm"}
mode=${1:-static}

case "$mode" in
    static|local)
        ;;
    *)
        die "Usage: preflight.sh [static|local]"
        ;;
esac

if [ ! -r "$lock_env" ]; then
    die "Phase 3 lock is missing: $lock_env"
fi
# This tracked file contains reviewed assignments only.
. "$lock_env"

require_command jq
if ! command -v shasum >/dev/null 2>&1 && ! command -v sha256sum >/dev/null 2>&1; then
    die "Neither shasum nor sha256sum is available"
fi
if [ ! -x "$ethquake_bin" ]; then
    die "Ethquake binary is missing: $ethquake_bin"
fi
pass "Ethquake binary: $ethquake_bin"

verify_sha256 "$scenario_file" "$PHASE3_SCENARIO_SHA256"
verify_sha256 "$network_params" "$PHASE3_NETWORK_PARAMS_SHA256"
verify_sha256 "$chaos_values" "$PHASE3_CHAOS_VALUES_SHA256"
verify_sha256 "$dependency_lock" "$PHASE3_DEPENDENCY_LOCK_SHA256"
if ! "$ethquake_bin" scenario validate --file "$scenario_file"; then
    die "the committed Phase 3 scenario did not pass runtime validation"
fi
scenario_chain_id=$(awk '$1 == "chain_id:" { print $2; exit }' "$scenario_file")
network_chain_id=$(awk '$1 == "network_id:" { gsub(/"/, "", $2); print $2; exit }' "$network_params")
if [ "$scenario_chain_id" != "$PHASE3_CHAIN_ID" ] || \
    [ "$network_chain_id" != "$PHASE3_CHAIN_ID" ]; then
    die "scenario and ethereum-package network IDs must both match the locked Phase 3 chain ID"
fi
pass "Phase 3 chain ID: $PHASE3_CHAIN_ID"
network_participant_count=$(awk '
    $0 == "participants:" { in_participants = 1; next }
    $0 == "network_params:" { in_participants = 0 }
    in_participants && /^  - el_type:/ { count++ }
    END { print count + 0 }
' "$network_params")
if [ "$network_participant_count" != "$PHASE3_PARTICIPANT_COUNT" ]; then
    die "network parameters must define exactly $PHASE3_PARTICIPANT_COUNT participants"
fi
pass "Phase 3 participants: $PHASE3_PARTICIPANT_COUNT"
jq -e '.schema_version == "ethquake.dependencies/v1alpha1"' "$dependency_lock" >/dev/null
pass "Dependency lock schema"
pass "Phase 3 static preflight"

if [ "$mode" = static ]; then
    exit 0
fi

for command_name in curl docker git kubectl lsof; do
    require_command "$command_name"
done
if [ ! -x "$helm_bin" ]; then
    die "repository-local Helm binary is missing: run make phase3-prepare"
fi
pass "Helm binary: $helm_bin"

helm_version=$("$helm_bin" version --template '{{.Version}}')
if [ "$helm_version" != "v$PHASE3_HELM_VERSION" ]; then
    die "Helm version $helm_version does not match v$PHASE3_HELM_VERSION"
fi
pass "Helm version: $helm_version"

if docker container inspect ethquake-kurtosis-access >/dev/null 2>&1; then
    die "Stop the local Ethquake Kurtosis gateway before a Phase 3 session"
fi
for local_port in 9710 15052 15053 15054 15055 18546 19090; do
    if lsof -nP -iTCP:"$local_port" -sTCP:LISTEN >/dev/null 2>&1; then
        die "Required loopback port is already in use: $local_port"
    fi
done
pass "Phase 3 loopback ports are free"
pass "Phase 3 local preflight"
