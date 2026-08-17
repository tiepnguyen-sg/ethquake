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

require_command() {
    if ! command -v "$1" >/dev/null 2>&1; then
        die "Required command not found: $1"
    fi
}

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repository_root=$(CDPATH= cd -- "$script_dir/../.." && pwd)
versions_file="$repository_root/devnet/versions.env"
repository_kubeconfig=${ETHQUAKE_KUBECONFIG:-"$repository_root/.kurtosis/kubeconfig"}
network_params=${ETHQUAKE_NETWORK_PARAMS:-"$repository_root/devnet/network_params.yaml"}
access_root=${ETHQUAKE_ACCESS_ROOT:-"$repository_root/.kurtosis/access"}
cache_dir="$access_root/cache"
runtime_dir="$access_root/runtime"
runtime_home="$runtime_dir/home"
forwards_dir="$runtime_dir/forwards"
gateway_container=ethquake-kurtosis-access
gateway_network=ethquake-kurtosis-access
gateway_port=9710
expected_context=${ETHQUAKE_KUBERNETES_CONTEXT:-kind-ethquake}
storage_class=${ETHQUAKE_STORAGE_CLASS:-standard}
package_mount=${ETHQUAKE_PACKAGE_PATH:-}
start_engine_if_missing=${ETHQUAKE_START_ENGINE_IF_MISSING:-false}
managed_label=dev.ethquake.managed
component_label=dev.ethquake.component
context_label=dev.ethquake.context

case "$access_root" in
    "$repository_root"/.kurtosis/access|"$repository_root"/.cache/experiment/access)
        ;;
    *)
        die "Access root is outside the Ethquake-owned allowlist: $access_root"
        ;;
esac
case "$start_engine_if_missing" in
    true|false)
        ;;
    *)
        die "ETHQUAKE_START_ENGINE_IF_MISSING must be true or false"
        ;;
esac
case "$expected_context" in
    kind-ethquake|ethquake-aws-phase3)
        ;;
    *)
        die "Kubernetes context is outside the Ethquake access allowlist: $expected_context"
        ;;
esac
case "$storage_class" in
    standard)
        ;;
    *)
        die "Storage class is outside the Ethquake access allowlist: $storage_class"
        ;;
esac

if [ ! -r "$versions_file" ]; then
    die "Cannot read version locks: $versions_file"
fi

# This tracked file contains reviewed assignments only.
. "$versions_file"

if [ -z "${KURTOSIS_VERSION:-}" ] || \
    [ -z "${KURTOSIS_LINUX_AMD64_ARCHIVE_SHA256:-}" ] || \
    [ -z "${KURTOSIS_LINUX_ARM64_ARCHIVE_SHA256:-}" ] || \
    [ -z "${KURTOSIS_GATEWAY_BASE_IMAGE:-}" ]; then
    die "One or more Kurtosis gateway version locks are missing"
fi

require_command docker
require_command kubectl
require_command lsof

sha256_file() {
    if command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$1" | awk '{ print $1 }'
    elif command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{ print $1 }'
    else
        die "Neither shasum nor sha256sum is available"
    fi
}

global_context() {
    kubectl config current-context 2>/dev/null || true
}

assert_global_context() {
    expected_global_context=$1
    actual_global_context=$(global_context)
    if [ "$actual_global_context" != "$expected_global_context" ]; then
        die "Global Kubernetes context changed unexpectedly"
    fi
}

assert_managed_container() {
    container_label=$(docker inspect \
        --format "{{ index .Config.Labels \"$managed_label\" }}" \
        "$gateway_container" 2>/dev/null || true)
    component=$(docker inspect \
        --format "{{ index .Config.Labels \"$component_label\" }}" \
        "$gateway_container" 2>/dev/null || true)
    container_context=$(docker inspect \
        --format "{{ index .Config.Labels \"$context_label\" }}" \
        "$gateway_container" 2>/dev/null || true)
    if [ "$container_label" != "true" ] || [ "$component" != "gateway" ] || \
        [ "$container_context" != "$expected_context" ]; then
        die "Refusing to manage foreign container: $gateway_container"
    fi
}

