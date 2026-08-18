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
qualification_scheduling_filter="$script_dir/qualification-scheduling.jq"
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
network_name=
subnet_name=
router_name=
nat_name=
current_enclave=
cluster_created=false
network_created=false
router_created=false
cleanup_failed=false
initial_global_context=
watchdog_pid=
runner_public_cidr=
action=${1:-run}
session_kind=evidence
dry_run=false

if [ ! -r "$lock_env" ]; then
    die "Phase 3 lock is missing: $lock_env"
fi
# This tracked file contains reviewed assignments only.
. "$lock_env"

case "$action" in
    dry-run)
        dry_run=true
        ;;
    run)
        ;;
    qualification-dry-run)
        session_kind=qualification
        dry_run=true
        ;;
    qualify)
        session_kind=qualification
        ;;
    *)
        die "Usage: phase3.sh [dry-run|run|qualification-dry-run|qualify]"
        ;;
esac
case "${ETHQUAKE_SESSION_ID:-}" in
    ''|*[!a-z0-9-]*|-*|*-)
        die "ETHQUAKE_SESSION_ID must be lowercase and DNS-safe"
        ;;
esac
if [ "${#ETHQUAKE_SESSION_ID}" -gt 24 ]; then
    die "ETHQUAKE_SESSION_ID must not exceed 24 characters"
