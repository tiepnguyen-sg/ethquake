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
