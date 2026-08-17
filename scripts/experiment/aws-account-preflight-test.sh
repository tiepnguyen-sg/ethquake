#!/bin/sh

set -eu

umask 077

die() {
    printf '[FAIL] %s\n' "$1" >&2
    exit 1
}

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
testdata="$script_dir/testdata/aws-preflight"
fake_aws="$testdata/fake-aws.sh"
test_root=$(mktemp -d "${TMPDIR:-/tmp}/ethquake-aws-preflight-test.XXXXXX")
trap 'rm -rf "$test_root"' EXIT HUP INT TERM
call_log="$test_root/aws-calls.log"
output="$test_root/output"

run_preflight() {
    mode=$1
    ETHQUAKE_AWS_BIN="$fake_aws" \
    ETHQUAKE_AWS_TESTDATA="$testdata" \
    ETHQUAKE_AWS_TEST_MODE="$mode" \
    ETHQUAKE_AWS_TEST_LOG="$call_log" \
    AWS_PROFILE=ethquake \
    AWS_REGION=ap-southeast-1 \
        "$script_dir/aws-account-preflight.sh"
}

: >"$call_log"
run_preflight success >"$output"
for expected in \
    '[PASS] AWS Free plan is active with positive credits' \
    '[PASS] All Standard Spot quota supports 16 vCPUs in ap-southeast-1' \
    '[PASS] AWS monthly cost budget is locked at USD 20' \
    '[PASS] AWS budget alert has a subscriber' \
    '[PASS] AWS read-only account preflight'; do
    if ! grep -F "$expected" "$output" >/dev/null; then
        die "successful preflight output is missing: $expected"
    fi
done
if [ "$(wc -l <"$call_log" | tr -d ' ')" != 7 ]; then
    die "successful preflight did not make exactly seven read-only calls"
fi
if grep -E '(^|[| ])(create|delete|put|request|run-instances|update|upgrade)($|[ ])' \
    "$call_log" >/dev/null; then
    die "preflight attempted a mutating AWS operation"
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

assert_failure paid-plan 'must be FREE and ACTIVE'
assert_failure low-quota 'below the required 16 vCPUs'
assert_failure missing-budget 'AWS budget lookup failed'
assert_failure api-error 'EC2 Spot quota lookup failed'
assert_failure no-alert 'requires an actual-spend alert at 100 percent'

assert_input_failure() {
    profile_value=$1
    region_value=$2
    expected=$3
    : >"$call_log"
    if ETHQUAKE_AWS_BIN="$fake_aws" \
        ETHQUAKE_AWS_TESTDATA="$testdata" \
        ETHQUAKE_AWS_TEST_MODE=success \
        ETHQUAKE_AWS_TEST_LOG="$call_log" \
        AWS_PROFILE="$profile_value" \
        AWS_REGION="$region_value" \
        "$script_dir/aws-account-preflight.sh" >"$output" 2>&1; then
        die "preflight accepted invalid account inputs"
    fi
    if ! grep -F "$expected" "$output" >/dev/null; then
        die "invalid account input did not report: $expected"
    fi
    if [ -s "$call_log" ]; then
        die "invalid account input reached the AWS CLI"
    fi
}

assert_input_failure '' ap-southeast-1 'AWS_PROFILE must be explicit'
assert_input_failure ethquake singapore 'AWS_REGION must be an explicit AWS region'

printf '[PASS] AWS account preflight accepts only the locked safe state\n'
printf '[PASS] AWS account preflight uses only the read-only command allowlist\n'
