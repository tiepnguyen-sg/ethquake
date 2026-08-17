#!/bin/sh

set -eu

umask 077

pass() {
    printf '[PASS] %s\n' "$1"
}

info() {
    printf '[INFO] %s\n' "$1"
}

die() {
    printf '[FAIL] %s\n' "$1" >&2
    exit 1
}

refuse() {
    printf 'REFUSING per %s: %s. To override, say "override %s" and I will comply and log it in docs/overrides.md.\n' "$1" "$2" "$1" >&2
    exit 1
}

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repository_root=$(CDPATH= cd -- "$script_dir/../.." && pwd)
lock_env="$repository_root/experiment/phase3.lock.env"
scenario_file="$repository_root/scenarios/cl-p2p-partition.yaml"
network_params="$repository_root/experiment/network_params.yaml"
chaos_values="$repository_root/experiment/chaos-mesh-values.yaml"
dependency_lock="$repository_root/experiment/dependencies.lock.json"
dependency_root="$repository_root/.cache/experiment/dependencies"
package_path="$dependency_root/ethereum-package"
chaos_root="$repository_root/.cache/experiment/chaos-mesh"
chaos_chart="$chaos_root/helm/chaos-mesh"
helm_bin="$repository_root/.cache/experiment/tools/helm"
access_root="$repository_root/.cache/experiment/access"
gcp_runtime_root="$repository_root/.cache/experiment/gcp"
kubeconfig=
cluster_server=
cluster_ca=
cluster_name=
current_enclave=
cluster_created=false
cleanup_failed=false
initial_global_context=$(kubectl config current-context 2>/dev/null || true)

if [ ! -r "$lock_env" ]; then
    die "Phase 3 lock is missing: $lock_env"
fi
# This tracked file contains reviewed assignments only.
. "$lock_env"

if [ -z "${GCP_PROJECT:-}" ] || [ -z "${GCP_ZONE:-}" ] || \
    [ -z "${GCP_BILLING_ACCOUNT:-}" ] || [ -z "${GCP_BUDGET_ID:-}" ] || \
    [ -z "${GKE_VERSION:-}" ] || [ -z "${ETHQUAKE_SESSION_ID:-}" ]; then
    refuse '§0.8' 'GCP session identifiers, exact GKE version, billing account, and budget ID are required'
fi

if [ "${ETHQUAKE_CLOUD_AUTHORIZED:-}" != "I_ACCEPT_GCP_CHARGES_AND_TEARDOWN" ]; then
    refuse '§0.7' 'explicit authorization for GCP charges and teardown is missing'
fi
case "$ETHQUAKE_SESSION_ID" in
    ''|*[!a-z0-9-]*|-*|*-)
        die "ETHQUAKE_SESSION_ID must be lowercase and DNS-safe"
        ;;
esac
if [ "${#ETHQUAKE_SESSION_ID}" -gt 24 ]; then
    die "ETHQUAKE_SESSION_ID must not exceed 24 characters"
fi

cluster_name="ethquake-p3-$ETHQUAKE_SESSION_ID"
session_runtime="$gcp_runtime_root/$ETHQUAKE_SESSION_ID"
kubeconfig="$session_runtime/kubeconfig"
evidence_root="$repository_root/runs/phase3-$ETHQUAKE_SESSION_ID"
runs_root="$evidence_root/runs"
analysis_root="$evidence_root/analysis"
go_binary=${GO:-go}

