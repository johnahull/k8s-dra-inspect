# dra-inspect Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `dra-inspect`, a read-only DRA administrator/developer diagnostics CLI with live inspection, validation checks, and offline snapshots.

**Architecture:** A Go/Cobra CLI collects typed Kubernetes DRA objects into normalized models, runs independent inspection/check analyzers, and renders tables or versioned JSON. Optional host and KubeVirt adapters are isolated from the core Kubernetes collector so normal cluster inspection never requires privileged access.

**Tech Stack:** Go 1.26+, Cobra, client-go v0.34+ (`resource.k8s.io/v1` only, Kubernetes 1.34+), typed fixture snapshots, GoReleaser, GitHub Actions, optional Helm Job manifests.

**Spec:** `docs/superpowers/specs/2026-10-29-dra-inspect-design.md`

## Global Constraints

- Repository/module: `github.com/johnahull/k8s-dra-inspect`.
- Binary name: `dra-inspect`.
- Default behavior is read-only; no command mutates Kubernetes resources.
- Default output is a human-readable table; `-o json` is versioned and stable.
- Snapshots redact secrets, credentials, and opaque driver parameters by default (rules in the spec).
- Composite-device inspection is excluded from v1.
- `inspect metadata` and `inspect guest` are excluded from v1 (they need `pods/exec` or SSH).
- `cpuSocketID` is not a standard attribute and is not interpreted.
- Host checks run only on the node (`--host-root`), return `unavailable` elsewhere, and are not part of the default Job.
- Shared types (`Result`, `Snapshot`, `Evidence`) live in `internal/model` and are created in Tasks 1-2.
- Cluster RBAC is read-only; namespace mode must not silently claim cluster-wide coverage.

## Review Focus

- DRA list attributes and consumable capacity must survive collection and JSON rendering; test list-valued attributes and capacity values.
- Cluster-scoped and namespace-scoped RBAC must produce an explicit “unavailable” result rather than partial or misleading topology claims; test both profiles.
- Long names and Unicode metadata must render without corrupting tables; test terminal output separately from exact JSON.
- Snapshot replay must be deterministic and independent of live API access; test capture, redaction, and replay.
- Optional adapters must fail clearly without blocking core inspection; test absent host files, missing KubeVirt APIs, and unavailable node access.

## Current `dra-verify.sh` Surface

| Current command/option | Decision for `dra-inspect` | New command or behavior |
|---|---|---|
| `slices` | Keep | `inspect slices` |
| `topology` | Keep | `inspect topology`; groups by NUMA node and PCIe root only |
| `drivers` | Keep | `inspect drivers` |
| `attributes` | Keep | `inspect attributes`; use `--all-attributes` or `--include-capacity` |
| `driverinfo` | Merge | `inspect drivers --attributes`; no standalone command |
| `deviceclasses` | Keep | `inspect deviceclasses` with explicit filters |
| `composite` | Drop | No v1 replacement |
| `claims` | Keep | `inspect claims` |
| `alignment` | Keep | `check alignment` |
| `cpupinning` | Keep as optional | `check cpupinning` |
| `counters` | Keep as optional | `check counters` |
| `vfio` | Keep as optional | `check vfio` |
| `metadata` | Drop | No v1 replacement (needs `pods/exec`) |
| `guest` | Drop | No v1 replacement (needs SSH; the `sshpass` fallback is never carried over) |
| `all` | Drop | `report cluster` and `report support` profiles |
| `help` | Keep | Cobra help |
| `completions` | Keep | `completion bash`, `completion zsh`, `completion fish` |
| `-n`, `--namespace` | Keep | Global namespace filter |
| `-v`, `--verbose` | Keep | Command-scoped verbosity |
| `-a`, `--all` | Drop as global | Explicit command flags such as `--all-attributes` |
| `-t`, `--simple` | Drop | `inspect topology --compact` |
| `-h`, `--help` | Keep | Standard Cobra help |
| Positional DeviceClass filters | Replace | a generic label `--selector` and `--partition` |
| Positional Pod/VM targets | Keep | Typed command-specific positional arguments |
| `source dra-verify.sh` | Drop | Standalone binary only |
| `eval ... completions` | Drop | Cobra completion generation |

## Target Command Surface

```text
dra-inspect inspect slices
dra-inspect inspect topology
dra-inspect inspect drivers [--attributes]
dra-inspect inspect attributes [--all-attributes] [--include-capacity]
dra-inspect inspect deviceclasses [--selector LABEL_SELECTOR]
dra-inspect inspect claims [--namespace NS]

dra-inspect check alignment [POD|CLAIM] [--namespace NS]
dra-inspect check cpupinning POD [--namespace NS] [--host-root PATH]
dra-inspect check counters
dra-inspect check capacity
dra-inspect check topology
dra-inspect check vfio [--host-root PATH]

dra-inspect report cluster
dra-inspect report support

dra-inspect snapshot capture [--file FILE] [--no-redact] [--force]
dra-inspect completion bash|zsh|fish
```

