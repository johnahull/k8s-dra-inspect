# Contributing to dra-inspect

Contributions are welcome. Please open an issue before large changes so the
command surface and output contracts can be discussed first.

Before submitting a pull request:

```bash
go test ./...
go vet ./...
```

Keep the default tool behavior read-only. New checks should include fixture
tests and stable JSON output where automation may consume the result.
