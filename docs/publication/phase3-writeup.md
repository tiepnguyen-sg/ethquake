# Ethquake Phase 3 publication outline

Status: Preregistered outline; no Phase 3 evidence session has run.

This document is an editorial skeleton, not an experiment report. Replace a
`[PENDING EVIDENCE]` marker only from the generated, checksum-verified Phase 3
evidence bundle. Local fixtures, unit tests, dry-runs, and the two-client
development devnet must never be presented as experimental findings.

## Working title

How Ethereum consensus clients recover from a three-epoch 50/50 P2P partition

## Abstract

- Problem: operators need reproducible evidence about client behavior during
  consensus-layer network isolation without treating permitted protocol
  behavior as a client bug.
- Method: run three preregistered control/fault pairs on four mixed-client
  participants, split active validator effective balance exactly 50/50, and
  cut only consensus-client P2P traffic for three epochs.
- Aggregate result: [PENDING EVIDENCE]
- Recovery result: [PENDING EVIDENCE]
- Gate outcome and bounded conclusion: [PENDING EVIDENCE]

## Claim boundary

The publication must follow the generated Gate A, then B, then C outcome.

| Gate path | Allowed conclusion |
|---|---|
| Gate A: no | Invalid experiment; fix the validity failure and rerun. |
| Gate A: yes; Gate B: no | Valid network-level finding that did not match the preregistered prediction. |
| Gate A: yes; Gate B: yes; Gate C: no | Prediction matched; no attributable client-family differentiation. |
| Gate A: yes; Gate B: yes; Gate C: yes | Attributable client-family differentiation under the recorded conditions. |

None of these paths, by itself, establishes a client bug or protocol defect.

## Research question

Under an exact 50/50 active-effective-balance split, does a three-epoch
consensus-layer P2P partition produce the preregistered loss and recovery of
finality, and do Lighthouse and Teku recover differently under controlled,
replicated conditions?

## Preregistered prediction

Neither side has the two-thirds active effective balance needed to justify new
checkpoints. Finalized-epoch progress is therefore predicted to remain zero on
all four targets during every fault window, then resume on every target after
connectivity is restored.

## Experimental design

| Property | Preregistered value |
|---|---|
| Chain ID | `3151908` |
| Participants | Four; 32 validators each |
| Client pairs | Geth/Lighthouse and Reth/Teku, each represented on both sides |
| Partition | Consensus-client P2P traffic only |
| Requested split | Exact 50/50 by active validator effective balance |
| Fault duration | Three epochs |
| Recovery window | Four epochs |
| Repetitions | Three matched control/fault pairs |
| Run order | `fault-3`, `control-2`, `control-1`, `fault-2`, `control-3`, `fault-1` |
| Head divergence tolerance | One current slot |

The evidence report must add the realized split, exact client image digests,
runtime chain constants, resource policy, node placement, AWS region,
Kubernetes substrate and version, instance types, and execution timestamps.

## Measurement model

- Derive current slots from Beacon genesis time and runtime
  `SECONDS_PER_SLOT`; do not substitute the latest block slot.
- Record finalized checkpoints, canonical head `(slot, root)`,
  optimistic-execution state, execution `newHeads`, continuity gaps, reorg
  depth when known, and explicit measurement gaps.
- Treat unknown reorg depth as unknown, never zero.
- Treat recovery outside the four-epoch observation window as right-censored
  and as a prediction mismatch, not as an invented duration.

## Analysis

### Gate A — validity

For every complete matched pair, the maximum finalized-epoch progress among
fault targets must be strictly lower than the minimum progress among control
targets. All three pairs must pass.

Outcome: [PENDING EVIDENCE]

### Gate B — preregistered surprise

After Gate A passes, compare the evidence with zero finalized-epoch progress
during every fault window and recovery on every target after removal.

Outcome: [PENDING EVIDENCE]

### Gate C — attributable differentiation

After Gates A and B pass, require the same client family to recover at least one
slot later on both partition sides in all three repetitions, with no baseline
skew, exact validator-weight balance, equal resource policy, controlled
placement, and digest-pinned dependencies.

Outcome: [PENDING EVIDENCE]

## Results

### Environment

| Field | Evidence value |
|---|---|
| Source commit | [PENDING EVIDENCE] |
| AWS account plan | [PENDING EVIDENCE] |
| AWS region | [PENDING EVIDENCE] |
| Kubernetes substrate/version | [PENDING EVIDENCE] |
| Participant instance type | [PENDING EVIDENCE] |
| Runtime images | [PENDING EVIDENCE] |
| Realized validator split | [PENDING EVIDENCE] |

### Conservative finality progress

| Repetition | Control minimum | Fault maximum | Conservative difference |
|---:|---:|---:|---:|
| 1 | [PENDING] | [PENDING] | [PENDING] |
| 2 | [PENDING] | [PENDING] | [PENDING] |
| 3 | [PENDING] | [PENDING] | [PENDING] |

Control median and range: [PENDING EVIDENCE]

Fault median and range: [PENDING EVIDENCE]

### Recovery

| Consensus client | Conservative values by repetition | Median | Range |
|---|---|---:|---:|
| Lighthouse | [PENDING] | [PENDING] | [PENDING] |
| Teku | [PENDING] | [PENDING] | [PENDING] |

Measurement gaps and censoring: [PENDING EVIDENCE]

### Figures

1. Paired control-minimum versus fault-maximum finalized-epoch progress. Use
   the generated `finality-progress.svg`; do not redraw values manually.
2. Recovery timeline by client, side, and repetition: [PENDING EVIDENCE]
3. Measurement-health or gap panel if any gap occurred: [PENDING EVIDENCE]

## Interpretation

State the generated conclusion first, then describe only effects visible in the
released evidence. Separate aggregate network behavior from client-family
differentiation. Discuss surprising behavior even when clients behave alike,
and describe confounding rather than converting an inconclusive comparison into
a finding.

Interpretation: [PENDING EVIDENCE]

## Limitations

- The experiment models one isolated four-participant devnet, not the public
  Ethereum network or arbitrary Internet partitions.
- It isolates consensus-layer P2P traffic; execution-layer P2P and Engine API
  traffic are outside the fault.
- Three repetitions support descriptive medians and ranges, not an inferential
  p-value.
- Conclusions are bounded to the recorded client versions, fork schedule,
  topology, resources, cloud capacity, and Chaos Mesh implementation.
- A valid difference is not automatically a client bug.
- Local development and smoke results validate tooling only and are excluded
  from the evidence analysis.

## Reproduction and evidence release

Before publication, release or checksum-reference:

- the executed `scenario.yaml` and source commit;
- `phase3.lock.env` and `dependencies.lock.json`;
- all resolved runtime image digests and imported package revisions;
- six run directories containing `manifest.json`, `metadata.json`,
  `checksums.json`, `realized-split.json`, and `raw-timeseries.jsonl`;
- `report.json`, `report.md`, and `finality-progress.svg`;
- the account-preflight result with account identifiers redacted;
- exact reproduction and teardown commands after the AWS runner is accepted.

The canonical evidence remains off-cluster. Do not publish credentials,
account IDs, endpoint tokens, kubeconfigs, payment information, or signed URLs.

## Acknowledgements and outreach

Acknowledgements: [PENDING]

Upstream outreach is intentionally deferred until a credible result exists or
the Phase 5 publication milestone begins, whichever comes first.
