#!/bin/sh

set -u

failure_count=0
preflight_tmp=
temporary_root=${TMPDIR:-/tmp}

case "$temporary_root" in
    /)
        ;;
    */)
        temporary_root=${temporary_root%/}
        ;;
esac

pass() {
    printf '[PASS] %s\n' "$1"
}

fail() {
    failure_count=$((failure_count + 1))
    printf '[FAIL] %s\n' "$1"
}

info() {
    printf '[INFO] %s\n' "$1"
}

check_sha256() {
    check_label=$1
    check_digest=$2

    case "$check_digest" in
        *[!0-9a-f]*|'')
            fail "$check_label: must be a lowercase SHA-256 digest"
            ;;
        *)
            if [ "${#check_digest}" -eq 64 ]; then
                pass "$check_label: checksum is locked"
            else
                fail "$check_label: digest must contain 64 hexadecimal characters"
            fi
            ;;
    esac
}

check_image_lock() {
    check_label=$1
    check_image=$2

    case "$check_image" in
        *@sha256:*)
            check_sha256 "$check_label" "${check_image##*@sha256:}"
            ;;
        *)
            fail "$check_label: image must use an immutable SHA-256 digest"
            ;;
    esac
}

cleanup() {
    case "$preflight_tmp" in
        "$temporary_root"/ethquake-preflight.*)
            if [ -d "$preflight_tmp" ]; then
                rm -rf -- "$preflight_tmp"
            fi
            ;;
    esac
}

trap cleanup EXIT HUP INT TERM

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repository_root=$(CDPATH= cd -- "$script_dir/../.." && pwd)
versions_file="$repository_root/devnet/versions.env"

if [ ! -r "$versions_file" ]; then
    fail "Version locks: cannot read $versions_file"
    printf '[FAIL] Preflight summary: %s prerequisite check failed\n' \
        "$failure_count"
    exit 1
fi

# This tracked file contains assignments only and is reviewed with the script.
. "$versions_file"

if [ -n "${KIND_VERSION:-}" ] && \
    [ -n "${KUBERNETES_VERSION:-}" ] && \
    [ -n "${KIND_NODE_IMAGE:-}" ] && \
    [ -n "${KURTOSIS_VERSION:-}" ] && \
    [ -n "${KURTOSIS_LINUX_AMD64_ARCHIVE_SHA256:-}" ] && \
    [ -n "${KURTOSIS_LINUX_ARM64_ARCHIVE_SHA256:-}" ] && \
    [ -n "${KURTOSIS_GATEWAY_BASE_IMAGE:-}" ] && \
    [ -n "${DEVNET_ACCESS_SMOKE_IMAGE:-}" ]; then
    pass "Version locks: required tool versions are set"
else
    fail "Version locks: one or more required tool versions are missing"
fi

check_sha256 "Kurtosis Linux amd64 archive" \
    "${KURTOSIS_LINUX_AMD64_ARCHIVE_SHA256:-}"
check_sha256 "Kurtosis Linux arm64 archive" \
    "${KURTOSIS_LINUX_ARM64_ARCHIVE_SHA256:-}"
check_image_lock "Kurtosis gateway base image" \
    "${KURTOSIS_GATEWAY_BASE_IMAGE:-}"
check_image_lock "Devnet access smoke image" \
    "${DEVNET_ACCESS_SMOKE_IMAGE:-}"

kind_node_prefix="kindest/node:v${KUBERNETES_VERSION}@sha256:"
case "${KIND_NODE_IMAGE:-}" in
    "$kind_node_prefix"*)
        kind_node_digest=${KIND_NODE_IMAGE#"$kind_node_prefix"}
        case "$kind_node_digest" in
            *[!0-9a-f]*|'')
                fail "kind node image lock: digest must be lowercase SHA-256"
                ;;
            *)
                if [ "${#kind_node_digest}" -eq 64 ]; then
                    pass "kind node image lock: $KIND_NODE_IMAGE"
                else
                    fail "kind node image lock: digest must contain 64 hexadecimal characters"
                fi
                ;;
        esac
        ;;
    *)
        fail "kind node image lock: image and Kubernetes versions do not match"
        ;;
esac

