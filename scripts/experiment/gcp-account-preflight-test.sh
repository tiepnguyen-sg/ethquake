#!/bin/sh

set -eu

umask 077

die() {
    printf '[FAIL] %s\n' "$1" >&2
    exit 1
}

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
testdata="$script_dir/testdata/gcp-preflight"
fake_gcloud="$testdata/fake-gcloud.sh"
catalog="$testdata/catalog.json"
test_root=$(mktemp -d "/tmp/ethquake-gcp-preflight-test.XXXXXX")
trap 'rm -rf "$test_root"' EXIT HUP INT TERM
call_log="$test_root/gcloud-calls.log"
output="$test_root/output"

run_preflight() {
    mode=$1
    ETHQUAKE_GCLOUD_BIN="$fake_gcloud" \
    ETHQUAKE_GCP_CATALOG_FIXTURE="$catalog" \
    ETHQUAKE_GCP_TEST_MODE=true \
    ETHQUAKE_GCP_TEST_MODE_NAME="$mode" \
    ETHQUAKE_GCP_TESTDATA="$testdata" \
    ETHQUAKE_GCP_TEST_LOG="$call_log" \
    GCP_PROJECT=ethquake-test \
    GCP_BILLING_ACCOUNT=AAAAAA-BBBBBB-CCCCCC \
    GCP_BUDGET_ID=test-budget \
    GCP_BUDGET_RECIPIENT=owner@example.com \
    GKE_VERSION=1.36.3-gke.1537000 \
        "$script_dir/gcp-account-preflight.sh"
}

chmod 700 "$fake_gcloud"
: >"$call_log"
run_preflight success >"$output"
for expected in \
    '[PASS] GCP paid billing link' \
    '[PASS] GCP gross budget alert: VND 5500000' \
    '[PASS] Regional quota supports the locked topology' \
    '[PASS] Global CPU quota supports 20 vCPUs' \
    '[PASS] Cheapest eligible GCP region: northamerica-northeast2' \
    '[PASS] GCP read-only account preflight'; do
    if ! grep -F "$expected" "$output" >/dev/null; then
        die "successful preflight output is missing: $expected"
    fi
done
if [ "$(wc -l <"$call_log" | tr -d ' ')" != 17 ]; then
    die "successful preflight did not make exactly seventeen read-only gcloud calls"
fi
if grep -E '(^|[[:space:]])(create|delete|disable|enable|request|run|set|update|upgrade)([[:space:]]|$)' \
    "$call_log" >/dev/null; then
    die "preflight attempted a mutating GCP operation"
fi

assert_failure() {
    mode=$1
    expected=$2
    : >"$call_log"
    if run_preflight "$mode" >"$output" 2>&1; then
        die "preflight unexpectedly passed in mode: $mode"
    fi
    if ! grep -F "$expected" "$output" >/dev/null; then
        die "failure mode $mode did not report: $expected"
    fi
}

assert_failure low-spot 'Region quota cannot support'
assert_failure low-address 'Region quota cannot support'
assert_failure low-global 'Global CPU quota is below'
assert_failure bad-budget 'budget must match'
assert_failure bad-version 'GKE_VERSION is unavailable'
assert_failure occupied 'must have empty GKE, instance, disk, and address inventory'
assert_failure api-error 'region quota lookup failed'

: >"$call_log"
if ETHQUAKE_GCLOUD_BIN="$fake_gcloud" \
    ETHQUAKE_GCP_TESTDATA="$testdata" \
    ETHQUAKE_GCP_TEST_LOG="$call_log" \
    GCP_PROJECT=INVALID \
    GCP_BILLING_ACCOUNT=AAAAAA-BBBBBB-CCCCCC \
    GCP_BUDGET_ID=test-budget \
    GCP_BUDGET_RECIPIENT=owner@example.com \
    GKE_VERSION=1.36.3-gke.1537000 \
    "$script_dir/gcp-account-preflight.sh" >"$output" 2>&1; then
    die "preflight accepted an invalid project ID"
fi
if [ -s "$call_log" ]; then
    die "invalid account input reached gcloud"
fi

other_catalog="$test_root/other-cheapest.json"
jq '(.skus[] | select(.description == "Spot Preemptible N2 Instance Core running in Toronto").pricingInfo[0].pricingExpression.tieredRates[0].unitPrice.nanos) = 50000000' \
    "$catalog" >"$other_catalog"
: >"$call_log"
if ETHQUAKE_GCLOUD_BIN="$fake_gcloud" \
    ETHQUAKE_GCP_CATALOG_FIXTURE="$other_catalog" \
    ETHQUAKE_GCP_TEST_MODE=true \
    ETHQUAKE_GCP_TEST_MODE_NAME=success \
    ETHQUAKE_GCP_TESTDATA="$testdata" \
    ETHQUAKE_GCP_TEST_LOG="$call_log" \
    GCP_PROJECT=ethquake-test \
    "$script_dir/gcp-price-preflight.sh" >"$output" 2>&1; then
    die "price preflight accepted a region that was no longer cheapest"
fi
if ! grep -F 'is not the cheapest eligible region' "$output" >/dev/null; then
    die "price preflight did not explain the region mismatch"
fi

printf '[PASS] GCP account preflight accepts only the locked safe state\n'
printf '[PASS] GCP price preflight enforces the cheapest selectable region\n'
printf '[PASS] GCP account preflight uses only the read-only command allowlist\n'
