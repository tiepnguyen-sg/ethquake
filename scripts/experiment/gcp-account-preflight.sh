#!/bin/sh

set -eu

umask 077

pass() {
    printf '[PASS] %s\n' "$1"
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

gcloud_json() {
    operation=$1
    shift
    if ! gcloud_output=$($gcloud_bin "$@" --format=json 2>/dev/null); then
        die "$operation failed"
    fi
    if ! printf '%s\n' "$gcloud_output" | jq -e \
        'type == "object" or type == "array"' >/dev/null; then
        die "$operation returned invalid JSON"
    fi
    printf '%s\n' "$gcloud_output"
}

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repository_root=$(CDPATH= cd -- "$script_dir/../.." && pwd)
lock_env="$repository_root/experiment/phase3.lock.env"
gcloud_bin=${ETHQUAKE_GCLOUD_BIN:-gcloud}
project=${GCP_PROJECT:-}
billing_account=${GCP_BILLING_ACCOUNT:-}
budget_id=${GCP_BUDGET_ID:-}
budget_recipient=${GCP_BUDGET_RECIPIENT:-}
gke_version=${GKE_VERSION:-}

require_command jq
require_command "$gcloud_bin"
if [ ! -r "$lock_env" ]; then
    die "Phase 3 lock is missing: $lock_env"
fi
# This tracked file contains reviewed assignments only.
. "$lock_env"

case "$project" in
    ''|*[!a-z0-9-]*|-*|*-)
        die "GCP_PROJECT must be an explicit project ID"
        ;;
esac
case "$billing_account" in
    [0-9A-F][0-9A-F][0-9A-F][0-9A-F][0-9A-F][0-9A-F]-[0-9A-F][0-9A-F][0-9A-F][0-9A-F][0-9A-F][0-9A-F]-[0-9A-F][0-9A-F][0-9A-F][0-9A-F][0-9A-F][0-9A-F])
        ;;
    *)
        die "GCP_BILLING_ACCOUNT is invalid"
        ;;
esac
case "$budget_id" in
    ''|*[!A-Za-z0-9-]*|-*|*-)
        die "GCP_BUDGET_ID is invalid"
        ;;
esac
if ! printf '%s\n' "$budget_recipient" | jq -R -e \
    'test("^[A-Za-z0-9.!#$%&\u0027*+/=?^_`{|}~-]+@[A-Za-z0-9.-]+\\.[A-Za-z]{2,}$")' >/dev/null; then
    die "GCP_BUDGET_RECIPIENT must be an explicit email address"
fi
if ! printf '%s\n' "$gke_version" | jq -R -e \
    'test("^[0-9]+\\.[0-9]+\\.[0-9]+-gke\\.[0-9]+$")' >/dev/null; then
    die "GKE_VERSION must be an exact GKE patch version"
fi
if [ "${PHASE3_GCP_REGION:-}" != northamerica-northeast2 ] || \
    [ "${PHASE3_GCP_ZONE:-}" != northamerica-northeast2-b ] || \
    [ "${PHASE3_KUBERNETES_CONTEXT:-}" != gke-ethquake-phase3 ] || \
    [ "${PHASE3_SYSTEM_MACHINE_TYPE:-}" != e2-standard-4 ] || \
    [ "${PHASE3_PARTICIPANT_MACHINE_TYPE:-}" != n2-custom-2-16384 ] || \
    [ "${PHASE3_REQUIRED_PREEMPTIBLE_VCPUS:-}" != 0 ] || \
    [ "${PHASE3_REQUIRED_ON_DEMAND_VCPUS:-}" != 12 ] || \
    [ "${PHASE3_REQUIRED_TOTAL_VCPUS:-}" != 12 ] || \
    [ "${PHASE3_PRIVATE_NODES:-}" != true ] || \
    [ "${PHASE3_CLOUD_NAT:-}" != true ] || \
    [ "${PHASE3_CLOUD_NAT_IP_COUNT:-}" != 1 ] || \
    [ "${PHASE3_GCP_BUDGET_VND:-}" != 5500000 ] || \
    [ "${PHASE3_GCP_BUDGET_DISPLAY_NAME:-}" != ethquake-phase3-gross-vnd-5500000 ] || \
    [ "${PHASE3_GKE_CLUSTER_HOURLY_USD:-}" != 0.10 ]; then
    die "GCP Phase 3 safety locks are incomplete or changed"
fi