Replay: any `inspect`, `check`, or `report` command accepts the global
`--from FILE` and then runs without a live client.

Global flags:

- `--kubeconfig` and `--context` select the Kubernetes client configuration.
- `-n`, `--namespace` limits namespaced reads where applicable.
- `-o`, `--output` accepts `table` or `json`.
- `--no-color` disables ANSI formatting (also disabled by `NO_COLOR` and non-TTY stdout).
- `--timeout` bounds API and adapter operations.
- `--from FILE` replays a snapshot instead of contacting a cluster.
- `--strict` makes `unavailable` results exit 3.

## Installation Methods

| Method | Audience | Behavior |
|---|---|---|
| Source build (`go install` or `go build`) | Developers | Uses the local kubeconfig; no cluster resources are installed |
| Release binary | Administrators and CI runners | Download a versioned binary and run it with a kubeconfig |
| OCI image | CI and support automation | Mount a kubeconfig read-only and run one command or capture job |
| Helm Job | Restricted environments | Creates a short-lived read-only Job with cluster or namespace RBAC |
| Host adapter | Node/driver maintainers | Explicit local or host execution for VFIO, CDI, and CPU topology checks |

The project will not install a long-running Deployment, Service, CRD, or
privileged DaemonSet. The Helm chart must expose separate cluster-read-only and
namespace-scoped RBAC profiles, and the documentation must show the exact
commands for each installation method.

## Implementation Tasks

### Task 1: Establish the CLI framework and shared execution options

**Files:**
- Modify: `cmd/dra-inspect/main.go`
- Create: `internal/model/result.go`, `internal/cli/root.go`, `internal/cli/options.go`, `internal/cli/exit.go`
- Test: `internal/cli/root_test.go`

**Interfaces:**
- `Options` carries kubeconfig, context, namespace, output format, color, timeout, `From` (snapshot path), and `Strict`.
- `NewRootCommand(opts *Options, version string) *cobra.Command` returns the executable command tree.
- `ExitCode(statuses []model.Status, strict bool) int` maps result statuses to the exit codes in the spec. Task 1 declares `model.Status` and `model.Result` in `internal/model/result.go`.

- [ ] Add failing tests for version output, invalid output format, `--no-color`/`NO_COLOR`/non-TTY, and exit status mapping (0 pass/warn, 1 fail, 2 error, 3 unavailable only with `--strict`).
- [ ] Implement the root command and the `inspect`, `check`, `report`, `snapshot`, and `completion` groups as empty parents.
- [ ] Verify `go test ./internal/cli/...` and `go run ./cmd/dra-inspect --help`.
- [ ] Commit: `feat: establish dra-inspect command framework`.

### Task 2: Add typed Kubernetes collection, shared models, and RBAC docs

**Files:**
- Create: `internal/model/snapshot.go`, `internal/model/redact.go`, `internal/model/dra.go`, `internal/collect/kubernetes.go`, `docs/rbac.md`
- Test: `internal/model/*_test.go`, `internal/collect/kubernetes_test.go`
- Fixtures: `testdata/` converted from the sibling repo's `dra-topology-aware-co-placement/testing/results/*/resourceslices.yaml`

**Interfaces:**
- `model.Result{Check, Status, Subject, Details}` is the single result type. Inspectors and checks both return `[]model.Result`.
- `model.Status` is `pass|warn|fail|unavailable|error`.
- `model.Snapshot` stores `SchemaVersion` (constant `dra-inspect/v1alpha1`), collection metadata, redacted objects, and `Evidence map[string]json.RawMessage` keyed by adapter name (empty until Task 6).
- `model.Redact(Snapshot) Snapshot` applies the redaction rules in the spec; `--no-redact` skips it.
- `Collector` exposes typed collection for ResourceSlices, DeviceClasses, ResourceClaims, Pods, Nodes, and optional VMIs (dynamic client).
- `Collect(ctx, scope) (model.Snapshot, error)` is the single core data acquisition entrypoint.

