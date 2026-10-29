# dra-inspect Project Design

## Goal

Provide a read-only command-line tool for Kubernetes Dynamic Resource
Allocation (DRA) administrators, driver developers, and CI systems.

The tool explains what drivers published, what the scheduler allocated, and
whether topology, host integrations, and KubeVirt expectations are correct.

## Scope

In scope:

- ResourceSlice, DeviceClass, ResourceClaim, Pod, Node, and optional VMI inspection.
- NUMA, PCIe-root, capacity, and allocation checks (`check alignment`, `counters`, `capacity`, `topology`).
- Offline snapshot capture and replay.
- Optional VFIO, CDI, CPU-pinning, KubeVirt, and shared-counter adapters.
- Human-readable tables and versioned JSON output.
- Workstation/CI execution and an optional read-only in-cluster Job.

Out of scope:

- Scheduling, admission, or DRA driver control-plane behavior.
- Resource mutation, repair, or automatic remediation.
- Composite-device inspection in v1.
- Commands that need `pods/exec`, SSH, or `kubectl debug` (`inspect metadata`
  and `inspect guest` are dropped in v1).
- A long-running API server or UI.

## Decisions

- Repository: `github.com/johnahull/k8s-dra-inspect`.
- Binary: `dra-inspect`.
- Implementation language: Go 1.26 or newer.
- CLI framework: Cobra.
- API target: `resource.k8s.io/v1` only, Kubernetes 1.34 or newer, client-go
  v0.34 or newer. Older API versions are not supported.
- Default output: terminal tables; automation output: versioned JSON.
- Deployment: standalone binary or OCI image; optional read-only Kubernetes Job.
- RBAC: cluster read-only profile plus a namespace-scoped workload profile.
- Snapshots are a first-class v1 feature.
- The command hierarchy is reorganized in v1; the legacy shell command names
  are migration references, not compatibility requirements.
- Host and KubeVirt checks are optional adapters and are never required for
  ordinary cluster inspection.

## Execution Model

Core commands use only the Kubernetes API.

Host adapters (`check cpupinning`, `check vfio`, CDI discovery) read node files
below `--host-root` (default `/`) and are meaningful only when the binary runs
on the node itself. They are read-only: no writes, no external commands, no
sudo, no `/proc`, and symlinks are never followed out of the tree. Both checks
take `--host-root` and `--node-name` (default `$NODE_NAME`, then the hostname);
no other command has these flags, and they cannot be combined with `--from`
(usage error). `nodeName` in the evidence is the resolved cluster node name:
`--node-name`, then `$NODE_NAME`, then `os.Hostname()` as a last resort (Jobs
should set `--node-name` or `$NODE_NAME` from the downward API). That one
resolved string is the only node identity stored. A check returns `unavailable`
when the evidence is from a different node than the pod or device; devices on
other nodes collapse into one result per node. Every scanned directory must
resolve (symlinks included) to a path inside the root; `cdiPresent` in the vfio
output is informational. The cpuset a pod is pinned to is the union of its
container cgroups' `cpuset.cpus.effective` (the pod-level file only inherits the
parent's CPUs); evidence `version` is 2 and version 1 is still read.

A live host check collects evidence in memory and attaches it to the snapshot
as `Evidence["host"]` before running the pure check. Evidence is redacted by
the producer: CDI specs keep only kind, device names and counts (never env,
mounts, annotations or hooks). `--from` uses the evidence the snapshot carries;
without any, the result is `unavailable` ("run on the node"). `snapshot
capture` does not collect adapter evidence unless `--with-adapters` is given;
`report support` always runs the adapters. v1 does not use `kubectl debug`,
`kubectl exec`, or SSH.

The KubeVirt adapter makes no API calls: it derives evidence
(`Evidence["kubevirt"]`, version 1) from the VMIs the core collector already put
in the snapshot, keeping only namespace, name, node, phase, claim aliases and
names, host devices, guest NUMA passthrough, dedicated CPU placement and CPU
topology (never volumes, cloud-init, credentials, annotations, interfaces or
guest info). It is unavailable when the VMI collection is not ok (for example
`kubevirt.io/v1 not served`). `check kubevirt [VMI]` maps each VMI host device
to its claim's allocation result and reports a missing claim or allocation, a
device on another node than the VMI (fail), and devices on two or more NUMA
nodes without `guestMappingPassthrough` (warn).

`report support` runs the `report cluster` members plus `check.vfio`,
`check.kubevirt` and one `adapter.<name>` result per adapter (host, kubevirt):
`pass` when its evidence was collected, `unavailable` with the reason
otherwise; an unavailable adapter never blocks the other results.
`check.cpupinning` needs a pod and is not part of it. A live run attaches
evidence to the in-memory snapshot first (`--host-root`, `--node-name` as on the
host checks); `--from` replays the evidence stored in the snapshot, and
`snapshot capture --with-adapters` is what stores it.

A global `--from FILE` replaces the live Kubernetes source with a loaded
snapshot for every `inspect`, `check`, and `report` command.

## Topology Attributes

Attribute keys are resolved in this order; the first key present on a device
wins.

| Meaning | Standard key | Vendor fallbacks |
|---|---|---|
| NUMA node | `resource.kubernetes.io/numaNode` | `numaNode`, `numa`, `numaNodeID` |
| PCIe root | `resource.kubernetes.io/pcieRoot` | `pcieRoot` |

A list-valued attribute is a set. A constraint holds when the intersection of the sets across all devices in its
scope is non-empty (not pairwise).
`cpuSocketID` is not a standard attribute and is not interpreted by v1.

## Results, Status, and Exit Codes

