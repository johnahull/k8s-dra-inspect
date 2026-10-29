# dra-inspect Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `dra-inspect`, a read-only DRA administrator/developer diagnostics CLI with live inspection, validation checks, and offline snapshots.

**Architecture:** A Go/Cobra CLI collects typed Kubernetes DRA objects into normalized models, runs independent inspection/check analyzers, and renders tables or versioned JSON. Optional host and KubeVirt adapters are isolated from the core Kubernetes collector so normal cluster inspection never requires privileged access.

**Tech Stack:** Go 1.25+, Cobra, client-go/resource APIs, typed fixture snapshots, GoReleaser, GitHub Actions, optional Helm Job manifests.

**Spec:** `docs/superpowers/specs/2026-10-29-dra-inspect-design.md`

## Global Constraints

- Repository/module: `github.com/johnahull/k8s-dra-inspect`.
- Binary name: `dra-inspect`.
- Default behavior is read-only; no command mutates Kubernetes resources.
- Default output is a human-readable table; `-o json` is versioned and stable.
- Snapshots redact secrets, credentials, and opaque driver parameters by default.
- Composite-device inspection is excluded from v1.
- Host checks require explicit adapter execution and are not part of the default Job.
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
| `topology` | Keep | `inspect topology`; host socket inference is an optional node adapter |
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
| `metadata` | Keep as optional | `inspect metadata` |
| `guest` | Keep as optional | `inspect guest` |
| `all` | Drop | `report cluster` and `report support` profiles |
| `help` | Keep | Cobra help |
| `completions` | Keep | `completion bash`, `completion zsh`, `completion fish` |
| `-n`, `--namespace` | Keep | Global namespace filter |
| `-v`, `--verbose` | Keep | Command-scoped verbosity |
| `-a`, `--all` | Drop as global | Explicit command flags such as `--all-attributes` |
| `-t`, `--simple` | Drop | `inspect topology --compact` |
| `-h`, `--help` | Keep | Standard Cobra help |
| Positional DeviceClass filters | Replace | `--kind`, `--grouping`, and `--partition` |
| Positional Pod/VM targets | Keep | Typed command-specific positional arguments |
| `source dra-verify.sh` | Drop | Standalone binary only |
| `eval ... completions` | Drop | Cobra completion generation |

## Target Command Surface

```text
dra-inspect inspect slices
dra-inspect inspect topology
dra-inspect inspect drivers [--attributes]
dra-inspect inspect attributes [--all-attributes] [--include-capacity]
dra-inspect inspect deviceclasses [--kind KIND] [--grouping NAME]
dra-inspect inspect claims [--namespace NS]
dra-inspect inspect metadata POD [--namespace NS]
dra-inspect inspect guest VM [--namespace NS]

dra-inspect check alignment [POD|CLAIM] [--namespace NS]
dra-inspect check cpupinning POD [--namespace NS]
dra-inspect check counters
dra-inspect check vfio

dra-inspect report cluster
dra-inspect report support

dra-inspect snapshot capture [--output FILE]
dra-inspect snapshot report --from FILE
dra-inspect completion bash|zsh|fish
```

Global flags:

- `--kubeconfig` and `--context` select the Kubernetes client configuration.
- `-n`, `--namespace` limits namespaced reads where applicable.
- `-o`, `--output` accepts `table` or `json`.
- `--no-color` disables ANSI formatting.
- `--timeout` bounds API and adapter operations.

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
- Create: `internal/cli/root.go`, `internal/cli/options.go`
- Test: `internal/cli/root_test.go`

**Interfaces:**
- `Options` carries kubeconfig, context, namespace, output format, color, timeout, and snapshot source.
- `NewRootCommand(opts *Options) *cobra.Command` returns the executable command tree.
- `RunResult` carries a status (`pass`, `fail`, `unavailable`, or `error`) and structured details.

- [ ] Add failing tests for version output, invalid output format, `--no-color`, and exit status mapping.
- [ ] Implement the root command and the `inspect`, `check`, `report`, `snapshot`, and `completion` groups.
- [ ] Verify `go test ./internal/cli/...` and `go run ./cmd/dra-inspect --help`.
- [ ] Commit: `feat: establish dra-inspect command framework`.

### Task 2: Add typed Kubernetes collection and normalized models

**Files:**
- Create: `internal/collect/kubernetes.go`, `internal/model/dra.go`
- Test: `internal/collect/kubernetes_test.go`, `internal/model/dra_test.go`

**Interfaces:**
- `Collector` exposes typed collection methods for ResourceSlices, DeviceClasses, ResourceClaims, Pods, Nodes, DaemonSets, and optional VMIs.
- `Snapshot` stores `SchemaVersion`, collection metadata, redacted objects, and adapter evidence.
- `Collect(ctx, scope) (Snapshot, error)` is the single core data acquisition entrypoint.

- [ ] Add fixture tests for scalar/list-valued attributes, quantities, device counts, allocation results, and namespaced filtering.
- [ ] Implement client construction from kubeconfig/context and typed collection with explicit permission errors.
- [ ] Mark unavailable resources in the model instead of fabricating empty data.
- [ ] Verify fake-client tests and `go vet ./...`.
- [ ] Commit: `feat: add typed DRA collectors`.

### Task 3: Implement inspection commands and renderers