case "${ETHEREUM_PACKAGE_VERSION:-}:${ETHEREUM_PACKAGE_REVISION:-}" in
    :*)
        fail "ethereum-package lock: version is missing"
        ;;
    *:*)
        ethereum_revision=${ETHEREUM_PACKAGE_REVISION}
        case "$ethereum_revision" in
            *[!0-9a-f]*|'')
                fail "ethereum-package lock: revision must be a lowercase commit SHA"
                ;;
            *)
                if [ "${#ethereum_revision}" -eq 40 ]; then
                    pass "ethereum-package lock: ${ETHEREUM_PACKAGE_VERSION}@${ethereum_revision}"
                else
                    fail "ethereum-package lock: revision must contain 40 hexadecimal characters"
                fi
                ;;
        esac
        ;;
    *)
        fail "ethereum-package lock: invalid version or revision"
        ;;
esac

host_os=$(uname -s 2>/dev/null || true)
case "$host_os" in
    Darwin|Linux)
        pass "Host OS: $host_os"
        ;;
    '')
        fail "Host OS: could not determine operating system"
        ;;
    *)
        fail "Host OS: unsupported operating system $host_os"
        ;;
esac

host_arch=$(uname -m 2>/dev/null || true)
case "$host_arch" in
    arm64|aarch64|x86_64|amd64)
        pass "Host architecture: $host_arch"
        ;;
    '')
        fail "Host architecture: could not determine architecture"
        ;;
    *)
        fail "Host architecture: unsupported architecture $host_arch"
        ;;
esac

host_cpu_count=
host_memory_bytes=
case "$host_os" in
    Darwin)
        host_cpu_count=$(sysctl -n hw.logicalcpu 2>/dev/null || true)
        host_memory_bytes=$(sysctl -n hw.memsize 2>/dev/null || true)
        ;;
    Linux)
        host_cpu_count=$(getconf _NPROCESSORS_ONLN 2>/dev/null || true)
        host_memory_kib=$(awk '/^MemTotal:/ { print $2; exit }' \
            /proc/meminfo 2>/dev/null || true)
        if [ -n "$host_memory_kib" ]; then
            host_memory_bytes=$((host_memory_kib * 1024))
        fi
        ;;
esac

if [ -n "$host_cpu_count" ]; then
    info "Host CPU: $host_cpu_count logical CPUs"
else
    info "Host CPU: unavailable"
fi

case "$host_memory_bytes" in
    ''|*[!0-9]*)
        info "Host memory: unavailable"
        ;;
    *)
        host_memory_gib=$(awk "BEGIN { printf \"%.2f\", $host_memory_bytes / 1073741824 }")
        info "Host memory: $host_memory_gib GiB"
        ;;
esac

disk_available_kib=$(df -Pk "$repository_root" 2>/dev/null | \
    awk 'NR == 2 { print $4 }')
case "$disk_available_kib" in
    ''|*[!0-9]*)
        info "Repository disk availability: unavailable"
        ;;
    *)
        disk_available_gib=$(awk "BEGIN { printf \"%.2f\", $disk_available_kib / 1048576 }")
        info "Repository disk availability: $disk_available_gib GiB"
        ;;
esac

if command -v docker >/dev/null 2>&1; then
    docker_client_version=$(docker --version 2>/dev/null || true)
    if [ -n "$docker_client_version" ]; then
        pass "Docker CLI: $docker_client_version"
    else
        fail "Docker CLI: installed but version could not be read"
    fi

    if docker_server_version=$(docker version \
        --format '{{.Server.Version}}' 2>&1); then
        pass "Docker daemon: server $docker_server_version"

        docker_resources=$(docker info \
            --format '{{.NCPU}}|{{.MemTotal}}' 2>/dev/null || true)
        case "$docker_resources" in
            *'|'*)
                docker_cpu_count=${docker_resources%%|*}
                docker_memory_bytes=${docker_resources#*|}
                case "$docker_memory_bytes" in
                    ''|*[!0-9]*)
                        info "Docker resources: $docker_cpu_count CPUs; memory unavailable"
                        ;;
                    *)
                        docker_memory_gib=$(awk \
                            "BEGIN { printf \"%.2f\", $docker_memory_bytes / 1073741824 }")
                        info "Docker resources: $docker_cpu_count CPUs; $docker_memory_gib GiB memory"
                        ;;
                esac
                ;;
            *)
                info "Docker resources: unavailable"
                ;;
        esac
    else
        fail "Docker daemon: $docker_server_version"
    fi
else
    fail "Docker CLI: command not found"
fi