fi
if [ "${PHASE3_PARTICIPANT_COUNT:-}" != 4 ] || \
    [ "${PHASE3_PARTICIPANT_MACHINE_TYPE:-}" != n2-custom-2-16384 ] || \
    [ "${PHASE3_REQUIRED_PREEMPTIBLE_VCPUS:-}" != 0 ] || \
    [ "${PHASE3_REQUIRED_ON_DEMAND_VCPUS:-}" != 12 ] || \
    [ "${PHASE3_REQUIRED_TOTAL_VCPUS:-}" != 12 ] || \
    [ "${PHASE3_PRIVATE_NODES:-}" != true ] || \
    [ "${PHASE3_CLOUD_NAT:-}" != true ] || \
    [ "${PHASE3_HTTP_LOAD_BALANCING:-}" != false ] || \
    [ "${PHASE3_PARTICIPANT_REQUEST_MCPU:-}" != 1400 ] || \
    [ "${PHASE3_NODE_DISK_TYPE:-}" != pd-balanced ] || \
    [ "${PHASE3_NODE_DISK_GB:-}" != 40 ] || \
    [ "${PHASE3_WORKLOAD_SSD_RESERVE_GB:-}" != 50 ] || \
    [ "${PHASE3_RUNNER_IP_DISCOVERY_URL:-}" != https://ifconfig.me/ip ] || \
    [ "${PHASE3_KUBERNETES_CONTEXT:-}" != gke-ethquake-phase3 ]; then
    die "Phase 3 GCP topology locks are incomplete or changed"
fi
cluster_name="ethquake-p3-$ETHQUAKE_SESSION_ID"
network_name="$cluster_name-net"
subnet_name="$cluster_name-subnet"
router_name="$cluster_name-router"
nat_name="$cluster_name-nat"
pods_range_name="$cluster_name-pods"
services_range_name="$cluster_name-services"
expected_network_description="Ethquake Phase 3 session $ETHQUAKE_SESSION_ID managed network"
expected_subnet_description="Ethquake Phase 3 session $ETHQUAKE_SESSION_ID managed subnet"
expected_router_description="Ethquake Phase 3 session $ETHQUAKE_SESSION_ID managed Cloud Router"

"$script_dir/preflight.sh" static
if [ "$dry_run" = true ]; then
    printf '[PLAN] provider=gcp\n'
    printf '[PLAN] mode=dry-run\n'
    printf '[PLAN] session_kind=%s\n' "$session_kind"
    printf '[PLAN] session_id=%s\n' "$ETHQUAKE_SESSION_ID"
    printf '[PLAN] participants=%s\n' "$PHASE3_PARTICIPANT_COUNT"
    printf '[PLAN] required_preemptible_vcpus=%s\n' "$PHASE3_REQUIRED_PREEMPTIBLE_VCPUS"
    printf '[PLAN] required_on_demand_vcpus=%s\n' "$PHASE3_REQUIRED_ON_DEMAND_VCPUS"
    printf '[PLAN] required_total_vcpus=%s\n' "$PHASE3_REQUIRED_TOTAL_VCPUS"
    printf '[PLAN] participant_machine_type=%s\n' "$PHASE3_PARTICIPANT_MACHINE_TYPE"
    printf '[PLAN] participant_request_mcpu=%s\n' "$PHASE3_PARTICIPANT_REQUEST_MCPU"
    printf '[PLAN] node_disk=%s:%sGiB\n' "$PHASE3_NODE_DISK_TYPE" "$PHASE3_NODE_DISK_GB"
    printf '[PLAN] workload_ssd_reserve_gb=%s\n' "$PHASE3_WORKLOAD_SSD_RESERVE_GB"
    printf '[PLAN] private_nodes=%s\n' "$PHASE3_PRIVATE_NODES"
    printf '[PLAN] cloud_nat=%s\n' "$PHASE3_CLOUD_NAT"
    printf '[PLAN] http_load_balancing=%s\n' "$PHASE3_HTTP_LOAD_BALANCING"
    printf '[PLAN] control_plane_authorization=runner-public-ip/32\n'
    printf '[PLAN] kubernetes_context=%s\n' "$PHASE3_KUBERNETES_CONTEXT"
    printf '[PLAN] region=%s\n' "$PHASE3_GCP_REGION"
    printf '[PLAN] zone=%s\n' "$PHASE3_GCP_ZONE"
    printf '[PLAN] cluster=%s\n' "$cluster_name"
    printf '[PLAN] network=%s\n' "$network_name"
    printf '[PLAN] subnet=%s\n' "$subnet_name"
    printf '[PLAN] router=%s\n' "$router_name"
    printf '[PLAN] nat=%s\n' "$nat_name"
    printf '[PLAN] run_order=%s\n' "$PHASE3_RUN_ORDER"
    printf '[PLAN] cloud_api_calls=false\n'
    printf '[PLAN] resource_creation=false\n'
    printf '[PLAN] account_preflight=not_run\n'
    printf '[PLAN] budget_alert=unverified\n'
    exit 0
fi

if [ -n "${ETHQUAKE_GCP_TEST_MODE:-}" ] || \
    [ -n "${ETHQUAKE_GCP_CATALOG_FIXTURE:-}" ]; then
    refuse '§0.8' 'test-only GCP preflight overrides are forbidden for real runs'
fi
if [ -z "${GCP_PROJECT:-}" ] || \
    [ -z "${GCP_BILLING_ACCOUNT:-}" ] || \
    [ -z "${GCP_BUDGET_ID:-}" ] || \
    [ -z "${GCP_BUDGET_RECIPIENT:-}" ] || \
    [ -z "${GKE_VERSION:-}" ]; then
    refuse '§0.8' 'GCP project, billing, budget, recipient, and exact GKE version are required'
fi
if [ "${ETHQUAKE_CLOUD_AUTHORIZED:-}" != "I_ACCEPT_GCP_CHARGES_AND_TEARDOWN" ]; then
    refuse '§0.7' 'explicit authorization for GCP charges and teardown is missing'
fi

GCP_ZONE=$PHASE3_GCP_ZONE
initial_global_context=$(kubectl config current-context 2>/dev/null || true)

session_runtime="$gcp_runtime_root/$ETHQUAKE_SESSION_ID"
kubeconfig="$session_runtime/kubeconfig"
if [ "$session_kind" = qualification ]; then
    artifact_root="$repository_root/runs/phase3-qualification-$ETHQUAKE_SESSION_ID"
else
    artifact_root="$repository_root/runs/phase3-$ETHQUAKE_SESSION_ID"
fi
runs_root="$artifact_root/runs"
analysis_root="$artifact_root/analysis"
go_binary=${GO:-go}

case "$session_runtime" in
    "$repository_root"/.cache/experiment/gcp/*)
        ;;
    *)
        die "Session runtime path is outside the Ethquake-owned cache"
        ;;
esac
case "$artifact_root" in
    "$repository_root"/runs/phase3-"$ETHQUAKE_SESSION_ID"|\
    "$repository_root"/runs/phase3-qualification-"$ETHQUAKE_SESSION_ID")
        ;;
    *)
        die "Artifact root must be an absolute Phase 3 path under the repository runs directory"
        ;;
esac
if [ -L "$repository_root/runs" ]; then
    die "Repository runs directory must not be a symlink"
fi
if [ -e "$artifact_root" ]; then
    die "Artifact root already exists; use a new session ID: $artifact_root"
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

verify_runtime_inventory_empty() {
    attempt=0
    while [ "$attempt" -lt 12 ]; do
        instances=$(gcloud compute instances list \
            --project "$GCP_PROJECT" --format=json 2>/dev/null) || instances=unknown
        disks=$(gcloud compute disks list \
            --project "$GCP_PROJECT" --format=json 2>/dev/null) || disks=unknown
        if printf '%s\n%s\n' "$instances" "$disks" |
            jq -s -e 'length == 2 and all(.[]; type == "array" and length == 0)' >/dev/null 2>&1; then
            pass "GCP node and disk inventory is empty"
            return
        fi
        attempt=$((attempt + 1))
        sleep 5
    done
    cleanup_failed=true
    printf '[FAIL] GCP instance or disk inventory remains after cluster teardown\n' >&2
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

delete_owned_pvc_disks() {
    attempt=0
    owned_disks=
    expected_namespace="kt-$current_enclave"
    while [ "$attempt" -lt 24 ]; do
        instances=$(gcloud compute instances list \
            --project "$GCP_PROJECT" --format=json 2>/dev/null) || instances=unknown
        disks=$(gcloud compute disks list \
            --project "$GCP_PROJECT" --format=json 2>/dev/null) || disks=unknown
        if [ -n "$current_enclave" ] && \
            printf '%s\n' "$instances" | jq -e 'type == "array" and length == 0' >/dev/null 2>&1 && \
            printf '%s\n' "$disks" | jq -e \
                --arg namespace "$expected_namespace" \
                --arg zone "$GCP_ZONE" \
                --argjson reserve "$PHASE3_WORKLOAD_SSD_RESERVE_GB" '
                type == "array" and
                ([.[].sizeGb | tonumber] | add // 0) <= $reserve and
                all(.[];
                  (.name | test("^pvc-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$")) and
                  (.zone | endswith("/" + $zone)) and
                  (.type | endswith("/pd-balanced")) and
                  ((.users // []) | length) == 0 and
                  .labels["reclaim-policy-gke-io"] == "delete" and
                  (((.description // "") | try fromjson catch {}) as $metadata |
                    $metadata["kubernetes.io/created-for/pv/name"] == .name and
                    $metadata["kubernetes.io/created-for/pvc/namespace"] == $namespace and
                    $metadata["storage.gke.io/created-by"] == "pd.csi.storage.gke.io"))
            ' >/dev/null 2>&1; then
            owned_disks=$disks
            break
        fi
        if [ -z "$current_enclave" ] && \
            printf '%s\n%s\n' "$instances" "$disks" |
                jq -s -e 'length == 2 and all(.[]; type == "array" and length == 0)' >/dev/null 2>&1; then
            owned_disks='[]'
            break
        fi
        attempt=$((attempt + 1))
        sleep 5
    done
    if [ -z "$owned_disks" ]; then
        cleanup_failed=true
        printf '[FAIL] Refusing to delete unexpected instance or disk inventory after GKE teardown\n' >&2
        return
    fi
    if [ "$(printf '%s\n' "$owned_disks" | jq 'length')" -eq 0 ]; then
        return
    fi
    disk_list="$session_runtime/residual-pvc-disks.tsv"
    printf '%s\n' "$owned_disks" | jq -r '.[] | [.name, (.zone | split("/") | last)] | @tsv' >"$disk_list"
    while IFS='	' read -r disk_name disk_zone; do
        info "Deleting detached qualification PVC disk: $disk_name"
        if ! gcloud compute disks delete "$disk_name" \
            --project "$GCP_PROJECT" --zone "$disk_zone" --quiet; then
            cleanup_failed=true
            printf '[FAIL] Qualification PVC disk deletion failed: %s\n' "$disk_name" >&2
            return
        fi
    done <"$disk_list"
    pass "Owned detached qualification PVC disks deleted"
}

delete_owned_network_endpoint_groups() {
    negs=$(gcloud compute network-endpoint-groups list \
        --project "$GCP_PROJECT" \
        --filter "network:$network_name" \
        --format=json 2>/dev/null) || {
        cleanup_failed=true
        printf '[FAIL] Could not list network endpoint groups for owned VPC: %s\n' "$network_name" >&2
        return
    }
    if ! printf '%s\n' "$negs" | jq -e \
        --arg network "$network_name" \
        --arg zone "$GCP_ZONE" '
        type == "array" and all(.[];
          (.name | test("^k8s1-[a-z0-9-]+$")) and
          (.network | endswith("/" + $network)) and
          (.zone | endswith("/" + $zone)) and
          .networkEndpointType == "GCE_VM_IP_PORT" and
          (.size // 0) == 0)
    ' >/dev/null; then
        cleanup_failed=true
        printf '[FAIL] Refusing to delete NEG without exact empty GKE/VPC ownership: %s\n' "$network_name" >&2
        return
    fi
    if [ "$(printf '%s\n' "$negs" | jq 'length')" -eq 0 ]; then
        return
    fi
    neg_list="$session_runtime/residual-negs.tsv"
    printf '%s\n' "$negs" | jq -r '.[] | [.name, (.zone | split("/") | last)] | @tsv' >"$neg_list"
    while IFS='	' read -r neg_name neg_zone; do
        case "$neg_name:$neg_zone" in
            k8s1-[a-z0-9-]*:"$GCP_ZONE")
                ;;
            *)
                cleanup_failed=true
                printf '[FAIL] Refusing to delete invalid residual NEG identity: %s / %s\n' "$neg_name" "$neg_zone" >&2
                return
                ;;
        esac
        info "Deleting empty GKE network endpoint group: $neg_name"
        if ! gcloud compute network-endpoint-groups delete "$neg_name" \
            --project "$GCP_PROJECT" --zone "$neg_zone" --quiet; then
            cleanup_failed=true
            printf '[FAIL] Network endpoint group deletion failed: %s\n' "$neg_name" >&2
            return
        fi
    done <"$neg_list"
    pass "Owned empty GKE network endpoint groups deleted"
}

wait_for_gke_load_balancer_gc() {
    attempt=0
    while [ "$attempt" -lt 20 ]; do
        forwarding_rules=$(gcloud compute forwarding-rules list --project "$GCP_PROJECT" --format=json 2>/dev/null) || forwarding_rules=unknown
        http_proxies=$(gcloud compute target-http-proxies list --project "$GCP_PROJECT" --format=json 2>/dev/null) || http_proxies=unknown
        https_proxies=$(gcloud compute target-https-proxies list --project "$GCP_PROJECT" --format=json 2>/dev/null) || https_proxies=unknown
        url_maps=$(gcloud compute url-maps list --project "$GCP_PROJECT" --format=json 2>/dev/null) || url_maps=unknown
        backend_services=$(gcloud compute backend-services list --project "$GCP_PROJECT" --format=json 2>/dev/null) || backend_services=unknown
        health_checks=$(gcloud compute health-checks list --project "$GCP_PROJECT" --format=json 2>/dev/null) || health_checks=unknown
        if printf '%s\n%s\n%s\n%s\n%s\n%s\n' \
            "$forwarding_rules" "$http_proxies" "$https_proxies" "$url_maps" \
            "$backend_services" "$health_checks" |
            jq -s -e 'length == 6 and all(.[]; type == "array" and length == 0)' >/dev/null 2>&1; then
            pass "GKE load-balancer controller resources are gone"
            return
        fi
        attempt=$((attempt + 1))
        if [ $((attempt % 2)) -eq 0 ]; then
            info "Waiting for GKE load-balancer garbage collection: attempt $attempt / 20"
        fi
        sleep 15
    done
    cleanup_failed=true
    printf '[FAIL] GKE load-balancer resources remain after ten-minute garbage-collection wait\n' >&2
}

delete_owned_firewall_rules() {
    firewalls=$(gcloud compute firewall-rules list \
        --project "$GCP_PROJECT" \
        --filter "network:$network_name" \
        --format=json 2>/dev/null) || {
        cleanup_failed=true
        printf '[FAIL] Could not list firewall rules for owned VPC: %s\n' "$network_name" >&2
        return
    }
    if ! printf '%s\n' "$firewalls" | jq -e \
        --arg network "$network_name" \
        --arg cluster_prefix "gke-$cluster_name-" '
        type == "array" and all(.[];
          (.name | test("^k8s-fw-l7--[a-z0-9]+$")) and
          .description == "GCE L7 firewall rule" and
          (.network | endswith("/" + $network)) and
          .direction == "INGRESS" and
          all(.targetTags[]?; startswith($cluster_prefix)))
    ' >/dev/null; then
        cleanup_failed=true
        printf '[FAIL] Refusing to delete firewall without exact GKE/VPC ownership: %s\n' "$network_name" >&2
        return
    fi
    if [ "$(printf '%s\n' "$firewalls" | jq 'length')" -eq 0 ]; then
        return
    fi
    firewall_list="$session_runtime/residual-firewalls.txt"
    printf '%s\n' "$firewalls" | jq -r '.[].name' >"$firewall_list"
    while IFS= read -r firewall_name; do
        info "Deleting GKE L7 firewall rule: $firewall_name"
        if ! gcloud compute firewall-rules delete "$firewall_name" \
            --project "$GCP_PROJECT" --quiet; then
            cleanup_failed=true
            printf '[FAIL] GKE firewall deletion failed: %s\n' "$firewall_name" >&2
            return
        fi
    done <"$firewall_list"
    pass "Owned GKE L7 firewall rules deleted"
}

delete_owned_nat_router() {
    if ! router=$(gcloud compute routers describe "$router_name" \
        --project "$GCP_PROJECT" --region "$PHASE3_GCP_REGION" --format=json 2>/dev/null); then
        router_listing=$(gcloud compute routers list \
            --project "$GCP_PROJECT" \
            --filter "name=$router_name AND region:($PHASE3_GCP_REGION)" \
            --format=json 2>/dev/null) || {
            cleanup_failed=true
            printf '[FAIL] Could not determine whether the Cloud Router still exists: %s\n' "$router_name" >&2
            return
        }
        if printf '%s\n' "$router_listing" | jq -e 'type == "array" and length == 0' >/dev/null; then
            pass "Cloud Router is already absent: $router_name"
            return
        fi
        cleanup_failed=true
        printf '[FAIL] Cloud Router exists but its ownership metadata could not be read: %s\n' "$router_name" >&2
        return
    fi
    if ! printf '%s\n' "$router" | jq -e \
        --arg description "$expected_router_description" \
        --arg network "$network_name" \
        '.description == $description and (.network | endswith("/" + $network))' >/dev/null; then
        cleanup_failed=true
        printf '[FAIL] Refusing to delete Cloud Router without exact Ethquake ownership metadata: %s\n' "$router_name" >&2
        return
    fi

    if nat=$(gcloud compute routers nats describe "$nat_name" \
        --router "$router_name" --project "$GCP_PROJECT" \
        --region "$PHASE3_GCP_REGION" --format=json 2>/dev/null); then
        if ! printf '%s\n' "$nat" | jq -e --arg name "$nat_name" \
            '.name == $name and .natIpAllocateOption == "AUTO_ONLY" and
             .sourceSubnetworkIpRangesToNat == "ALL_SUBNETWORKS_ALL_IP_RANGES"' >/dev/null; then
            cleanup_failed=true
            printf '[FAIL] Refusing to delete Cloud NAT without exact allocation policy: %s\n' "$nat_name" >&2
            return
        fi
        info "Deleting owned Cloud NAT: $nat_name"
        if ! gcloud compute routers nats delete "$nat_name" \
            --router "$router_name" --project "$GCP_PROJECT" \
            --region "$PHASE3_GCP_REGION" --quiet; then
            cleanup_failed=true
            printf '[FAIL] Cloud NAT deletion failed: %s\n' "$nat_name" >&2
            return
        fi
    else
        nat_listing=$(gcloud compute routers nats list \
            --router "$router_name" --project "$GCP_PROJECT" \
            --region "$PHASE3_GCP_REGION" --format=json 2>/dev/null) || {
            cleanup_failed=true
            printf '[FAIL] Could not determine whether Cloud NAT still exists: %s\n' "$nat_name" >&2
            return
        }
        if ! printf '%s\n' "$nat_listing" | jq -e --arg name "$nat_name" \
            'type == "array" and all(.[]; .name != $name)' >/dev/null; then
            cleanup_failed=true
            printf '[FAIL] Cloud NAT exists but its allocation policy could not be read: %s\n' "$nat_name" >&2
            return
        fi
    fi

    info "Deleting owned Cloud Router: $router_name"
    if ! gcloud compute routers delete "$router_name" \
        --project "$GCP_PROJECT" --region "$PHASE3_GCP_REGION" --quiet; then
        cleanup_failed=true
        printf '[FAIL] Cloud Router deletion failed: %s\n' "$router_name" >&2
        return
    fi
    pass "Cloud NAT and Router deleted: $nat_name / $router_name"
}

delete_owned_network() {
    if subnet=$(gcloud compute networks subnets describe "$subnet_name" \
        --project "$GCP_PROJECT" --region "$PHASE3_GCP_REGION" --format=json 2>/dev/null); then
        if ! printf '%s\n' "$subnet" | jq -e \
            --arg description "$expected_subnet_description" \
            --arg network "$network_name" \
            '.description == $description and (.network | endswith("/" + $network))' >/dev/null; then
            cleanup_failed=true
            printf '[FAIL] Refusing to delete subnet without exact Ethquake ownership metadata: %s\n' "$subnet_name" >&2
            return
        fi
        info "Deleting owned GCP subnet: $subnet_name"
        if ! gcloud compute networks subnets delete "$subnet_name" \
            --project "$GCP_PROJECT" --region "$PHASE3_GCP_REGION" --quiet; then
            cleanup_failed=true
            printf '[FAIL] GCP subnet deletion failed: %s\n' "$subnet_name" >&2
            return
        fi
    else
        subnet_listing=$(gcloud compute networks subnets list \
            --project "$GCP_PROJECT" \
            --filter "name=$subnet_name AND region:($PHASE3_GCP_REGION)" \
            --format=json 2>/dev/null) || {
            cleanup_failed=true
            printf '[FAIL] Could not determine whether the GCP subnet still exists: %s\n' "$subnet_name" >&2
            return
        }
        if ! printf '%s\n' "$subnet_listing" | jq -e 'type == "array" and length == 0' >/dev/null; then
            cleanup_failed=true
            printf '[FAIL] GCP subnet exists but its ownership metadata could not be read: %s\n' "$subnet_name" >&2
            return
        fi
    fi

    if ! network=$(gcloud compute networks describe "$network_name" \
        --project "$GCP_PROJECT" --format=json 2>/dev/null); then
        network_listing=$(gcloud compute networks list \
            --project "$GCP_PROJECT" --filter "name=$network_name" \
            --format=json 2>/dev/null) || {
            cleanup_failed=true
            printf '[FAIL] Could not determine whether the GCP network still exists: %s\n' "$network_name" >&2
            return
        }
        if printf '%s\n' "$network_listing" | jq -e 'type == "array" and length == 0' >/dev/null; then
            pass "GCP network is already absent: $network_name"
            return
        fi
        cleanup_failed=true
        printf '[FAIL] GCP network exists but its ownership metadata could not be read: %s\n' "$network_name" >&2
        return
    fi
    if ! printf '%s\n' "$network" | jq -e \
        --arg description "$expected_network_description" \
        '.description == $description and .autoCreateSubnetworks == false' >/dev/null; then
        cleanup_failed=true
        printf '[FAIL] Refusing to delete network without exact Ethquake ownership metadata: %s\n' "$network_name" >&2
        return
    fi
    info "Deleting owned GCP network: $network_name"
    attempt=0
    while [ "$attempt" -lt 12 ]; do
        if gcloud compute networks delete "$network_name" \
            --project "$GCP_PROJECT" --quiet >/dev/null 2>&1; then
            pass "GCP network deleted: $network_name"
            return
        fi
        attempt=$((attempt + 1))
        sleep 5
    done
    cleanup_failed=true
    printf '[FAIL] GCP network deletion failed after bounded retries: %s\n' "$network_name" >&2
}

verify_cloud_inventory_empty() {
    attempt=0
    while [ "$attempt" -lt 12 ]; do
        clusters=$(gcloud container clusters list --project "$GCP_PROJECT" --format=json 2>/dev/null) || clusters=unknown
        addresses=$(gcloud compute addresses list --project "$GCP_PROJECT" --format=json 2>/dev/null) || addresses=unknown
        routers=$(gcloud compute routers list --project "$GCP_PROJECT" --format=json 2>/dev/null) || routers=unknown
        networks=$(gcloud compute networks list --project "$GCP_PROJECT" --format=json 2>/dev/null) || networks=unknown
        subnets=$(gcloud compute networks subnets list --project "$GCP_PROJECT" --format=json 2>/dev/null) || subnets=unknown
        firewalls=$(gcloud compute firewall-rules list --project "$GCP_PROJECT" --format=json 2>/dev/null) || firewalls=unknown
        negs=$(gcloud compute network-endpoint-groups list --project "$GCP_PROJECT" --format=json 2>/dev/null) || negs=unknown
        backend_services=$(gcloud compute backend-services list --project "$GCP_PROJECT" --format=json 2>/dev/null) || backend_services=unknown
        forwarding_rules=$(gcloud compute forwarding-rules list --project "$GCP_PROJECT" --format=json 2>/dev/null) || forwarding_rules=unknown
        http_proxies=$(gcloud compute target-http-proxies list --project "$GCP_PROJECT" --format=json 2>/dev/null) || http_proxies=unknown
        https_proxies=$(gcloud compute target-https-proxies list --project "$GCP_PROJECT" --format=json 2>/dev/null) || https_proxies=unknown
        url_maps=$(gcloud compute url-maps list --project "$GCP_PROJECT" --format=json 2>/dev/null) || url_maps=unknown
        health_checks=$(gcloud compute health-checks list --project "$GCP_PROJECT" --format=json 2>/dev/null) || health_checks=unknown
        if printf '%s\n%s\n%s\n%s\n%s\n%s\n%s\n%s\n%s\n%s\n%s\n%s\n%s\n' \
            "$clusters" "$addresses" "$routers" "$networks" "$subnets" "$firewalls" \
            "$negs" "$backend_services" "$forwarding_rules" "$http_proxies" "$https_proxies" \
            "$url_maps" "$health_checks" |
            jq -s -e 'length == 13 and all(.[]; type == "array" and length == 0)' >/dev/null 2>&1; then
            pass "Dedicated GCP project inventory is empty"
            return
        fi
        attempt=$((attempt + 1))
        sleep 5
    done
    cleanup_failed=true
    printf '[FAIL] Residual GKE, VPC, NEG, backend, or forwarding-rule resource remains\n' >&2
}

verify_private_node_and_nat_configuration() {
    cluster_json="$artifact_root/cluster.json"
    instances_json="$artifact_root/instances.json"
    nat_json="$artifact_root/cloud-nat.json"
    cluster_description >"$cluster_json"
    gcloud compute instances list --project "$GCP_PROJECT" --format=json >"$instances_json"
    gcloud compute routers nats describe "$nat_name" \
        --router "$router_name" --project "$GCP_PROJECT" \
        --region "$PHASE3_GCP_REGION" --format=json >"$nat_json"

    if ! jq -e \
        --arg runner_cidr "$runner_public_cidr" \
        --arg system "$PHASE3_SYSTEM_MACHINE_TYPE" \
        --arg participant "$PHASE3_PARTICIPANT_MACHINE_TYPE" \
        --arg disk_type "$PHASE3_NODE_DISK_TYPE" \
        --argjson disk_gb "$PHASE3_NODE_DISK_GB" '
        .privateClusterConfig.enablePrivateNodes == true and
        (.privateClusterConfig.enablePrivateEndpoint // false) == false and
        .addonsConfig.httpLoadBalancing.disabled == true and
        .masterAuthorizedNetworksConfig.enabled == true and
        any(.masterAuthorizedNetworksConfig.cidrBlocks[]?; .cidrBlock == $runner_cidr) and
        ([.nodePools[] | select(
          .name == "default-pool" and .config.machineType == $system and
          .config.diskType == $disk_type and .config.diskSizeGb == $disk_gb and
          (.config.spot // false) == false and .initialNodeCount == 1 and .status == "RUNNING"
        )] | length) == 1 and
        ([.nodePools[] | select(
          (.name == "ethquake-p1" or .name == "ethquake-p2" or
           .name == "ethquake-p3" or .name == "ethquake-p4") and
          .config.machineType == $participant and
          .config.diskType == $disk_type and .config.diskSizeGb == $disk_gb and
          (.config.spot // false) == false and .initialNodeCount == 1 and .status == "RUNNING"
        )] | length) == 4
    ' "$cluster_json" >/dev/null; then
        die "GKE private nodes or runner-only control-plane authorization does not match the lock"
    fi
    if ! jq -e \
        --arg cluster "$cluster_name" \
        --arg session "$ETHQUAKE_SESSION_ID" \
        --arg zone "$GCP_ZONE" \
        --arg system "$PHASE3_SYSTEM_MACHINE_TYPE" \
        --arg participant_alias "n2-highmem-2" '
        length == 5 and
        all(.[];
          .labels["dev-ethquake-managed"] == "true" and
          .labels["dev-ethquake-phase"] == "3" and
          .labels["dev-ethquake-session"] == $session and
          .labels["goog-k8s-cluster-name"] == $cluster and
          .labels["goog-k8s-cluster-location"] == $zone and
          .labels["goog-gke-node-pool-provisioning-model"] == "on-demand" and
          all(.networkInterfaces[]?; ((.accessConfigs // []) | length) == 0)) and
        ([.[] | select(.machineType | endswith("/" + $system))] | length) == 1 and
        ([.[] | select(.machineType | endswith("/" + $participant_alias))] | length) == 4 and
        ([.[].labels["goog-k8s-node-pool-name"]] | sort) ==
          ["default-pool", "ethquake-p1", "ethquake-p2", "ethquake-p3", "ethquake-p4"]
    ' "$instances_json" >/dev/null; then
        die "GKE nodes must be exactly one system and four participant VMs without external IPs"
    fi
    if ! jq -e --arg name "$nat_name" '
        .name == $name and
        .natIpAllocateOption == "AUTO_ONLY" and
        .sourceSubnetworkIpRangesToNat == "ALL_SUBNETWORKS_ALL_IP_RANGES" and
        .logConfig.enable == true and .logConfig.filter == "ALL"
    ' "$nat_json" >/dev/null; then
        die "Cloud NAT allocation or logging policy does not match the qualification lock"
    fi
    pass "Private-node topology: 1 system + 4 participant VMs, no node external IPs"
    pass "Public Cloud NAT: automatic IP allocation with full flow logging"
}

run_nat_egress_gate() {
    nat_pod="ethquake-nat-$ETHQUAKE_SESSION_ID"
    kubectl_retry \
        run "$nat_pod" \
        --image="$PHASE3_NAT_CHECK_IMAGE" \
        --restart=Never \
        --labels="dev.ethquake.managed=true,dev.ethquake.phase=3,dev.ethquake.purpose=qualification" \
        --overrides='{"spec":{"nodeSelector":{"dev.ethquake.role":"system"}}}' \
        --command -- sh -c \
        'wget -q -O /dev/null -T 30 http://connectivitycheck.gstatic.com/generate_204'
    if ! kubectl_retry \
        wait --for=jsonpath='{.status.phase}'=Succeeded "pod/$nat_pod" --timeout=180s; then
        kubectl_retry \
            get "pod/$nat_pod" --output=json >"$artifact_root/nat-egress-pod.json" 2>/dev/null || true
        kubectl_retry \
            logs "$nat_pod" >"$artifact_root/nat-egress.log" 2>&1 || true
        die "Private-node DNS/HTTP egress through Cloud NAT did not complete"
    fi
    kubectl_retry \
        get "pod/$nat_pod" --output=json >"$artifact_root/nat-egress-pod.json"
    kubectl_retry \
        logs "$nat_pod" >"$artifact_root/nat-egress.log" 2>&1 || true
    kubectl_retry \
        delete "pod/$nat_pod" --wait=true >/dev/null
    pass "Cloud NAT egress: private system node resolved DNS and reached public HTTP"
}

wait_for_qualification_workload() {
    namespace=$1
    attempt=0
    while [ "$attempt" -lt 60 ]; do
        pods=$(kubectl_retry \
            --namespace "$namespace" get pods --output=json 2>/dev/null || true)
        if printf '%s\n' "$pods" | jq -e '
            [.items[] | select(.status.phase != "Succeeded")] as $active |
            ($active | length) >= 12 and
            all($active[];
              .status.phase == "Running" and
              ((.status.containerStatuses // []) | length) > 0 and
              all(.status.containerStatuses[]; .ready == true))
        ' >/dev/null 2>&1; then
            printf '%s\n' "$pods" >"$artifact_root/pods-ready.json"
            pass "Scheduling: all active qualification pods are Running and Ready"
            return
        fi
        attempt=$((attempt + 1))
        if [ $((attempt % 4)) -eq 0 ]; then
            info "Waiting for qualification workload readiness: $((attempt * 15))s"
        fi
        sleep 15
    done
    printf '%s\n' "$pods" >"$artifact_root/pods-readiness-timeout.json"
    kubectl_retry \
        --namespace "$namespace" get events --sort-by=.lastTimestamp \
        --output=json >"$artifact_root/events-readiness-timeout.json" 2>/dev/null || true
    die "Qualification workload did not become fully scheduled and Ready within 15 minutes"
}

assert_qualification_scheduling() {
    namespace=$1
    nodes_file="$artifact_root/participant-nodes.json"
    pods_file="$artifact_root/pods-scheduled.json"
    kubectl_retry \
        get nodes --selector dev.ethquake.participant --output=json >"$nodes_file"
    kubectl_retry \
        --namespace "$namespace" get pods --output=json >"$pods_file"
    if ! jq -s -e -f "$qualification_scheduling_filter" \
        "$nodes_file" "$pods_file" >/dev/null; then
        die "Each participant node must host its co-located EL, CL, and VC workload"
    fi
    pass "Scheduling placement: EL, CL, and VC are present on each participant node"
}

assert_qualification_health() {
    namespace=$1
    phase=$2
    pods_file="$artifact_root/pods-$phase.json"
    nodes_file="$artifact_root/nodes-$phase.json"
    events_file="$artifact_root/events-$phase.json"
    kubectl_retry \
        --namespace "$namespace" get pods --output=json >"$pods_file"
    kubectl_retry \
        get nodes --output=json >"$nodes_file"
    kubectl_retry \
        --namespace "$namespace" get events --sort-by=.lastTimestamp \
        --output=json >"$events_file" 2>/dev/null || printf '{"items":[]}\n' >"$events_file"

    if ! jq -e '
        all(.items[];
          (.status.phase == "Running" or .status.phase == "Succeeded") and
          all(((.status.initContainerStatuses // []) + (.status.containerStatuses // []))[];
            (.restartCount // 0) == 0 and
            (.lastState.terminated.reason // "") != "OOMKilled" and
            (.state.terminated.reason // "") != "OOMKilled" and
            (.state.waiting.reason // "") != "CrashLoopBackOff"))
    ' "$pods_file" >/dev/null; then
        die "OOM, restart, crash-loop, or failed-pod gate failed during $phase"
    fi
    if ! jq -e '
        all(.items[];
          any(.status.conditions[]; .type == "Ready" and .status == "True") and
          all(.status.conditions[];
            if (.type == "MemoryPressure" or .type == "DiskPressure" or .type == "PIDPressure")
            then .status == "False" else true end))
    ' "$nodes_file" >/dev/null; then
        die "Node Ready/MemoryPressure/DiskPressure/PIDPressure gate failed during $phase"
    fi
    pass "Workload health ($phase): zero restarts, OOMKills, crash loops, or failed pods"
    pass "Node pressure ($phase): Ready with memory, disk, and PID pressure false"
}

capture_consensus_snapshot() {
    phase=$1
    snapshot_file="$artifact_root/consensus-$phase.tsv"
    : >"$snapshot_file"
    for endpoint in lighthouse-a:15052 teku-a:15053 lighthouse-b:15054 teku-b:15055; do
        label=${endpoint%%:*}
        port=${endpoint#*:}
        head_file="$artifact_root/$label-head-$phase.json"
        finality_file="$artifact_root/$label-finality-$phase.json"
        curl --fail --silent --show-error --max-time 15 \
            "http://127.0.0.1:$port/eth/v1/beacon/headers/head" >"$head_file"
        curl --fail --silent --show-error --max-time 15 \
            "http://127.0.0.1:$port/eth/v1/beacon/states/head/finality_checkpoints" >"$finality_file"
        head_slot=$(jq -er '.data.header.message.slot | select(test("^[0-9]+$"))' "$head_file") || \
            die "Invalid head slot from $label during $phase"
        finalized_epoch=$(jq -er '.data.finalized.epoch | select(test("^[0-9]+$"))' "$finality_file") || \
            die "Invalid finalized epoch from $label during $phase"
        printf '%s\t%s\t%s\n' "$label" "$head_slot" "$finalized_epoch" >>"$snapshot_file"
    done
}

assert_consensus_progress() {
    if ! awk -F '\t' '
        NR == FNR { head[$1] = $2; finalized[$1] = $3; count += 1; next }
        {
          seen += 1
          if (!($1 in head) || $2 <= head[$1] || $3 <= finalized[$1]) failed = 1
          printf "%s\t%s\t%s\t%s\t%s\n", $1, head[$1], $2, finalized[$1], $3
        }
        END { if (count != 4 || seen != 4 || failed) exit 1 }
    ' "$artifact_root/consensus-start.tsv" "$artifact_root/consensus-end.tsv" \
        >"$artifact_root/consensus-progress.tsv"; then
        die "All four beacon nodes must advance both head slot and finalized epoch"
    fi
    pass "Consensus progress: all four beacon heads and finalized epochs advanced"
}

capture_throttle_snapshot() {
    namespace=$1
    phase=$2
    snapshot_file="$artifact_root/cpu-throttling-$phase.tsv"
    : >"$snapshot_file"
    participant_nodes=$(kubectl_retry \
        get nodes --selector dev.ethquake.participant \
        --output=jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}')
    for node in $participant_nodes; do
        kubectl_retry \
            get --raw "/api/v1/nodes/$node/proxy/metrics/cadvisor" |
            awk -v namespace="$namespace" -v node="$node" '
              /^container_cpu_cfs_(periods|throttled_periods)_total\{/ {
                metric_labels = $1
                value = $2
                if (index(metric_labels, "namespace=\"" namespace "\"") == 0 ||
                    index(metric_labels, "container=\"\"") > 0) next
                metric = metric_labels
                sub(/\{.*/, "", metric)
                labels = metric_labels
                sub(/^[^{]*\{/, "", labels)
                sub(/\}$/, "", labels)
                printf "%s\t%s\t%s\t%s\n", node, metric, labels, value
              }
            ' >>"$snapshot_file"
    done
    if [ "$(awk -F '\t' '$2 == "container_cpu_cfs_periods_total" { count++ } END { print count + 0 }' "$snapshot_file")" -lt 12 ]; then
        die "cAdvisor did not expose CPU CFS counters for all participant containers during $phase"
    fi
}

