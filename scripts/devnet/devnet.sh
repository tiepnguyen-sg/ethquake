#!/bin/sh

set -eu

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

require_command() {
    if ! command -v "$1" >/dev/null 2>&1; then
        die "Required command not found: $1"
    fi
}

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repository_root=$(CDPATH= cd -- "$script_dir/../.." && pwd)
access_script="$script_dir/access.sh"
versions_file="$repository_root/devnet/versions.env"
network_params="$repository_root/devnet/network_params.yaml"
finality_dashboard="$repository_root/devnet/grafana/ethquake-finality.json"
kubeconfig="$repository_root/.kurtosis/kubeconfig"
enclave=ethquake
namespace="kt-$enclave"
expected_context=kind-ethquake
container_network_params=/ethquake/network_params.yaml
lighthouse_service=cl-1-lighthouse-geth
teku_service=cl-2-teku-reth
prometheus_service=prometheus
grafana_service=grafana
lighthouse_port=15052
teku_port=15053
prometheus_port=19090
grafana_port=13000
finality_dashboard_uid=ethquake-finality
finality_dashboard_title='Ethquake Finality'
finality_query='beacon_finalized_epoch{client_type="beacon"}'

if [ ! -r "$versions_file" ]; then
    die "Cannot read version locks: $versions_file"
fi

# This tracked file contains reviewed assignments only.
. "$versions_file"

if [ -z "${ETHEREUM_PACKAGE_REVISION:-}" ]; then
    die "ethereum-package revision lock is missing"
fi
package_locator="github.com/ethpandaops/ethereum-package@$ETHEREUM_PACKAGE_REVISION"

for command_name in curl docker jq kubectl; do
    require_command "$command_name"
done

global_context() {
    kubectl config current-context 2>/dev/null || true
}

assert_global_context() {
    expected_global_context=$1
    if [ "$(global_context)" != "$expected_global_context" ]; then
        die "Global Kubernetes context changed unexpectedly"
    fi
}

assert_local_target() {
    if [ ! -r "$kubeconfig" ]; then
        die "Repository-local kubeconfig is missing: $kubeconfig"
    fi
    local_context=$(kubectl --kubeconfig="$kubeconfig" \
        config current-context 2>/dev/null || true)
    if [ "$local_context" != "$expected_context" ]; then
        die "Repository-local kubeconfig targets '$local_context'; expected '$expected_context'"
    fi
    if ! kubectl --kubeconfig="$kubeconfig" get node ethquake-control-plane \
        >/dev/null 2>&1; then
        die "Named Ethquake kind control-plane is unavailable"
    fi
    configured_network=$(awk '$1 == "network:" { print $2; exit }' \
        "$network_params")
    if [ "$configured_network" != "kurtosis" ]; then
        die "Devnet configuration must target the private Kurtosis network"
    fi
}

discover_service() {
    service_id=$1
    selector="kurtosistech.com/resource-type=user-service,kurtosistech.com/id=$service_id"
    resources=$(kubectl --kubeconfig="$kubeconfig" --namespace "$namespace" \
        get services --selector "$selector" --output name 2>/dev/null || true)
    resource_count=$(printf '%s\n' "$resources" |
        awk 'NF { count += 1 } END { print count + 0 }')
    if [ "$resource_count" -ne 1 ]; then
        die "Service discovery found $resource_count matches for $service_id"
    fi
    printf '%s\n' "$resources" | awk 'NF { print; exit }'
}

