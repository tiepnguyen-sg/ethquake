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
    "compute machine-types describe e2-standard-4 --project ethquake-test --zone northamerica-northeast2-a --format=json")
        key=systemMachine
        ;;
    "compute machine-types describe n2-custom-2-16384 --project ethquake-test --zone northamerica-northeast2-a --format=json")
        key=participantMachine
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
    "compute routers list --project ethquake-test --format=json")
        key=empty
        [ "$test_mode" != residual-router ] || key=occupied
        ;;
    "compute network-endpoint-groups list --project ethquake-test --format=json")
        key=empty
        [ "$test_mode" != residual-neg ] || key=occupied
        ;;
    "compute backend-services list --project ethquake-test --format=json" | \
    "compute forwarding-rules list --project ethquake-test --format=json" | \
    "compute target-http-proxies list --project ethquake-test --format=json" | \
    "compute target-https-proxies list --project ethquake-test --format=json" | \
    "compute url-maps list --project ethquake-test --format=json" | \
    "compute health-checks list --project ethquake-test --format=json")
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
    region:low-n2)
        jq -c '(.region.quotas[] | select(.metric == "N2_CPUS").limit) = 7 | .region' "$responses"
        ;;
    region:low-ssd)
        jq -c '(.region.quotas[] | select(.metric == "SSD_TOTAL_GB").limit) = 249 | .region' "$responses"
        ;;
    region:low-address)
        jq -c '(.region.quotas[] | select(.metric == "IN_USE_ADDRESSES").limit) = 0 | .region' "$responses"
        ;;
    projectQuota:low-global)
        jq -c '.projectQuota.quotas[0].limit = 11 | .projectQuota' "$responses"
        ;;
    budget:bad-budget)
        jq -c '.budget.amount.specifiedAmount.units = "1" | .budget' "$responses"
        ;;
    serverConfig:bad-version)
        jq -c '.serverConfig.validMasterVersions = [] | .serverConfig' "$responses"
        ;;
    occupied:*)
        printf '[{"name":"unexpected-instance"}]\n'
        ;;
    *)
        jq -c --arg key "$key" '.[$key]' "$responses"
        ;;
esac