assert_cpu_throttling() {
    ratios_file="$artifact_root/cpu-throttling-ratios.tsv"
    if ! awk -F '\t' '
        FNR == NR {
          key = $1 SUBSEP $3
          node[key] = $1
          labels[key] = $3
          if ($2 == "container_cpu_cfs_periods_total") start_periods[key] = $4
          if ($2 == "container_cpu_cfs_throttled_periods_total") start_throttled[key] = $4
          next
        }
        {
          key = $1 SUBSEP $3
          if ($2 == "container_cpu_cfs_periods_total") end_periods[key] = $4
          if ($2 == "container_cpu_cfs_throttled_periods_total") end_throttled[key] = $4
        }
        END {
          for (key in end_periods) {
            if (!(key in start_periods) || !(key in start_throttled) || !(key in end_throttled)) continue
            periods = end_periods[key] - start_periods[key]
            throttled = end_throttled[key] - start_throttled[key]
            if (periods <= 0 || throttled < 0) continue
            printf "%s\t%.6f\t%s\n", node[key], throttled / periods, labels[key]
          }
        }
    ' "$artifact_root/cpu-throttling-start.tsv" "$artifact_root/cpu-throttling-end.tsv" \
        >"$ratios_file"; then
        die "Could not calculate CPU throttling ratios"
    fi
    ratio_count=$(awk 'END { print NR + 0 }' "$ratios_file")
    node_count=$(awk -F '\t' '!seen[$1]++ { count++ } END { print count + 0 }' "$ratios_file")
    if [ "$ratio_count" -lt 12 ] || [ "$node_count" -ne 4 ]; then
        die "CPU throttling deltas must cover at least twelve containers on four participant nodes"
    fi
    max_ratio=$(awk -F '\t' 'BEGIN { max = 0 } $2 > max { max = $2 } END { printf "%.6f", max }' "$ratios_file")
    if ! awk -v actual="$max_ratio" -v limit="$PHASE3_QUALIFICATION_MAX_CPU_THROTTLE_RATIO" \
        'BEGIN { exit !(actual <= limit) }'; then
        die "Maximum CPU throttle ratio $max_ratio exceeds locked limit $PHASE3_QUALIFICATION_MAX_CPU_THROTTLE_RATIO"
    fi
    pass "CPU throttling: max CFS ratio $max_ratio <= $PHASE3_QUALIFICATION_MAX_CPU_THROTTLE_RATIO"
}

