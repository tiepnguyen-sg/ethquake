# ADR-0005: Experiment dependency closure

Status: Accepted

## Context

The immutable `ethereum-package` 6.1.0 revision still imports Prometheus from
an unversioned branch and invokes helper images through mutable references.
Recording one resolution does not make a later clean run reproducible.

## Decision

Prepare each evidence run from the accepted upstream commit in a disposable
checkout. Apply one reviewed dependency-only patch that pins the imported
Prometheus package and helper images; do not modify Ethereum topology or client
behavior. Verify the checkout revision and patch digest before use.

Require every configured experiment workload and Chaos Mesh image by OCI
digest. Before measurement, compare the running Kubernetes workload image
references with the committed lock and write the resolved metadata into the
off-cluster evidence bundle. A mismatch blocks the run.

This is a reproducible source overlay, not a maintained fork: upstream remains
the source of truth and the overlay contains no functional package changes.

## Consequences

A clean run cannot silently follow `main`, `latest`, or a moved image tag in
the experiment dependency path. Updating the upstream package requires a new
lock, a freshly reviewed patch, and verification before evidence collection.
