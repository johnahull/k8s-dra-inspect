#!/usr/bin/env bash
# Builds the binary the way GoReleaser will and asserts the release properties.
set -euo pipefail
cd "$(dirname "$0")/.."
fail() { echo "check-release: $*" >&2; exit 1; }

goreleaser build --snapshot --clean --single-target >/dev/null
bin=$(find dist -type f -name dra-inspect | head -n1)
[ -n "$bin" ] || fail "no dra-inspect binary under dist/"

want=$(grep -o '"version":"[^"]*"' dist/metadata.json | cut -d'"' -f4)
[ -n "$want" ] || fail "no snapshot version in dist/metadata.json"
got=$("$bin" version)
[ "$got" != dev ] || fail "version is 'dev': -X main.version ldflag missing"
[ "$got" = "$want" ] || fail "version '$got' != snapshot '$want'"

info=$(go version -m "$bin")
echo "$info" | grep -q 'CGO_ENABLED=0' || fail "binary not built with CGO_ENABLED=0"
echo "$info" | grep -q -- '-trimpath=true' || fail "binary not built with -trimpath"
desc=$(file "$bin")
echo "$desc" | grep -q 'statically linked' || fail "binary is not statically linked: $desc"
echo "$desc" | grep -q 'stripped' && ! echo "$desc" | grep -q 'not stripped' || fail "binary is not stripped (-s -w): $desc"
echo "check-release: ok ($got)"
