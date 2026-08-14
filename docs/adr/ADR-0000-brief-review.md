# ADR-0000: Project brief review

Status: Accepted

## Context

The v4 brief review found two unresolved methodological gaps.

Gate B asks whether observed network behavior matched the prediction, but the
scenario contains only free-text prediction text. This makes the Gate B result
open to post-run interpretation.

The brief also does not state clearly whether the required repetitions apply
to both control and fault runs, so baseline variance may be under-measured.

## Decision

Before Phase 3, scenarios will gain a structured preregistered primary
prediction alongside the explanatory free text.

Control and fault conditions will both be replicated sufficiently to compare
their distributions. The concrete repetition count, run ordering, pairing,
and statistical method will be decided in a separate methodology ADR before
Phase 3.

`head_divergence_tolerance_slots` and the deterministic Gate A effect rule
remain open. Phase 1 will not depend on either value.

## Consequences

Gate B cannot be classified from an interpretation written after seeing the
data.

Phase 1 remains focused on the devnet substrate and does not prematurely fix
the experimental design.