- [ ] Add fixture tests for scalar/list-valued attributes, quantities, device counts, allocation results, and namespaced filtering, using the client-go v0.34 fake clientset (`ResourceV1()`) and a dynamic fake client for VMIs.
- [ ] Add redaction tests: opaque parameters dropped, Pods reduced to the allowed fields.
- [ ] Implement client construction from kubeconfig/context and typed collection with explicit permission errors.
- [ ] Mark unavailable resources in the model instead of fabricating empty data.
- [ ] Write `docs/rbac.md` from the RBAC table in the spec.
- [ ] Verify fake-client tests and `go vet ./...`.
- [ ] Commit: `feat: add typed DRA collectors and shared models`.

### Task 3a: Add renderers and the first inspectors

**Files:**
- Create: `internal/inspect/slices.go`, `internal/inspect/drivers.go`, `internal/inspect/attributes.go`, `internal/render/table.go`, `internal/render/json.go`, `internal/cli/inspect.go`
- Test: `internal/inspect/*_test.go`, `internal/render/*_test.go`; goldens in `testdata/golden/`

**Interfaces:**
- Inspectors consume `model.Snapshot` and return `[]model.Result` without issuing API calls.
- JSON output uses the envelope in the spec and exact resource names.
- Table output may abbreviate long names but must preserve clear columns and report unavailable data.
- Goldens are refreshed with an `-update` test flag.

- [ ] Add golden tests for `inspect slices`, `drivers` (including `--attributes`), and `attributes` (`--all-attributes`, `--include-capacity`).
- [ ] Wire the commands in `internal/cli/inspect.go`.
- [ ] Verify table width, Unicode, list attributes, capacity formatting, and JSON stability, with terminal and JSON output tested separately.
- [ ] Commit: `feat: add slice, driver, and attribute inspection`.

### Task 3b: Add topology, DeviceClass, and claim inspectors

**Files:**
- Create: `internal/inspect/topology.go`, `internal/inspect/deviceclasses.go`, `internal/inspect/claims.go`
- Modify: `internal/cli/inspect.go`
- Test: `internal/inspect/*_test.go`; goldens in `testdata/golden/`

- [ ] Add golden tests for `inspect topology` (grouped by NUMA node and PCIe root using the attribute resolution table in the spec), `deviceclasses` (`--selector`), and `claims`.
- [ ] `inspect claims` joins VMIs when available; otherwise it reports the VMI field as `unavailable`.
- [ ] Ensure opaque selectors/configuration are omitted from normal user output.
- [ ] Commit: `feat: add topology, deviceclass, and claim inspection`.

### Task 4: Implement topology and allocation checks

**Files:**
- Create: `internal/check/alignment.go`, `internal/check/topology.go`, `internal/check/capacity.go`, `internal/check/counters.go`, `internal/cli/check.go`, `internal/cli/report.go`
- Test: `internal/check/*_test.go`

**Interfaces:**
- `Check(ctx context.Context, snapshot model.Snapshot) []model.Result` is the common check contract.
- Alignment compares claim allocation results with NUMA, PCIe-root, and request constraints; list-valued attributes align by set intersection.
- Severity follows the spec: violated `matchAttribute` constraint is `fail`, NUMA split is `warn`, PCIe-root split is informational unless constrained.
- `check counters` uses ResourceSlice `sharedCounters` and `consumesCounters` only; it needs no adapter. Allocated claims count as consuming shared counters even when `reservedFor` is empty, as the scheduler does.

- [ ] Add passing and failing fixtures for NUMA, PCIe-root, mixed-device, missing-attribute, and consumable-capacity cases.
- [ ] Port the cases from the legacy `test_dra_counters.py` as counter fixtures.
- [ ] Implement `check alignment`, `check counters`, and core topology validation, wired in `internal/cli/check.go`.
- [ ] Implement `report cluster` from the safe core inspectors/checks only, wired in `internal/cli/report.go`.
- [ ] Verify exit codes and JSON results for pass, warn, fail, unavailable, and error.
- [ ] Commit: `feat: add topology and allocation checks`.

### Task 5: Add snapshot capture and replay

**Files:**
- Create: `internal/snapshot/capture.go`, `internal/snapshot/load.go`, `internal/cli/snapshot.go`
- Modify: `internal/cli/options.go`, `internal/cli/root.go`
- Test: `internal/snapshot/*_test.go`

**Interfaces:**
- `Capture(w io.Writer, s model.Snapshot) error` writes the versioned snapshot with deterministic ordering. The CLI calls `Collect`, then `model.Redact`, then `Capture`.
- `Load(path string) (model.Snapshot, error)` validates the schema version and returns normalized data.
- The global `--from FILE` makes every `inspect`, `check`, and `report` command read the loaded snapshot instead of a live client.
- `snapshot capture` writes to `--file FILE` (`-f`; default stdout). `-o/--output` stays the output format. The file is written 0600 via a temp file and rename, and an existing file is not overwritten without `--force`.
- `--from` cannot be combined with `--kubeconfig` or `--context`. With `-n NS` it filters claims, pods and VMIs to NS and refuses to widen a snapshot captured for another namespace.

