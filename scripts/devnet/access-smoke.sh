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

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repository_root=$(CDPATH= cd -- "$script_dir/../.." && pwd)
access_script="$script_dir/access.sh"
versions_file="$repository_root/devnet/versions.env"
kubeconfig="$repository_root/.kurtosis/kubeconfig"
enclave=ethquake-access-smoke
namespace="kt-$enclave"
service_id=cl-1-lighthouse-geth
local_port=18080
gateway_port=9710
gateway_container=ethquake-kurtosis-access
gateway_network=ethquake-kurtosis-access
runtime_dir="$repository_root/.kurtosis/access/runtime"
gateway_started=false
enclave_created=false
forward_started=false
initial_global_context=$(kubectl config current-context 2>/dev/null || true)

if [ ! -r "$versions_file" ]; then
    die "Cannot read version locks: $versions_file"
fi

# This tracked file contains reviewed assignments only.
. "$versions_file"

if [ -z "${DEVNET_ACCESS_SMOKE_IMAGE:-}" ]; then
    die "Version lock is missing: DEVNET_ACCESS_SMOKE_IMAGE"
fi

cleanup() {
    original_status=$?
    cleanup_status=0
    trap - EXIT HUP INT TERM
    set +e

    if [ "$forward_started" = true ]; then
        "$access_script" forward-stop beacon-api "$service_id"
        if [ "$?" -ne 0 ]; then
            cleanup_status=1
        fi
    fi
    if [ "$enclave_created" = true ]; then
        "$access_script" kurtosis enclave rm --force "$enclave"
        if [ "$?" -ne 0 ]; then
            cleanup_status=1
        fi

        namespace_attempt=0
        while kubectl --kubeconfig="$kubeconfig" get namespace "$namespace" \
            >/dev/null 2>&1 && [ "$namespace_attempt" -lt 30 ]; do
            namespace_attempt=$((namespace_attempt + 1))
            sleep 1
        done
    fi
    if [ "$gateway_started" = true ]; then
        "$access_script" gateway-stop
        if [ "$?" -ne 0 ]; then
            cleanup_status=1
        fi
    fi

    if docker container inspect "$gateway_container" >/dev/null 2>&1; then
        printf '[FAIL] Cleanup: gateway container remains\n' >&2
        cleanup_status=1
    else
        pass "Cleanup: gateway container absent"
    fi
    if docker network inspect "$gateway_network" >/dev/null 2>&1; then
        printf '[FAIL] Cleanup: gateway network remains\n' >&2
        cleanup_status=1
    else
        pass "Cleanup: gateway network absent"
    fi
    if kubectl --kubeconfig="$kubeconfig" get namespace "$namespace" \
        >/dev/null 2>&1; then
        printf '[FAIL] Cleanup: disposable namespace remains\n' >&2
        cleanup_status=1
    else
        pass "Cleanup: disposable namespace absent"
    fi
    if lsof -nP -iTCP:"$gateway_port" -sTCP:LISTEN >/dev/null 2>&1 || \
        lsof -nP -iTCP:"$local_port" -sTCP:LISTEN >/dev/null 2>&1; then
        printf '[FAIL] Cleanup: access listener remains\n' >&2
        cleanup_status=1
    else
        pass "Cleanup: no access listeners remain"
    fi
    if [ -e "$runtime_dir" ]; then
        printf '[FAIL] Cleanup: temporary credentials or runtime files remain\n' >&2
        cleanup_status=1
    else
        pass "Cleanup: temporary credentials and runtime files absent"
    fi
    final_global_context=$(kubectl config current-context 2>/dev/null || true)
    if [ "$final_global_context" != "$initial_global_context" ]; then
        printf '[FAIL] Cleanup: global Kubernetes context changed\n' >&2
        cleanup_status=1
    else
        pass "Global Kubernetes context unchanged"
    fi

    if [ "$original_status" -ne 0 ]; then
        exit "$original_status"
    fi
    if [ "$cleanup_status" -ne 0 ]; then
        exit "$cleanup_status"
    fi
    pass "Local access smoke test and cleanup completed"
}