workload_label() {
    service_id=$1
    label_name=$2
    case "$label_name" in
        client-type)
            jsonpath='{.metadata.labels.kurtosistech\.com\.custom/ethereum-package\.client-type}'
            ;;
        client)
            jsonpath='{.metadata.labels.kurtosistech\.com\.custom/ethereum-package\.client}'
            ;;
        *)
            die "Unsupported service label: $label_name"
            ;;
    esac
    selector="kurtosistech.com/resource-type=user-service,kurtosistech.com/id=$service_id"
    label_values=$(kubectl --kubeconfig="$kubeconfig" --namespace "$namespace" \
        get pods --selector "$selector" \
        --output "jsonpath={range .items[*]}$jsonpath{\"\\n\"}{end}")
    label_count=$(printf '%s\n' "$label_values" |
        awk 'NF { count += 1 } END { print count + 0 }')
    if [ "$label_count" -ne 1 ]; then
        die "Workload discovery found $label_count matches for $service_id"
    fi
    printf '%s\n' "$label_values" | awk 'NF { print; exit }'
}

verify_client_service() {
    service_id=$1
    expected_type=$2
    expected_client=$3
    discover_service "$service_id" >/dev/null
    actual_type=$(workload_label "$service_id" client-type)
    actual_client=$(workload_label "$service_id" client)
    if [ "$actual_type" != "$expected_type" ] || \
        [ "$actual_client" != "$expected_client" ]; then
        die "Unexpected identity for $service_id: $actual_type/$actual_client"
    fi
    pass "Client service: $service_id ($actual_type/$actual_client)"
}

verify_topology() {
    if ! kubectl --kubeconfig="$kubeconfig" get namespace "$namespace" \
        >/dev/null 2>&1; then
        die "Ethquake enclave namespace is absent"
    fi

    verify_client_service el-1-geth-lighthouse execution geth
    verify_client_service el-2-reth-teku execution reth
    verify_client_service "$lighthouse_service" beacon lighthouse
    verify_client_service "$teku_service" beacon teku
    discover_service "$prometheus_service" >/dev/null
    discover_service "$grafana_service" >/dev/null
    pass "Observability services: Prometheus and Grafana"

    not_ready=$(kubectl --kubeconfig="$kubeconfig" --namespace "$namespace" \
        get pods --output json |
        jq '[.items[] | select(
            (.status.phase != "Succeeded") and
            (.status.phase != "Running" or
             ([.status.containerStatuses[]? | select(.ready == false)] | length) > 0)
        )] | length')
    if [ "$not_ready" -ne 0 ]; then
        die "$not_ready Ethquake enclave pod(s) are not ready"
    fi
    pass "Ethquake enclave pods are Running/Ready"
}

start_forwards() {
    "$access_script" forward-start \
        beacon-api "$enclave" "$lighthouse_service" "$lighthouse_port"
    "$access_script" forward-start \
        beacon-api "$enclave" "$teku_service" "$teku_port"
    "$access_script" forward-start \
        prometheus "$enclave" "$prometheus_service" "$prometheus_port"
    "$access_script" forward-start \
        grafana "$enclave" "$grafana_service" "$grafana_port"
}

beacon_head_slot() {
    port=$1
    curl --fail --silent --show-error --max-time 10 \
        "http://127.0.0.1:$port/eth/v1/beacon/headers/head" |
        jq -er '.data.header.message.slot | tonumber'
}

verify_beacon_finality() {
    port=$1
    client_name=$2
    response=$(curl --fail --silent --show-error --max-time 10 \
        "http://127.0.0.1:$port/eth/v1/beacon/states/head/finality_checkpoints")
    finalized_epoch=$(printf '%s\n' "$response" |
        jq -er '.data.finalized.epoch | tonumber')
    has_finalized_root=$(printf '%s\n' "$response" |
        jq -r '.data.finalized.root | test("^0x0+$") | not')
    if [ "$has_finalized_root" != "true" ]; then
        die "$client_name has no finalized checkpoint"
    fi
    pass "$client_name finalized epoch: $finalized_epoch"
}

