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
preflight. Report the cheapest comparable region, but keep the owner-selected
fixed location in Toronto when its capacity and cost ceiling pass. The locked
region is `northamerica-northeast2`, with `northamerica-northeast2-a` as its
zone. Once a session creates
its first resource, every regional or zonal resource for all six runs must stay
in that region and zone; a price change cannot move part of an evidence session.

Global control-plane resources such as the project, billing link, budget, and
quota preferences are unavoidable exceptions to the regional-location rule.
Canonical evidence remains off-cluster on the runner host; no Cloud Storage
bucket is required.

Do not use the multi-region auto-mode default VPC. The runner creates an owned
custom-mode VPC with one subnet in the locked region, binds the cluster
to that subnet, and deletes both after cluster teardown. The automatically
created default VPC and its firewall rules were removed while the project had
no runtime resources.

Use the explicitly supplied dedicated billing account and require the
project-scoped gross monthly budget `ethquake-phase3-gross-vnd-5500000` to
exist with its exact VND 5,500,000 amount and alert thresholds before
provisioning. This alert ceiling does not replace the lower USD 100 total
project cost target.

Do not reduce participant resources merely to fit quota. A smaller topology is
eligible only as a separately reviewed resource-policy revision with a
non-evidence qualification run that detects scheduling failure, loss of
head/finality progress, OOMs or restarts, node pressure, excessive CPU
throttling, and loss of outbound connectivity.

## 2026-08-18 resource-policy amendment

The owner authorized qualification of a 12-vCPU topology after the original
quota requests were denied. Use one on-demand `e2-standard-4` system node and
four on-demand `n2-custom-2-16384` participant nodes. Each participant retains
16 GiB memory and co-locates its EL, CL, and VC. Their CPU requests are revised
from 1000m/1000m/250m to 700m/700m/100m; limits remain unchanged. The 1500m
aggregate request leaves allocatable CPU for GKE system DaemonSets, while the
unchanged limits deliberately retain CPU overcommit for qualification.

The owner explicitly retained Toronto after the live on-demand price comparison
reported a cheaper region. Price preflight therefore requires complete live
Toronto pricing and the locked cost ceiling, while reporting but not selecting
other regions. Toronto exposes both locked machine types and GKE
`1.36.3-gke.1537000`; regional quotas cover the complete topology.

Each node uses a 40 GiB `pd-balanced` boot disk. Account preflight checks the
matching regional `SSD_TOTAL_GB` quota for all five boot disks plus a 50 GiB
workload reserve. This prevents node disks from exhausting the quota needed by
Kurtosis dynamic PVCs.

Fulu is active from genesis in the pinned ethereum-package revision. The first
participant is the single explicit PeerDAS supernode; static preflight rejects
zero or multiple supernodes in this four-participant topology.

All nodes are private and have no external IP. The owned custom subnet enables
Private Google Access. One owned regional Cloud Router and Public Cloud NAT use
automatic external-IP allocation and full NAT logging. The Kubernetes control
plane retains its public authenticated endpoint so the repository-local runner
can reach it. The runner discovers and strictly validates its current public
IPv4 before provisioning, then supplies only that `/32` to GKE master authorized
networks. `0.0.0.0/0` is prohibited and basic authentication remains disabled.
The unused GKE HTTP load-balancing addon is disabled; runner access to Kurtosis
services uses repository-owned `kubectl port-forward` listeners on loopback
only, so no public forwarding rule, proxy, URL map, or backend is required.

The qualification runner has a 90-minute teardown watchdog, an eight-minute
observation interval, and a five-minute live UI inspection window. It must
verify all of the following before the topology can be considered for a fresh
evidence session:

- every participant has co-located EL, CL, and VC pods in Running/Ready state;
- all four beacon heads and finalized epochs advance during observation;
- no workload container restarts, OOMKills, crash loops, or failed pods;
- every node remains Ready without memory, disk, or PID pressure;
- CFS throttled periods divided by total periods remains at or below 0.25 for
  every measured participant container;
- all five nodes lack external IPs and a private-node pod completes a public
  DNS/HTTP request through Cloud NAT.

Qualification artifacts are explicitly marked `evidence_eligible: false` and
cannot be reused as a control run. Failure blocks evidence execution; thresholds
and requests must not be changed post hoc within the failed session. A passing
qualification permits, but does not itself authorize, a new six-run evidence
session using the same locked resource policy.

## 2026-08-18 evidence topology decision

The owner decided not to wait further on the denied 20-vCPU/16-preemptible
quota request. The 12-vCPU on-demand topology from the resource-policy
amendment above is now the locked Phase 3 evidence topology, not only a
qualification-only configuration. `experiment/phase3.lock.env` already
carries these values for both the qualification and the evidence path; no
lock-file change accompanies this decision.

This does not by itself authorize a six-run evidence session. Per the
resource-policy amendment, a passing qualification on the current runner is
still required first, and the owner separately authorizes the evidence
session itself.

## Consequences

The AWS preflight and fail-safe runner are retired from the allowed workflow and
remain available only in Git history.

The revised topology fits the granted on-demand CPU and address quotas, subject
to the live fail-closed preflight. Provider-independent experiment code remains
reusable. The rejected quota preferences are retained as auditable, non-billable
project configuration and disappear with project deletion.

Relevant pricing sources are the [Cloud Billing Catalog API](https://cloud.google.com/billing/v1/how-tos/catalog-api),
[Spot VM pricing](https://cloud.google.com/spot-vms/pricing), and
[GKE pricing](https://cloud.google.com/kubernetes-engine/pricing).
