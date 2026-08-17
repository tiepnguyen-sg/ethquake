# ADR-0006: Phase 3 cloud provider

Status: Accepted

## Context

The GCP preflight found zero preemptible CPU quota on the Free Trial account.
The owner declined a billable-account upgrade, closed billing, scheduled the
dedicated project for deletion, and directed Phase 3 to AWS. The repository
still contains a GKE-specific runner that must not be mistaken for an
authorized evidence path.

The [AWS Free plan documentation](https://docs.aws.amazon.com/awsaccountbilling/latest/aboutv2/free-tier-plans.html)
does not establish EKS availability. [EKS pricing](https://aws.amazon.com/eks/pricing/)
has a separate per-cluster charge, and the
[documented default Standard Spot quota](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/using-spot-limits.html)
is 5 vCPUs rather than the experiment's required 16 vCPUs. These properties
must be measured against the actual account.

## Decision

Use AWS as the target Phase 3 cloud provider and retire the existing GCP runner
from the allowed workflow.

Do not select EKS or self-managed Kubernetes until a read-only account preflight
verifies plan eligibility, regional capacity and quotas, available Kubernetes
versions, and cost controls. Do not create cloud resources during that
preflight.

The replacement runner must preserve the accepted methodology, dependency
closure, off-cluster evidence, ownership, budget, and unconditional teardown
guards. No evidence session may run until it passes objective preflight and the
owner approves the calculated spend.

## Alternatives

Continuing on GCP was rejected by the owner after the quota preflight required
a billable-account upgrade. Selecting EKS immediately would assume both service
eligibility and unavailable capacity; selecting self-managed Kubernetes before
account inspection would make the same capacity assumption.

## Consequences

The current `make phase3-run` path is retired and undocumented as a runnable
command. Phase 3 implementation is incomplete until the AWS runner replaces it.

The scenario, Observer, fault backend, analysis, reports, and static dependency
preparation remain provider-independent and reusable.
