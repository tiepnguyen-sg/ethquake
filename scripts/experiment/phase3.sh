#!/bin/sh

set -eu

umask 077

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
action=${1:-run}

case "$action" in
    dry-run|run)
        ;;
    *)
        die "Usage: phase3.sh [dry-run|run]"
        ;;
esac

if [ ! -r "$lock_env" ]; then
    die "Phase 3 lock is missing: $lock_env"
fi
# This tracked file contains reviewed assignments only.
. "$lock_env"

if [ "$action" = run ]; then
    refuse '§0.8' 'AWS account preflight, compute substrate, cost controls, provisioning, and teardown are not implemented'
fi

case "${ETHQUAKE_SESSION_ID:-}" in
    ''|*[!a-z0-9-]*|-*|*-)
        die "ETHQUAKE_SESSION_ID must be lowercase and DNS-safe"
        ;;
esac
if [ "${#ETHQUAKE_SESSION_ID}" -gt 24 ]; then
    die "ETHQUAKE_SESSION_ID must not exceed 24 characters"
fi
if [ "${PHASE3_PARTICIPANT_COUNT:-}" != 4 ]; then
    die "Phase 3 must lock exactly four participants"
fi
if [ "${PHASE3_REQUIRED_VCPUS:-}" != 16 ]; then
    die "Phase 3 must lock the 16-vCPU capacity requirement"
fi
if [ "${PHASE3_KUBERNETES_CONTEXT:-}" != ethquake-aws-phase3 ]; then
    die "Phase 3 Kubernetes context must be ethquake-aws-phase3"
fi

"$script_dir/preflight.sh" static

printf '[PLAN] provider=aws\n'
printf '[PLAN] mode=dry-run\n'
printf '[PLAN] session_id=%s\n' "$ETHQUAKE_SESSION_ID"
printf '[PLAN] participants=%s\n' "$PHASE3_PARTICIPANT_COUNT"
printf '[PLAN] required_vcpus=%s\n' "$PHASE3_REQUIRED_VCPUS"
printf '[PLAN] kubernetes_context=%s\n' "$PHASE3_KUBERNETES_CONTEXT"
printf '[PLAN] run_order=%s\n' "$PHASE3_RUN_ORDER"
printf '[PLAN] cloud_api_calls=false\n'
printf '[PLAN] resource_creation=false\n'
printf '[PLAN] account_plan=unverified\n'
printf '[PLAN] compute_substrate=unselected\n'
printf '[PLAN] region=unselected\n'
printf '[PLAN] kubernetes_version=unselected\n'
printf '[PLAN] spot_quota=unverified\n'
printf '[PLAN] budget_alert=unverified\n'
