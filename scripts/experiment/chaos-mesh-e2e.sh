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
versions_file="$repository_root/devnet/versions.env"
dependency_lock="$repository_root/experiment/dependencies.lock.json"
values_file="$repository_root/experiment/chaos-mesh-values.yaml"
chart="$repository_root/.cache/experiment/chaos-mesh/helm/chaos-mesh"
helm_bin=${ETHQUAKE_HELM_BIN:-"$repository_root/.cache/experiment/tools/helm"}
kind_bin=${ETHQUAKE_KIND_BIN:-kind}
kubectl_bin=${ETHQUAKE_KUBECTL_BIN:-kubectl}
go_binary=${GO:-go}
cluster_name=ethquake-chaos-smoke
context_name=kind-ethquake-chaos-smoke
release_name=ethquake-chaos-smoke
namespace=kt-ethquake-phase3-local-smoke
run_id=local-smoke
deadman_namespace=kt-ethquake-phase3-local-deadman
deadman_run_id=local-deadman
cache_root="$repository_root/.cache/experiment/chaos-smoke"
kubeconfig="$cache_root/kubeconfig"
test_binary="$cache_root/chaos-fault-e2e.test"
deadman_ready_file="$cache_root/deadman-ready"
deadman_runner_log="$cache_root/deadman-runner.log"
owns_cluster=false
test_complete=false
deadman_runner_pid=

global_context() {
    "$kubectl_bin" config current-context 2>/dev/null || true
}

initial_global_context=$(global_context)

stop_deadman_runner() {
    if [ -z "$deadman_runner_pid" ]; then
        return
    fi
    if kill -0 "$deadman_runner_pid" 2>/dev/null; then
        kill -KILL "$deadman_runner_pid" 2>/dev/null || true
    fi
    wait "$deadman_runner_pid" 2>/dev/null || true
    deadman_runner_pid=
}

cleanup() {
    stop_deadman_runner
    if [ "$owns_cluster" = true ]; then
        if [ "$test_complete" = false ] && [ -r "$kubeconfig" ]; then
            printf '[INFO] Chaos Mesh E2E diagnostics follow\n' >&2
            "$kubectl_bin" --kubeconfig "$kubeconfig" --context "$context_name" \
                get pods --all-namespaces --output wide >&2 || true
            "$kubectl_bin" --kubeconfig "$kubeconfig" --context "$context_name" \
                get networkchaos --all-namespaces --output yaml >&2 || true
            "$kubectl_bin" --kubeconfig "$kubeconfig" --context "$context_name" \
                --namespace chaos-mesh logs deployment/chaos-controller-manager \
                --all-containers --tail=120 >&2 || true
            "$kubectl_bin" --kubeconfig "$kubeconfig" --context "$context_name" \
                --namespace chaos-mesh logs daemonset/chaos-daemon \
                --all-containers --tail=120 >&2 || true
        fi
        "$kind_bin" delete cluster --name "$cluster_name" >/dev/null 2>&1 || true
    fi
    rm -f -- "$kubeconfig" "$test_binary" "$deadman_ready_file" "$deadman_runner_log"
    if [ "$(global_context)" != "$initial_global_context" ]; then
        printf '[FAIL] Global Kubernetes context changed during Chaos Mesh E2E test\n' >&2
        return 1
    fi
}

trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

for command_name in docker git jq; do
    require_command "$command_name"
done
for executable in "$kind_bin" "$kubectl_bin" "$go_binary"; do
    if [ ! -x "$executable" ] && ! command -v "$executable" >/dev/null 2>&1; then
        die "Required executable not found: $executable"
    fi
done
if [ ! -x "$helm_bin" ]; then
    die "Repository-local Helm is missing; run make phase3-prepare"
fi
for required_file in "$versions_file" "$dependency_lock" "$values_file" "$chart/Chart.yaml"; do
    if [ ! -r "$required_file" ]; then
        die "Required input is missing: $required_file"
    fi
