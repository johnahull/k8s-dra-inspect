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

This repository contains the project bootstrap and CLI skeleton. Inspection
commands are being added incrementally from the lab `dra-verify.sh` tool.

## Planned command groups

```text
dra-inspect inspect slices
dra-inspect inspect topology
dra-inspect inspect drivers
dra-inspect inspect attributes
dra-inspect inspect deviceclasses
dra-inspect inspect claims

dra-inspect check alignment
dra-inspect check cpupinning
dra-inspect check counters
dra-inspect check vfio
dra-inspect check kubevirt

dra-inspect report cluster
dra-inspect report support
dra-inspect snapshot capture
```

Human-readable tables will be the default output. Versioned JSON output and
offline snapshot replay are design requirements for CI and support workflows.

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
`list` only, see [docs/rbac.md](docs/rbac.md)) and, optionally, a one-shot Job.
The chart is not published to a registry yet; install from the repository path.

```bash
# RBAC + ServiceAccount only (default): run the CLI yourself with this identity.
helm install dra-inspect deploy/helm/dra-inspect -n dra-inspect --create-namespace

# Cluster profile plus a one-shot report Job.
helm install dra-inspect deploy/helm/dra-inspect -n dra-inspect --create-namespace \
  --set job.enabled=true

# Namespace profile: Role+RoleBinding in one namespace (default: the release
# namespace). The Job then runs with -n <namespace>; slices, DeviceClasses and
# nodes are reported unavailable. A Job covers one namespace, so install one
# release per namespace; without the Job, rbac.namespaces={a,b} is accepted.
helm install dra-inspect deploy/helm/dra-inspect -n team-a \
  --set rbac.profile=namespace --set job.enabled=true

kubectl -n dra-inspect wait --for=condition=complete job/dra-inspect --timeout=120s
kubectl -n dra-inspect logs job/dra-inspect > report.json
helm uninstall dra-inspect -n dra-inspect
```

The Job name is `<release>-dra-inspect` (just the release name when it already
contains `dra-inspect`). The report is JSON on the pod log. The Job is deleted
`job.ttlSecondsAfterFinished` (default 3600) seconds after it finishes; use
`helm uninstall` to remove the RBAC. After `helm upgrade` with changed Job
settings, delete the finished Job first, because Job specs are immutable.

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

Run the current checks with:

```bash
go test -race ./...
go vet ./...
```

## License

Apache License 2.0. See [LICENSE](LICENSE).
