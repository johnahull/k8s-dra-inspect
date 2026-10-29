#!/usr/bin/env bash
# Offline self-test of the pure helpers in hack/live-kind.sh: each must PASS on
# good input and FAIL on bad input. Needs only bash and coreutils; no cluster.
set -euo pipefail
cd "$(dirname "$0")/.."
# shellcheck source=hack/live-kind.sh
source hack/live-kind.sh
set +e   # the helpers' check() and the probes below return non-zero on purpose

T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
BAD=0
# expect WANT(pass|fail) NAME CMD... -- CMD runs in a subshell; check() output is hidden.
expect() {
  local want=$1 name=$2; shift 2
  FAILS=0
  ( FAILS=0; "$@" >/dev/null 2>&1 && [ "$FAILS" -eq 0 ] ) && got=pass || got=fail
  if [ "$got" = "$want" ]; then echo "ok    $name"; else echo "NOT OK $name: want $want got $got"; BAD=$((BAD + 1)); fi
}

# --- absent: present -> fail, absent -> pass, unreadable/missing path -> fail
printf 'hello\nsecret-x\n' >"$T/f"
expect pass "absent: marker not in file" absent t secret-y "$T/f"
expect fail "absent: marker in file" absent t secret-x "$T/f"
expect fail "absent: nonexistent path" absent t secret-y "$T/nope"
expect fail "absent: one good, one nonexistent path" absent t secret-y "$T/f" "$T/nope"
mkdir "$T/d"; printf 'secret-x\n' >"$T/d/g"
expect fail "absent: marker in dir" absent t secret-x "$T/d"
expect pass "absent: clean dir" absent t secret-y "$T/d"

# --- canilist_offenders: realistic `kubectl auth can-i --list` output
cat >"$T/ok.txt" <<'X'
Resources                                       Non-Resource URLs   Resource Names   Verbs
selfsubjectreviews.authentication.k8s.io        []                  []               [create]
selfsubjectaccessreviews.authorization.k8s.io   []                  []               [create]
selfsubjectrulesreviews.authorization.k8s.io    []                  []               [create]
                                                [/api/*]            []               [get]
nodes                                           []                  []               [get list]
pods                                            []                  []               [get list]
deviceclasses.resource.k8s.io                   []                  []               [get list]
resourceclaims.resource.k8s.io                  []                  []               [get list]
resourceslices.resource.k8s.io                  []                  []               [get list]
X
expect pass "can-i list: healthy output (selfsubject create excluded)" check t "" "$(canilist_offenders "$T/ok.txt")"
sed 's/^pods .*/pods                                            []                  []               [get list delete]/' "$T/ok.txt" >"$T/del.txt"
expect fail "can-i list: extra verb on pods" check t "" "$(canilist_offenders "$T/del.txt")"
sed 's/^nodes .*/nodes                                           []                  []               [get list watch]/' "$T/ok.txt" >"$T/watch.txt"
expect fail "can-i list: watch on nodes" check t "" "$(canilist_offenders "$T/watch.txt")"
: >"$T/empty.txt"
expect fail "can-i list: empty file cannot pass" check t "" "$(canilist_offenders "$T/empty.txt")"
grep -v '^resourceslices' "$T/ok.txt" >"$T/noslices.txt"
expect fail "can-i list: missing resourceslices cannot pass" check t "" "$(canilist_offenders "$T/noslices.txt")"
grep -v '^pods' "$T/ok.txt" >"$T/nopods.txt"
expect fail "can-i list: missing pods cannot pass" check t "" "$(canilist_offenders "$T/nopods.txt")"
expect fail "can-i list: missing file cannot pass" check t "" "$(canilist_offenders "$T/nonexistent")"

# --- exit_code: reads recorded codes; a missing record never equals a number
OUT=$T; printf '%-40s exit=%s\n' forbidden-report-ns-strict 3 unreachable 0 >"$OUT/exit-codes.txt"
expect pass "exit_code: strict is 3" check t 3 "$(exit_code forbidden-report-ns-strict)"
expect fail "exit_code: strict is not 0" check t 0 "$(exit_code forbidden-report-ns-strict)"
expect fail "exit_code: unreachable is not 3" check t 3 "$(exit_code unreachable)"
expect fail "exit_code: unrecorded run" check t 0 "$(exit_code never-ran)"

# --- host_goarch
expect pass "host_goarch x86_64" check t amd64 "$(host_goarch x86_64)"
expect pass "host_goarch aarch64" check t arm64 "$(host_goarch aarch64)"
expect pass "host_goarch arm64" check t arm64 "$(host_goarch arm64)"
expect fail "host_goarch riscv64 is refused" host_goarch riscv64

# --- kind_version_ok
KIND_VERSION=v0.33.0
expect pass "kind version matches" kind_version_ok "kind v0.33.0 go1.26.0 linux/amd64"
expect fail "kind version differs" kind_version_ok "kind v0.32.0 go1.26.0 linux/amd64"
expect fail "kind version empty" kind_version_ok ""

# --- require_eq aborts on mismatch
expect pass "require_eq equal" require_eq t abc abc
expect fail "require_eq mismatch" require_eq t abc abd

[ "$BAD" -eq 0 ] && echo "test-live-helpers: ok" || { echo "test-live-helpers: $BAD failure(s)" >&2; exit 1; }
