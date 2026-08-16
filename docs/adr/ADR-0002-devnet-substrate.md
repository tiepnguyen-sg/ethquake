# ADR-0002: Devnet substrate

Status: Accepted

## Context

Phase 1 requires a Kubernetes devnet with at least two EL and two CL
implementations, Prometheus, Grafana, advancing finality, and one-command
startup. Phase 2 must reuse it without infrastructure cost.

## Decision

Use a local kind cluster with the Kurtosis Kubernetes backend. Consume
ethPandaOps `ethereum-package` at an immutable verified revision; never fork it.

Start with two client pairs covering two EL and two CL implementations,
targeting Geth/Lighthouse and Reth/Teku subject to verified Apple-silicon image
availability. Enable Prometheus and Grafana and commit an Ethquake finality
dashboard.

Automation will target only the named Ethquake cluster and enclave. It will
reject unexpected Kubernetes contexts and public Ethereum networks and provide
idempotent teardown.

For local access, the official Kurtosis gateway runs inside a constrained
container. Its only host exposure is `127.0.0.1:9710`, and Kurtosis CLI control
operations run inside that container. Docker provides process isolation only;
Kubernetes remains the selected Kurtosis backend. Kurtosis dynamic user-service
ports are not published to the host.

Ethquake automation discovers target Services from the enclave namespace and
stable labels, then creates explicit Kubernetes port-forwards bound to
`127.0.0.1` using the repository-local kubeconfig. The initial allowlist is the
Beacon API, Prometheus, and Grafana. Every forward has explicit ownership,
lifecycle, and cleanup. Wildcard listeners are prohibited.

Phase 1 and Phase 2 accept the upstream package's transitive mutable references
as a documented limitation. No Phase 3 evidence run may begin until the full
experiment dependency closure is reproducible: the package and its imports are
immutable, runtime images are digest-pinned, resolved metadata is included in
the evidence bundle, and a clean run cannot silently resolve different inputs.

## Alternatives

Docker-only Kurtosis does not satisfy the Kubernetes requirement.

A hand-built devnet duplicates `ethereum-package`.

Using GKE during Phase 1 adds cost and forgotten-cluster risk without improving
the acceptance result.

## Consequences

The local substrate costs nothing and supports Phase 2 development, but its
results are never presented as experimental findings.

Kurtosis gateway lifecycle, image availability, and container digests must be
verified during the next increments.

Local Kubernetes service access is intentionally managed by Ethquake automation
instead of Kurtosis dynamic host port publication.

The Phase 1 run resolved `protolambda/eth2-val-tools:latest` to
`sha256:46147228f291266148a6a21a2b9541367ad5f70e619d79cd5393459baf539f58`
and `badouralix/curl-jq:latest` to
`sha256:1e7c0284e24572ace7170df9fc91f15fd3b79ebf056d4dde17244d5d74bbfabc`.
These observations preserve what ran; they do not pin future resolution.
