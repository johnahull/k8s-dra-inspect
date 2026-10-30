#!/usr/bin/env bash
# Fails when lab-specific or identifying strings leak into fixtures, parity facts and docs/examples.
cd "$(dirname "$0")/.." || exit 1
rc=0
if grep -rEi 'redhat\.com|labs\.|rdu[0-9]|perfscale|localdomain' testdata/ docs/examples/; then
  echo "fixtures/examples contain lab-specific strings" >&2; rc=1
fi
# Only synthetic UUIDs (00000000-0000-4000-8000-xxxxxxxxxxxx) are allowed.
if grep -rEio '[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}' testdata/ docs/examples/ |
  grep -Evi ':00000000-0000-4000-8000-[0-9a-f]{12}$' | grep .; then
  echo "fixtures/examples contain non-synthetic UUIDs" >&2; rc=1
fi
if grep -rEn '\b([0-9]{1,3}\.){3}[0-9]{1,3}\b' testdata/ docs/examples/; then
  echo "fixtures/examples contain IPv4 addresses" >&2; rc=1
fi
if grep -rEin '\b([0-9a-f]{2}:){5}[0-9a-f]{2}\b' testdata/ docs/examples/; then
  echo "fixtures/examples contain MAC addresses" >&2; rc=1
fi
exit $rc
