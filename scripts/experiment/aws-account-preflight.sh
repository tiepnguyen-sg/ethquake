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

aws_json() {
    operation=$1
    api_region=$2
    shift 2
    if ! aws_output=$(AWS_PAGER= "$aws_bin" \
        --profile "$profile" \
        --region "$api_region" \
        --no-cli-pager \
        --no-cli-auto-prompt \
        --cli-connect-timeout 5 \
        --cli-read-timeout 20 \
        --output json \
        "$@" 2>/dev/null); then
        die "$operation failed"
    fi
    if ! printf '%s\n' "$aws_output" | jq -e 'type == "object"' >/dev/null; then
        die "$operation returned invalid JSON"
    fi
    printf '%s\n' "$aws_output"
}

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repository_root=$(CDPATH= cd -- "$script_dir/../.." && pwd)
lock_env="$repository_root/experiment/phase3.lock.env"
aws_bin=${ETHQUAKE_AWS_BIN:-aws}
profile=${AWS_PROFILE:-}
region=${AWS_REGION:-}

require_command jq
require_command "$aws_bin"
if [ ! -r "$lock_env" ]; then
    die "Phase 3 lock is missing: $lock_env"
fi
# This tracked file contains reviewed assignments only.
. "$lock_env"

if ! printf '%s\n' "$profile" | jq -R -e \
    'test("^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$")' >/dev/null; then
    die "AWS_PROFILE must be explicit and contain only letters, numbers, dot, underscore, or hyphen"
fi
if ! printf '%s\n' "$region" | jq -R -e \
    'test("^[a-z]{2}(-gov)?-[a-z]+-[0-9]+$")' >/dev/null; then
    die "AWS_REGION must be an explicit AWS region"
fi
if [ "${PHASE3_AWS_BILLING_REGION:-}" != us-east-1 ] || \
    [ "${PHASE3_AWS_SPOT_QUOTA_CODE:-}" != L-34B43A08 ] || \
    [ "${PHASE3_AWS_BUDGET_NAME:-}" != ethquake-phase3 ] || \
    [ "${PHASE3_AWS_BUDGET_USD:-}" != 20 ] || \
    [ "${PHASE3_REQUIRED_VCPUS:-}" != 16 ]; then
    die "AWS Phase 3 safety locks are incomplete or changed"
fi

identity=$(aws_json "AWS identity lookup" "$region" sts get-caller-identity)
account_id=$(printf '%s\n' "$identity" | jq -er \
    '.Account | strings | select(test("^[0-9]{12}$"))') || \
    die "AWS identity did not return a 12-digit account ID"
pass "AWS identity resolved through explicit profile"

plan=$(aws_json "AWS account plan lookup" "$PHASE3_AWS_BILLING_REGION" \
    freetier get-account-plan-state)
if ! printf '%s\n' "$plan" | jq -e --arg account "$account_id" '
    .accountId == $account and
    .accountPlanType == "FREE" and
    .accountPlanStatus == "ACTIVE" and
    .accountPlanRemainingCredits.unit == "USD" and
    (.accountPlanRemainingCredits.amount | type) == "number" and
    .accountPlanRemainingCredits.amount > 0 and
    (.accountPlanExpirationDate | type) == "string" and
    (.accountPlanExpirationDate | length) > 0
' >/dev/null; then
    die "AWS account plan must be FREE and ACTIVE with positive USD credits"
fi
pass "AWS Free plan is active with positive credits"

quota=$(aws_json "EC2 Spot quota lookup" "$region" \
    service-quotas get-service-quota \
    --service-code ec2 \
    --quota-code "$PHASE3_AWS_SPOT_QUOTA_CODE")
if ! printf '%s\n' "$quota" | jq -e \
    --arg code "$PHASE3_AWS_SPOT_QUOTA_CODE" \
    --argjson required "$PHASE3_REQUIRED_VCPUS" '
    .Quota.ServiceCode == "ec2" and
    .Quota.QuotaCode == $code and
    (.Quota.Value | type) == "number" and
    .Quota.Value >= $required
' >/dev/null; then
    die "All Standard Spot quota is below the required $PHASE3_REQUIRED_VCPUS vCPUs in $region"
fi
pass "All Standard Spot quota supports $PHASE3_REQUIRED_VCPUS vCPUs in $region"

versions=$(aws_json "EKS version catalog lookup" "$region" \
    eks describe-cluster-versions \
    --version-status STANDARD_SUPPORT)
if ! printf '%s\n' "$versions" | jq -e '
    [.clusterVersions[]? |
        select(.versionStatus == "STANDARD_SUPPORT") |
        .clusterVersion |
        select(type == "string" and test("^[0-9]+\\.[0-9]+$"))] |
    length > 0
' >/dev/null; then
    die "EKS returned no standard-support Kubernetes version in $region"
fi
pass "EKS standard-support version catalog is readable in $region"
info "EKS read access does not prove Free-plan cluster creation eligibility"

budget=$(aws_json "AWS budget lookup" "$PHASE3_AWS_BILLING_REGION" \
    budgets describe-budget \
    --account-id "$account_id" \
    --budget-name "$PHASE3_AWS_BUDGET_NAME")
if ! printf '%s\n' "$budget" | jq -e \
    --arg name "$PHASE3_AWS_BUDGET_NAME" \
    --argjson amount "$PHASE3_AWS_BUDGET_USD" '
    .Budget.BudgetName == $name and
    .Budget.BudgetType == "COST" and
    .Budget.TimeUnit == "MONTHLY" and
    .Budget.BudgetLimit.Unit == "USD" and
    (.Budget.BudgetLimit.Amount | tonumber) == $amount
' >/dev/null; then
    die "AWS budget must be a monthly USD $PHASE3_AWS_BUDGET_USD cost budget"
fi
pass "AWS monthly cost budget is locked at USD $PHASE3_AWS_BUDGET_USD"

notifications=$(aws_json "AWS budget notification lookup" "$PHASE3_AWS_BILLING_REGION" \
    budgets describe-notifications-for-budget \
    --account-id "$account_id" \
    --budget-name "$PHASE3_AWS_BUDGET_NAME")
if ! printf '%s\n' "$notifications" | jq -e '
    any(.Notifications[]?;
        .NotificationType == "ACTUAL" and
        .ComparisonOperator == "GREATER_THAN" and
        .ThresholdType == "PERCENTAGE" and
        .Threshold == 100)
' >/dev/null; then
    die "AWS budget requires an actual-spend alert at 100 percent"
fi
pass "AWS budget has an actual-spend alert at 100 percent"

notification='{"ComparisonOperator":"GREATER_THAN","NotificationType":"ACTUAL","Threshold":100,"ThresholdType":"PERCENTAGE"}'
subscribers=$(aws_json "AWS budget subscriber lookup" "$PHASE3_AWS_BILLING_REGION" \
    budgets describe-subscribers-for-notification \
    --account-id "$account_id" \
    --budget-name "$PHASE3_AWS_BUDGET_NAME" \
    --notification "$notification")
if ! printf '%s\n' "$subscribers" | jq -e \
    '.Subscribers | type == "array" and length > 0' >/dev/null; then
    die "AWS budget alert requires at least one subscriber"
fi
pass "AWS budget alert has a subscriber"
pass "AWS read-only account preflight"
