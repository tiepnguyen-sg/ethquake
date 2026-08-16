#!/bin/sh

set -eu

pass() {
    printf '[PASS] %s\n' "$1"
}

die() {
    printf '[FAIL] %s\n' "$1" >&2
    exit 1
}

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repository_root=$(CDPATH= cd -- "$script_dir/../.." && pwd)
access_script="$repository_root/scripts/devnet/access.sh"
go_binary=${GO:-go}

global_context() {
    kubectl config current-context 2>/dev/null || true
}

initial_global_context=$(global_context)

cleanup_best_effort() {
    "$access_script" gateway-stop >/dev/null 2>&1 || true
}

trap cleanup_best_effort EXIT INT TERM

"$access_script" gateway-start
"$access_script" forward-start beacon-api ethquake cl-1-lighthouse-geth 25052
"$access_script" forward-start beacon-api ethquake cl-2-teku-reth 25053
"$access_script" forward-start execution-ws ethquake el-1-geth-lighthouse 28546
"$access_script" forward-start execution-ws ethquake el-2-reth-teku 28547

ETHQUAKE_E2E_BEACON_ONE=lighthouse=http://127.0.0.1:25052 \
ETHQUAKE_E2E_BEACON_TWO=teku=http://127.0.0.1:25053 \
ETHQUAKE_E2E_EXECUTION_ONE=geth=ws://127.0.0.1:28546 \
ETHQUAKE_E2E_EXECUTION_TWO=reth=ws://127.0.0.1:28547 \
GOTOOLCHAIN=local "$go_binary" test -count=1 -tags=e2e \
    -run '^TestObserverAgainstLiveDevnet$' ./internal/cli

"$access_script" gateway-stop
trap - EXIT INT TERM

if [ "$(global_context)" != "$initial_global_context" ]; then
    die "Global Kubernetes context changed during Observer E2E test"
fi

for port in 9710 25052 25053 28546 28547; do
    if lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
        die "Listener remains on port $port after Observer E2E cleanup"
    fi
done

pass "Observer E2E devnet measurements and cleanup"