trap cleanup EXIT
trap 'exit 130' HUP INT TERM

for command_name in docker kubectl curl lsof; do
    if ! command -v "$command_name" >/dev/null 2>&1; then
        die "Required command not found: $command_name"
    fi
done

if docker container inspect "$gateway_container" >/dev/null 2>&1 || \
    docker network inspect "$gateway_network" >/dev/null 2>&1; then
    die "Access smoke test requires a stopped gateway and no owned gateway network"
fi
if lsof -nP -iTCP:"$gateway_port" -sTCP:LISTEN >/dev/null 2>&1 || \
    lsof -nP -iTCP:"$local_port" -sTCP:LISTEN >/dev/null 2>&1; then
    die "Access smoke test ports must be free"
fi

"$access_script" gateway-start
gateway_started=true
"$access_script" kurtosis engine status >/dev/null
"$access_script" kurtosis enclave ls >/dev/null
pass "Containerized Kurtosis control operations"

if "$access_script" kurtosis enclave inspect "$enclave" >/dev/null 2>&1; then
    die "Disposable enclave already exists: $enclave"
fi
"$access_script" kurtosis enclave add --name "$enclave" >/dev/null
enclave_created=true
"$access_script" kurtosis service add \
    --ports http=http:80/tcp \
    "$enclave" "$service_id" "$DEVNET_ACCESS_SMOKE_IMAGE" >/dev/null
pass "Disposable Kurtosis service created"

selector="kurtosistech.com/resource-type=user-service,kurtosistech.com/id=$service_id"
service_resources=$(kubectl --kubeconfig="$kubeconfig" \
    --namespace "$namespace" get services --selector "$selector" --output name)
service_count=$(printf '%s\n' "$service_resources" |
    awk 'NF { count += 1 } END { print count + 0 }')
if [ "$service_count" -ne 1 ]; then
    die "Smoke service discovery found $service_count matches"
fi
service_resource=$(printf '%s\n' "$service_resources" | awk 'NF { print; exit }')
pod_resource=$(kubectl --kubeconfig="$kubeconfig" --namespace "$namespace" \
    get pods --selector "$selector" --output name)
kubectl --kubeconfig="$kubeconfig" --namespace "$namespace" \
    label "$pod_resource" \
    kurtosistech.com.custom/ethereum-package.client-type=beacon \
    --overwrite >/dev/null
pass "Deterministic namespace and stable-label service discovery"

"$access_script" forward-start \
    beacon-api "$enclave" "$service_id" "$local_port"
forward_started=true

response=$(curl --fail --silent --show-error --max-time 10 \
    "http://127.0.0.1:$local_port/")
case "$response" in
    *'Welcome to nginx!'*)
        pass "Host access through explicit Kubernetes Service port-forward"
        ;;
    *)
        die "Disposable service returned an unexpected response"
        ;;
esac

published_binding_count=$(docker inspect \
    --format '{{len .HostConfig.PortBindings}}' "$gateway_container")
if [ "$published_binding_count" -ne 1 ]; then
    die "Gateway container publishes unexpected host ports"
fi
published_port=$(docker port "$gateway_container" "$gateway_port/tcp")
if [ "$published_port" != "127.0.0.1:$gateway_port" ]; then
    die "Gateway container has an unsafe host binding: $published_port"
fi
pass "Kurtosis dynamic user-service ports are not published to the host"

for checked_port in "$gateway_port" "$local_port"; do
    listener_output=$(lsof -nP -iTCP:"$checked_port" -sTCP:LISTEN)
    unexpected_address=$(printf '%s\n' "$listener_output" |
        awk -v expected="127.0.0.1:$checked_port" \
            'NR > 1 && $(NF - 1) != expected { print $(NF - 1); exit }')
    if [ -n "$unexpected_address" ]; then
        die "Non-loopback listener detected: $unexpected_address"
    fi
done
pass "No wildcard Kurtosis or Ethquake access listener"