assert_managed_network() {
    network_label=$(docker network inspect \
        --format "{{ index .Labels \"$managed_label\" }}" \
        "$gateway_network" 2>/dev/null || true)
    component=$(docker network inspect \
        --format "{{ index .Labels \"$component_label\" }}" \
        "$gateway_network" 2>/dev/null || true)
    if [ "$network_label" != "true" ] || [ "$component" != "gateway" ]; then
        die "Refusing to manage foreign network: $gateway_network"
    fi
}

assert_loopback_listener() {
    listener_port=$1
    listener_name=$2
    listener_output=$(lsof -nP -iTCP:"$listener_port" -sTCP:LISTEN 2>/dev/null || true)

    if [ -z "$listener_output" ]; then
        die "$listener_name has no listener on port $listener_port"
    fi

    listener_addresses=$(printf '%s\n' "$listener_output" |
        awk 'NR > 1 { print $(NF - 1) }')
    if [ -z "$listener_addresses" ]; then
        die "$listener_name listener address could not be parsed"
    fi

    unexpected_address=$(printf '%s\n' "$listener_addresses" |
        awk -v expected="127.0.0.1:$listener_port" '$0 != expected { print; exit }')
    if [ -n "$unexpected_address" ]; then
        die "$listener_name has a non-loopback listener: $unexpected_address"
    fi

    pass "$listener_name: 127.0.0.1:$listener_port only"
}

remove_runtime() {
    case "$runtime_dir" in
        "$repository_root"/.kurtosis/access/runtime|"$repository_root"/.cache/experiment/access/runtime)
            if [ -d "$runtime_dir" ]; then
                rm -rf -- "$runtime_dir"
            fi
            ;;
        *)
            die "Refusing to remove unexpected runtime path: $runtime_dir"
            ;;
    esac
}

prepare_kurtosis_binary() {
    require_command curl
    require_command tar

    host_arch=$(uname -m)
    case "$host_arch" in
        arm64|aarch64)
            artifact_arch=arm64
            expected_archive_sha=$KURTOSIS_LINUX_ARM64_ARCHIVE_SHA256
            ;;
        x86_64|amd64)
            artifact_arch=amd64
            expected_archive_sha=$KURTOSIS_LINUX_AMD64_ARCHIVE_SHA256
            ;;
        *)
            die "Unsupported host architecture for the gateway: $host_arch"
            ;;
    esac

    archive_name="kurtosis-cli_${KURTOSIS_VERSION}_linux_${artifact_arch}.tar.gz"
    archive_path="$cache_dir/$archive_name"
    archive_url="https://github.com/kurtosis-tech/kurtosis-cli-release-artifacts/releases/download/${KURTOSIS_VERSION}/${archive_name}"

    mkdir -p -- "$cache_dir" "$runtime_dir/bin"
    if [ ! -f "$archive_path" ]; then
        download_path="$cache_dir/.${archive_name}.download"
        rm -f -- "$download_path"
        info "Downloading official Kurtosis $KURTOSIS_VERSION Linux $artifact_arch archive"
        if ! curl --fail --location --proto '=https' --tlsv1.2 \
            --output "$download_path" "$archive_url"; then
            rm -f -- "$download_path"
            die "Kurtosis archive download failed"
        fi

        downloaded_sha=$(sha256_file "$download_path")
        if [ "$downloaded_sha" != "$expected_archive_sha" ]; then
            rm -f -- "$download_path"
            die "Kurtosis archive checksum mismatch"
        fi
        mv -- "$download_path" "$archive_path"
    fi

    archive_sha=$(sha256_file "$archive_path")
    if [ "$archive_sha" != "$expected_archive_sha" ]; then
        die "Cached Kurtosis archive checksum mismatch: $archive_path"
    fi
    pass "Kurtosis archive checksum: $archive_sha"

    if ! tar -tzf "$archive_path" |
        awk '$0 == "kurtosis" { found = 1 } END { exit !found }'; then
        die "Kurtosis archive does not contain the expected binary"
    fi
    tar -xzf "$archive_path" -C "$runtime_dir/bin" kurtosis
    chmod 0500 "$runtime_dir/bin/kurtosis"
}

