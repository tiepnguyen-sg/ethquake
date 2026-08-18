#!/bin/sh

set -eu

umask 077

die() {
    printf '[FAIL] %s\n' "$1" >&2
    exit 1
}

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
test_root=$(mktemp -d "${TMPDIR:-/tmp}/ethquake-phase3-test.XXXXXX")
trap 'rm -rf "$test_root"' EXIT HUP INT TERM
fake_bin="$test_root/bin"
cloud_marker="$test_root/cloud-api-called"
first_output="$test_root/first.out"
second_output="$test_root/second.out"
qualification_output="$test_root/qualification.out"
mkdir -p "$fake_bin"

for cloud_command in gcloud curl; do
    command_path="$fake_bin/$cloud_command"
    {
        printf '#!/bin/sh\n'
        printf '%s\n' ': >"${ETHQUAKE_CLOUD_MARKER:?}"'
        printf 'exit 97\n'
    } >"$command_path"
    chmod 700 "$command_path"
done

PATH="$fake_bin:$PATH" ETHQUAKE_CLOUD_MARKER="$cloud_marker" \
    ETHQUAKE_SESSION_ID=local-check \
    "$script_dir/phase3.sh" dry-run >"$first_output"
PATH="$fake_bin:$PATH" ETHQUAKE_CLOUD_MARKER="$cloud_marker" \
    ETHQUAKE_SESSION_ID=local-check \
    "$script_dir/phase3.sh" dry-run >"$second_output"
PATH="$fake_bin:$PATH" ETHQUAKE_CLOUD_MARKER="$cloud_marker" \
    ETHQUAKE_SESSION_ID=local-check \
    "$script_dir/phase3.sh" qualification-dry-run >"$qualification_output"

if [ -e "$cloud_marker" ]; then
    die "dry-run called a cloud CLI"
fi
if ! cmp -s "$first_output" "$second_output"; then
    die "dry-run output is not deterministic"
fi
for expected in \
    '[PLAN] provider=gcp' \
    '[PLAN] session_kind=evidence' \
    '[PLAN] participants=4' \
    '[PLAN] required_preemptible_vcpus=0' \
    '[PLAN] required_on_demand_vcpus=12' \
    '[PLAN] required_total_vcpus=12' \
    '[PLAN] participant_machine_type=n2-custom-2-16384' \
    '[PLAN] participant_request_mcpu=1500' \
    '[PLAN] node_disk=pd-balanced:40GiB' \
    '[PLAN] workload_ssd_reserve_gb=50' \
    '[PLAN] private_nodes=true' \
    '[PLAN] cloud_nat=true' \
    '[PLAN] http_load_balancing=false' \
    '[PLAN] control_plane_authorization=runner-public-ip/32' \
    '[PLAN] region=northamerica-northeast2' \
    '[PLAN] zone=northamerica-northeast2-a' \
    '[PLAN] cluster=ethquake-p3-local-check' \
    '[PLAN] network=ethquake-p3-local-check-net' \
    '[PLAN] subnet=ethquake-p3-local-check-subnet' \
    '[PLAN] router=ethquake-p3-local-check-router' \
    '[PLAN] nat=ethquake-p3-local-check-nat' \
    '[PLAN] cloud_api_calls=false' \
    '[PLAN] resource_creation=false'; do
    if ! grep -F "$expected" "$first_output" >/dev/null; then
        die "dry-run output is missing: $expected"
    fi
done
for expected in \
    '[PLAN] session_kind=qualification' \
    '[PLAN] required_total_vcpus=12' \
    '[PLAN] private_nodes=true' \
    '[PLAN] cloud_nat=true' \
    '[PLAN] resource_creation=false'; do
    if ! grep -F "$expected" "$qualification_output" >/dev/null; then
        die "qualification dry-run output is missing: $expected"
    fi
done

if PATH="$fake_bin:$PATH" ETHQUAKE_CLOUD_MARKER="$cloud_marker" \
    ETHQUAKE_SESSION_ID='INVALID' \
    "$script_dir/phase3.sh" dry-run >"$test_root/invalid.out" 2>&1; then
    die "dry-run accepted an invalid session ID"
fi
if PATH="$fake_bin:$PATH" ETHQUAKE_CLOUD_MARKER="$cloud_marker" \
    ETHQUAKE_SESSION_ID=local-check \
    "$script_dir/phase3.sh" run \
    >"$test_root/run.out" 2>&1; then
    die "real GCP run was not blocked"
