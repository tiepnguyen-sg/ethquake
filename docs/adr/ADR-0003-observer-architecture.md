# ADR-0003: Observer architecture

Status: Accepted

## Context

The Observer must measure arbitrary Ethereum networks without depending on
Kubernetes, Kurtosis, fault injection, scenarios, or Ethquake topology.
Measurement failures must remain distinguishable from valid zero values.

## Decision

Expose the Observer as `ethquake observe`. Configure network targets only with
repeatable named Beacon API and execution WebSocket endpoint flags.

Keep protocol clients, polling and lifecycle, telemetry, time-series output,
and CLI composition in separate internal packages. Protocol constants are read
from each Beacon API at startup. Timing constants must agree, and shared fork
epoch fields must not conflict; client-specific future fork fields are retained.
Successful polls produce typed observations; failed polls produce explicit gaps
and self-health metrics.

Prometheus is the live metrics surface. Versioned JSON Lines is the run-scoped
time-series surface. Metrics listen on loopback by default. The assertion engine
consumes observations separately and is never imported by the Observer.

Head comparison records exact same-slot root agreement only. It does not choose
or apply a divergence tolerance. Reorg depth is nullable when subscription
history cannot establish a common ancestor.

Use `client_golang` for Prometheus correctness, `x/sync/errgroup` for owned
concurrency, and `coder/websocket` for execution `newHeads` subscriptions. Pin
all dependencies in `go.mod` and `go.sum`.

## Consequences

Ethquake topology discovery must translate topology into endpoint flags before
starting the Observer. The Observer cannot classify experimental outcomes or
choose scientific thresholds. Replacing a protocol adapter or output sink does
not require changing polling logic.