prepare_runtime_home() {
    if [ ! -r "$repository_kubeconfig" ]; then
        die "Repository-local kubeconfig is missing: $repository_kubeconfig"
    fi
    if [ ! -r "$network_params" ]; then
        die "Devnet configuration is missing: $network_params"
    fi

    local_context=$(kubectl --kubeconfig="$repository_kubeconfig" \
        config current-context 2>/dev/null || true)
    if [ "$local_context" != "$expected_context" ]; then
        die "Repository-local kubeconfig targets '$local_context'; expected '$expected_context'"
    fi

    cluster_name=$(kubectl --kubeconfig="$repository_kubeconfig" \
        config view --minify -o jsonpath='{.clusters[0].name}')
    api_server=$(kubectl --kubeconfig="$repository_kubeconfig" \
        config view --minify -o jsonpath='{.clusters[0].cluster.server}')
    mkdir -p -- \
        "$runtime_home/.config/kurtosis" \
        "$runtime_home/.local/share/kurtosis" \
        "$runtime_home/.kube" \
        "$forwards_dir"
    cp -- "$repository_kubeconfig" "$runtime_home/.kube/config"
    case "$api_server" in
        https://127.0.0.1:*)
            api_port=${api_server##*:}
            case "$api_port" in
                ''|*[!0-9]*)
                    die "Could not parse the kind API server port"
                    ;;
            esac
            kubectl --kubeconfig="$runtime_home/.kube/config" \
                config set-cluster "$cluster_name" \
                --server="https://host.docker.internal:$api_port" \
                --tls-server-name=127.0.0.1 >/dev/null
            ;;
        https://*)
            ;;
        *)
            die "Repository-local kubeconfig has an unexpected API server: $api_server"
            ;;
    esac

    chmod 0400 "$runtime_home/.kube/config"

    {
        printf '%s\n' 'config-version: 9'
        printf '%s\n' 'should-send-metrics: false'
        printf '%s\n' 'kurtosis-clusters:'
        printf '%s\n' '  docker:'
        printf '%s\n' '    type: docker'
        printf '%s\n' '  ethquake:'
        printf '%s\n' '    type: kubernetes'
        printf '%s\n' '    config:'
        printf '      kubernetes-cluster-name: %s\n' "$cluster_name"
        printf '      storage-class: %s\n' "$storage_class"
    } > "$runtime_home/.config/kurtosis/kurtosis-config.yml"
    printf '%s' 'ethquake' > \
        "$runtime_home/.local/share/kurtosis/cluster-setting"

    machine_id=$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n')
    case "$machine_id" in
        *[!0-9a-f]*|'')
            die "Could not generate an isolated container machine ID"
            ;;
    esac
    printf '%s\n' "$machine_id" > "$runtime_dir/machine-id"
    chmod 0400 "$runtime_dir/machine-id"

    if [ -n "$package_mount" ]; then
        package_mount=$(CDPATH= cd -- "$package_mount" && pwd)
        case "$package_mount" in
            "$repository_root"/.cache/experiment/dependencies/ethereum-package)
                ;;
            *)
                die "Package mount is outside the prepared dependency path"
                ;;
        esac
    else
        package_mount="$runtime_dir/empty-package"
        mkdir -p -- "$package_mount"
    fi
}

gateway_is_running() {
    [ "$(docker inspect --format '{{.State.Running}}' \
        "$gateway_container" 2>/dev/null || true)" = "true" ]
}

engine_is_running() {
    engine_status=$(docker exec "$gateway_container" \
        /usr/local/bin/kurtosis engine status 2>/dev/null || true)
    case "$engine_status" in
        'A Kurtosis engine is running'*)
            return 0
            ;;
        *)
            return 1
            ;;
    esac
}

