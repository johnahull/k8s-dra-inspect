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