discover_runner_public_ipv4() {
    if ! discovered_ipv4=$(curl --fail --silent --show-error --max-time 15 \
        "$PHASE3_RUNNER_IP_DISCOVERY_URL"); then
        die "Could not discover the runner public IPv4 for GKE control-plane authorization"
    fi
    if ! printf '%s\n' "$discovered_ipv4" | awk -F '.' '
        NF != 4 { exit 1 }
        {
          for (i = 1; i <= 4; i++) {
            if ($i !~ /^[0-9]+$/ || $i < 0 || $i > 255) exit 1
          }
        }
    '; then
        die "Runner IP discovery returned an invalid IPv4 address"
    fi
    printf '%s\n' "$discovered_ipv4"
}

refresh_runner_authorization_if_changed() {
    set +e
    current_ipv4=$(discover_runner_public_ipv4)
    discovery_status=$?
    set -e
    if [ "$discovery_status" -ne 0 ]; then
        info "Runner IP re-discovery failed during retry; leaving GKE control-plane authorization unchanged"
        return 1
    fi
    current_cidr="$current_ipv4/32"
    if [ "$current_cidr" = "$runner_public_cidr" ]; then
        return 1
    fi
    info "Runner public IPv4 changed from $runner_public_cidr to $current_cidr; updating GKE control-plane authorization"
    set +e
    gcloud container clusters update "$cluster_name" \
        --project "$GCP_PROJECT" --zone "$GCP_ZONE" \
        --enable-master-authorized-networks \
        --master-authorized-networks "$current_cidr" --quiet
    update_status=$?
    set -e
    if [ "$update_status" -ne 0 ]; then
        info "GKE control-plane authorization update failed; leaving prior authorization in place"
        return 1
    fi
    runner_public_cidr=$current_cidr
    printf '%s\n' "$runner_public_cidr" >"$artifact_root/runner-public-cidr.txt"
    pass "GKE control-plane authorization refreshed to $runner_public_cidr"
}