gateway_start() {
    initial_global_context=$(global_context)

    if docker container inspect "$gateway_container" >/dev/null 2>&1; then
        assert_managed_container
        if gateway_is_running; then
            published_port=$(docker port "$gateway_container" \
                "$gateway_port/tcp" 2>/dev/null || true)
            if [ "$published_port" != "127.0.0.1:$gateway_port" ]; then
                die "Gateway host publication is unsafe: $published_port"
            fi
            selected_cluster=$(docker exec "$gateway_container" \
                /usr/local/bin/kurtosis cluster get 2>/dev/null |
                awk 'NF { value = $0 } END { print value }')
            if [ "$selected_cluster" != "ethquake" ]; then
                die "Existing gateway does not select the Ethquake Kubernetes backend"
            fi
            if ! engine_is_running; then
                if [ "$start_engine_if_missing" != true ] || \
                    ! docker exec "$gateway_container" /usr/bin/timeout 300 \
                        /usr/local/bin/kurtosis engine start; then
                    die "Existing gateway does not manage the Ethquake Kubernetes engine"
                fi
                if ! engine_is_running; then
                    die "Kurtosis engine bootstrap returned without a running engine"
                fi
            fi
            assert_loopback_listener "$gateway_port" "Kurtosis gateway"
            assert_global_context "$initial_global_context"
            pass "Kurtosis access gateway is already running"
            return
        fi
        docker container rm "$gateway_container" >/dev/null
    fi

    if lsof -nP -iTCP:"$gateway_port" -sTCP:LISTEN >/dev/null 2>&1; then
        die "Host port $gateway_port is already in use"
    fi

    if docker network inspect "$gateway_network" >/dev/null 2>&1; then
        assert_managed_network
    else
        docker network create \
            --label "$managed_label=true" \
            --label "$component_label=gateway" \
            "$gateway_network" >/dev/null
    fi

    remove_runtime
    prepare_kurtosis_binary
    prepare_runtime_home

    if ! docker run --detach \
        --name "$gateway_container" \
        --hostname "$gateway_container" \
        --label "$managed_label=true" \
        --label "$component_label=gateway" \
        --label "$context_label=$expected_context" \
        --network "$gateway_network" \
        --add-host host.docker.internal:host-gateway \
        --publish "127.0.0.1:$gateway_port:$gateway_port/tcp" \
        --user "$(id -u):$(id -g)" \
        --read-only \
        --cap-drop ALL \
        --security-opt no-new-privileges \
        --pids-limit 256 \
        --memory 512m \
        --cpus 1 \
        --tmpfs /tmp:rw,noexec,nosuid,nodev,size=64m \
        --tmpfs /run:rw,noexec,nosuid,nodev,size=8m \
        --env HOME=/home/kurtosis \
        --env XDG_CONFIG_HOME=/home/kurtosis/.config \
        --env XDG_DATA_HOME=/home/kurtosis/.local/share \
        --mount "type=bind,src=$runtime_home,dst=/home/kurtosis" \
        --mount "type=bind,src=$runtime_home/.kube/config,dst=/home/kurtosis/.kube/config,readonly" \
        --mount "type=bind,src=$runtime_dir/bin/kurtosis,dst=/usr/local/bin/kurtosis,readonly" \
        --mount "type=bind,src=$runtime_dir/machine-id,dst=/etc/machine-id,readonly" \
        --mount "type=bind,src=$network_params,dst=/ethquake/network_params.yaml,readonly" \
        --mount "type=bind,src=$package_mount,dst=/ethquake/ethereum-package,readonly" \
        "$KURTOSIS_GATEWAY_BASE_IMAGE" \
        /usr/local/bin/kurtosis gateway >/dev/null; then
        gateway_stop || true
        die "Kurtosis access gateway container failed to start"
    fi

    attempt=0
    while [ "$attempt" -lt 15 ]; do
        if gateway_is_running && \
            lsof -nP -iTCP:"$gateway_port" -sTCP:LISTEN >/dev/null 2>&1; then
            break
        fi
        attempt=$((attempt + 1))
        sleep 1
    done
    if [ "$attempt" -ge 15 ]; then
        docker logs "$gateway_container" >&2 || true
        gateway_stop || true
        die "Kurtosis gateway control listener did not become ready"
    fi

    if ! engine_is_running && \
        [ "$start_engine_if_missing" = true ]; then
        info "Starting the Kurtosis Kubernetes engine through the constrained gateway"
        if ! docker exec "$gateway_container" /usr/bin/timeout 300 \
            /usr/local/bin/kurtosis engine start; then
            gateway_stop || true
            die "Kurtosis Kubernetes engine bootstrap failed"
        fi
    fi

    attempt=0
    while [ "$attempt" -lt 30 ]; do
        if gateway_is_running && engine_is_running; then
            break
        fi
        attempt=$((attempt + 1))
        sleep 1
    done
    if [ "$attempt" -ge 30 ]; then
        docker logs "$gateway_container" >&2 || true
        gateway_stop || true
        die "Kurtosis gateway did not become ready"
    fi

    selected_cluster=$(docker exec "$gateway_container" \
        /usr/local/bin/kurtosis cluster get 2>/dev/null |
        awk 'NF { value = $0 } END { print value }')
    if [ "$selected_cluster" != "ethquake" ]; then
        gateway_stop || true
        die "Container selected Kurtosis backend '$selected_cluster'; expected 'ethquake'"
    fi

    published_port=$(docker port "$gateway_container" "$gateway_port/tcp")
    if [ "$published_port" != "127.0.0.1:$gateway_port" ]; then
        gateway_stop || true
        die "Gateway host publication is unsafe: $published_port"
    fi
    assert_loopback_listener "$gateway_port" "Kurtosis gateway"
    assert_global_context "$initial_global_context"
    pass "Kurtosis backend: ethquake"
    pass "Kurtosis gateway container is ready"
}