if command -v kubectl >/dev/null 2>&1; then
    if kubectl_output=$(kubectl version --client=true --output=json 2>&1); then
        kubectl_version=$(printf '%s\n' "$kubectl_output" | \
            awk -F'"' '/"gitVersion"/ { print $4; exit }')
        kubectl_core=${kubectl_version#v}
        kubectl_major=${kubectl_core%%.*}
        kubectl_tail=${kubectl_core#*.}
        kubectl_minor=${kubectl_tail%%.*}

        kubernetes_core=${KUBERNETES_VERSION#v}
        kubernetes_major=${kubernetes_core%%.*}
        kubernetes_tail=${kubernetes_core#*.}
        kubernetes_minor=${kubernetes_tail%%.*}

        case "$kubectl_major$kubectl_minor$kubernetes_major$kubernetes_minor" in
            ''|*[!0-9]*)
                fail "kubectl: could not parse client version"
                ;;
            *)
                minimum_minor=$((kubernetes_minor - 1))
                maximum_minor=$((kubernetes_minor + 1))
                if [ "$kubectl_major" -eq "$kubernetes_major" ] && \
                    [ "$kubectl_minor" -ge "$minimum_minor" ] && \
                    [ "$kubectl_minor" -le "$maximum_minor" ]; then
                    pass "kubectl: $kubectl_version is supported for Kubernetes v$KUBERNETES_VERSION"
                else
                    fail "kubectl: $kubectl_version is outside the supported skew for Kubernetes v$KUBERNETES_VERSION"
                fi
                ;;
        esac
    else
        fail "kubectl: version check failed: $kubectl_output"
    fi

    kubernetes_context=$(kubectl config current-context 2>/dev/null || true)
    if [ -n "$kubernetes_context" ]; then
        info "Kubernetes context: $kubernetes_context (not modified)"
    else
        info "Kubernetes context: not set (not modified)"
    fi
else
    fail "kubectl: command not found"
fi

if command -v kind >/dev/null 2>&1; then
    if kind_output=$(kind version 2>&1); then
        kind_actual=$(printf '%s\n' "$kind_output" | \
            awk 'NR == 1 { print $2 }')
        kind_actual=${kind_actual#v}
        if [ "$kind_actual" = "$KIND_VERSION" ]; then
            pass "kind: v$kind_actual"
        else
            fail "kind: found v$kind_actual; expected v$KIND_VERSION"
        fi
    else
        fail "kind: version check failed: $kind_output"
    fi
else
    fail "kind: command not found; expected v$KIND_VERSION"
fi

if command -v kurtosis >/dev/null 2>&1; then
    if preflight_tmp=$(mktemp -d "$temporary_root/ethquake-preflight.XXXXXX"); then
        if kurtosis_output=$(HOME="$preflight_tmp" \
            XDG_CONFIG_HOME="$preflight_tmp/config" \
            kurtosis version 2>&1); then
            kurtosis_actual=$(printf '%s\n' "$kurtosis_output" | \
                awk -F':' '/CLI Version/ {
                    gsub(/[[:space:]]/, "", $2)
                    print $2
                    exit
                }')
            if [ "$kurtosis_actual" = "$KURTOSIS_VERSION" ]; then
                pass "Kurtosis CLI: $kurtosis_actual"
            elif [ -z "$kurtosis_actual" ]; then
                fail "Kurtosis CLI: could not parse CLI version"
            else
                fail "Kurtosis CLI: found $kurtosis_actual; expected $KURTOSIS_VERSION"
            fi
        else
            fail "Kurtosis CLI: version check failed: $kurtosis_output"
        fi
    else
        fail "Kurtosis CLI: could not create an isolated config directory"
    fi
else
    fail "Kurtosis CLI: command not found; expected $KURTOSIS_VERSION"
fi

for access_command in curl jq tar lsof; do
    if command -v "$access_command" >/dev/null 2>&1; then
        pass "Local access dependency: $access_command"
    else
        fail "Local access dependency: $access_command command not found"
    fi
done

if command -v shasum >/dev/null 2>&1 || \
    command -v sha256sum >/dev/null 2>&1; then
    pass "Local access dependency: SHA-256 verifier"
else
    fail "Local access dependency: shasum or sha256sum command not found"
fi

if [ "$failure_count" -ne 0 ]; then
    printf '[FAIL] Preflight summary: %s prerequisite check(s) failed\n' \
        "$failure_count"
    exit 1
fi

printf '[PASS] Preflight summary: all prerequisite checks passed\n'
