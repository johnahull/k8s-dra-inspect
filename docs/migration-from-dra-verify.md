# Migrating from dra-verify.sh

`dra-inspect` replaces the lab script `dra-verify.sh` (and its helper
`dra-counters.py`). It is a single static binary: no `kubectl`, `python3`,
`sudo` or `sshpass` is needed, and it never mutates the cluster. See the
[README](../README.md) for installation and the
[RBAC reference](rbac.md) for the exact (get/list only) permissions.

## Command map

| `dra-verify.sh` | `dra-inspect` |
|---|---|
| `slices` | `inspect slices` |
| `topology` | `inspect topology` (groups by node, NUMA node and PCIe root) |
| `topology -t` | `inspect topology --compact` (or `-t`) |
| `drivers` | `inspect drivers` |
| `driverinfo` | `inspect drivers --attributes` (no standalone command) |
| `attributes` | `inspect attributes` |
| `attributes -a` | `inspect attributes --all-attributes` (add `--include-capacity` for capacities) |
| `deviceclasses [pairs\|partitions\|aggregates]` | `inspect deviceclasses [--selector LABEL_SELECTOR]` |
| `claims [-n NS]` | `inspect claims [-n NS]` |
| `alignment [POD] [-n NS]` | `check alignment [POD\|CLAIM] [-n NS]` |
| `cpupinning [POD] [-n NS]` | `check cpupinning POD [-n NS] [--host-root PATH] [--node-name NAME]` (run on the node) |
| `counters` | `check counters` |
| `vfio` | `check vfio [--host-root PATH] [--node-name NAME]` (run on the node) |
| `all [-n NS]` | `report cluster` (API only) or `report support` (adds host and KubeVirt adapters) |
| `help`, `-h`, `--help` | `dra-inspect --help`, `dra-inspect <group> --help` |
| `completions`, `eval "$(dra-verify.sh completions)"` | `dra-inspect completion bash\|zsh\|fish`, for example `source <(dra-inspect completion bash)` |

New commands with no legacy counterpart: `check capacity`, `check topology`,
`check kubevirt [VMI]`, `snapshot capture`, and the global `--from FILE`
replay.

### The text layout and the legacy sections

The default `-o text` output keeps the script's sections but is laid out for
reading rather than for `awk`; use `-o table` for flat rows and `-o json` for
machines.

| Legacy section | Text layout |
|---|---|
| `slices` hardware summary | `inspect slices`: per node and driver, one line per current device with its NUMA nodes, bus ID and product, and after an arrow the claim or pod using it; the header counts devices in use and free. |
| `topology` tree | `inspect topology`: node, then NUMA set, then PCIe-root set, then devices. There is no socket level (`cpuSocketID` is gone). NUMA sets sort numerically (`0,1` before `10,11`), PCIe roots by count and then naturally, and unknown branches come last. |
| `alignment` | `check alignment`: each request name on its own line with its device, NUMA nodes and PCIe root indented below; then one sentence per finding. A sentence naming devices (a NUMA split, a violated `matchAttribute`) is followed by one line per group (`NUMA 0,1: gpu-0, gpu-2`). |
| `counters` | `check counters`: per pool and counter set, "N of M in use", the claims using it, then the verdict. |

Long device lists wrap at the terminal width (100 columns when piped) and
never lose a name.

### DeviceClass filters

The legacy script always listed only classes labeled
`nodepartition.dra.k8s.io/managed=true` and then filtered by label. The
equivalent selectors are:

| Legacy filter | Legacy meaning | Equivalent `--selector` |
|---|---|---|
| `deviceclasses pairs` (also `groupings`) | classes with a `nodepartition.dra.k8s.io/grouping` label | `nodepartition.dra.k8s.io/managed=true,nodepartition.dra.k8s.io/grouping` |
| `deviceclasses partitions` | classes without a grouping label, grouped by profile | `nodepartition.dra.k8s.io/managed=true,!nodepartition.dra.k8s.io/grouping` |
| `deviceclasses aggregates` (also `aggregate`) | classes without a `nodepartition.dra.k8s.io/numa` label | `nodepartition.dra.k8s.io/managed=true,!nodepartition.dra.k8s.io/numa` |
| `deviceclasses` (no filter) | all managed classes | `nodepartition.dra.k8s.io/managed=true` |