accounts=$(gcloud_json "gcloud identity lookup" auth list --filter=status:ACTIVE)
active_account=$(printf '%s\n' "$accounts" | jq -er '
    [.[] | select(.status == "ACTIVE") | .account |
     select(type == "string" and length > 0)] |
    if length == 1 then .[0] else empty end
') || die "gcloud must have exactly one active authenticated account"
pass "gcloud account: $active_account"

project_info=$(gcloud_json "GCP project lookup" projects describe "$project")
project_number=$(printf '%s\n' "$project_info" | jq -er --arg project "$project" '
    select(.projectId == $project and .lifecycleState == "ACTIVE") |
    .projectNumber | tostring | select(test("^[0-9]+$"))
') || die "GCP project must exist and be ACTIVE"
pass "GCP project: $project ($project_number)"

billing=$(gcloud_json "GCP project billing lookup" billing projects describe "$project")
if ! printf '%s\n' "$billing" | jq -e \
    --arg account "billingAccounts/$billing_account" \
    'select(.billingEnabled == true and .billingAccountName == $account)' >/dev/null; then
    die "GCP project billing must be enabled on the explicit billing account"
fi
pass "GCP paid billing link"

budget=$(gcloud_json "GCP budget lookup" billing budgets describe "$budget_id" \
    --billing-account "$billing_account" --billing-project "$project")
if ! printf '%s\n' "$budget" | jq -e \
    --arg name "$PHASE3_GCP_BUDGET_DISPLAY_NAME" \
    --arg project_number "$project_number" \
    --argjson amount "$PHASE3_GCP_BUDGET_VND" '
    .displayName == $name and
    .amount.specifiedAmount.currencyCode == "VND" and
    (.amount.specifiedAmount.units | tonumber) == $amount and
    .budgetFilter.calendarPeriod == "MONTH" and
    .budgetFilter.creditTypesTreatment == "EXCLUDE_ALL_CREDITS" and
    .budgetFilter.projects == ["projects/" + $project_number] and
    (.notificationsRule.disableDefaultIamRecipients // false) == false and
    ([.thresholdRules[] | select(.spendBasis == "CURRENT_SPEND") | .thresholdPercent] | sort) == [0.25, 0.5, 0.75, 0.9, 1] and
    ([.thresholdRules[] | select(.spendBasis == "FORECASTED_SPEND") | .thresholdPercent] | sort) == [1]
' >/dev/null; then
    die "GCP budget must match the locked gross VND 5,500,000 project alert"
fi
pass "GCP gross budget alert: VND $PHASE3_GCP_BUDGET_VND"

billing_iam=$(gcloud_json "GCP billing recipient lookup" billing accounts get-iam-policy "$billing_account")
if ! printf '%s\n' "$billing_iam" | jq -e \
    --arg recipient "user:$budget_recipient" '
    any(.bindings[]?;
        (.role == "roles/billing.admin" or .role == "roles/billing.user") and
        any(.members[]?; . == $recipient))
' >/dev/null; then
    die "Budget recipient lacks a default-notification billing IAM role"
fi
pass "GCP budget recipient: $budget_recipient"

region=$(gcloud_json "GCP region quota lookup" compute regions describe "$PHASE3_GCP_REGION" \
    --project "$project")
if ! printf '%s\n' "$region" | jq -e \
    --arg region "$PHASE3_GCP_REGION" \
    --arg zone "$PHASE3_GCP_ZONE" \
    --argjson total "$PHASE3_REQUIRED_ON_DEMAND_VCPUS" \
    --argjson nat_ips "$PHASE3_CLOUD_NAT_IP_COUNT" \
    --argjson disk_gb "$PHASE3_NODE_DISK_GB" \
    --argjson workload_ssd_reserve_gb "$PHASE3_WORKLOAD_SSD_RESERVE_GB" '
    def available($metric):
      [.quotas[] | select(.metric == $metric) | (.limit - (.usage // 0))] |
      if length == 1 then .[0] else -1 end;
    .name == $region and .status == "UP" and
    any(.zones[]?; endswith("/" + $zone)) and
    available("CPUS") >= $total and
    available("E2_CPUS") >= 4 and
    available("N2_CPUS") >= 8 and
    available("INSTANCES") >= 5 and
    available("SSD_TOTAL_GB") >= ((5 * $disk_gb) + $workload_ssd_reserve_gb) and
    available("IN_USE_ADDRESSES") >= $nat_ips
' >/dev/null; then
    die "Region quota cannot support the locked private five-node GKE topology in $PHASE3_GCP_REGION"
fi
pass "Regional quota supports the locked topology"

project_quota=$(gcloud_json "GCP global CPU quota lookup" compute project-info describe \
    --project "$project")
if ! printf '%s\n' "$project_quota" | jq -e \
    --argjson required "$PHASE3_REQUIRED_TOTAL_VCPUS" '
    [.quotas[] | select(.metric == "CPUS_ALL_REGIONS") | .limit] |
    length == 1 and .[0] >= $required
' >/dev/null; then
    die "Global CPU quota is below the locked $PHASE3_REQUIRED_TOTAL_VCPUS vCPU topology"
fi
pass "Global CPU quota supports $PHASE3_REQUIRED_TOTAL_VCPUS vCPUs"

system_machine=$(gcloud_json "GCP system machine-type lookup" compute machine-types describe \
    "$PHASE3_SYSTEM_MACHINE_TYPE" --project "$project" --zone "$PHASE3_GCP_ZONE")
participant_machine=$(gcloud_json "GCP participant machine-type lookup" compute machine-types describe \
    "$PHASE3_PARTICIPANT_MACHINE_TYPE" --project "$project" --zone "$PHASE3_GCP_ZONE")
if ! printf '%s\n%s\n' "$system_machine" "$participant_machine" | jq -s -e \
    --arg system "$PHASE3_SYSTEM_MACHINE_TYPE" \
    --arg participant "$PHASE3_PARTICIPANT_MACHINE_TYPE" \
    --arg zone "$PHASE3_GCP_ZONE" '
    length == 2 and
    .[0].name == $system and ((.[0].zone == $zone) or (.[0].zone | endswith("/" + $zone))) and
    .[0].guestCpus == 4 and .[0].memoryMb == 16384 and
    .[1].name == $participant and ((.[1].zone == $zone) or (.[1].zone | endswith("/" + $zone))) and
    .[1].guestCpus == 2 and .[1].memoryMb == 16384
' >/dev/null; then
    die "Locked machine types are unavailable in $PHASE3_GCP_ZONE"
fi
pass "Locked machine types are available in $PHASE3_GCP_ZONE"

server_config=$(gcloud_json "GKE version lookup" container get-server-config \
    --project "$project" --zone "$PHASE3_GCP_ZONE")
if ! printf '%s\n' "$server_config" | jq -e --arg version "$gke_version" '
    (.validMasterVersions | index($version)) != null and
    (.validNodeVersions | index($version)) != null
' >/dev/null; then
    die "GKE_VERSION is unavailable for both control plane and nodes in $PHASE3_GCP_ZONE"
fi
pass "GKE version available: $gke_version"

clusters=$(gcloud_json "GKE inventory lookup" container clusters list --project "$project")
instances=$(gcloud_json "Compute instance inventory lookup" compute instances list --project "$project")
disks=$(gcloud_json "Compute disk inventory lookup" compute disks list --project "$project")
addresses=$(gcloud_json "Compute address inventory lookup" compute addresses list --project "$project")
networks=$(gcloud_json "VPC network inventory lookup" compute networks list --project "$project")
subnets=$(gcloud_json "VPC subnet inventory lookup" compute networks subnets list --project "$project")
firewalls=$(gcloud_json "VPC firewall inventory lookup" compute firewall-rules list --project "$project")
routers=$(gcloud_json "Cloud Router inventory lookup" compute routers list --project "$project")
negs=$(gcloud_json "Network endpoint group inventory lookup" compute network-endpoint-groups list --project "$project")
backend_services=$(gcloud_json "Backend service inventory lookup" compute backend-services list --project "$project")
forwarding_rules=$(gcloud_json "Forwarding rule inventory lookup" compute forwarding-rules list --project "$project")
http_proxies=$(gcloud_json "HTTP proxy inventory lookup" compute target-http-proxies list --project "$project")
https_proxies=$(gcloud_json "HTTPS proxy inventory lookup" compute target-https-proxies list --project "$project")
url_maps=$(gcloud_json "URL map inventory lookup" compute url-maps list --project "$project")
health_checks=$(gcloud_json "Health check inventory lookup" compute health-checks list --project "$project")
for inventory in "$clusters" "$instances" "$disks" "$addresses"; do
    if ! printf '%s\n' "$inventory" | jq -e 'type == "array" and length == 0' >/dev/null; then
        die "Dedicated Phase 3 project must have empty GKE, instance, disk, and address inventory"
    fi
done
pass "Dedicated project has no runtime cloud resources"
for inventory in "$networks" "$subnets" "$firewalls" "$routers" "$negs" "$backend_services" \
    "$forwarding_rules" "$http_proxies" "$https_proxies" "$url_maps" "$health_checks"; do
    if ! printf '%s\n' "$inventory" | jq -e 'type == "array" and length == 0' >/dev/null; then
        die "Dedicated Phase 3 project must have empty VPC and load-balancer inventory"
    fi
done
pass "Dedicated project has no residual VPC or load-balancer resources"

GCP_PROJECT="$project" "$script_dir/gcp-price-preflight.sh"
pass "GCP read-only account preflight"
