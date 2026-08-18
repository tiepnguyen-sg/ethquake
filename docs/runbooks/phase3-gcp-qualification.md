# Phase 3 GCP qualification runbook

This runbook covers the non-evidence 12-vCPU qualification topology. The runner
keeps the live resources available for five minutes after all automated gates
pass, then tears them down unconditionally.

## Run

Use a new DNS-safe session ID. Supply the dedicated project, billing, budget,
recipient, exact GKE version, and explicit charge authorization through the
shell environment.

```sh
make phase3-gcp-preflight
ETHQUAKE_SESSION_ID=qual-YYYYMMDD-HHMM make phase3-qualification-dry-run
ETHQUAKE_SESSION_ID=qual-YYYYMMDD-HHMM make phase3-qualify
```

The real run is bounded by a 90-minute watchdog. Its artifact directory is
`runs/phase3-qualification-<session-id>/`. A passing
`qualification.json` always contains `evidence_eligible: false`.

The full session transcript is duplicated live to
`runs/phase3-qualification-<session-id>/run.log` from the moment the artifact
root is created. If a session stops without a `qualification.json` or a
`[FAIL]` message on the terminal, read this file first: `tail` it for the last
command attempted. A session that stops mid-run with no `[FAIL]` anywhere in
the log is most likely a transient control-plane connectivity loss (the
runner's public IPv4 changed, or a network blip), not a scenario or assertion
failure; `kubectl_retry` in `phase3.sh` re-checks the runner's IP and retries
once before giving up.

Live account preflight requires 250 GiB of free regional `SSD_TOTAL_GB`: five
40 GiB node disks plus a 50 GiB reserve for dynamically provisioned workload
volumes.

## GCP Console inspection

Select the dedicated project before opening any page. The cluster, VMs, router,
and NAT are ephemeral, so inspect them when the runner prints `Live UI` or
`UI inspection window`.

### Scheduling and restarts

1. Open **Kubernetes Engine > Workloads**.
2. Select cluster `ethquake-p3-<session-id>` and namespace
   `kt-ethquake-p3q-<session-id>`.
3. Confirm each active workload is green/OK and its desired, current, and ready
   counts agree.
4. Open a participant pod and inspect **Container status**. `Restarts` must be
   zero. `Pending`, `Unschedulable`, `CrashLoopBackOff`, `OOMKilled`, or a
   non-zero restart count fails qualification.

The automated scheduling gate additionally proves that each of the four
participant nodes hosts at least three active pods: its co-located EL, CL, and
VC. The authoritative snapshots are `participant-nodes.json`,
`pods-scheduled.json`, `pods-start.json`, and `pods-end.json`.

### Node pressure and private-node state

1. Open **Kubernetes Engine > Clusters > ethquake-p3-<session-id> > Nodes**.
2. Confirm five Ready nodes: one `e2-standard-4` and four
   `n2-custom-2-16384` nodes.
3. Open each node and inspect **Conditions**. `MemoryPressure`, `DiskPressure`,
   and `PIDPressure` must be false.
4. Open **Compute Engine > VM instances**. The five `gke-ethquake-p3-...` rows
   must have a blank **External IP** column.

The exact machine and external-IP checks are captured in `instances.json`; node
conditions are in `nodes-start.json` and `nodes-end.json`.

### Head and finality progress

GCP Console does not understand Ethereum head slots or finalized epochs. During
the live window, open the runner-forwarded Grafana at
`http://127.0.0.1:13000` for a visual protocol-health view. Treat it as a
supporting display, not the pass/fail source.

**Known issue (accepted, not fixed):** Grafana's dashboard UI currently fails
with `Failed to load home dashboard` / HTTP 500 on
`/api/dashboards/home`. The `ethereum-package`-provisioned dashboard JSON files
under `/dashboards` are mounted mode `600` owned by `root`, but the Grafana
image's server process runs as its default non-root user and cannot open
them. This is an upstream `ethereum-package`/Kurtosis file-mounting issue, not
an ethquake bug, and does not affect any qualification or evidence gate — all
of them read the Beacon API and cAdvisor directly. Use the Beacon API
endpoints and `kubectl` directly (as this runbook already does throughout) for
live visual inspection instead of the Grafana dashboard list.

A local workaround (patching the Grafana pod's `securityContext` to run as
root) was considered and rejected: Kurtosis's Kubernetes backend represents
this service as a bare Pod, not a Deployment, so `securityContext` cannot be
patched on the running Pod; the only way to change it is to delete and
recreate the Pod outside Kurtosis's own bookkeeping, which risks breaking
`kurtosis enclave rm` and the teardown guarantee this project treats as
non-negotiable (§10). The owner decided the dashboard UI is not worth that
risk. Fixing this upstream in `ethereum-package` remains the intended
long-term path, deferred until after the current build work.

The automated gate queries all four standard Beacon APIs before and after the
eight-minute observation interval. For every client, both the head slot and the
finalized epoch must increase. Compare `consensus-start.tsv`,
`consensus-end.tsv`, and `consensus-progress.tsv` for the exact values.

### CPU and throttling

Open **Monitoring > Metrics Explorer** and chart Kubernetes container CPU usage
or limit utilization for the qualification cluster and namespace. This is a
useful visual check for sustained saturation, but it is not the exact gate.

The exact gate reads kubelet cAdvisor CFS counters at both ends of the
observation interval. For each participant container it calculates:

```text
delta(container_cpu_cfs_throttled_periods_total)
------------------------------------------------
delta(container_cpu_cfs_periods_total)
```

The maximum allowed ratio is `0.25`. Inspect
`cpu-throttling-ratios.tsv`; column two is the calculated ratio. Missing
counters, counter resets, fewer than twelve containers, or fewer than four
participant nodes fails closed.

### Cloud NAT egress

1. Open **Network Services > Cloud NAT** and select
   `ethquake-p3-<session-id>-nat`.
2. Confirm the NAT is attached to
   `ethquake-p3-<session-id>-router`, uses automatic IP allocation, and covers
   all subnet IP ranges.
3. Open **Logging > Logs Explorer** and use:

```text
resource.type="nat_gateway"
resource.labels.gateway_name="ethquake-p3-<session-id>-nat"
```

The runner creates a pod pinned to the private system node. The pod must resolve
DNS and reach a public HTTP endpoint, then finish with phase `Succeeded`.
`nat-egress-pod.json` records the completed pod. `cloud-nat.json` records the
allocation and logging policy.

NAT logs can take several minutes to appear. The completed egress pod plus the
absence of node external IPs is the immediate automated proof.

## Local verification

Run these before a paid qualification:

```sh
make phase3-runner-test
make phase3-gcp-preflight-test
make verify
make phase3-preflight
make phase3-gcp-preflight
```

Expected results are `[PASS]` throughout. The live account preflight must also
report empty GKE, VM, disk, address, VPC, firewall, and Cloud Router inventory.

After teardown, GCP Console must show no cluster, VM, disk, address, router,
NAT, VPC, subnet, firewall, NEG, health check, backend service, URL map, target
proxy, or forwarding rule in the dedicated project. NAT logs and billing
telemetry can remain after the resources are deleted.