`inspect deviceclasses` lists the matching classes; the legacy per-profile
summaries, counts and analysis text are not reproduced.

### Options

| Legacy option | New |
|---|---|
| `-n`, `--namespace NS` | global `-n`, `--namespace NS` (no flag means all namespaces) |
| `-a`, `--all` | `inspect attributes --all-attributes`; not a global flag any more |
| `-t`, `--simple` | `inspect topology --compact` / `-t` |
| `-v`, `--verbose` | dropped; `inspect attributes --all-attributes` shows every attribute, including product names, and `-o json` always carries full detail |
| positional DeviceClass filter (`pairs`, `partitions`, `aggregates`) | `--selector`, a standard Kubernetes label selector; see the table below |
| positional Pod or VM | typed positional argument of the command that takes it (`check alignment POD`, `check cpupinning POD`, `check kubevirt VMI`) |
| (none) | `-o text\|table\|json`, `--from FILE`, `--kubeconfig`, `--context`, `--timeout`, `--strict`, `--no-color` |
| `source dra-verify.sh` | gone; use `dra-inspect completion` |

## Dropped

| Legacy | Why | What to do instead |
|---|---|---|
| `composite` | No v1 replacement. | None. `inspect attributes --all-attributes` still shows the raw attributes. |
| `metadata` | Needed `kubectl exec` (`pods/exec`). dra-inspect grants no exec. | None in v1. |
| `guest` | Needed SSH (with an `sshpass` fallback). dra-inspect never opens a shell into a workload. | None in v1. |
| `cpuSocketID` handling | Not a standard DRA attribute. The legacy socket column, the NUMA-to-socket inference and the "cpuSocketID SPLIT" verdict are gone. | Group by NUMA node and PCIe root (`inspect topology`). |
| `cpuSocketID`-style vendor keys as a separate concept | Not separate: NUMA and PCIe root are resolved through three tiers, and the first tier with any key wins (see below). | `inspect topology` shows the resolved value. |
| lab-only parsing (DaemonSet argument scraping, kubelet plugin directory listing, PartitionConfig reconstruction) | Driver-specific and tied to the topology-coordinator project. | `inspect drivers` summarizes drivers from ResourceSlices; `inspect deviceclasses` lists classes and never prints CEL or opaque config. |

## How NUMA and PCIe root are resolved

`dra-inspect` does not interpret only the standard keys. For each device it
tries, in order, and stops at the first tier that has any matching key:

1. the standard `resource.kubernetes.io/numaNode` / `resource.kubernetes.io/pcieRoot`;
2. `<driver>/<name>` or the bare `<name>`, where `<name>` is `numaNode`, `numa`
   or `numaNodeID` (NUMA) and `pcieRoot` (PCIe root);
3. any `<vendor>/<name>` with the same names (keys taken in lexical order).

If several keys of the winning tier disagree, the result is flagged as a
conflict instead of being guessed. A device with no key in any tier reports the
attribute as missing (`unavailable`/`warn`).

How to read the output:

- A NUMA set is a set. `numa=0,1,2,3` means the device lists all four NUMA
  nodes (the legacy script grouped it under its first node only, `0`).
- Single-device claims have nothing to align, so `check alignment` omits the
  `numa` and `pcieRoot` sections for them; read a missing section as trivially
  aligned.

## Behavior differences worth knowing

- **Alignment is a global intersection.** `check alignment` evaluates each
  claim's `matchAttribute` constraints and, for unconstrained claims, reports
  whether the devices share at least one value of `numaNode` (list-valued
  attributes are intersected as sets). The script compared the printed
  strings, so `[0,1,2,3]` versus `[1,0,2,3]` was a "split". Only
  `matchAttribute` is evaluated; `distinctAttribute` and derived attributes
  are `unavailable`. A claim's status is the worst finding
  (error > fail > unavailable > warn > pass).
