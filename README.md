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

- runtime `SECONDS_PER_SLOT`, `SLOTS_PER_EPOCH`, and fork epochs from every
  Beacon API;
- canonical head and finality observations, optimistic-execution state, and
  finality lag in slots;
- exact same-slot head-root comparisons;
- execution `newHeads`, continuity gaps, and reorg depth when it is known;
- explicit measurement gaps and instrument self-health metrics.

Exact same-slot disagreement is a measurement, not an experimental verdict.
Ethquake does not currently choose a `head_divergence_tolerance_slots` default
or apply Gate A/B/C methodology. Unknown reorg depth is emitted as `null`, never
as zero.
