# Ethquake

Ethereum Client Resilience Test Harness.

## Local development

Ethquake uses the Go version and archive checksum in
`toolchain/versions.env`. The repository does not install Go or change shell
configuration. Point `GO` at a matching toolchain when it is not already on
`PATH`:

```sh
make GO=/path/to/go go-preflight
make GO=/path/to/go verify
```

`make test` uses fixtures and requires no cluster. `make test-e2e` is the one
build-tagged integration test against the existing Phase 1 devnet; it owns and
cleans up its loopback-only access processes but does not create the devnet.

## Observer

The standalone Observer needs only named protocol endpoints:

```sh
bin/ethquake observe \
  --beacon lighthouse=http://127.0.0.1:15052 \
  --beacon teku=http://127.0.0.1:15053 \
  --execution geth=ws://127.0.0.1:18546 \
  --execution reth=ws://127.0.0.1:18547 \
  --output observations.jsonl
```

Prometheus metrics listen on `127.0.0.1:9464` by default. Non-loopback metric
listeners and endpoint URLs containing embedded credentials are rejected.
Output files are created with mode `0600` and never overwrite an existing file.

The Observer records:

- runtime `SECONDS_PER_SLOT`, `SLOTS_PER_EPOCH`, fork epochs, genesis time, and
  genesis slot from every Beacon API;
- canonical head and finality observations, optimistic-execution state, and
  finality lag in slots, using wall-clock `current_slot` from Beacon genesis
  time and the genesis header rather than the latest block slot;
- exact same-slot head-root comparisons;
- execution `newHeads`, continuity gaps, and reorg depth when it is known;
- explicit measurement gaps and instrument self-health metrics.

Exact same-slot disagreement is a measurement, not an experimental verdict.
The standalone Observer does not choose a `head_divergence_tolerance_slots`
default or apply Gate A/B/C methodology. Unknown reorg depth is emitted as
`null`, never as zero.

## Phase 3 experiment

The committed `cl-p2p-partition` protocol uses four participants, three paired
control/fault repetitions, an exact validator-effective-balance split, and a
three-epoch CL P2P partition. Its thresholds and run order are preregistered in
the scenario and ADR-0004. Measurement windows use genesis-derived current
slots, so empty block slots remain represented during the partition.

The AWS dry-run validates the committed inputs and prints the unresolved account
gates without requiring AWS credentials or calling a cloud API:

```sh
make phase3-dry-run ETHQUAKE_SESSION_ID=local-check
```

The AWS account preflight is also tested locally against a fake AWS CLI and
committed response fixtures. Once an account is active and an explicit CLI
profile exists, run the real read-only checks with a candidate region:

```sh
AWS_PROFILE=ethquake AWS_REGION=ap-southeast-1 make phase3-aws-preflight
```

This command reads identity, Free-plan state, remaining credits, regional Spot
quota, the EKS version catalog, and the locked USD 20 budget alert. It neither
changes account configuration nor creates resources. EKS read access alone is
not treated as proof that Free-plan cluster creation is allowed.

For a fuller local readiness check, prepare the locked dependencies and verify
the local container, Kubernetes, Helm, port, and scenario prerequisites:

```sh
make phase3-prepare
make phase3-preflight
```

Preparation downloads the official locked Helm archive into the ignored
repository cache and verifies its checksum; it does not install a host tool,
change user configuration, authenticate to AWS, or create a cloud resource.

`make test` also drives the complete committed six-run order against a
deterministic in-process Beacon fixture. It exercises orchestration, fault
lifecycle, checksum-protected evidence loading, Gate A/B/C analysis, and
JSON/Markdown/SVG report generation. These fixture outputs are not Phase 3
experimental evidence.

The fault backend can be exercised locally against a disposable kind cluster:

```sh
make phase3-chaos-e2e
```

This installs the pinned Chaos Mesh chart, verifies a real network partition,
kills the standalone fault runner with `SIGKILL`, then verifies independent TTL
recovery, idempotent reversion, and exact cluster cleanup. It uses a
repository-local kubeconfig and does not modify the retained development
cluster or the global Kubernetes context. This smoke test is not Phase 3
experimental evidence.

The Phase 3 cloud runner is being migrated to AWS under
[ADR-0006](docs/adr/ADR-0006-phase3-cloud-provider.md). `make phase3-run` is a
fail-safe placeholder that exits before any cloud API call. No cloud experiment
is authorized until the AWS account-specific preflight passes and provisioning,
cost controls, and teardown are implemented and verified.

The replacement preflight must verify the account plan, service eligibility,
regional capacity for the required 16 vCPUs, Kubernetes version, budget alerts,
ownership tags, session-local kubeconfig, and teardown trap before creating a
cluster. The evidence-session workflow will continue to install the pinned
Chaos Mesh chart, execute the committed run order, preserve evidence under
`runs/`, and analyze Gate A then B then C.

The preregistered
[Phase 3 publication outline](docs/publication/phase3-writeup.md) fixes the
claim boundaries and evidence placeholders before results exist. It is not an
experiment report and must not be populated from local smoke tests.