kubectl_retry() {
    if kubectl --kubeconfig "$kubeconfig" --context "$PHASE3_KUBERNETES_CONTEXT" "$@"; then
        return 0
    fi
    info "kubectl call failed; checking for a runner IP change before one retry"
    refresh_runner_authorization_if_changed || true
    sleep 10
    kubectl --kubeconfig "$kubeconfig" --context "$PHASE3_KUBERNETES_CONTEXT" "$@"
}

bounded_wait() {
    total=$1
    label=$2
    elapsed=0
    while [ "$elapsed" -lt "$total" ]; do
        remaining=$((total - elapsed))
        step=30
        if [ "$remaining" -lt "$step" ]; then
            step=$remaining
        fi
        sleep "$step"
        elapsed=$((elapsed + step))
        if [ $((elapsed % 60)) -eq 0 ] || [ "$elapsed" -eq "$total" ]; then
            info "$label: ${elapsed}s / ${total}s"
        fi
    done
}

cleanup() {
    original_status=$?
    trap - EXIT HUP INT TERM
    set +e
    if [ -n "$watchdog_pid" ]; then
        kill "$watchdog_pid" >/dev/null 2>&1 || true
    fi
    if [ -n "$kubeconfig" ] && [ -r "$kubeconfig" ]; then
        access gateway-stop >/dev/null 2>&1 || cleanup_failed=true
    fi
    if [ "$cluster_created" = true ]; then
        delete_owned_cluster
        delete_owned_pvc_disks
        verify_runtime_inventory_empty
        wait_for_gke_load_balancer_gc
        delete_owned_network_endpoint_groups
        delete_owned_firewall_rules
    fi
    if [ "$router_created" = true ]; then
        delete_owned_nat_router
    fi
    if [ "$network_created" = true ]; then
        delete_owned_network
    fi
    if [ "$cluster_created" = true ] || [ "$router_created" = true ] || [ "$network_created" = true ]; then
        verify_cloud_inventory_empty
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

"$repository_root/scripts/experiment/gcp-account-preflight.sh"
"$repository_root/scripts/experiment/prepare-dependencies.sh"
if [ "$session_kind" = evidence ]; then
    "$repository_root/scripts/experiment/prepare-chaos-mesh.sh"
fi
"$repository_root/scripts/experiment/prepare-helm.sh"
make -C "$repository_root" GO="$go_binary" build
"$repository_root/scripts/experiment/preflight.sh" local

if [ "$session_kind" = evidence ]; then
    pass "Gate checklist: three committed control runs and three committed fault runs"
    pass "Gate checklist: preregistered prediction and thresholds validated"
    pass "Gate checklist: every fault has a runtime-derived deadman TTL"
else
    pass "Qualification is non-evidence and cannot be reused as a control run"
    pass "Qualification gates are scheduling, finality, health, pressure, throttling, and NAT egress"
fi
pass "Gate checklist: exact namespace, chain ID, and ownership guards enabled"
pass "Gate checklist: off-cluster artifact root validated"
pass "Gate checklist: EXIT teardown trap registered"
pass "Gate checklist: VND $PHASE3_GCP_BUDGET_VND gross budget alert active"
pass "Gate checklist: single region $PHASE3_GCP_REGION and zone $PHASE3_GCP_ZONE locked"

if [ "$session_kind" = evidence ]; then
    "$helm_bin" lint "$chaos_chart" --values "$chaos_values" >/dev/null
    pass "Chaos Mesh Helm chart lint"
fi

mkdir -p -- "$session_runtime" "$artifact_root"
chmod 0700 "$session_runtime" "$artifact_root"
run_log="$artifact_root/run.log"
: >"$run_log"
chmod 0600 "$run_log"
log_pipe=$(mktemp -u)
mkfifo -m 600 "$log_pipe"
tee -a "$run_log" <"$log_pipe" &
exec >"$log_pipe" 2>&1
rm -f "$log_pipe"
info "Session output is duplicated to $run_log"
runner_public_ipv4=$(discover_runner_public_ipv4)
runner_public_cidr="$runner_public_ipv4/32"
printf '%s\n' "$runner_public_cidr" >"$artifact_root/runner-public-cidr.txt"
chmod 0600 "$artifact_root/runner-public-cidr.txt"
pass "GKE control-plane authorization resolved to one runner IPv4 /32"
if [ "$session_kind" = evidence ]; then
    mkdir -p -- "$runs_root" "$analysis_root"
    chmod 0700 "$runs_root" "$analysis_root"
fi
cp -- "$dependency_lock" "$artifact_root/dependencies.lock.json"
cp -- "$lock_env" "$artifact_root/phase3.lock.env"
cp -- "$network_params" "$artifact_root/network_params.yaml"

if [ "$session_kind" = qualification ]; then
    info "Starting $PHASE3_QUALIFICATION_MAX_SECONDS-second qualification watchdog"
    (
        sleep "$PHASE3_QUALIFICATION_MAX_SECONDS"
        kill -TERM "$$" >/dev/null 2>&1 || true
    ) &
    watchdog_pid=$!
fi

info "Creating single-region GCP network: $network_name"
network_created=true
gcloud compute networks create "$network_name" \
    --project "$GCP_PROJECT" \
    --subnet-mode custom \
    --bgp-routing-mode regional \
    --mtu 1460 \
    --description "$expected_network_description" \
    --quiet
gcloud compute networks subnets create "$subnet_name" \
    --project "$GCP_PROJECT" \
    --network "$network_name" \
    --region "$PHASE3_GCP_REGION" \
    --range 10.42.0.0/20 \
    --secondary-range "$pods_range_name=10.44.0.0/14,$services_range_name=10.48.0.0/20" \
    --enable-private-ip-google-access \
    --description "$expected_subnet_description" \
    --quiet

info "Creating Cloud Router and Public NAT: $router_name / $nat_name"
router_created=true
gcloud compute routers create "$router_name" \
    --project "$GCP_PROJECT" \
    --network "$network_name" \
    --region "$PHASE3_GCP_REGION" \
    --description "$expected_router_description" \
    --quiet
gcloud compute routers nats create "$nat_name" \
    --router "$router_name" \
    --project "$GCP_PROJECT" \
    --region "$PHASE3_GCP_REGION" \
    --nat-all-subnet-ip-ranges \
    --auto-allocate-nat-external-ips \
    --enable-logging \
    --log-filter=ALL \
    --quiet

info "Creating ephemeral GKE cluster: $cluster_name"
cluster_created=true
KUBECONFIG="$kubeconfig" gcloud container clusters create "$cluster_name" \
    --project "$GCP_PROJECT" \
    --zone "$GCP_ZONE" \
    --cluster-version "$GKE_VERSION" \
    --machine-type "$PHASE3_SYSTEM_MACHINE_TYPE" \
    --num-nodes 1 \
    --disk-type "$PHASE3_NODE_DISK_TYPE" \
    --disk-size "$PHASE3_NODE_DISK_GB" \
    --image-type COS_CONTAINERD \
    --network "$network_name" \
    --subnetwork "$subnet_name" \
    --enable-ip-alias \
    --enable-private-nodes \
    --addons=HttpLoadBalancing=DISABLED,GcePersistentDiskCsiDriver=ENABLED \
    --master-ipv4-cidr 172.16.0.0/28 \
    --enable-master-authorized-networks \
    --master-authorized-networks "$runner_public_cidr" \
    --cluster-secondary-range-name "$pods_range_name" \
    --services-secondary-range-name "$services_range_name" \
    --enable-autorepair \
    --no-enable-autoupgrade \
    --no-enable-basic-auth \
    --logging SYSTEM \
    --monitoring SYSTEM \
    --node-labels "dev.ethquake.managed=true,dev.ethquake.phase=3,dev.ethquake.role=system" \
    --labels "dev-ethquake-managed=true,dev-ethquake-phase=3,dev-ethquake-session=$ETHQUAKE_SESSION_ID,dev-ethquake-purpose=$session_kind" \
    --quiet

for pool in ethquake-p1 ethquake-p2 ethquake-p3 ethquake-p4; do
    gcloud container node-pools create "$pool" \
        --cluster "$cluster_name" \
        --project "$GCP_PROJECT" \
        --zone "$GCP_ZONE" \
        --node-version "$GKE_VERSION" \
        --machine-type "$PHASE3_PARTICIPANT_MACHINE_TYPE" \
        --num-nodes 1 \
        --disk-type "$PHASE3_NODE_DISK_TYPE" \
        --disk-size "$PHASE3_NODE_DISK_GB" \
        --image-type COS_CONTAINERD \
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

if ! kubectl_retry \
    get storageclass "$PHASE3_STORAGE_CLASS" >/dev/null 2>&1; then
    die "Required GKE storage class is absent: $PHASE3_STORAGE_CLASS"
fi

verify_private_node_and_nat_configuration
run_nat_egress_gate

if [ "$session_kind" = evidence ]; then
"$helm_bin" upgrade --install chaos-mesh "$chaos_chart" \
    --kubeconfig "$kubeconfig" \
    --kube-context "$PHASE3_KUBERNETES_CONTEXT" \
    --namespace chaos-mesh \
    --create-namespace \
    --values "$chaos_values" \
    --atomic \
    --wait \
    --timeout 10m
kubectl_retry \
    --namespace chaos-mesh delete serviceaccount chaos-dashboard \
    --ignore-not-found >/dev/null
kubectl_retry \
    delete clusterrole \
    chaos-mesh-chaos-dashboard-cluster-level \
    chaos-mesh-chaos-dashboard-target-namespace \
    --ignore-not-found >/dev/null
kubectl_retry \
    delete clusterrolebinding \
    chaos-mesh-chaos-dashboard-cluster-level \
    chaos-mesh-chaos-dashboard-target-namespace \
    --ignore-not-found >/dev/null
if [ -n "$(kubectl_retry \
    get serviceaccount,clusterrole,clusterrolebinding \
    --all-namespaces --selector app.kubernetes.io/component=chaos-dashboard \
    --output=name 2>/dev/null)" ]; then
    die "Disabled Chaos Mesh dashboard left active RBAC resources"
fi
kubectl_retry \
    label namespace chaos-mesh \
    dev.ethquake.managed=true dev.ethquake.phase=3 --overwrite >/dev/null
pass "Chaos Mesh NetworkChaos runtime"
fi

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
    }],
    private_nodes: .privateClusterConfig.enablePrivateNodes,
    private_endpoint: (.privateClusterConfig.enablePrivateEndpoint // false)
  }' > "$artifact_root/session-metadata.json"
