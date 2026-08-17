# ADR-0004: Phase 3 methodology

Status: Accepted

## Context

Phase 3 partitions consensus-client P2P traffic for three epochs. The method
must distinguish an invalid injection from a surprising aggregate result and
from attributable cross-client differentiation before any result is observed.

The [Ethereum consensus specification v1.6.0](https://github.com/ethereum/consensus-specs/blob/v1.6.0/specs/phase0/beacon-chain.md#justification-and-finalization)
requires at least two thirds of active effective balance for justification. A
50/50 split therefore predicts no finality progress on either side while the
partition is active. The specification derives the current slot from genesis
time and wall-clock time; a canonical head block slot may lag during empty
slots.

## Decision

Use four participants, with Lighthouse and Teku represented on both sides.
Assign equal validator counts and require an exact 50/50 split by measured
active effective balance. Cut only traffic between the two sets of consensus
client Pods; EL P2P and EL-CL traffic remain outside the fault.

Run three matched control/fault pairs on clean, identically configured
networks. Derive the committed run order by sorting
`SHA-256(seed + ":" + run-id)`. Report every value plus median and range; do
not use an inferential p-value at N=3.

Gate A is valid only when the maximum progress among fault targets is strictly
less than the minimum progress among targets in its matched control, in all
three pairs with complete primary windows. This conservative rule prevents one
healthy target from hiding a broken control or an unaffected fault target.
Failure is an invalid experiment, not a client result.

Gate B predicts zero finalized-epoch progress during every fault window and
recovery on every target after removal. Gate A:YES and Gate B:NO is a
network-level finding even if all clients behave alike.

Gate C requires the same client family to recover at least one slot later on
both partition sides in all three fault repetitions, no matching baseline
skew, exact validator-weight balance, identical resource policy, and
digest-pinned inputs. Otherwise there is no attributable client
differentiation; unresolved confounding makes the result inconclusive rather
than a client finding.

Measure windows in current slots derived from the Beacon genesis time and
runtime `SECONDS_PER_SLOT`. At each current slot, compare each target's
canonical head `(slot, root)`. This covers empty slots while preserving the
standalone Observer's exact same-head-slot comparison. Set
`head_divergence_tolerance_slots` to 1: disagreement must persist into a second
consecutive current slot. This filters a single observation-boundary race and
does not imply that a longer divergence is a protocol violation.

Observe recovery for four epochs after fault removal. Treat absence of recovery
inside that preregistered window as observed right-censoring and a Gate B
prediction mismatch, not as an invented duration.

## Consequences

The scenario contains the exact order, seed, thresholds, structured prediction,
and limitations before execution. Missing measurements or an imbalanced split
invalidate the affected comparison. Reports distinguish network-level and
client-specific conclusions and never label permitted behavior as a client bug.
