#!/usr/bin/env bash
# Usage: hack/check-image.sh IMAGE   (needs podman; image built from the Dockerfile)
# Asserts: runs `version` as a non-root user and ships no shell.
set -euo pipefail
img=${1:?usage: check-image.sh IMAGE}
fail() { echo "check-image: $*" >&2; exit 1; }
user=$(podman image inspect "$img" --format '{{.Config.User}}') || fail "cannot inspect $img"
case "$user" in ""|0|root|0:*|root:*) fail "image user is root ('$user')" ;; esac
out=$(podman run --rm "$img" version) || fail "'version' failed"
[ -n "$out" ] && [ "$out" != dev ] || fail "unexpected version output '$out'"
if podman run --rm --entrypoint /bin/sh "$img" -c true >/dev/null 2>&1; then fail "image contains a shell"; fi
echo "check-image: ok ($out, user $user)"
