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
on the node itself. Anywhere else they return `unavailable` with the reason
"run on node". v1 does not use `kubectl debug`, `kubectl exec`, or SSH.

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

Redaction is on by default; `--no-redact` opts out.

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
