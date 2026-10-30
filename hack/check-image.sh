#!/usr/bin/env bash
# Usage: hack/check-image.sh IMAGE [EXPECTED_VERSION]   (needs podman; image built from the Dockerfile)
# Asserts: non-root user, entrypoint is exactly /dra-inspect with no Cmd, no shell,
# and `version` prints exactly EXPECTED_VERSION (default: the snapshot version in dist/metadata.json).
set -euo pipefail
cd "$(dirname "$0")/.."
img=${1:?usage: check-image.sh IMAGE [EXPECTED_VERSION]}
fail() { echo "check-image: $*" >&2; exit 1; }
want=${2:-}
if [ -z "$want" ]; then
  want=$(grep -o '"version":"[^"]*"' dist/metadata.json 2>/dev/null | cut -d'"' -f4 || true)
fi
[ -n "$want" ] || fail "no expected version: pass it as 2nd argument or run goreleaser first (dist/metadata.json)"

user=$(podman image inspect "$img" --format '{{.Config.User}}') || fail "cannot inspect $img"
case "$user" in ""|0|root|0:*|root:*) fail "image user is root ('$user')" ;; esac
ep=$(podman image inspect "$img" --format '{{json .Config.Entrypoint}}')
[ "$ep" = '["/dra-inspect"]' ] || fail "entrypoint is $ep, want [\"/dra-inspect\"]"
cmd=$(podman image inspect "$img" --format '{{json .Config.Cmd}}')
case "$cmd" in null|"[]"|"") ;; *) fail "image has a default Cmd: $cmd" ;; esac

out=$(podman run --rm "$img" version) || fail "'version' failed"
[ "$out" = "$want" ] || fail "version output '$out' != expected '$want'"
for sh in /bin/sh /busybox/sh /bin/bash /bin/ash; do
  if podman run --rm --entrypoint "$sh" "$img" -c true >/dev/null 2>&1; then fail "image contains a shell ($sh)"; fi
done
echo "check-image: ok ($out, user $user)"
