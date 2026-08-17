# ADR-0007: Phase 3 GCP substrate

Status: Accepted

## Context

AWS account verification remained blocked. The owner created a second dedicated
GCP project, upgraded its billing account to paid status, and authorized GCP for
Phase 3. This new evidence invalidates ADR-0006's account-availability premise.

Phase 3 requires four isolated participant nodes, one system node, immediate
teardown, a verified budget alert, and one fixed location for the complete
six-run evidence session. Spot prices can change daily, so region selection must
use the live Cloud Billing Catalog immediately before the first resource is
created.

On 2026-08-18, the live catalog priced the locked topology of one on-demand
`e2-standard-4`, four Spot `n2-standard-4` VMs, and five 50 GiB balanced
persistent disks at approximately USD 0.265855313 per hour in
`northamerica-northeast2`. Including the USD 0.10 per-hour GKE management fee,
the eight-hour gross estimate is USD 2.926843, excluding network and logging.
The nominally cheaper `us-west8` price was not selectable because that region
was absent from this project's Compute Engine region catalog.

The paid project still has zero preemptible CPU quota, a 12-vCPU global CPU
quota, and four regional in-use addresses. Requests for 16 regional
preemptible vCPUs, 20 global vCPUs, and five regional in-use addresses were not
granted because the new project lacks sufficient usage history. No compute,
storage, address, load balancer, or GKE resource exists.

## Decision

Return Phase 3 to GCP and supersede ADR-0006.

Use a zonal GKE Standard cluster. Before the first billable resource is created,
rerun the live price, availability, version, billing, budget, and quota
preflight. Select the cheapest eligible region for the complete locked topology.
The current candidate is `northamerica-northeast2`, with
`northamerica-northeast2-a` as the single candidate zone. Once a session creates
its first resource, every regional or zonal resource for all six runs must stay
in that region and zone; a price change cannot move part of an evidence session.

Global control-plane resources such as the project, billing link, budget, and
quota preferences are unavoidable exceptions to the regional-location rule.
Canonical evidence remains off-cluster on the runner host; no Cloud Storage
bucket is required.

Do not use the multi-region auto-mode default VPC. The runner creates an owned
custom-mode VPC with one subnet in `northamerica-northeast2`, binds the cluster
to that subnet, and deletes both after cluster teardown. The automatically
created default VPC and its firewall rules were removed while the project had
no runtime resources.

Use the explicitly supplied dedicated billing account and require the
project-scoped gross monthly budget `ethquake-phase3-gross-vnd-5500000` to
exist with its exact VND 5,500,000 amount and alert thresholds before
provisioning. This alert ceiling does not replace the lower USD 100 total
project cost target.

Do not reduce participant resources merely to fit the current quota. The real
runner must remain fail-closed until the locked topology fits granted quota, its
expected session cost is presented, ownership labels and unconditional teardown
are verified, and the owner authorizes the evidence session.

## Consequences

The AWS preflight and fail-safe runner are retired from the allowed workflow and
remain available only in Git history.

Quota approval is the only account-level capacity blocker. Provider-independent
experiment code remains reusable. The rejected quota preferences are retained
as auditable, non-billable project configuration and disappear with project
deletion.

Relevant pricing sources are the [Cloud Billing Catalog API](https://cloud.google.com/billing/v1/how-tos/catalog-api),
[Spot VM pricing](https://cloud.google.com/spot-vms/pricing), and
[GKE pricing](https://cloud.google.com/kubernetes-engine/pricing).