done

# This tracked file contains reviewed assignments only.
. "$versions_file"

case "$KIND_NODE_IMAGE" in
    *@sha256:*) ;;
    *) die "kind node image is not digest-pinned" ;;
esac
case "$DEVNET_ACCESS_SMOKE_IMAGE" in
    *@sha256:*) ;;
    *) die "smoke image is not digest-pinned" ;;
esac

expected_chaos_revision=$(jq -er '.fault_runtime.chaos_mesh_revision' "$dependency_lock")
actual_chaos_revision=$(git -C "$repository_root/.cache/experiment/chaos-mesh" rev-parse HEAD)
if [ "$actual_chaos_revision" != "$expected_chaos_revision" ]; then
    die "Chaos Mesh checkout does not match the dependency lock"
fi
if [ -n "$(git -C "$repository_root/.cache/experiment/chaos-mesh" status --porcelain)" ]; then
    die "Chaos Mesh checkout is dirty"
fi

kind_version=$($kind_bin version | awk 'NR == 1 { print $2 }')
if [ "${kind_version#v}" != "$KIND_VERSION" ]; then
    die "kind version ${kind_version#v} does not match $KIND_VERSION"
fi
if ! docker version --format '{{.Server.Version}}' >/dev/null 2>&1; then
    die "Docker daemon is unavailable"
fi
kind_clusters=$($kind_bin get clusters) || die "Could not list kind clusters"
if printf '%s\n' "$kind_clusters" | \
    awk -v target="$cluster_name" '$0 == target { found = 1 } END { exit !found }'; then
    die "Refusing to replace existing kind cluster: $cluster_name"
fi

case "$cache_root" in
    "$repository_root"/.cache/experiment/chaos-smoke) ;;
    *) die "Smoke cache is outside the Ethquake-owned path" ;;
esac
mkdir -p -- "$cache_root"
rm -f -- "$kubeconfig" "$test_binary" "$deadman_ready_file" "$deadman_runner_log"

owns_cluster=true
"$kind_bin" create cluster \
    --name "$cluster_name" \
    --image "$KIND_NODE_IMAGE" \
    --kubeconfig "$kubeconfig" \
    --wait 120s
actual_context=$($kubectl_bin --kubeconfig "$kubeconfig" config current-context)
if [ "$actual_context" != "$context_name" ]; then
    die "Disposable kubeconfig targets $actual_context; expected $context_name"
fi

"$helm_bin" install "$release_name" "$chart" \
    --kubeconfig "$kubeconfig" \
    --kube-context "$context_name" \
    --namespace chaos-mesh \
    --create-namespace \
    --values "$values_file" \
    --wait \
    --timeout 180s

"$kubectl_bin" --kubeconfig "$kubeconfig" --context "$context_name" \
    --namespace chaos-mesh rollout status deployment/chaos-controller-manager --timeout=180s
"$kubectl_bin" --kubeconfig "$kubeconfig" --context "$context_name" \
    --namespace chaos-mesh rollout status daemonset/chaos-daemon --timeout=180s

create_test_workload() {
    target_namespace=$1
    target_run_id=$2
    pod_a=$3
    pod_b=$4

    "$kubectl_bin" --kubeconfig "$kubeconfig" --context "$context_name" \
        create namespace "$target_namespace"
    "$kubectl_bin" --kubeconfig "$kubeconfig" --context "$context_name" \
        label namespace "$target_namespace" \
        dev.ethquake.managed=true \
        dev.ethquake.phase=3 \
        dev.ethquake.run-id="$target_run_id"
    "$kubectl_bin" --kubeconfig "$kubeconfig" --context "$context_name" \
        annotate namespace "$target_namespace" chaos-mesh.org/inject=enabled

    for pod_name in "$pod_a" "$pod_b"; do
        "$kubectl_bin" --kubeconfig "$kubeconfig" --context "$context_name" \
            --namespace "$target_namespace" run "$pod_name" \
            --image "$DEVNET_ACCESS_SMOKE_IMAGE" \
            --labels "dev.ethquake.managed=true,dev.ethquake.phase=3,dev.ethquake.run-id=$target_run_id" \
            --restart Never
    done
    "$kubectl_bin" --kubeconfig "$kubeconfig" --context "$context_name" \
        --namespace "$target_namespace" wait \
        --for=condition=Ready "pod/$pod_a" "pod/$pod_b" --timeout=180s
}

