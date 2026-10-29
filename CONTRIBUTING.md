# Contributing to dra-inspect

Contributions are welcome. Please open an issue before large changes so the
command surface and output contracts can be discussed first.

Before submitting a pull request:

```bash
go test ./...
go vet ./...
```

Golden files under `testdata/golden/` are regenerated with
`go test ./... -update`; review the resulting diff before committing.

Keep the default tool behavior read-only. New checks should include fixture
tests and stable JSON output where automation may consume the result.

## Platforms

Host adapter code is unix-only (`O_NOFOLLOW`/`O_NONBLOCK`), so Windows builds
are not supported.

## Helm chart

`hack/check-chart.sh` needs helm, python3 and PyYAML. Helm 3 and 4 are
supported; both are verified (the script rejects other majors). CI prints the
helm version in the chart job log.

## Live kind harness

`hack/live-kind.sh` needs rootless podman and creates a throwaway kind cluster; it
is run by hand, never in CI. Its pure helpers are tested offline by
`hack/test-live-helpers.sh` (bash only), which CI runs.

## Releases

Releases are built with [GoReleaser](https://goreleaser.com) (`.goreleaser.yaml`).
Dry-run locally without publishing anything:

```bash
goreleaser check
hack/check-release.sh   # snapshot build; asserts version injection and static build
```

`goreleaser release --snapshot --clean` also builds the image, which needs
docker/buildx. Without docker, build the binary with
`goreleaser build --snapshot --clean --single-target`, build the image with
podman from the Dockerfile (binary at `<ctx>/linux/amd64/dra-inspect`,
`--build-arg TARGETPLATFORM=linux/amd64`), and check it with
`hack/check-image.sh IMAGE`.

CI runs `goreleaser check`, a snapshot build, and `hack/check-release.sh` on
every push. Tagging and publishing (binaries and the
`ghcr.io/johnahull/k8s-dra-inspect` image) is a manual maintainer step and is
intentionally not automated yet.