- **`unavailable` is not empty.** If a collection could not be read
  (forbidden, not found, timeout) the result is `unavailable` with the reason;
  the script printed an empty section or "(none found)". Namespaced RBAC makes
  slice, DeviceClass and node checks `unavailable` (see [rbac.md](rbac.md)).
- **Exit codes.** `0` pass or warn, `1` fail, `2` usage or internal error,
  `3` unavailable, only with `--strict`. The script almost always exited 0.
- **Redaction.** Opaque driver parameters, last-applied annotations,
  `managedFields` and VMI cloud-init data are never printed and are removed from
  snapshots unless `snapshot capture --no-redact` is given. CEL selector text
  is not printed by `inspect`, but CEL selector text is kept in snapshots for
  replay fidelity: review a snapshot before sharing it (see
  [SECURITY.md](../SECURITY.md)).
  Opaque parameters and `PartitionConfig` contents that the old
  `claims`/`deviceclasses` output showed are no longer displayed.
- **JSON envelope.** `-o json` prints
  `{"schemaVersion":"dra-inspect/v1alpha1","generatedAt":...,"source":...,"results":[{"check","status","subject","details"}]}`.
  The script had no machine-readable output.
- **Host checks are node-local.** `check cpupinning`, `check vfio` and the
  host adapter read files below `--host-root` on the machine they run on, never
  use `sudo` or run commands, and are `unavailable` anywhere else. The script
  scanned `/proc/*/fd` with `sudo` to find who holds a VFIO device;
  dra-inspect only reports `vfio-pci` binding, IOMMU groups and CDI specs from
  files, and does not look at process file descriptors. The Helm Job does not
  run them.
- **Current generation only.** `drivers`, `attributes`, `topology` and
  `check counters` use the newest generation of each pool; `inspect slices -o
  table` lists every slice and generation, while the default text view shows
  the current devices and notes stale or incomplete slices. The script counted superseded slices too, so its
  counter totals could include stale devices.
- **Allocation without `reservedFor`.** The script ignored allocated claims
  that no pod reserved (`status.reservedFor` empty); dra-inspect counts every
  allocation, so counter remaining values and alignment results can differ.
- **Overcommit fails.** A counter whose consumers exceed the published total
  is `fail` (exit 1). The legacy `dra-counters.py` printed a red negative
  number and exited 0.
- **Output is plain.** No ANSI unless stdout is a terminal (`--no-color` and
  `NO_COLOR` disable it). The default `-o text` never truncates a name; only
  `-o table` truncates long cells with an ellipsis to fit the terminal, and
  `-o json` keeps everything whole.
- **Requirements.** Kubernetes 1.34 or newer (the `resource.k8s.io/v1` API).
  The legacy `deviceclasses` command needs Python 3.12 or newer; with 3.10 it
  fails silently after the heading.

## Offline replay replaces ad hoc captures

```bash
dra-inspect snapshot capture --file snap.json            # redacted by default
dra-inspect check alignment --from snap.json             # no cluster, no kubeconfig
dra-inspect report support --from snap.json
```

A sample bundle and its report are in
[`docs/examples/`](examples/support-bundle.json) ([report](examples/support-report.txt)).

## How parity is tested

`hack/capture-legacy-facts.sh` is a manual tool (not run in CI). It runs the
legacy script against the committed fixtures through a stub `kubectl` that
serves only `kubectl get <resource> -o json` and refuses everything else, then
stores machine-comparable facts under `testdata/parity/`. The Go tests in
`internal/cli/parity_test.go` assert that dra-inspect yields the same facts
(driver and device counts, device to NUMA and PCIe root, claim allocations,
alignment verdicts, DeviceClass selection, counter remaining) and list the
intentional differences in their header. `slices`, `attributes`, `cpupinning`
and `vfio` are not covered: the first two only format columns, the last two need
a node. Live verification against a real kube-apiserver (no kubelet) has
been run; the full kind run has not. See [harness-test-plan.md](harness-test-plan.md).
