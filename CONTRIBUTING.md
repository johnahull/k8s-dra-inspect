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

## Releases

Releases are built with [GoReleaser](https://goreleaser.com) (`.goreleaser.yaml`).
Dry-run locally without publishing anything:

```bash
goreleaser check
goreleaser release --snapshot --clean   # image build needs docker buildx
hack/check-release.sh                   # asserts version injection and static build
```

CI runs `goreleaser check`, a snapshot build, and `hack/check-release.sh` on
every push. Tagging and publishing (binaries and the
`ghcr.io/johnahull/k8s-dra-inspect` image) is a manual maintainer step and is
intentionally not automated yet.
