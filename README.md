# dra-inspect

`dra-inspect` is a read-only command-line tool for inspecting and diagnosing
Kubernetes Dynamic Resource Allocation (DRA) environments.

The project is intended for cluster administrators, driver developers, and CI
systems that need to answer questions such as:

- What did the DRA drivers publish in their `ResourceSlice` objects?
- Which `DeviceClass` objects and claims are available or allocated?
- Are allocated devices aligned on the requested NUMA or PCIe topology?
- Are optional host integrations such as VFIO and CDI configured correctly?

The tool does not replace DRA drivers, the scheduler, or admission webhooks. It
does not create or mutate Kubernetes resources.

## Status

The v1 command surface is implemented (inspect, check, report, snapshot,
completion). Live-cluster verification on kind is planned but **not yet run**;
see [docs/harness-test-plan.md](docs/harness-test-plan.md). Coming from the lab
`dra-verify.sh`? See [docs/migration-from-dra-verify.md](docs/migration-from-dra-verify.md).

## Commands

```text
dra-inspect inspect slices | topology [--compact] | drivers [--attributes]
dra-inspect inspect attributes [--all-attributes] [--include-capacity]
dra-inspect inspect deviceclasses [--selector LABEL_SELECTOR]
dra-inspect inspect claims

dra-inspect check alignment [POD|CLAIM]
dra-inspect check counters | capacity | topology
dra-inspect check cpupinning POD [--host-root PATH] [--node-name NAME]
dra-inspect check vfio [--host-root PATH] [--node-name NAME]
dra-inspect check kubevirt [VMI]

dra-inspect report cluster
dra-inspect report support [--host-root PATH] [--node-name NAME]
dra-inspect snapshot capture [--file FILE] [--force] [--no-redact] [--with-adapters]
dra-inspect completion bash|zsh|fish
dra-inspect version
```

Global flags: `-n/--namespace`, `-o/--output table|json`, `--from FILE`,
`--kubeconfig`, `--context`, `--timeout`, `--strict`, `--no-color`.

`--timeout` (default 30s) is one overall deadline for collecting from the
cluster, all resource kinds together, so a hung API server costs about that
long in total. Kinds not collected by then are reported as unavailable
(`timed out after <timeout>`). It applies to `inspect`, `check`, `snapshot
capture` and `report` alike.

- Tables are the default; `-o json` prints a versioned envelope
  (`schemaVersion`, `results[]` with `check`, `status`, `subject`, `details`).
- Every result is `pass`, `warn`, `fail`, `unavailable` or `error`. Exit codes:
  0 pass or warn, 1 fail, 2 error, 3 unavailable (only with `--strict`).
  Missing permissions or data are `unavailable`, never a guess.
- `check cpupinning`, `check vfio` and the host adapter read files of the node
  they run on (`--host-root`, default `/`); elsewhere they are `unavailable`.
  `check kubevirt` and the KubeVirt adapter work from the VMIs in the snapshot.
- `report support` is `report cluster` plus `check vfio`, `check kubevirt` and
  one `adapter.<name>` result per optional adapter (host, kubevirt).

```bash
dra-inspect inspect topology --compact
dra-inspect inspect deviceclasses --selector tier=gold
dra-inspect check alignment my-pod -n team-a
dra-inspect report cluster -o json

# Capture once, analyze anywhere. Snapshots are redacted by default (opaque
# driver parameters, CEL selectors, cloud-init data); --from needs no cluster.
dra-inspect snapshot capture --file snap.json
dra-inspect check alignment --from snap.json

# On a node: also store host and KubeVirt adapter evidence for support.
dra-inspect snapshot capture --with-adapters --file bundle.json
dra-inspect report support --from bundle.json

source <(dra-inspect completion bash)
```

A sample redacted bundle and its report are in
[docs/examples/](docs/examples/support-bundle.json)
([report](docs/examples/support-report.txt)); both are regenerated and checked
by a test.

## Install

Once a release is published (publishing is a manual maintainer step for now),
download the archive for your platform from the GitHub releases page, verify it
against `checksums.txt`, and put `dra-inspect` on your `PATH`. Or run the OCI
image (distroless, non-root, no shell):

