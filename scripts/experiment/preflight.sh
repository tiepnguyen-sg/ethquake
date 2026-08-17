#!/bin/sh

set -eu

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
    shasum -a 256 "$1" | awk '{ print $1 }'
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
mode=${1:-local}

if [ ! -r "$lock_env" ]; then
    die "Phase 3 lock is missing: $lock_env"
fi
# This tracked file contains reviewed assignments only.
. "$lock_env"

for command_name in curl docker gcloud git jq kubectl lsof shasum; do
    require_command "$command_name"
done
if [ ! -x "$ethquake_bin" ]; then
    die "Ethquake binary is missing: $ethquake_bin"
fi
pass "Ethquake binary: $ethquake_bin"
if [ ! -x "$helm_bin" ]; then
    die "repository-local Helm binary is missing: run make phase3-prepare"
fi
pass "Helm binary: $helm_bin"

helm_version=$("$helm_bin" version --template '{{.Version}}')
if [ "$helm_version" != "v$PHASE3_HELM_VERSION" ]; then
    die "Helm version $helm_version does not match v$PHASE3_HELM_VERSION"
fi
pass "Helm version: $helm_version"

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
jq -e '.schema_version == "ethquake.dependencies/v1alpha1"' "$dependency_lock" >/dev/null
pass "Dependency lock schema"

if docker container inspect ethquake-kurtosis-access >/dev/null 2>&1; then
    die "Stop the local Ethquake Kurtosis gateway before a Phase 3 session"
fi
for local_port in 9710 15052 15053 15054 15055 18546 19090; do
    if lsof -nP -iTCP:"$local_port" -sTCP:LISTEN >/dev/null 2>&1; then
        die "Required loopback port is already in use: $local_port"
    fi
done
pass "Phase 3 loopback ports are free"

case "$mode" in
    local)
        pass "Phase 3 static preflight"
        exit 0
        ;;
    cloud)
        ;;
    *)
        die "Usage: preflight.sh [local|cloud]"
        ;;
esac

if [ -z "${GCP_PROJECT:-}" ] || [ -z "${GCP_ZONE:-}" ] || \
    [ -z "${GCP_BILLING_ACCOUNT:-}" ] || [ -z "${GCP_BUDGET_ID:-}" ] || \
    [ -z "${GKE_VERSION:-}" ] || [ -z "${ETHQUAKE_SESSION_ID:-}" ]; then
    die "GCP_PROJECT, GCP_ZONE, GCP_BILLING_ACCOUNT, GCP_BUDGET_ID, GKE_VERSION, and ETHQUAKE_SESSION_ID are required"
fi

case "$GCP_PROJECT" in
    ''|*[!a-z0-9-]*|-*|*-)
        die "GCP_PROJECT is invalid"
        ;;
esac
case "$GCP_ZONE" in
    ''|*[!a-z0-9-]*|-*|*-)
        die "GCP_ZONE is invalid"
        ;;
esac
case "$GCP_BILLING_ACCOUNT" in
    [0-9A-F][0-9A-F][0-9A-F][0-9A-F][0-9A-F][0-9A-F]-[0-9A-F][0-9A-F][0-9A-F][0-9A-F][0-9A-F][0-9A-F]-[0-9A-F][0-9A-F][0-9A-F][0-9A-F][0-9A-F][0-9A-F])
        ;;
    *)
        die "GCP_BILLING_ACCOUNT is invalid"
        ;;
esac
case "$GCP_BUDGET_ID" in
    ''|*[!A-Za-z0-9-]*|-*|*-)
        die "GCP_BUDGET_ID is invalid"
        ;;
esac
if ! printf '%s\n' "$GKE_VERSION" | jq -R -e \
    'test("^[0-9]+\\.[0-9]+\\.[0-9]+-gke\\.[0-9]+$")' >/dev/null; then
    die "GKE_VERSION must be an exact GKE patch version"
fi
case "$ETHQUAKE_SESSION_ID" in
    ''|*[!a-z0-9-]*|-*|*-)
        die "ETHQUAKE_SESSION_ID must be lowercase and DNS-safe"
        ;;
esac
if [ "${#ETHQUAKE_SESSION_ID}" -gt 24 ]; then
    die "ETHQUAKE_SESSION_ID must not exceed 24 characters"
fi

active_account=$(gcloud auth list --filter=status:ACTIVE --format='value(account)' | awk 'NF { print; exit }')
if [ -z "$active_account" ]; then
    die "gcloud has no active authenticated account"
fi
pass "gcloud account: $active_account"

configured_project=$(gcloud config get-value project 2>/dev/null || true)
if [ -n "$configured_project" ] && [ "$configured_project" != "(unset)" ] && [ "$configured_project" != "$GCP_PROJECT" ]; then
    pass "gcloud default project differs; every command will use explicit --project"
fi

server_config=$(gcloud container get-server-config \
    --project "$GCP_PROJECT" --zone "$GCP_ZONE" --format=json)
if ! printf '%s\n' "$server_config" | jq -e --arg version "$GKE_VERSION" \
    '(.validMasterVersions | index($version)) != null and (.validNodeVersions | index($version)) != null' >/dev/null; then
    die "GKE_VERSION is not currently available for both control plane and nodes in $GCP_ZONE"
fi
pass "GKE version available: $GKE_VERSION"

budget=$(gcloud billing budgets describe "$GCP_BUDGET_ID" \
    --billing-account "$GCP_BILLING_ACCOUNT" --format=json)
if ! printf '%s\n' "$budget" | jq -e \
    --argjson amount "$PHASE3_GCP_BUDGET_USD" \
    '.amount.specifiedAmount.currencyCode == "USD" and
     (.amount.specifiedAmount.units | tonumber) == $amount and
     any(.thresholdRules[]?; .thresholdPercent == 1)' >/dev/null; then
    die "The required USD $PHASE3_GCP_BUDGET_USD budget with a 100% alert threshold is not active"
fi
pass "GCP budget alert: USD $PHASE3_GCP_BUDGET_USD"

cluster_name="ethquake-p3-$ETHQUAKE_SESSION_ID"
if gcloud container clusters describe "$cluster_name" \
    --project "$GCP_PROJECT" --zone "$GCP_ZONE" >/dev/null 2>&1; then
    die "Phase 3 cluster already exists: $cluster_name"
fi
pass "Cluster target is absent: $cluster_name"
pass "Phase 3 cloud preflight"
