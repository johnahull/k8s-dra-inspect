#!/usr/bin/env bash
# Fails when lab hostnames leak into fixtures.
cd "$(dirname "$0")/.." || exit 1
if grep -rEi 'redhat\.com|labs\.|rdu[0-9]|perfscale' testdata/; then
  echo "fixtures contain lab-specific strings" >&2
  exit 1
fi