**Files:**
- Create: `internal/inspect/`, `internal/render/table.go`, `internal/render/json.go`
- Test: `internal/inspect/*_test.go`, `internal/render/*_test.go`

**Interfaces:**
- Inspectors consume `Snapshot` and return `[]Result` without issuing additional API calls.
- JSON output includes a top-level `schemaVersion` and exact resource names.
- Table output may abbreviate long names but must preserve clear columns and report unavailable data.

- [ ] Add fixture/golden tests for `inspect slices`, `topology`, `drivers`, `attributes`, `deviceclasses`, and `claims`.
- [ ] Implement merged `inspect drivers --attributes` behavior and explicit DeviceClass filters.
- [ ] Ensure opaque selectors/configuration are omitted from normal user output.
- [ ] Verify table width, Unicode, list attributes, capacity formatting, and JSON stability.
- [ ] Commit: `feat: add core inspection commands`.

### Task 4: Implement topology and allocation checks

**Files:**
- Create: `internal/check/alignment.go`, `internal/check/topology.go`, `internal/check/capacity.go`
- Test: `internal/check/*_test.go`

**Interfaces:**
- `Check(ctx context.Context, snapshot model.Snapshot) Result` is the common check contract.
- Alignment checks compare claim allocation results with NUMA, PCIe-root, socket, and request constraints.
- Failed checks return status `fail`; missing permissions or missing adapter data return `unavailable`.

- [ ] Add passing and failing fixtures for NUMA, PCIe-root, mixed-device, missing-attribute, and consumable-capacity cases.
- [ ] Implement `check alignment`, `check counters`, and core topology validation.
- [ ] Implement `report cluster` from the safe core inspectors/checks only.
- [ ] Verify exit codes and JSON results for pass, fail, unavailable, and error.
- [ ] Commit: `feat: add topology and allocation checks`.

### Task 5: Add snapshot capture and replay

**Files:**
- Create: `internal/snapshot/format.go`, `internal/snapshot/capture.go`, `internal/snapshot/replay.go`
- Test: `internal/snapshot/*_test.go`

**Interfaces:**
- `Capture(ctx context.Context, collector collect.Collector, path string) error` writes the versioned redacted snapshot.
- `Load(path string) (model.Snapshot, error)` validates the schema and returns normalized data.
- `report support --from FILE` and all inspectors/checks can run without a live client.

- [ ] Add tests proving capture/load round trips, deterministic ordering, schema validation, and redaction of secrets/opaque parameters.
- [ ] Implement `snapshot capture` and `snapshot report --from`.
- [ ] Reject unsupported snapshot versions with an actionable error.
- [ ] Verify a report generated from a snapshot matches the live fixture report.
- [ ] Commit: `feat: add offline DRA snapshots`.

### Task 6: Add optional host and KubeVirt adapters

**Files:**
- Create: `internal/adapters/host/`, `internal/adapters/kubevirt/`, `internal/adapters/counters/`
- Test: adapter fixtures with fake sysfs, CDI, KubeVirt, and counter data

**Interfaces:**
- `Adapter.Available(ctx context.Context) (bool, error)` reports capability without failing core inspection.
- `Adapter.Collect(ctx context.Context) (model.Evidence, error)` returns evidence only when explicitly requested.
- `report support` runs available adapters; direct adapter commands fail clearly when their prerequisites are absent.

- [ ] Add fake-sysfs tests for CPU pinning, NUMA/socket mapping, VFIO/IOMMU, and CDI discovery.
- [ ] Add KubeVirt tests for metadata files, VMI host-device mappings, and guest NUMA evidence.
- [ ] Add shared-counter tests for capacity consumption.
- [ ] Keep composite-device inspection out of the adapter set.
- [ ] Verify core commands never require host privileges or KubeVirt APIs.
- [ ] Commit: `feat: add optional diagnostic adapters`.

### Task 7: Package and deploy the tool safely

**Files:**
- Create: `deploy/helm/dra-inspect/`, `.goreleaser.yaml`, `docs/rbac.md`
- Modify: `.github/workflows/ci.yaml`, `README.md`

- [ ] Add a cluster read-only ClusterRole and a namespace-scoped Role profile.
- [ ] Add an opt-in Helm Job with `restartPolicy: Never`, no privileged host mounts, and TTL cleanup.
- [ ] Add GoReleaser configuration for binaries and an OCI image build definition.
- [ ] Add CI jobs for tests, vet, lint, snapshot fixtures, and chart validation.
- [ ] Document workstation, container, Job, and explicit host-adapter execution.
- [ ] Verify the chart renders without a long-running Deployment or CRD.
- [ ] Commit: `build: package dra-inspect for releases and Jobs`.

### Task 8: Migrate the lab script and validate against the harness

**Files:**
- Create: `docs/migration-from-dra-verify.md`, `docs/harness-test-plan.md`
- Modify: `README.md`

- [ ] Map every retained `dra-verify.sh` command to its new command group.
- [ ] Document dropped composite inspection and the replacement for `all`.
- [ ] Add fixture parity tests for the supported script outputs.
- [ ] Run live checks against `devel/k8s-dra-harness` and record expected unavailable-adapter behavior.
- [ ] Publish a support-bundle example using capture/replay.
- [ ] Commit: `docs: document dra-verify migration and harness coverage`.

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
dra-inspect snapshot capture --output snapshot.json
dra-inspect snapshot report --from snapshot.json
```
