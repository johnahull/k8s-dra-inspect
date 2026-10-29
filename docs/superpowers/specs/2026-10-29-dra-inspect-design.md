# dra-inspect Project Design

## Goal

Provide a read-only command-line tool for Kubernetes Dynamic Resource
Allocation (DRA) administrators, driver developers, and CI systems.

The tool explains what drivers published, what the scheduler allocated, and
whether topology, host integrations, and KubeVirt expectations are correct.

## Scope

In scope:

- ResourceSlice, DeviceClass, ResourceClaim, Pod, Node, and optional VMI inspection.
- NUMA, PCIe-root, socket, capacity, and allocation checks.
- Offline snapshot capture and replay.
- Optional VFIO, CDI, CPU-pinning, KubeVirt, and shared-counter adapters.
- Human-readable tables and versioned JSON output.
- Workstation/CI execution and an optional read-only in-cluster Job.

Out of scope:

- Scheduling, admission, or DRA driver control-plane behavior.
- Resource mutation, repair, or automatic remediation.
- Composite-device inspection in v1.
- A long-running API server or UI.

## Decisions

- Repository: `github.com/johnahull/k8s-dra-inspect`.
- Binary: `dra-inspect`.
- Implementation language: Go 1.25 or newer.
- CLI framework: Cobra.
- Default output: terminal tables; automation output: versioned JSON.
- Deployment: standalone binary or OCI image; optional read-only Kubernetes Job.
- RBAC: cluster read-only profile plus a namespace-scoped workload profile.
- Snapshots are a first-class v1 feature.
- The command hierarchy is reorganized in v1; the legacy shell command names
  are migration references, not compatibility requirements.
- Host and KubeVirt checks are optional adapters and are never required for
  ordinary cluster inspection.