case "$session_runtime" in
    "$repository_root"/.cache/experiment/gcp/*)
        ;;
    *)
        die "Session runtime path is outside the Ethquake-owned cache"
        ;;
esac
case "$evidence_root" in
    "$repository_root"/runs/phase3-"$ETHQUAKE_SESSION_ID")
        ;;
    *)
        die "Evidence root must be an absolute Phase 3 path under the repository runs directory"
        ;;
esac
if [ -L "$repository_root/runs" ]; then
    die "Repository runs directory must not be a symlink"
fi
if [ -e "$evidence_root" ]; then
    die "Evidence root already exists; use a new session ID or an empty explicit path: $evidence_root"
fi

access() {
    ETHQUAKE_KUBECONFIG="$kubeconfig" \
    ETHQUAKE_KUBERNETES_CONTEXT="$PHASE3_KUBERNETES_CONTEXT" \
    ETHQUAKE_STORAGE_CLASS="$PHASE3_STORAGE_CLASS" \
    ETHQUAKE_NETWORK_PARAMS="$network_params" \
    ETHQUAKE_PACKAGE_PATH="$package_path" \
    ETHQUAKE_ACCESS_ROOT="$access_root" \
    ETHQUAKE_START_ENGINE_IF_MISSING="${ETHQUAKE_START_ENGINE_IF_MISSING:-false}" \
        "$repository_root/scripts/devnet/access.sh" "$@"
}

refresh_session_kubeconfig() {
    if [ -z "$cluster_server" ] || [ -z "$cluster_ca" ]; then
        die "GKE server and certificate authority must be discovered before credential refresh"
    fi
    next_kubeconfig="$session_runtime/kubeconfig.next"
    if ! gcloud auth print-access-token |
        jq -Rn \
            --arg server "$cluster_server" \
            --arg ca "$cluster_ca" \
            --arg context "$PHASE3_KUBERNETES_CONTEXT" \
            'input as $token |
             if ($token | length) < 20 then error("access token is empty") else
             {
               apiVersion: "v1",
               kind: "Config",
               clusters: [{name: "ethquake", cluster: {server: $server, "certificate-authority-data": $ca}}],
               users: [{name: "ethquake-session", user: {token: $token}}],
               contexts: [{name: $context, context: {cluster: "ethquake", user: "ethquake-session"}}],
               "current-context": $context
             }
             end' > "$next_kubeconfig"; then
        die "Could not create a short-lived session kubeconfig"
    fi
    chmod 0600 "$next_kubeconfig"
    mv -- "$next_kubeconfig" "$kubeconfig"
    if [ "$(kubectl --kubeconfig "$kubeconfig" config current-context)" != "$PHASE3_KUBERNETES_CONTEXT" ]; then
        die "Session-local kubeconfig credential refresh failed"
    fi
    pass "Refreshed short-lived session kubeconfig"
}

restart_gateway_with_fresh_credentials() {
    access gateway-stop
    refresh_session_kubeconfig
    ETHQUAKE_START_ENGINE_IF_MISSING=true access gateway-start
}

assert_global_context() {
    actual=$(kubectl config current-context 2>/dev/null || true)
    if [ "$actual" != "$initial_global_context" ]; then
        die "Global Kubernetes context changed unexpectedly"
    fi
}

remove_session_runtime() {
    case "$session_runtime" in
        "$repository_root"/.cache/experiment/gcp/*)
            if [ -d "$session_runtime" ]; then
                rm -rf -- "$session_runtime"
            fi
            ;;
        *)
            cleanup_failed=true
            printf '[FAIL] Refusing to remove unexpected session runtime: %s\n' "$session_runtime" >&2
            ;;
    esac
}

cluster_description() {
    gcloud container clusters describe "$cluster_name" \
        --project "$GCP_PROJECT" --zone "$GCP_ZONE" --format=json
}

cluster_count() {
    listing=$(gcloud container clusters list \
        --project "$GCP_PROJECT" \
        --filter "name=$cluster_name AND location=$GCP_ZONE" \
        --format=json) || return 1
    printf '%s\n' "$listing" | jq -er --arg name "$cluster_name" --arg zone "$GCP_ZONE" \
        '[.[] | select(.name == $name and .location == $zone)] | length'
}

delete_owned_cluster() {
    if ! description=$(cluster_description 2>/dev/null); then
        count=$(cluster_count 2>/dev/null) || {
            cleanup_failed=true
            printf '[FAIL] Could not determine whether the GKE cluster still exists: %s\n' "$cluster_name" >&2
            return
        }
        if [ "$count" -eq 0 ]; then
            pass "GKE cluster is already absent: $cluster_name"
            return
        fi
        cleanup_failed=true
        printf '[FAIL] GKE cluster exists but its ownership metadata could not be read: %s\n' "$cluster_name" >&2
        return
    fi
    if ! printf '%s\n' "$description" | jq -e --arg session "$ETHQUAKE_SESSION_ID" \
        '.resourceLabels["dev-ethquake-managed"] == "true" and
         .resourceLabels["dev-ethquake-phase"] == "3" and
         .resourceLabels["dev-ethquake-session"] == $session' >/dev/null; then
        cleanup_failed=true
        printf '[FAIL] Refusing to delete cluster without exact Ethquake ownership labels: %s\n' "$cluster_name" >&2
        return
    fi
    info "Deleting owned GKE cluster: $cluster_name"
    if ! gcloud container clusters delete "$cluster_name" \
        --project "$GCP_PROJECT" --zone "$GCP_ZONE" --quiet; then
        cleanup_failed=true
        printf '[FAIL] GKE cluster deletion command failed: %s\n' "$cluster_name" >&2
        return
    fi
    attempt=0
    while [ "$attempt" -lt 12 ]; do
        count=$(cluster_count 2>/dev/null) || count=unknown
        if [ "$count" = 0 ]; then
            pass "GKE cluster deleted: $cluster_name"
            return
        fi
        attempt=$((attempt + 1))
        sleep 5
    done
    cleanup_failed=true
    printf '[FAIL] GKE cluster still exists after bounded teardown verification: %s\n' "$cluster_name" >&2
}

cleanup() {
    original_status=$?
    trap - EXIT HUP INT TERM
    set +e
    if [ -n "$kubeconfig" ] && [ -r "$kubeconfig" ]; then
        if [ -n "$current_enclave" ]; then
            access kurtosis enclave rm --force "$current_enclave" >/dev/null 2>&1 || true
        fi
        access gateway-stop >/dev/null 2>&1 || cleanup_failed=true
    fi
    if [ "$cluster_created" = true ]; then
        delete_owned_cluster
    fi
    remove_session_runtime
    if [ "$(kubectl config current-context 2>/dev/null || true)" != "$initial_global_context" ]; then
        cleanup_failed=true
        printf '[FAIL] Global Kubernetes context changed during the Phase 3 session\n' >&2
    fi
    if [ "$cleanup_failed" = true ]; then
        exit 1
    fi
    exit "$original_status"
}

trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

"$repository_root/scripts/experiment/prepare-dependencies.sh"
"$repository_root/scripts/experiment/prepare-chaos-mesh.sh"
"$repository_root/scripts/experiment/prepare-helm.sh"
make -C "$repository_root" GO="$go_binary" build
ETHQUAKE_SESSION_ID="$ETHQUAKE_SESSION_ID" \
GCP_PROJECT="$GCP_PROJECT" \
GCP_ZONE="$GCP_ZONE" \
GCP_BILLING_ACCOUNT="$GCP_BILLING_ACCOUNT" \
GCP_BUDGET_ID="$GCP_BUDGET_ID" \
GKE_VERSION="$GKE_VERSION" \
    "$repository_root/scripts/experiment/preflight.sh" cloud

pass "Gate checklist: three committed control runs and three committed fault runs"
pass "Gate checklist: preregistered prediction and thresholds validated"
pass "Gate checklist: every fault has a runtime-derived deadman TTL"
pass "Gate checklist: exact namespace, chain ID, and ownership guards enabled"
pass "Gate checklist: off-cluster artifact root validated"
pass "Gate checklist: EXIT teardown trap registered"
pass "Gate checklist: USD $PHASE3_GCP_BUDGET_USD budget alert active"

"$helm_bin" lint "$chaos_chart" --values "$chaos_values" >/dev/null
pass "Chaos Mesh Helm chart lint"

mkdir -p -- "$session_runtime" "$runs_root" "$analysis_root"
chmod 0700 "$session_runtime" "$evidence_root" "$runs_root" "$analysis_root"
cp -- "$dependency_lock" "$evidence_root/dependencies.lock.json"
cp -- "$lock_env" "$evidence_root/phase3.lock.env"

info "Creating ephemeral GKE cluster: $cluster_name"
cluster_created=true
gcloud container clusters create "$cluster_name" \
    --project "$GCP_PROJECT" \
    --zone "$GCP_ZONE" \
    --cluster-version "$GKE_VERSION" \
    --machine-type "$PHASE3_SYSTEM_MACHINE_TYPE" \
    --num-nodes 1 \
    --disk-type pd-balanced \
    --disk-size 50 \
    --image-type COS_CONTAINERD \
    --enable-ip-alias \
    --enable-autorepair \
    --no-enable-autoupgrade \
    --no-enable-basic-auth \
    --logging SYSTEM \
    --monitoring SYSTEM \
    --labels "dev-ethquake-managed=true,dev-ethquake-phase=3,dev-ethquake-session=$ETHQUAKE_SESSION_ID" \
    --quiet

for pool in ethquake-p1 ethquake-p2 ethquake-p3 ethquake-p4; do
    gcloud container node-pools create "$pool" \
        --cluster "$cluster_name" \
        --project "$GCP_PROJECT" \
        --zone "$GCP_ZONE" \
        --node-version "$GKE_VERSION" \
        --machine-type "$PHASE3_PARTICIPANT_MACHINE_TYPE" \
        --num-nodes 1 \
        --disk-type pd-balanced \
        --disk-size 50 \
        --image-type COS_CONTAINERD \
        --spot \
        --enable-autorepair \
        --no-enable-autoupgrade \
        --node-labels "dev.ethquake.managed=true,dev.ethquake.phase=3,dev.ethquake.participant=$pool" \
        --quiet
done

KUBECONFIG="$kubeconfig" gcloud container clusters get-credentials "$cluster_name" \
    --project "$GCP_PROJECT" --zone "$GCP_ZONE"
cluster_server=$(kubectl --kubeconfig "$kubeconfig" config view --raw --minify \
    --output jsonpath='{.clusters[0].cluster.server}')
cluster_ca=$(kubectl --kubeconfig "$kubeconfig" config view --raw --minify \
    --output jsonpath='{.clusters[0].cluster.certificate-authority-data}')
case "$cluster_server" in
    https://*)
        ;;
    *)
        die "GKE API server is not HTTPS"
        ;;
esac
case "$cluster_ca" in
    ''|*[!A-Za-z0-9+/=]*)
        die "GKE certificate authority data is invalid"
        ;;
esac
refresh_session_kubeconfig
assert_global_context
pass "Repository-local Kubernetes context: $PHASE3_KUBERNETES_CONTEXT"

if ! kubectl --kubeconfig "$kubeconfig" --context "$PHASE3_KUBERNETES_CONTEXT" \
    get storageclass "$PHASE3_STORAGE_CLASS" >/dev/null 2>&1; then
    die "Required GKE storage class is absent: $PHASE3_STORAGE_CLASS"
fi

"$helm_bin" upgrade --install chaos-mesh "$chaos_chart" \
    --kubeconfig "$kubeconfig" \
    --kube-context "$PHASE3_KUBERNETES_CONTEXT" \
    --namespace chaos-mesh \
    --create-namespace \
    --values "$chaos_values" \
    --atomic \
    --wait \
    --timeout 10m
kubectl --kubeconfig "$kubeconfig" --context "$PHASE3_KUBERNETES_CONTEXT" \
    --namespace chaos-mesh delete serviceaccount chaos-dashboard \
    --ignore-not-found >/dev/null
kubectl --kubeconfig "$kubeconfig" --context "$PHASE3_KUBERNETES_CONTEXT" \
    delete clusterrole \
    chaos-mesh-chaos-dashboard-cluster-level \
    chaos-mesh-chaos-dashboard-target-namespace \
    --ignore-not-found >/dev/null
kubectl --kubeconfig "$kubeconfig" --context "$PHASE3_KUBERNETES_CONTEXT" \
    delete clusterrolebinding \
    chaos-mesh-chaos-dashboard-cluster-level \
    chaos-mesh-chaos-dashboard-target-namespace \
    --ignore-not-found >/dev/null
if [ -n "$(kubectl --kubeconfig "$kubeconfig" --context "$PHASE3_KUBERNETES_CONTEXT" \
    get serviceaccount,clusterrole,clusterrolebinding \
    --all-namespaces --selector app.kubernetes.io/component=chaos-dashboard \
    --output=name 2>/dev/null)" ]; then
    die "Disabled Chaos Mesh dashboard left active RBAC resources"
fi
kubectl --kubeconfig "$kubeconfig" --context "$PHASE3_KUBERNETES_CONTEXT" \
    label namespace chaos-mesh \
    dev.ethquake.managed=true dev.ethquake.phase=3 --overwrite >/dev/null
pass "Chaos Mesh NetworkChaos runtime"

cluster_description | jq '{
    schema_version: "ethquake.session/v1alpha1",
    name: .name,
    location: .location,
    current_master_version: .currentMasterVersion,
    current_node_version: .currentNodeVersion,
    resource_labels: .resourceLabels,
    node_pools: [.nodePools[] | {
      name: .name,
      version: .version,
      machine_type: .config.machineType,
      image_type: .config.imageType,
      spot: (.config.spot // false),
      labels: .config.labels
    }]
  }' > "$evidence_root/session-metadata.json"
chmod 0600 "$evidence_root/session-metadata.json"

ETHQUAKE_START_ENGINE_IF_MISSING=true access gateway-start
access gateway-stop

stop_run_forwards() {
    access forward-stop beacon-api cl-1-lighthouse-geth || true
    access forward-stop beacon-api cl-2-teku-reth || true
    access forward-stop beacon-api cl-3-lighthouse-geth || true
    access forward-stop beacon-api cl-4-teku-reth || true
    access forward-stop execution-ws el-1-geth-lighthouse || true
    access forward-stop prometheus prometheus || true
}

for run_id in $PHASE3_RUN_ORDER; do
    current_enclave="ethquake-phase3-$run_id"
    namespace="kt-$current_enclave"
    if access kurtosis enclave inspect "$current_enclave" >/dev/null 2>&1; then
        die "Run enclave already exists: $current_enclave"
    fi
    if kubectl --kubeconfig "$kubeconfig" --context "$PHASE3_KUBERNETES_CONTEXT" \
        get namespace "$namespace" >/dev/null 2>&1; then
        die "Run namespace already exists: $namespace"
    fi

    info "Starting committed Phase 3 run: $run_id"
    restart_gateway_with_fresh_credentials
    access kurtosis run \
        --enclave "$current_enclave" \
        /ethquake/ethereum-package \
        --args-file /ethquake/network_params.yaml

    restart_gateway_with_fresh_credentials

    kubectl --kubeconfig "$kubeconfig" --context "$PHASE3_KUBERNETES_CONTEXT" \
        label namespace "$namespace" \
        dev.ethquake.managed=true \
        dev.ethquake.phase=3 \
        "dev.ethquake.run-id=$run_id" \
        --overwrite >/dev/null
    kubectl --kubeconfig "$kubeconfig" --context "$PHASE3_KUBERNETES_CONTEXT" \
        annotate namespace "$namespace" chaos-mesh.org/inject=enabled --overwrite >/dev/null

    validator_destination="/home/kurtosis/validator-ranges-$run_id"
    access kurtosis files download "$current_enclave" validator-ranges "$validator_destination"
    validator_ranges="$access_root/runtime/home/validator-ranges-$run_id/validator-ranges.yaml"
    if [ ! -r "$validator_ranges" ]; then
        die "Downloaded validator range artifact is missing: $validator_ranges"
    fi

    access forward-start beacon-api "$current_enclave" cl-1-lighthouse-geth 15052
    access forward-start beacon-api "$current_enclave" cl-2-teku-reth 15053
    access forward-start beacon-api "$current_enclave" cl-3-lighthouse-geth 15054
    access forward-start beacon-api "$current_enclave" cl-4-teku-reth 15055
    access forward-start execution-ws "$current_enclave" el-1-geth-lighthouse 18546
    access forward-start prometheus "$current_enclave" prometheus 19090

    curl --fail --silent --show-error --max-time 10 \
        http://127.0.0.1:19090/-/ready >/dev/null
    pass "Prometheus access: $run_id"

    # Capture can span almost an hour. Refresh the repository-local credential
    # immediately before it begins; existing loopback forwards remain bound.
    refresh_session_kubeconfig

    "$repository_root/bin/ethquake" experiment capture \
        --scenario "$scenario_file" \
        --run-id "$run_id" \
        --kubeconfig "$kubeconfig" \
        --context "$PHASE3_KUBERNETES_CONTEXT" \
        --namespace "$namespace" \
        --validator-ranges "$validator_ranges" \
        --dependency-lock "$dependency_lock" \
        --artifact-root "$runs_root" \
        --execution-chain ws://127.0.0.1:18546 \
        --beacon lighthouse-a=http://127.0.0.1:15052 \
        --beacon teku-a=http://127.0.0.1:15053 \
        --beacon lighthouse-b=http://127.0.0.1:15054 \
        --beacon teku-b=http://127.0.0.1:15055

    stop_run_forwards
    restart_gateway_with_fresh_credentials
    access kurtosis enclave rm --force "$current_enclave"
    if ! kubectl --kubeconfig "$kubeconfig" --context "$PHASE3_KUBERNETES_CONTEXT" \
        wait --for=delete "namespace/$namespace" --timeout=180s >/dev/null 2>&1; then
        die "Run namespace remains after exact enclave cleanup: $namespace"
    fi
    current_enclave=
    pass "Clean run network removed: $run_id"
done

"$repository_root/bin/ethquake" experiment analyze \
    --scenario "$scenario_file" \
    --runs-root "$runs_root" \
    --output-root "$analysis_root"

report_path="$analysis_root/cl-p2p-partition-analysis/report.json"
if [ ! -r "$report_path" ]; then
    die "Phase 3 report was not created"
fi
jq -e '.schema_version == "ethquake.report/v1alpha1"' "$report_path" >/dev/null
pass "Phase 3 evidence bundle: $evidence_root"
assert_global_context