create_test_workload "$namespace" "$run_id" smoke-a smoke-b
create_test_workload "$deadman_namespace" "$deadman_run_id" deadman-a deadman-b

GOTOOLCHAIN=local "$go_binary" test -c -tags=e2e \
    -o "$test_binary" ./internal/fault

ETHQUAKE_CHAOS_E2E_KUBECTL=$kubectl_bin \
ETHQUAKE_CHAOS_E2E_KUBECONFIG=$kubeconfig \
ETHQUAKE_CHAOS_E2E_CONTEXT=$context_name \
    "$test_binary" -test.v -test.run '^TestChaosMeshAgainstDisposableKind$'

ETHQUAKE_CHAOS_E2E_KUBECTL=$kubectl_bin \
ETHQUAKE_CHAOS_E2E_KUBECONFIG=$kubeconfig \
ETHQUAKE_CHAOS_E2E_CONTEXT=$context_name \
ETHQUAKE_CHAOS_E2E_READY_FILE=$deadman_ready_file \
    "$test_binary" -test.v -test.run '^TestChaosMeshDeadmanRunner$' \
    >"$deadman_runner_log" 2>&1 &
deadman_runner_pid=$!

ready_wait_seconds=0
while [ ! -f "$deadman_ready_file" ]; do
    if ! kill -0 "$deadman_runner_pid" 2>/dev/null; then
        wait "$deadman_runner_pid" 2>/dev/null || true
        deadman_runner_pid=
        sed -n '1,160p' "$deadman_runner_log" >&2
        die "Deadman runner exited before confirming fault injection"
    fi
    if [ "$ready_wait_seconds" -ge 60 ]; then
        sed -n '1,160p' "$deadman_runner_log" >&2
        die "Timed out waiting for deadman runner fault injection"
    fi
    sleep 1
    ready_wait_seconds=$((ready_wait_seconds + 1))
done

if ! kill -KILL "$deadman_runner_pid"; then
    die "Could not SIGKILL the deadman runner"
fi
if wait "$deadman_runner_pid" 2>/dev/null; then
    die "Deadman runner exited successfully after SIGKILL"
fi
deadman_runner_pid=
pass "Deadman runner reached AllInjected and was terminated by SIGKILL"

ETHQUAKE_CHAOS_E2E_KUBECTL=$kubectl_bin \
ETHQUAKE_CHAOS_E2E_KUBECONFIG=$kubeconfig \
ETHQUAKE_CHAOS_E2E_CONTEXT=$context_name \
    "$test_binary" -test.v -test.run '^TestChaosMeshDeadmanRecovery$'

"$kind_bin" delete cluster --name "$cluster_name"
owns_cluster=false
test_complete=true
rm -f -- "$kubeconfig" "$test_binary" "$deadman_ready_file" "$deadman_runner_log"
trap - EXIT HUP INT TERM

if [ "$(global_context)" != "$initial_global_context" ]; then
    die "Global Kubernetes context changed during Chaos Mesh E2E test"
fi
remaining_clusters=$($kind_bin get clusters) || die "Could not verify kind cluster cleanup"
if printf '%s\n' "$remaining_clusters" | \
    awk -v target="$cluster_name" '$0 == target { found = 1 } END { exit !found }'; then
    die "Disposable kind cluster remains after cleanup"
fi

pass "Chaos Mesh TTL recovery after runner SIGKILL and exact cluster cleanup"