stop_forward_pid_file() {
    pid_file=$1
    forward_pid=$(sed -n '1p' "$pid_file" 2>/dev/null || true)
    case "$forward_pid" in
        ''|*[!0-9]*)
            die "Invalid forward PID file: $pid_file"
            ;;
    esac

    if kill -0 "$forward_pid" 2>/dev/null; then
        forward_command=$(ps -p "$forward_pid" -o command= 2>/dev/null || true)
        case "$forward_command" in
            *"kubectl --kubeconfig=$repository_kubeconfig"*"port-forward --address=127.0.0.1"*)
                ;;
            *)
                die "Refusing to stop PID $forward_pid because ownership could not be verified"
                ;;
        esac

        kill "$forward_pid"
        stop_attempt=0
        while kill -0 "$forward_pid" 2>/dev/null && [ "$stop_attempt" -lt 10 ]; do
            stop_attempt=$((stop_attempt + 1))
            sleep 1
        done
        if kill -0 "$forward_pid" 2>/dev/null; then
            kill -KILL "$forward_pid"
        fi
    fi

    forward_prefix=${pid_file%.pid}
    rm -f -- "$pid_file" "$forward_prefix.meta" "$forward_prefix.log"
}

forward_stop_all() {
    if [ ! -d "$forwards_dir" ]; then
        return
    fi

    find "$forwards_dir" -type f -name '*.pid' -print |
        while IFS= read -r pid_file; do
            stop_forward_pid_file "$pid_file"
        done
}

gateway_stop() {
    initial_global_context=$(global_context)
    forward_stop_all

    if docker container inspect "$gateway_container" >/dev/null 2>&1; then
        assert_managed_container
        docker container rm --force "$gateway_container" >/dev/null
    fi
    if docker network inspect "$gateway_network" >/dev/null 2>&1; then
        assert_managed_network
        docker network rm "$gateway_network" >/dev/null
    fi

    remove_runtime
    if lsof -nP -iTCP:"$gateway_port" -sTCP:LISTEN >/dev/null 2>&1; then
        die "A listener remains on gateway port $gateway_port after cleanup"
    fi
    assert_global_context "$initial_global_context"
    pass "Kurtosis access gateway and owned forwards are stopped"
}

validate_endpoint() {
    endpoint=$1
    service_id=$2

    case "$endpoint" in
        beacon-api)
            case "$service_id" in
                cl-[a-z0-9-]*)
                    ;;
                *)
                    die "Beacon API service ID must start with 'cl-'"
                    ;;
            esac
            endpoint_port_name=http
            expected_client_type=beacon
            ;;
        execution-ws)
            case "$service_id" in
                el-[a-z0-9-]*)
                    ;;
                *)
                    die "Execution WebSocket service ID must start with 'el-'"
                    ;;
            esac
            endpoint_port_name=ws
            expected_client_type=execution
            ;;
        prometheus)
            if [ "$service_id" != "prometheus" ]; then
                die "Prometheus endpoint requires service ID 'prometheus'"
            fi
            endpoint_port_name=http
            expected_client_type=
            ;;
        grafana)
            if [ "$service_id" != "grafana" ]; then
                die "Grafana endpoint requires service ID 'grafana'"
            fi
            endpoint_port_name=http
            expected_client_type=
            ;;
        *)
            die "Endpoint is not allowlisted: $endpoint"
            ;;
    esac
}