Every inspector and check returns `[]Result`. A `Result` has a status of `pass`,
`warn`, `fail`, `unavailable`, or `error`.

| Condition | Status |
|---|---|
| A claim `matchAttribute` constraint is violated | `fail` |
| Devices span NUMA nodes | `warn` |
| Devices span PCIe roots | informational; `fail` only if the claim has a `matchAttribute` constraint on pcieRoot |
| Required permission or adapter data is missing | `unavailable` |
| Collection or internal failure | `error` |

| Exit code | Meaning |
|---|---|
| 0 | all results `pass` or `warn` |
| 1 | at least one `fail` |
| 2 | at least one `error` |
| 3 | at least one `unavailable`, only with `--strict` |

Precedence when several apply: error (2), then fail (1), then unavailable with
`--strict` (3), then 0. Cobra usage errors also exit 2.

### Check coverage

v1 does not evaluate: `distinctAttribute` constraints, derived attributes
(alpha `derivedAttributes` on a request), and capacity `requestPolicy` `step`,
`validValues`, and `default` (only `validRange` min/max are validated).
`distinctAttribute` and derived attributes are reported as `unavailable`.

A claim's status is the worst of its findings: error, then fail, then
unavailable, then warn, then pass.

## JSON Output

Every JSON document is an envelope:

```json
{"schemaVersion": "dra-inspect/v1alpha1", "generatedAt": "...", "source": "live|snapshot", "results": []}
```

Within one `schemaVersion`, changes are additive only. Resource names are exact
(never abbreviated). Golden files live in `testdata/golden/`.

## Snapshots and Redaction

Redaction is on by default; `--no-redact` opts out and prints a warning that
the file may contain sensitive data.

- The snapshot file is the JSON of `model.Snapshot`, with lists sorted so the
  same cluster state yields identical bytes. `snapshot capture --file FILE`
  (`-f`; `-o/--output` is the output format) writes it with mode 0600 through a
  temp file and rename, refuses to overwrite an existing file without
  `--force`, and writes to stdout when `--file` is omitted. Collections that
  were not readable are listed on stderr; capture still exits 0 (3 with
  `--strict`).
- Replay (`--from FILE`) rejects files without a `schemaVersion` or with an
  unsupported one, and cannot be combined with `--kubeconfig` or `--context`.
  With `-n NS` it filters claims, pods and VMIs to NS and fails if the file was
  captured for a different namespace.

- Drop `config[].opaque.parameters` from DeviceClasses and ResourceClaims.
- Drop `status.allocation.devices.config[].opaque.parameters` and
  `status.devices[].data` from ResourceClaims.
- Drop `metadata.annotations["kubectl.kubernetes.io/last-applied-configuration"]`
  and `managedFields` from all objects.
- For VirtualMachineInstances, drop inline `cloudInitNoCloud` and
  `cloudInitConfigDrive` data (`userData`, `userDataBase64`, `networkData`,
  `networkDataBase64`) from `spec.volumes[]` and delete `spec.accessCredentials`
  (the same under `spec.template.spec` when present). Secret references
  (names only) stay.
- Nodes are reduced to an allow-list: `metadata.name`, `metadata.labels` and
  `status.allocatable`. Everything else is dropped: all annotations, UID and
  resourceVersion, `status.addresses` (IPs and hostnames), `status.nodeInfo`
  (machineID, systemUUID, bootID), `status.images`, `status.volumesInUse`,
  `status.volumesAttached`, `status.daemonEndpoints`, conditions, capacity,
  taints, `spec.providerID`, `spec.podCIDR` and `spec.podCIDRs`.
- VirtualMachineInstances additionally lose `status.interfaces`,
  `status.guestOSInfo`, `status.volumeStatus`, and
  `spec.domain.firmware.uuid` and `.serial` (also under `spec.template.spec`).
  `status.nodeName` stays.
- Intentionally kept, because replay needs it and it is not secret on its own:
  DeviceClass and ResourceClaim CEL selector text (user-facing inspect output
  hides selector text, but the snapshot file contains it), `reservedFor` pod
  names, Node labels, and VMI spec devices (GPUs, host devices). Review a
  snapshot before sharing it outside the team that owns the cluster.
- `Read` accepts at most 256 MiB (larger input fails with
  `snapshot larger than 256 MiB`), rejects any data after the snapshot object
  (`unexpected data after snapshot`; trailing whitespace is fine), and rejects
  a `collection` entry whose `state` is not `ok`, `forbidden`, `notFound` or
  `error`. An absent or null `collection` is allowed.
- `evidence` is not touched by redaction: adapter producers must redact their
  own evidence before storing it in the snapshot.
- A namespace-scoped run records `namespace` in the snapshot and marks the
  resourceclaims and pods collection entries `ok` with reason
  `namespace <ns> only`.
- Keep Pods only as name, namespace, uid, nodeName, and resourceClaims. Never
  capture env, annotations, or managedFields.
- Never capture kubeconfig contents.

## Terminal Output

Color is disabled by `--no-color`, by the `NO_COLOR` environment variable, and
when stdout is not a terminal.

## RBAC

| API group | Resource | Verbs | Cluster profile | Namespace profile |
|---|---|---|---|---|
| `resource.k8s.io` | resourceslices | get, list | yes | no (reported `unavailable`) |
| `resource.k8s.io` | deviceclasses | get, list | yes | no (reported `unavailable`) |
| `resource.k8s.io` | resourceclaims | get, list | yes | yes |
| core | pods | get, list | yes | yes |
| core | nodes | get, list | yes | no (reported `unavailable`) |
| `kubevirt.io` | virtualmachineinstances | get, list | optional | optional |

No profile grants a mutating verb or `pods/exec`.