- [ ] Add tests proving capture/load round trips, deterministic ordering, schema validation, and that redacted fields are absent from the file.
- [ ] Implement `snapshot capture` and the `--from` source switch.
- [ ] Reject unsupported snapshot versions with an actionable error.
- [ ] Verify a report generated with `--from` matches the live fixture report.
- [ ] Commit: `feat: add offline DRA snapshots`.

### Task 6a: Add the host adapter

**Files:**
- Create: `internal/adapters/host/`
- Modify: `internal/cli/check.go`
- Test: adapter fixtures using a temp-dir fake sysfs/CDI tree passed as `--host-root`

**Interfaces:**
- `Adapter.Available(ctx context.Context) (bool, error)` reports capability without failing core inspection.
- `Adapter.Collect(ctx context.Context) (model.Evidence, error)` returns evidence only when explicitly requested; `model.Evidence` is `json.RawMessage` stored under `Snapshot.Evidence["host"]`.
- Off-node, or when `--host-root` is missing the expected files, adapters return `unavailable` with the reason "run on node".

- [ ] Add fake-sysfs tests for CPU pinning, NUMA mapping, VFIO/IOMMU, and CDI discovery.
- [ ] Test absent host files and unreadable `/proc`.
- [ ] Implement `check cpupinning` and `check vfio`.
- [ ] Verify core commands never require host privileges.
- [ ] Commit: `feat: add optional host adapter`.

### Task 6b: Add the KubeVirt adapter and support report

**Files:**
- Create: `internal/adapters/kubevirt/`
- Modify: `internal/cli/report.go`
- Test: adapter fixtures with fake VMI data

- [ ] Add tests for VMI host-device mappings and missing KubeVirt APIs (`unavailable`, core inspection unaffected).
- [ ] Implement `report support`, which runs the available adapters and includes their evidence.
- [ ] Keep composite-device inspection out of the adapter set.
- [ ] Commit: `feat: add KubeVirt adapter and support report`.

### Task 7a: Release packaging

**Files:**
- Create: `.goreleaser.yaml`, `Dockerfile`
- Modify: `.github/workflows/ci.yaml`

- [ ] Add GoReleaser configuration for binaries and an OCI image build definition.
- [ ] Add CI jobs for tests, vet, lint, and snapshot fixtures.
- [ ] Commit: `build: add release packaging`.

### Task 7b: Helm Job and deployment docs

**Files:**
- Create: `deploy/helm/dra-inspect/`
- Modify: `.github/workflows/ci.yaml`, `README.md`, `docs/rbac.md`

- [ ] Add a cluster read-only ClusterRole and a namespace-scoped Role profile matching `docs/rbac.md`.
- [ ] Add an opt-in Helm Job with `restartPolicy: Never`, no privileged host mounts, and TTL cleanup.
- [ ] Add a chart validation CI job.
- [ ] Document workstation, container, Job, and explicit host-adapter execution.
- [ ] Verify the chart renders without a long-running Deployment or CRD.
- [ ] Commit: `build: add read-only Helm Job`.

### Task 8: Migrate the lab script and validate against a live cluster

**Files:**
- Create: `docs/migration-from-dra-verify.md`, `docs/harness-test-plan.md`
- Modify: `README.md`

- [ ] Map every retained `dra-verify.sh` command to its new command group.
- [ ] Document the dropped commands (`composite`, `metadata`, `guest`), the dropped `cpuSocketID` handling, and the replacement for `all`.
- [ ] Add fixture parity tests for the supported script outputs.
- [ ] Run live checks against a kind cluster loaded with `dra-topology-aware-co-placement/testing/manifests/drivers/*.yaml`, and record expected unavailable-adapter behavior. TODO: the former harness path `devel/k8s-dra-harness` does not exist; replace this step if a real harness location is supplied.
- [ ] Publish a support-bundle example using capture and `--from` replay.
- [ ] Commit: `docs: document dra-verify migration and live-cluster coverage`.

## Final Verification

Run from the project root:

```bash
go test ./...
go vet ./...
golangci-lint run
helm lint deploy/helm/dra-inspect
go run ./cmd/dra-inspect --help
```

Run live verification only against an explicitly selected test cluster:

```bash
dra-inspect inspect slices
dra-inspect inspect deviceclasses -o json
dra-inspect check alignment
dra-inspect snapshot capture --file snapshot.json
dra-inspect inspect slices --from snapshot.json
dra-inspect report support --from snapshot.json
```
