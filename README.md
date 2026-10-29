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

## Development

Requirements:

- Go 1.25 or newer

Run the current checks with:

```bash
go test ./...
go vet ./...
```

## License

Apache License 2.0. See [LICENSE](LICENSE).
