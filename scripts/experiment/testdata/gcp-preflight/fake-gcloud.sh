#!/bin/sh

set -eu

testdata=$ETHQUAKE_GCP_TESTDATA
test_mode=${ETHQUAKE_GCP_TEST_MODE_NAME:-success}
call_log=$ETHQUAKE_GCP_TEST_LOG
responses="$testdata/responses.json"

printf '%s\n' "$*" >>"$call_log"

case "$*" in
    "auth list --filter=status:ACTIVE --format=json")
        key=accounts
        ;;
    "projects describe ethquake-test --format=json")
        key=project
        ;;
    "billing projects describe ethquake-test --format=json")
        key=billing
        ;;
    "billing budgets describe test-budget --billing-account AAAAAA-BBBBBB-CCCCCC --billing-project ethquake-test --format=json")
        key=budget
        ;;
    "billing accounts get-iam-policy AAAAAA-BBBBBB-CCCCCC --format=json")
        key=billingIam
        ;;
    "compute regions describe northamerica-northeast2 --project ethquake-test --format=json")
        key=region
        ;;
    "compute project-info describe --project ethquake-test --format=json")
        key=projectQuota
        ;;
    "compute machine-types list --project ethquake-test --filter zone:(northamerica-northeast2-a) --format=json")
        key=machineTypes
        ;;
    "container get-server-config --project ethquake-test --zone northamerica-northeast2-a --format=json")
        key=serverConfig
        ;;
    "container clusters list --project ethquake-test --format=json")
        key=empty
        ;;
    "compute instances list --project ethquake-test --format=json")
        key=empty
        [ "$test_mode" != occupied ] || key=occupied
        ;;
    "compute disks list --project ethquake-test --format=json" | \
    "compute addresses list --project ethquake-test --format=json" | \
    "compute networks list --project ethquake-test --format=json" | \
    "compute networks subnets list --project ethquake-test --format=json" | \
    "compute firewall-rules list --project ethquake-test --format=json")
        key=empty
        ;;
    "compute regions list --project ethquake-test --format=json")
        key=regions
        ;;
    *)
        exit 96
        ;;
esac

if [ "$test_mode" = api-error ] && [ "$key" = region ]; then
    exit 95
fi
case "$key:$test_mode" in
    region:low-spot)
        jq -c '(.region.quotas[] | select(.metric == "PREEMPTIBLE_CPUS").limit) = 0 | .region' "$responses"
        ;;
    region:low-address)
        jq -c '(.region.quotas[] | select(.metric == "IN_USE_ADDRESSES").limit) = 4 | .region' "$responses"
        ;;
    projectQuota:low-global)
        jq -c '.projectQuota.quotas[0].limit = 12 | .projectQuota' "$responses"
        ;;
    budget:bad-budget)
        jq -c '.budget.amount.specifiedAmount.units = "1" | .budget' "$responses"
        ;;
    serverConfig:bad-version)
        jq -c '.serverConfig.validMasterVersions = [] | .serverConfig' "$responses"
        ;;
    occupied:occupied)
        printf '[{"name":"unexpected-instance"}]\n'
        ;;
    *)
        jq -c --arg key "$key" '.[$key]' "$responses"
        ;;
esac
