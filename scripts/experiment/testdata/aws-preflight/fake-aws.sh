#!/bin/sh

set -eu

testdata=${ETHQUAKE_AWS_TESTDATA:?}
test_mode=${ETHQUAKE_AWS_TEST_MODE:-success}
call_log=${ETHQUAKE_AWS_TEST_LOG:?}
profile=
api_region=
output=
no_pager=false
no_prompt=false

while [ "$#" -gt 0 ]; do
    case "$1" in
        --profile)
            profile=$2
            shift 2
            ;;
        --region)
            api_region=$2
            shift 2
            ;;
        --no-cli-pager)
            no_pager=true
            shift
            ;;
        --no-cli-auto-prompt)
            no_prompt=true
            shift
            ;;
        --cli-connect-timeout|--cli-read-timeout)
            shift 2
            ;;
        --output)
            output=$2
            shift 2
            ;;
        *)
            break
            ;;
    esac
done

if [ "$profile" != ethquake ] || [ "$output" != json ] || \
    [ "$no_pager" != true ] || [ "$no_prompt" != true ]; then
    exit 90
fi
case "$api_region" in
    ap-southeast-1|us-east-1)
        ;;
    *)
        exit 91
        ;;
esac
if [ "$#" -lt 2 ]; then
    exit 92
fi
service=$1
operation=$2
shift 2
printf '%s|%s|%s|%s|%s\n' "$profile" "$api_region" "$service" "$operation" "$*" >>"$call_log"

case "$service $operation" in
    "sts get-caller-identity")
        [ "$api_region" = ap-southeast-1 ] && [ "$#" -eq 0 ] || exit 93
        response=identity.json
        ;;
    "freetier get-account-plan-state")
        [ "$api_region" = us-east-1 ] && [ "$#" -eq 0 ] || exit 94
        if [ "$test_mode" = paid-plan ]; then
            response=paid-plan-active.json
        else
            response=free-plan-active.json
        fi
        ;;
    "service-quotas get-service-quota")
        [ "$api_region" = ap-southeast-1 ] || exit 95
        [ "$*" = "--service-code ec2 --quota-code L-34B43A08" ] || exit 96
        if [ "$test_mode" = api-error ]; then
            exit 42
        elif [ "$test_mode" = low-quota ]; then
            response=spot-quota-5.json
        else
            response=spot-quota-16.json
        fi
        ;;
    "eks describe-cluster-versions")
        [ "$api_region" = ap-southeast-1 ] || exit 97
        [ "$*" = "--version-status STANDARD_SUPPORT" ] || exit 98
        response=eks-versions.json
        ;;
    "budgets describe-budget")
        [ "$api_region" = us-east-1 ] || exit 99
        [ "$*" = "--account-id 123456789012 --budget-name ethquake-phase3" ] || exit 100
        if [ "$test_mode" = missing-budget ]; then
            exit 43
        fi
        response=budget-20.json
        ;;
    "budgets describe-notifications-for-budget")
        [ "$api_region" = us-east-1 ] || exit 101
        [ "$*" = "--account-id 123456789012 --budget-name ethquake-phase3" ] || exit 102
        if [ "$test_mode" = no-alert ]; then
            response=budget-notifications-empty.json
        else
            response=budget-notifications.json
        fi
        ;;
    "budgets describe-subscribers-for-notification")
        [ "$api_region" = us-east-1 ] || exit 103
        expected='--account-id 123456789012 --budget-name ethquake-phase3 --notification {"ComparisonOperator":"GREATER_THAN","NotificationType":"ACTUAL","Threshold":100,"ThresholdType":"PERCENTAGE"}'
        [ "$*" = "$expected" ] || exit 104
        response=budget-subscribers.json
        ;;
    *)
        exit 105
        ;;
esac

cat "$testdata/$response"
