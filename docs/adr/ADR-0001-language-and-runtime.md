# ADR-0001: Language and runtime

Status: Accepted

## Context

Ethquake needs a standalone measurement tool, Kubernetes integration,
Prometheus instrumentation, deterministic tests, and simple binary
distribution.

## Decision

Use Go for the Ethquake binary and module
`github.com/tiepnguyen-sg/ethquake`.

Distribute one static binary with separate subcommands and internal package
boundaries. The Observer must remain independent of Kubernetes, Kurtosis,
scenarios, and fault injection.

Immediately before installing the toolchain, verify the official Go release
history and pin the latest verified supported Go 1.26.x patch. Record the
selected version and checksum in repository tooling metadata. If the official
source results observed by reviewers differ, show the exact source output and
resolve the discrepancy before selecting a version.

## Alternatives

Python would reduce initial implementation time but weaken static distribution
and alignment with cloud-native infrastructure tooling.

Rust would provide strong correctness guarantees but increase delivery risk
without improving the core measurement problem.

## Consequences

The owner accepts a Go learning cost. Code must remain explicit and idiomatic,
with owned cancellation, bounded concurrency, and minimal dependencies.