verify_head_advances() {
    lighthouse_initial=$(beacon_head_slot "$lighthouse_port")
    teku_initial=$(beacon_head_slot "$teku_port")
    attempt=0
    while [ "$attempt" -lt 18 ]; do
        sleep 5
        lighthouse_current=$(beacon_head_slot "$lighthouse_port")
        teku_current=$(beacon_head_slot "$teku_port")
        if [ "$lighthouse_current" -gt "$lighthouse_initial" ] && \
            [ "$teku_current" -gt "$teku_initial" ]; then
            pass "Beacon heads advanced: Lighthouse $lighthouse_initial->$lighthouse_current; Teku $teku_initial->$teku_current"
            return
        fi
        attempt=$((attempt + 1))
    done
    die "Beacon heads did not advance within 90 seconds"
}

verify_observability() {
    curl --fail --silent --show-error --max-time 10 \
        "http://127.0.0.1:$prometheus_port/-/ready" >/dev/null
    pass "Prometheus readiness endpoint"

    targets=$(curl --fail --silent --show-error --max-time 10 \
        "http://127.0.0.1:$prometheus_port/api/v1/targets")
    execution_targets=$(printf '%s\n' "$targets" |
        jq '[.data.activeTargets[] |
             select(.health == "up" and .labels.client_type == "execution")] |
            length')
    beacon_targets=$(printf '%s\n' "$targets" |
        jq '[.data.activeTargets[] |
             select(.health == "up" and .labels.client_type == "beacon")] |
            length')
    if [ "$execution_targets" -lt 2 ] || [ "$beacon_targets" -lt 2 ]; then
        die "Prometheus lacks healthy targets for both EL and CL implementations"
    fi
    pass "Prometheus targets: $execution_targets execution; $beacon_targets beacon"

    grafana_health=$(curl --fail --silent --show-error --max-time 10 \
        "http://127.0.0.1:$grafana_port/api/health" | jq -er '.database')
    if [ "$grafana_health" != "ok" ]; then
        die "Grafana database health is not ok"
    fi
    pass "Grafana health endpoint"

    dashboard_response=$(curl --fail --silent --show-error --max-time 10 \
        "http://127.0.0.1:$grafana_port/api/dashboards/uid/$finality_dashboard_uid")
    if ! printf '%s\n' "$dashboard_response" | jq -e \
        --arg uid "$finality_dashboard_uid" \
        --arg title "$finality_dashboard_title" \
        --arg query "$finality_query" \
        '.dashboard.uid == $uid and
         .dashboard.title == $title and
         any(.dashboard.panels[]?.targets[]?; .expr == $query)' \
        >/dev/null; then
        die "Grafana finality dashboard contract is invalid"
    fi
    pass "Grafana dashboard: $finality_dashboard_title"

    advancing_clients=0
    finality_attempt=0
    while [ "$finality_attempt" -lt 12 ]; do
        range_end=$(date +%s)
        range_start=$((range_end - 900))
        finality_range=$(curl --fail --silent --show-error --max-time 10 \
            --get "http://127.0.0.1:$prometheus_port/api/v1/query_range" \
            --data-urlencode "query=$finality_query" \
            --data-urlencode "start=$range_start" \
            --data-urlencode "end=$range_end" \
            --data-urlencode 'step=15')
        advancing_clients=$(printf '%s\n' "$finality_range" | jq -er '
            [.data.result[]
             | select((.values | length) >= 2)
             | select(
                 (.values | map(.[1] | tonumber) | max) >
                 (.values | map(.[1] | tonumber) | min)
               )
             | .metric.client_name]
            | map(select(type == "string" and length > 0))
            | unique
            | length')
        if [ "$advancing_clients" -ge 2 ]; then
            break
        fi
        finality_attempt=$((finality_attempt + 1))
        if [ "$finality_attempt" -lt 12 ]; then
            sleep 5
        fi
    done
    if [ "$advancing_clients" -lt 2 ]; then
        die "Finality did not advance for two beacon clients within 60 seconds of metric polling"
    fi
    pass "Finality advanced in the dashboard range for $advancing_clients beacon clients"
}

provision_finality_dashboard() {
    if [ ! -r "$finality_dashboard" ]; then
        die "Cannot read Grafana dashboard: $finality_dashboard"
    fi
    if ! jq -e \
        --arg uid "$finality_dashboard_uid" \
        --arg title "$finality_dashboard_title" \
        '.id == null and .uid == $uid and .title == $title' \
        "$finality_dashboard" >/dev/null; then
        die "Tracked Grafana dashboard has an invalid identity"
    fi

    dashboard_import=$(jq -c \
        '{dashboard: ., overwrite: true, message: "Provisioned by Ethquake"}' \
        "$finality_dashboard" |
        curl --fail --silent --show-error --max-time 10 \
            --header 'Content-Type: application/json' \
            --data-binary @- \
            "http://127.0.0.1:$grafana_port/api/dashboards/db")
    imported_status=$(printf '%s\n' "$dashboard_import" | jq -er '.status')
    imported_uid=$(printf '%s\n' "$dashboard_import" | jq -er '.uid')
    if [ "$imported_status" != "success" ] || \
        [ "$imported_uid" != "$finality_dashboard_uid" ]; then
        die "Grafana finality dashboard import failed"
    fi
    pass "Provisioned Grafana dashboard: $finality_dashboard_title"
}

verify_devnet() {
    initial_global_context=$(global_context)
    assert_local_target
    "$access_script" gateway-start
    verify_topology
    start_forwards
    verify_beacon_finality "$lighthouse_port" Lighthouse
    verify_beacon_finality "$teku_port" Teku
    verify_head_advances
    verify_observability
    assert_global_context "$initial_global_context"
    pass "Ethquake devnet verification"
}

devnet_up() {
    initial_global_context=$(global_context)
    assert_local_target
    "$access_script" gateway-start

    if "$access_script" kurtosis enclave inspect "$enclave" \
        >/dev/null 2>&1; then
        info "Kurtosis enclave already exists: $enclave"
    else
        "$access_script" kurtosis run \
            --enclave "$enclave" \
            "$package_locator" \
            --args-file "$container_network_params"
    fi

    assert_global_context "$initial_global_context"
    "$access_script" forward-start \
        grafana "$enclave" "$grafana_service" "$grafana_port"
    provision_finality_dashboard
    verify_devnet
}

devnet_down() {
    initial_global_context=$(global_context)
    assert_local_target
    "$access_script" gateway-start

    namespace_remains=false
    if "$access_script" kurtosis enclave inspect "$enclave" \
        >/dev/null 2>&1; then
        "$access_script" kurtosis enclave rm --force "$enclave"
        attempt=0
        while kubectl --kubeconfig="$kubeconfig" get namespace "$namespace" \
            >/dev/null 2>&1 && [ "$attempt" -lt 90 ]; do
            attempt=$((attempt + 1))
            sleep 1
        done
        if kubectl --kubeconfig="$kubeconfig" get namespace "$namespace" \
            >/dev/null 2>&1; then
            namespace_remains=true
        else
            pass "Removed named Kurtosis enclave: $enclave"
        fi
    else
        info "Named Kurtosis enclave is already absent: $enclave"
    fi

    "$access_script" gateway-stop
    assert_global_context "$initial_global_context"
    if [ "$namespace_remains" = true ]; then
        die "Ethquake enclave namespace remains after teardown"
    fi
    pass "Ethquake devnet is down; kind cluster retained"
}

devnet_status() {
    assert_local_target
    if ! kubectl --kubeconfig="$kubeconfig" get namespace "$namespace" \
        >/dev/null 2>&1; then
        info "Ethquake devnet is absent"
        return
    fi
    kubectl --kubeconfig="$kubeconfig" --namespace "$namespace" get pods
    kubectl --kubeconfig="$kubeconfig" --namespace "$namespace" get services
}

usage() {
    printf '%s\n' 'Usage: devnet.sh up|down|status|verify'
}

command_name=${1:-}
case "$command_name" in
    up)
        devnet_up
        ;;
    down)
        devnet_down
        ;;
    status)
        devnet_status
        ;;
    verify)
        verify_devnet
        ;;
    *)
        usage >&2
        exit 1
        ;;
esac
