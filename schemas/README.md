# Public contract policy

Ethquake schemas use explicit alpha versions. Unknown fields are rejected by
both the schema and the runtime decoder.

Within one version, changes are additive and preserve the meaning of existing
fields. Removing a field, changing its type or meaning, or tightening a value
that valid producers may emit requires a new schema version. Deprecated fields
remain readable for at least one subsequent version and are identified here
before removal.

There are no deprecated fields in `v1alpha1`.

Phase 3 writes one `ethquake.run/v1alpha1` manifest per committed run under
`<evidence-root>/runs/<run-id>/`. The aggregate
`ethquake.report/v1alpha1` document and its Markdown/SVG renderings live under
`<evidence-root>/analysis/<scenario-name>-analysis/`. Session metadata and the
dependency lock are stored at the evidence root. The root is host-owned and
must not share the lifecycle of the experiment cluster.

Each run's `metadata.json` follows `ethquake.metadata/v1alpha1`, and its
`realized-split.json` follows the standalone realized-split schema. Before
analysis, Ethquake verifies these contracts and their semantic consistency with
the checksum-protected manifest and executed scenario.