forward_start() {
    if [ "$#" -ne 4 ]; then
        die "Usage: access.sh forward-start ENDPOINT ENCLAVE SERVICE_ID LOCAL_PORT"
    fi
    endpoint=$1
    enclave=$2
    service_id=$3
    local_port=$4

    validate_endpoint "$endpoint" "$service_id"
    case "$enclave" in
        ''|*[!a-z0-9-]*)
            die "Enclave name must use lowercase DNS-safe characters"
            ;;
    esac
    case "$service_id" in
        ''|*[!a-z0-9-]*)
            die "Service ID must use lowercase DNS-safe characters"
            ;;
    esac
    case "$local_port" in
        ''|*[!0-9]*)
            die "Local port must be numeric"
            ;;
    esac
    if [ "$local_port" -lt 1024 ] || [ "$local_port" -gt 65535 ]; then
        die "Local port must be between 1024 and 65535"
    fi
    if [ ! -r "$repository_kubeconfig" ]; then
        die "Repository-local kubeconfig is missing"
    fi
    if [ "$(kubectl --kubeconfig="$repository_kubeconfig" config current-context)" != \
        "$expected_context" ]; then
        die "Repository-local kubeconfig does not target $expected_context"
    fi

    initial_global_context=$(global_context)
    namespace="kt-$enclave"
    selector="kurtosistech.com/resource-type=user-service,kurtosistech.com/id=$service_id"
    service_resources=$(kubectl --kubeconfig="$repository_kubeconfig" \
        --namespace "$namespace" get services --selector "$selector" \
        --output name 2>/dev/null || true)
    service_count=$(printf '%s\n' "$service_resources" |
        awk 'NF { count += 1 } END { print count + 0 }')
    if [ "$service_count" -ne 1 ]; then
        die "Service discovery found $service_count matches for $namespace and $selector"
    fi
    service_resource=$(printf '%s\n' "$service_resources" | awk 'NF { print; exit }')
    service_name=${service_resource#service/}

    if [ -n "$expected_client_type" ]; then
        client_types=$(kubectl --kubeconfig="$repository_kubeconfig" \
            --namespace "$namespace" get pods --selector "$selector" \
            --output jsonpath='{range .items[*]}{.metadata.labels.kurtosistech\.com\.custom/ethereum-package\.client-type}{"\n"}{end}')
        matching_workloads=$(printf '%s\n' "$client_types" |
            awk -v expected="$expected_client_type" \
                '$0 == expected { count += 1 } END { print count + 0 }')
        workload_count=$(printf '%s\n' "$client_types" |
            awk 'NF { count += 1 } END { print count + 0 }')
        if [ "$workload_count" -ne 1 ] || [ "$matching_workloads" -ne 1 ]; then
            die "Service $service_name is not labeled as an Ethereum $expected_client_type client"
        fi
    fi

    matching_ports=$(kubectl --kubeconfig="$repository_kubeconfig" \
        --namespace "$namespace" get "$service_resource" \
        --output jsonpath='{range .spec.ports[*]}{.name}{"\n"}{end}' |
        awk -v expected="$endpoint_port_name" \
            '$0 == expected { count += 1 } END { print count + 0 }')
    if [ "$matching_ports" -ne 1 ]; then
        die "Service $service_name does not expose exactly one '$endpoint_port_name' port"
    fi

    mkdir -p -- "$forwards_dir"
    forward_prefix="$forwards_dir/${endpoint}--${service_id}"
    pid_file="$forward_prefix.pid"
    if [ -f "$pid_file" ]; then
        existing_pid=$(sed -n '1p' "$pid_file" 2>/dev/null || true)
        if [ -n "$existing_pid" ] && kill -0 "$existing_pid" 2>/dev/null; then
            if grep -Fx "endpoint=$endpoint" "$forward_prefix.meta" >/dev/null && \
                grep -Fx "enclave=$enclave" "$forward_prefix.meta" >/dev/null && \
                grep -Fx "service=$service_name" "$forward_prefix.meta" >/dev/null && \
                grep -Fx "local_port=$local_port" "$forward_prefix.meta" >/dev/null; then
                assert_loopback_listener "$local_port" "$endpoint forward"
                assert_global_context "$initial_global_context"
                pass "Forward already running: $endpoint/$service_id"
                return
            fi
            die "Existing forward ownership does not match the requested endpoint"
        fi
        rm -f -- "$pid_file" "$forward_prefix.meta" "$forward_prefix.log"
    fi
    if lsof -nP -iTCP:"$local_port" -sTCP:LISTEN >/dev/null 2>&1; then
        die "Local port $local_port is already in use"
    fi

    kubectl --kubeconfig="$repository_kubeconfig" \
        --namespace "$namespace" port-forward \
        --address=127.0.0.1 "$service_resource" \
        "$local_port:$endpoint_port_name" \
        >"$forward_prefix.log" 2>&1 &
    forward_pid=$!
    printf '%s\n' "$forward_pid" > "$pid_file"
    {
        printf 'endpoint=%s\n' "$endpoint"
        printf 'enclave=%s\n' "$enclave"
        printf 'namespace=%s\n' "$namespace"
        printf 'service=%s\n' "$service_name"
        printf 'local_port=%s\n' "$local_port"
    } > "$forward_prefix.meta"

    attempt=0
    while [ "$attempt" -lt 20 ]; do
        if ! kill -0 "$forward_pid" 2>/dev/null; then
            cat "$forward_prefix.log" >&2
            rm -f -- "$pid_file" "$forward_prefix.meta"
            die "Port-forward exited before becoming ready"
        fi
        if lsof -nP -iTCP:"$local_port" -sTCP:LISTEN >/dev/null 2>&1; then
            break
        fi
        attempt=$((attempt + 1))
        sleep 1
    done
    if [ "$attempt" -ge 20 ]; then
        stop_forward_pid_file "$pid_file"
        die "Port-forward did not become ready"
    fi

    assert_loopback_listener "$local_port" "$endpoint forward"
    assert_global_context "$initial_global_context"
    pass "Service discovery: $namespace/$service_name"
    pass "$endpoint forward PID: $forward_pid"
}