```bash
docker run --rm --user "$(id -u):$(id -g)" --read-only \
  -e KUBECONFIG=/kube/config -v "$HOME/.kube/config:/kube/config:ro" \
  -v "$PWD:/out" \
  ghcr.io/johnahull/k8s-dra-inspect:<version> \
  snapshot capture --file /out/snapshot.json
```

Run as your own UID, not the image's default 65532: a kubeconfig is usually
mode 0600, so only its owner can read the mounted file. With rootless Podman
also add `--userns=keep-id`, or the container UID does not map to the file
owner. The kubeconfig is mounted read-only; snapshots go to the mounted
current directory, so the container filesystem can stay read-only.

## Run as a Kubernetes Job

The Helm chart in `deploy/helm/dra-inspect` installs read-only RBAC (`get` and
`list` only, see [docs/rbac.md](docs/rbac.md); the live Job check is still to be run, see
[docs/harness-test-plan.md](docs/harness-test-plan.md)) and, optionally, a one-shot Job.
The chart is not published to a registry yet; install from the repository path.

```bash
# RBAC + ServiceAccount only (default): run the CLI yourself with this identity.
helm install dra-inspect deploy/helm/dra-inspect -n dra-inspect --create-namespace

# Cluster profile plus a one-shot report Job. job.strict=true makes the Job
# fail (exit 3) when any result is unavailable, for example on total API
# failure; without it such a Job still exits 0.
helm install dra-inspect deploy/helm/dra-inspect -n dra-inspect --create-namespace \
  --set job.enabled=true --set job.strict=true

# Namespace profile: Role+RoleBinding in one namespace (default: the release
# namespace). The Job then runs with -n <namespace>; slices, DeviceClasses and
# nodes are reported unavailable. A Job covers one namespace, so install one
# release per namespace; without the Job, rbac.namespaces={a,b} is accepted.
# Do not set job.strict here: slices, DeviceClasses and nodes are forbidden by
# design in this profile, so --strict would always fail the Job.
helm install dra-inspect deploy/helm/dra-inspect -n team-a \
  --set rbac.profile=namespace --set job.enabled=true

kubectl -n dra-inspect wait --for=condition=complete job/dra-inspect --timeout=120s
kubectl -n dra-inspect logs job/dra-inspect | sed -n '/^{$/,/^}$/p' > report.json
helm uninstall dra-inspect -n dra-inspect
```

The Job name is `<release>-dra-inspect` (just the release name when it already
contains `dra-inspect`). The report is JSON on the pod log. The Job is deleted
`job.ttlSecondsAfterFinished` (default 3600) seconds after it finishes; use
`helm uninstall` to remove the RBAC. After `helm upgrade` with changed Job
settings, delete the finished Job first, because Job specs are immutable.

`kubectl logs` merges stderr into stdout, so a klog line can come before or
after the JSON. The report is indented JSON whose first and last lines are `{`
and `}`; the `sed` range above extracts it (a per-line `jq -R fromjson?` does
not work on indented output). The exit code is the Job result: 0 pass or warn,
1 fail, 2 error, and 3 unavailable only with `job.strict=true`. A non-zero exit
shows as a failed Job; read the reason with
`kubectl -n dra-inspect get job/dra-inspect -o jsonpath='{.status.conditions}'`
and the exit code in the pod's `containerStatuses` (`kubectl get pod`).

The Job pod meets Pod Security `restricted`: non-root UID 65532, RuntimeDefault
seccomp, no privilege escalation, all capabilities dropped, read-only root
filesystem, no host mounts or host namespaces. Only that pod mounts a service
account token. The host adapter and host checks (`check cpupinning`,
`check vfio`, `report support`) are not part of the Job; run them explicitly
on the node with the workstation binary or the container image. Set
`rbac.kubevirt=true` to also allow reading VirtualMachineInstances.

## Development

Requirements:

- Go 1.26.10 or newer

Run the checks with:

```bash
go test -race ./...
go vet ./...
hack/check-fixtures.sh   # no real identifiers in testdata/ or docs/examples/
```

Legacy parity facts (`testdata/parity/`) come from the manual tool
`hack/capture-legacy-facts.sh`; it is not run in CI.

## License

Apache License 2.0. See [LICENSE](LICENSE).