fi
if ! grep -F 'GCP project, billing, budget, recipient, and exact GKE version are required' "$test_root/run.out" >/dev/null; then
    die "real-run refusal did not identify the missing GCP inputs"
fi
if [ -e "$cloud_marker" ]; then
    die "blocked run called a cloud CLI"
fi
if PATH="$fake_bin:$PATH" ETHQUAKE_CLOUD_MARKER="$cloud_marker" \
    ETHQUAKE_SESSION_ID=local-check \
    "$script_dir/phase3.sh" qualify \
    >"$test_root/qualify.out" 2>&1; then
    die "real GCP qualification was not blocked"
fi
if ! grep -F 'GCP project, billing, budget, recipient, and exact GKE version are required' \
    "$test_root/qualify.out" >/dev/null; then
    die "qualification refusal did not identify the missing GCP inputs"
fi
if [ -e "$cloud_marker" ]; then
    die "blocked qualification called a cloud CLI"
fi
if ! grep -F 'KUBECONFIG="$kubeconfig" gcloud container clusters create' \
    "$script_dir/phase3.sh" >/dev/null; then
    die "cluster creation is not isolated to the session-local kubeconfig"
fi
if ! grep -F -- '--master-authorized-networks "$runner_public_cidr"' \
    "$script_dir/phase3.sh" >/dev/null; then
    die "cluster creation does not restrict the public control plane to the runner /32"
fi
if ! grep -F -- '--addons=HttpLoadBalancing=DISABLED,GcePersistentDiskCsiDriver=ENABLED' \
    "$script_dir/phase3.sh" >/dev/null; then
    die "cluster creation leaves the unused GKE HTTP load-balancing addon enabled"
fi

nodes_fixture="$test_root/participant-nodes.json"
pods_fixture="$test_root/pods-scheduled.json"
jq -n '{items: [range(1; 5) as $index | {
  metadata: {
    name: ("node-" + ($index | tostring)),
    labels: {"dev.ethquake.participant": ("ethquake-p" + ($index | tostring))}
  }
}]}' >"$nodes_fixture"
jq -n '{items: [range(1; 5) as $index | range(0; 3) | {
  spec: {nodeName: ("node-" + ($index | tostring))},
  status: {phase: "Running"}
}]}' >"$pods_fixture"
if ! jq -s -e -f "$script_dir/qualification-scheduling.jq" \
    "$nodes_fixture" "$pods_fixture" >/dev/null; then
    die "qualification scheduling filter rejected valid EL/CL/VC placement"
fi
jq '.items |= .[:-1]' "$pods_fixture" >"$test_root/pods-incomplete.json"
if jq -s -e -f "$script_dir/qualification-scheduling.jq" \
    "$nodes_fixture" "$test_root/pods-incomplete.json" >/dev/null; then
    die "qualification scheduling filter accepted an incomplete participant placement"
fi

raw_kubectl_call_sites=$(grep -c 'kubectl --kubeconfig "\$kubeconfig" --context "\$PHASE3_KUBERNETES_CONTEXT"' \
    "$script_dir/phase3.sh")
if [ "$raw_kubectl_call_sites" -ne 2 ]; then
    die "every live-cluster kubectl call must go through kubectl_retry, except its own definition"
fi
if ! grep -F 'kubectl_retry() {' "$script_dir/phase3.sh" >/dev/null; then
    die "kubectl_retry retry-with-reauthorization wrapper is missing"
fi
if ! grep -F 'refresh_runner_authorization_if_changed() {' "$script_dir/phase3.sh" >/dev/null; then
    die "runner public IP re-authorization helper is missing"
fi
if ! grep -F 'run_log="$artifact_root/run.log"' "$script_dir/phase3.sh" >/dev/null; then
    die "session run log capture is missing"
fi

printf '[PASS] Phase 3 GCP dry-run is deterministic and cloud-free\n'
printf '[PASS] Phase 3 GCP qualification dry-run is cloud-free\n'
printf '[PASS] Phase 3 GKE credential writes are session-local\n'
printf '[PASS] Phase 3 GKE public control plane is restricted to the runner /32\n'
printf '[PASS] Phase 3 GKE HTTP load balancing is disabled for loopback-only access\n'
printf '[PASS] Phase 3 qualification scheduling gate validates four EL/CL/VC placements\n'
printf '[PASS] Phase 3 GCP real-run is fail-safe\n'
printf '[PASS] Phase 3 kubectl calls retry once through a runner-IP re-authorization check\n'
printf '[PASS] Phase 3 session output is captured to a durable run log\n'
