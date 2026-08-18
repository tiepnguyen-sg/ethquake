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

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repository_root=$(CDPATH= cd -- "$script_dir/../.." && pwd)
lock_env="$repository_root/experiment/phase3.lock.env"
gcloud_bin=${ETHQUAKE_GCLOUD_BIN:-gcloud}
curl_bin=${ETHQUAKE_CURL_BIN:-curl}
catalog_fixture=${ETHQUAKE_GCP_CATALOG_FIXTURE:-}
project=${GCP_PROJECT:-}

require_command jq
require_command awk
require_command sort
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
if [ -n "$catalog_fixture" ]; then
    if [ "${ETHQUAKE_GCP_TEST_MODE:-}" != true ]; then
        die "Catalog fixtures are allowed only in explicit test mode"
    fi
    if [ ! -r "$catalog_fixture" ]; then
        die "Catalog fixture is unreadable: $catalog_fixture"
    fi
else
    require_command "$curl_bin"
fi

case "${PHASE3_GCP_REGION:-}" in
    ''|*[!a-z0-9-]*|-*|*-)
        die "Locked GCP region is invalid"
        ;;
esac
for numeric_value in \
    "$PHASE3_PARTICIPANT_COUNT" \
    "$PHASE3_NODE_DISK_GB" \
    "$PHASE3_WORKLOAD_SSD_RESERVE_GB" \
    "$PHASE3_MAX_SESSION_HOURS"; do
    case "$numeric_value" in
        ''|*[!0-9]*)
            die "Phase 3 price inputs must be positive integers"
            ;;
    esac
done
case "${PHASE3_GKE_CLUSTER_HOURLY_USD:-}" in
    ''|*[!0-9.]*|*.*.*)
        die "GKE cluster hourly price lock must be a non-negative decimal"
        ;;
esac
for decimal_value in \
    "$PHASE3_N2_CUSTOM_PREMIUM_MULTIPLIER" \
    "$PHASE3_CLOUD_NAT_VM_HOURLY_USD" \
    "$PHASE3_CLOUD_NAT_IP_HOURLY_USD" \
    "$PHASE3_CLOUD_NAT_DATA_USD_PER_GIB"; do
    case "$decimal_value" in
        ''|*[!0-9.]*|*.*.*)
            die "Phase 3 custom-machine and Cloud NAT price locks must be non-negative decimals"
            ;;
    esac
done
if [ "$PHASE3_PARTICIPANT_COUNT" -ne 4 ] || \
    [ "$PHASE3_NODE_DISK_TYPE" != pd-balanced ] || \
    [ "$PHASE3_NODE_DISK_GB" -ne 40 ] || \
    [ "$PHASE3_WORKLOAD_SSD_RESERVE_GB" -ne 50 ] || \
    [ "$PHASE3_MAX_SESSION_HOURS" -ne 8 ] || \
    [ "$PHASE3_CLOUD_NAT_IP_COUNT" -ne 1 ] || \
    [ "$PHASE3_MAX_NAT_DATA_GIB" -ne 10 ]; then
    die "Phase 3 topology price inputs changed unexpectedly"
fi

temp_root=$(mktemp -d "${TMPDIR:-/tmp}/ethquake-gcp-price.XXXXXX")
trap 'rm -rf "$temp_root"' EXIT HUP INT TERM
raw_prices="$temp_root/raw-prices.tsv"
price_table="$temp_root/price-table.tsv"

if ! regions=$($gcloud_bin compute regions list \
    --project "$project" --format=json 2>/dev/null); then
    die "Compute Engine region catalog lookup failed"
fi
if ! printf '%s\n' "$regions" | jq -e '
    type == "array" and
    all(.[]; (.name | type) == "string" and (.status | type) == "string")
' >/dev/null; then
    die "Compute Engine region catalog returned invalid JSON"
fi
printf '%s\n' "$regions" | jq -r '.[] | select(.status == "UP") | ["AVAILABLE", .name] | @tsv' >"$raw_prices"