forward_stop() {
    if [ "$#" -ne 2 ]; then
        die "Usage: access.sh forward-stop ENDPOINT SERVICE_ID"
    fi
    endpoint=$1
    service_id=$2
    validate_endpoint "$endpoint" "$service_id"
    initial_global_context=$(global_context)
    pid_file="$forwards_dir/${endpoint}--${service_id}.pid"
    if [ ! -f "$pid_file" ]; then
        info "No owned forward exists for $endpoint/$service_id"
        return
    fi
    stop_forward_pid_file "$pid_file"
    assert_global_context "$initial_global_context"
    pass "Stopped forward: $endpoint/$service_id"
}

kurtosis_exec() {
    if [ "$#" -eq 0 ]; then
        die "Usage: access.sh kurtosis COMMAND [ARGUMENTS...]"
    fi
    if ! gateway_is_running; then
        die "Kurtosis access gateway is not running"
    fi
    assert_managed_container
    docker exec "$gateway_container" /usr/local/bin/kurtosis "$@"
}

status() {
    if gateway_is_running; then
        assert_managed_container
        published_port=$(docker port "$gateway_container" "$gateway_port/tcp")
        if [ "$published_port" != "127.0.0.1:$gateway_port" ]; then
            die "Gateway host publication is unsafe: $published_port"
        fi
        assert_loopback_listener "$gateway_port" "Kurtosis gateway"
        pass "Kurtosis access gateway is running"
    else
        info "Kurtosis access gateway is stopped"
    fi

    if [ -d "$forwards_dir" ]; then
        find "$forwards_dir" -type f -name '*.meta' -print |
            while IFS= read -r metadata_file; do
                info "Owned forward: $(tr '\n' ' ' < "$metadata_file")"
            done
    fi
}

usage() {
    printf '%s\n' \
        'Usage: access.sh gateway-start' \
        '       access.sh gateway-stop' \
        '       access.sh status' \
        '       access.sh kurtosis COMMAND [ARGUMENTS...]' \
        '       access.sh forward-start ENDPOINT ENCLAVE SERVICE_ID LOCAL_PORT' \
        '       access.sh forward-stop ENDPOINT SERVICE_ID' \
        '' \
        'Allowlisted endpoints: beacon-api, execution-ws, prometheus, grafana'
}

command_name=${1:-}
if [ -z "$command_name" ]; then
    usage >&2
    exit 1
fi
shift

case "$command_name" in
    gateway-start)
        gateway_start "$@"
        ;;
    gateway-stop)
        gateway_stop "$@"
        ;;
    status)
        status "$@"
        ;;
    kurtosis)
        kurtosis_exec "$@"
        ;;
    forward-start)
        forward_start "$@"
        ;;
    forward-stop)
        forward_stop "$@"
        ;;
    *)
        usage >&2
        exit 1
        ;;
esac