chmod 0600 "$artifact_root/session-metadata.json"

ETHQUAKE_START_ENGINE_IF_MISSING=true access gateway-start
access gateway-stop

stop_run_forwards() {
    access forward-stop beacon-api cl-1-lighthouse-geth || true
    access forward-stop beacon-api cl-2-teku-reth || true
    access forward-stop beacon-api cl-3-lighthouse-geth || true
    access forward-stop beacon-api cl-4-teku-reth || true
    access forward-stop execution-ws el-1-geth-lighthouse || true
    access forward-stop prometheus prometheus || true
    access forward-stop grafana grafana || true
}

run_qualification() {
    qualification_started_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
    current_enclave="ethquake-p3q-$ETHQUAKE_SESSION_ID"
    namespace="kt-$current_enclave"
    if access kurtosis enclave inspect "$current_enclave" >/dev/null 2>&1; then
        die "Qualification enclave already exists: $current_enclave"
    fi
    if kubectl_retry \
        get namespace "$namespace" >/dev/null 2>&1; then
        die "Qualification namespace already exists: $namespace"
    fi

    info "Starting non-evidence qualification workload"
    restart_gateway_with_fresh_credentials
    access kurtosis run \
        --enclave "$current_enclave" \
        /ethquake/ethereum-package \
        --args-file /ethquake/network_params.yaml
    restart_gateway_with_fresh_credentials
    kubectl_retry \
        label namespace "$namespace" \
        dev.ethquake.managed=true \
        dev.ethquake.phase=3 \
        dev.ethquake.purpose=qualification \
        dev.ethquake.evidence-eligible=false \
        --overwrite >/dev/null

    access forward-start beacon-api "$current_enclave" cl-1-lighthouse-geth 15052
    access forward-start beacon-api "$current_enclave" cl-2-teku-reth 15053
    access forward-start beacon-api "$current_enclave" cl-3-lighthouse-geth 15054
    access forward-start beacon-api "$current_enclave" cl-4-teku-reth 15055
    access forward-start prometheus "$current_enclave" prometheus 19090
    access forward-start grafana "$current_enclave" grafana 13000
    curl --fail --silent --show-error --max-time 10 \
        http://127.0.0.1:19090/-/ready >/dev/null
    curl --fail --silent --show-error --max-time 10 \
        http://127.0.0.1:13000/api/health >"$artifact_root/grafana-health.json"

    wait_for_qualification_workload "$namespace"
    assert_qualification_scheduling "$namespace"
    assert_qualification_health "$namespace" start
    capture_consensus_snapshot start
    capture_throttle_snapshot "$namespace" start

    info "Live UI: GCP Console namespace=$namespace; Grafana=http://127.0.0.1:13000"
    info "Observing head/finality and CPU throttling for $PHASE3_QUALIFICATION_OBSERVATION_SECONDS seconds"
    bounded_wait "$PHASE3_QUALIFICATION_OBSERVATION_SECONDS" "Qualification observation"

    refresh_session_kubeconfig
    capture_consensus_snapshot end
    capture_throttle_snapshot "$namespace" end
    assert_consensus_progress
    assert_qualification_health "$namespace" end
    assert_cpu_throttling

    maximum_throttle=$(awk -F '\t' 'BEGIN { max = 0 } $2 > max { max = $2 } END { printf "%.6f", max }' \
        "$artifact_root/cpu-throttling-ratios.tsv")
    jq -n \
        --arg started_at "$qualification_started_at" \
        --arg completed_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
        --arg session_id "$ETHQUAKE_SESSION_ID" \
        --arg namespace "$namespace" \
        --argjson max_cpu_throttle_ratio "$maximum_throttle" \
        --argjson throttle_limit "$PHASE3_QUALIFICATION_MAX_CPU_THROTTLE_RATIO" '
        {
          schema_version: "ethquake.qualification/v1alpha1",
          result: "PASS",
          evidence_eligible: false,
          started_at: $started_at,
          completed_at: $completed_at,
          session_id: $session_id,
          namespace: $namespace,
          topology: {
            total_vcpus: 12,
            private_nodes: true,
            public_cloud_nat: true,
            participant_request_mcpu: 1400
          },
          gates: {
            scheduling: "PASS",
            consensus_head_and_finality_progress: "PASS",
            oom_and_restarts: "PASS",
            node_pressure: "PASS",
            nat_egress: "PASS",
            cpu_throttling: {
              result: "PASS",
              maximum_ratio: $max_cpu_throttle_ratio,
              limit: $throttle_limit
            }
          }
        }
    ' >"$artifact_root/qualification.json"
    chmod 0600 "$artifact_root/qualification.json"
    pass "Qualification gates passed; result remains non-evidence"

    info "UI inspection window: $PHASE3_QUALIFICATION_UI_HOLD_SECONDS seconds before teardown"
    info "GCP: Kubernetes Engine > Workloads, namespace $namespace"
    info "GCP: Network Services > Cloud NAT > $nat_name"
    info "Local Grafana: http://127.0.0.1:13000"
    bounded_wait "$PHASE3_QUALIFICATION_UI_HOLD_SECONDS" "UI inspection window"

    stop_run_forwards
    restart_gateway_with_fresh_credentials
    access kurtosis enclave rm --force "$current_enclave"
    if ! kubectl_retry \
        wait --for=delete "namespace/$namespace" --timeout=180s >/dev/null 2>&1; then
        die "Qualification namespace remains after exact enclave cleanup: $namespace"
    fi
    current_enclave=
    pass "Qualification artifact bundle: $artifact_root"
    assert_global_context
}

if [ "$session_kind" = qualification ]; then
    run_qualification
    exit 0
fi

for run_id in $PHASE3_RUN_ORDER; do
    current_enclave="ethquake-phase3-$run_id"
    namespace="kt-$current_enclave"
    if access kurtosis enclave inspect "$current_enclave" >/dev/null 2>&1; then
        die "Run enclave already exists: $current_enclave"
    fi
    if kubectl_retry \
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

    kubectl_retry \
        label namespace "$namespace" \
        dev.ethquake.managed=true \
        dev.ethquake.phase=3 \
        "dev.ethquake.run-id=$run_id" \
        --overwrite >/dev/null
    kubectl_retry \
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
    if ! kubectl_retry \
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
pass "Phase 3 evidence bundle: $artifact_root"
assert_global_context