append_catalog_prices() {
    response=$1
    if ! printf '%s\n' "$response" | jq -e \
        '.skus | type == "array"' >/dev/null; then
        die "Cloud Billing catalog returned invalid JSON"
    fi
    if ! printf '%s\n' "$response" | jq -r '
        def unit_price:
            (((.pricingInfo[0].pricingExpression.tieredRates[0].unitPrice.units // "0") | tonumber) +
             ((.pricingInfo[0].pricingExpression.tieredRates[0].unitPrice.nanos // 0) / 1000000000));
        .skus[] |
        select(
            ((.description | test("^N2 Instance (Core|Ram) running")) and .category.usageType == "OnDemand") or
            ((.description | test("^E2 Instance (Core|Ram) running")) and .category.usageType == "OnDemand") or
            ((.description | test("^Balanced PD Capacity in ")) and
             .category.usageType == "OnDemand" and
             .category.resourceGroup == "SSD" and
             .pricingInfo[0].pricingExpression.usageUnit == "GiBy.mo")
        ) |
        . as $sku |
        .geoTaxonomy.regions[] |
        [
            "SKU",
            .,
            (if ($sku.description | startswith("N2")) then "N2_OD"
             elif ($sku.description | startswith("E2")) then "E2_OD"
             else "PD_BALANCED" end),
            (if ($sku.category.resourceGroup == "CPU") then "CPU"
             elif ($sku.category.resourceGroup == "RAM") then "RAM"
             else "CAPACITY" end),
            ($sku | unit_price)
        ] | @tsv
    ' >>"$raw_prices"; then
        die "Cloud Billing catalog pricing extraction failed"
    fi
}

if [ -n "$catalog_fixture" ]; then
    append_catalog_prices "$(jq -c . "$catalog_fixture")"
else
    if ! access_token=$($gcloud_bin auth print-access-token 2>/dev/null); then
        die "gcloud access-token lookup failed"
    fi
    case "$access_token" in
        ''|*[!A-Za-z0-9._~-]*)
            die "gcloud returned an invalid access token"
            ;;
    esac
    auth_header="Authorization: Bearer $access_token"
    page_token=
    while :; do
        request_url='https://cloudbilling.googleapis.com/v1/services/6F81-5844-456A/skus?pageSize=5000&currencyCode=USD'
        if [ -n "$page_token" ]; then
            request_url="$request_url&pageToken=$page_token"
        fi
        if ! response=$($curl_bin --silent --show-error --fail-with-body \
            --header "$auth_header" \
            --header "x-goog-user-project: $project" \
            "$request_url"); then
            die "Cloud Billing catalog request failed"
        fi
        append_catalog_prices "$response"
        page_token=$(printf '%s\n' "$response" | jq -r '.nextPageToken // empty')
        case "$page_token" in
            *[!A-Za-z0-9_=-]*)
                die "Cloud Billing catalog returned an invalid page token"
                ;;
        esac
        [ -n "$page_token" ] || break
    done
fi

awk -F '\t' \
    -v participants="$PHASE3_PARTICIPANT_COUNT" \
    -v disk_gb="$PHASE3_NODE_DISK_GB" \
    -v workload_ssd_reserve_gb="$PHASE3_WORKLOAD_SSD_RESERVE_GB" \
    -v custom_premium="$PHASE3_N2_CUSTOM_PREMIUM_MULTIPLIER" '
    $1 == "AVAILABLE" { available[$2] = 1 }
    $1 == "SKU" && $3 == "E2_OD" && $4 == "CPU" { e2_cpu[$2] = $5 }
    $1 == "SKU" && $3 == "E2_OD" && $4 == "RAM" { e2_ram[$2] = $5 }
    $1 == "SKU" && $3 == "N2_OD" && $4 == "CPU" { n2_cpu[$2] = $5 }
    $1 == "SKU" && $3 == "N2_OD" && $4 == "RAM" { n2_ram[$2] = $5 }
    $1 == "SKU" && $3 == "PD_BALANCED" { pd[$2] = $5 }
    END {
        for (region in available) {
            if ((region in e2_cpu) && (region in e2_ram) &&
                (region in n2_cpu) && (region in n2_ram) &&
                (region in pd)) {
                system_vm = 4 * e2_cpu[region] + 16 * e2_ram[region]
                participant_vm = (2 * n2_cpu[region] + 16 * n2_ram[region]) * custom_premium
                compute = system_vm + participants * participant_vm
                disk = (((participants + 1) * disk_gb + workload_ssd_reserve_gb) * pd[region]) / 730
                printf "%.9f\t%s\t%.9f\t%.9f\n", compute + disk, region, compute, disk
            }
        }
    }
' "$raw_prices" | sort -n >"$price_table"

if [ ! -s "$price_table" ]; then
    die "No selectable region has complete pricing for the locked topology"
fi
cheapest=$(awk 'NR == 1 { print $2 }' "$price_table")
if [ "$cheapest" != "$PHASE3_GCP_REGION" ]; then
    info "Cheapest comparable region is $cheapest; fixed-location policy retains $PHASE3_GCP_REGION"
fi
selected=$(awk -v region="$PHASE3_GCP_REGION" '$2 == region { print; exit }' "$price_table")
if [ -z "$selected" ]; then
    die "Locked region lacks complete pricing for the locked topology"
fi
hourly_total=$(printf '%s\n' "$selected" | awk '{ print $1 }')
hourly_compute=$(printf '%s\n' "$selected" | awk '{ print $3 }')
hourly_disk=$(printf '%s\n' "$selected" | awk '{ print $4 }')
hourly_nat=$(awk \
    -v vms="$((PHASE3_PARTICIPANT_COUNT + 1))" \
    -v vm_price="$PHASE3_CLOUD_NAT_VM_HOURLY_USD" \
    -v ips="$PHASE3_CLOUD_NAT_IP_COUNT" \
    -v ip_price="$PHASE3_CLOUD_NAT_IP_HOURLY_USD" \
    'BEGIN { printf "%.9f", (vms * vm_price) + (ips * ip_price) }')
hourly_with_gke=$(awk -v hourly="$hourly_total" -v gke="$PHASE3_GKE_CLUSTER_HOURLY_USD" \
    -v nat="$hourly_nat" 'BEGIN { printf "%.9f", hourly + gke + nat }')
maximum_nat_data=$(awk -v gib="$PHASE3_MAX_NAT_DATA_GIB" \
    -v price="$PHASE3_CLOUD_NAT_DATA_USD_PER_GIB" \
    'BEGIN { printf "%.6f", gib * price }')
estimated_session=$(awk -v hourly="$hourly_with_gke" -v hours="$PHASE3_MAX_SESSION_HOURS" \
    -v nat_data="$maximum_nat_data" 'BEGIN { printf "%.6f", (hourly * hours) + nat_data }')
if ! awk -v estimate="$estimated_session" -v ceiling="$PHASE3_MAX_ESTIMATED_SESSION_USD" \
    'BEGIN { exit !(estimate <= ceiling) }'; then
    die "Estimated USD $estimated_session session exceeds the USD $PHASE3_MAX_ESTIMATED_SESSION_USD safety ceiling"
fi

pass "Locked GCP region has complete live pricing: $PHASE3_GCP_REGION"
info "Locked topology hourly estimate: USD $hourly_with_gke (compute $hourly_compute; disk $hourly_disk; GKE $PHASE3_GKE_CLUSTER_HOURLY_USD; NAT fixed $hourly_nat)"
info "Cloud NAT data allowance: $PHASE3_MAX_NAT_DATA_GIB GiB at USD $PHASE3_CLOUD_NAT_DATA_USD_PER_GIB/GiB = USD $maximum_nat_data"
pass "Eight-hour compute, disk, GKE, and Cloud NAT estimate: USD $estimated_session"
