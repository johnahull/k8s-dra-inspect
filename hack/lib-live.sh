#!/usr/bin/env bash
# Pure helpers shared by hack/live-kind.sh and hack/live-apiserver.sh.
# Sourced, never executed. Callers set FAILS=0 and, where a helper needs them,
# OUT (output dir), DI (the dra-inspect binary or wrapper), SNAP (snapshot file)
# and SA (service account user). Offline self-test: hack/test-live-helpers.sh
check() { # NAME WANT GOT -- one assertion; prints a FAIL line instead of aborting
  [ "${RED:-0}" = 1 ] && set -- "$1" "RED-WRONG-$2" "$3"   # --red: prove every assertion can fail
  if [ "$2" = "$3" ]; then echo "PASS  $1"; else echo "FAIL  $1: want [$2] got [$3]"; FAILS=$((FAILS + 1)); fi
}
xfail() { # NAME WANT GOT -- a KNOWN FINDING: expected to differ. Matching now means it was fixed: drop the xfail.
  [ "${RED:-0}" = 1 ] && set -- "$1" "RED-WRONG-$2" "$3"
  if [ "$2" = "$3" ]; then echo "XPASS $1: finding fixed, turn this back into a check"; FAILS=$((FAILS + 1))
  else echo "XFAIL $1: want [$2] got [$3]"; fi
}
is_forbidden() { # NAME OUTPUT -- a probe that must be refused with a Forbidden error
  grep -qi 'forbidden' <<<"$2" || echo "  probe output: ${2:0:240}"   # kubectl spells it both ways
  check "$1" true "$(grep -qi 'forbidden' <<<"$2" && echo true || echo false)"
}
absent() { # NAME MARKER FILE_OR_DIR... -- every path must exist; a grep error (rc>1) is a FAIL, not "absent"
  local n=$1 m=$2 p rc=0; shift 2
  for p in "$@"; do [ -e "$p" ] || { check "$n" "absent" "missing path $p"; return; }; done
  grep -rqF -- "$m" "$@" 2>/dev/null || rc=$?
  case $rc in 0) rc=present ;; 1) rc=absent ;; *) rc="grep error" ;; esac
  check "$n" "absent" "$rc"
}
require_eq() { # NAME WANT GOT -- abort the whole run on mismatch (for preconditions)
  [ "$2" = "$3" ] || { echo "ABORT $1: want [$2] got [$3]" >&2; exit 1; }
}
canilist_offenders() { # FILE -- prints problems in `kubectl auth can-i --list` output; empty means good
  local f=$1 body need
  [ -s "$f" ] || { echo "can-i list is empty or missing"; return 0; }
  # Every SA may create selfsubject{reviews,accessreviews,rulesreviews}; they are not ours.
  body=$(grep -Eiv '^ *selfsubject' "$f" | tail -n +2)
  for need in pods 'resourceslices\.resource\.k8s\.io'; do
    grep -Eq "^ *$need[[:space:]]" <<<"$body" || echo "no $need rule listed"
  done
  { grep -E '\.k8s\.io|^ *(pods|nodes)[[:space:]]' <<<"$body" | grep -Ev '\[get list\][[:space:]]*$'; } || true
}
exit_code() { # NAME -- the exit code recorded by run(), or "unrecorded"
  awk -v n="$1" '$1==n {sub("exit=", "", $2); print $2; f=1} END {if (!f) print "unrecorded"}' "$OUT/exit-codes.txt"
}
host_goarch() { # UNAME_M -- kind nodes follow the host architecture
  case $1 in
    x86_64 | amd64) echo amd64 ;;
    aarch64 | arm64) echo arm64 ;;
    *) echo "unsupported host architecture: $1" >&2; return 1 ;;
  esac
}
free_kb() { df -Pk "$1" | awk 'NR==2{print $4}'; }
wait_gone() { # NS JOB -- wait for the Job's pods to be deleted (after helm uninstall)
  kubectl -n "$1" wait --for=delete pod -l "job-name=$2" --timeout=60s >/dev/null 2>&1 || true
}
same_results() { # ARGS... -- live results[] must equal replay results[] (envelope source/generatedAt ignored)
  local live replay
  live=$("$DI" "$@" -o json 2>/dev/null | jq -S '.results' || true)
  replay=$("$DI" "$@" --from "$SNAP" -o json 2>/dev/null | jq -S '.results' || true)
  check "non-empty results: $*" "true" "$([ -n "$live" ] && [ "$live" != "[]" ] && echo true || echo false)"
  if [ "$live" = "$replay" ]; then check "replay==live: $*" ok ok
  else check "replay==live: $*" "live $(jq length <<<"$live") results" "replay differs: $(diff <(echo "$live") <(echo "$replay") | head -n 3 | tr '\n' ' ')"; fi
}
can_i() { # WANT VERB RESOURCE... -- via impersonation of $SA
  local want=$1; shift
  check "can-i $* as $SA" "$want" "$(kubectl auth can-i "$@" --as="$SA" 2>/dev/null || true)"
}
run() { # NAME ARGS... -- run the CLI, save stdout+stderr to $OUT/NAME.txt, record exit code
  local name=$1; shift
  set +e; "$DI" "$@" >"$OUT/$name.txt" 2>&1; local rc=$?; set -e
  printf '%-40s exit=%s\n' "$name" "$rc" | tee -a "$OUT/exit-codes.txt"
}
port_free() { # PORT -- true when nothing listens on it
  ! ss -Hltn "sport = :$1" 2>/dev/null | grep -q .
}
pick_port() { # prints a free high port; callers pass already-chosen ports to avoid
  local p i
  for i in $(seq 1 50); do
    p=$((20000 + RANDOM % 20000))
    case " $* " in *" $p "*) continue ;; esac
    port_free "$p" && { echo "$p"; return; }
  done
  echo "no free port found" >&2; return 1
}
golden_results() { # NAME GOLDEN_JSON JQ_FILTER ARGS... -- live results[] must equal the committed golden's (both through FILTER, e.g. '.')
  local n=$1 g=$2 f=$3 live want; shift 3
  want=$(jq -S ".results | $f" "$g")
  live=$("$DI" "$@" -o json 2>/dev/null | jq -S ".results | $f" || true)
  if [ "$live" = "$want" ]; then check "golden: $n" ok ok
  else check "golden: $n" "golden $(jq length <<<"$want") results" "live differs: $(diff <(echo "$want") <(echo "$live") | grep -c '^[<>]') changed lines"; fi
}
nsprofile_offenders() { # FILE -- problems in `can-i --list -n NS` for the namespace profile; empty means good
  local f=$1 body need
  [ -s "$f" ] || { echo "can-i list is empty or missing"; return 0; }
  body=$(grep -Eiv '^ *selfsubject' "$f" | tail -n +2 | grep -Ev '^ *\[' || true)   # drop selfsubject* and non-resource URL rows
  for need in pods 'resourceclaims\.resource\.k8s\.io'; do
    grep -Eq "^ *$need[[:space:]]" <<<"$body" || echo "no $need rule listed"
  done
  { grep -Ev '\[get list\][[:space:]]*$' <<<"$body" | grep -E '[a-z]'; } || true
}
build_image() { # needs REPO SCRATCH IMAGE; sets BUILT_IMAGE VER -- goreleaser-checked binary, host-arch image, check-image
  local arch
  arch=$(host_goarch "$(uname -m)") || return 1
  (cd "$REPO" && hack/check-release.sh)
  VER=$(grep -o '"version":"[^"]*"' "$REPO/dist/metadata.json" | cut -d'"' -f4)
  mkdir -p "$SCRATCH/ctx/linux/$arch"
  (cd "$REPO" && CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -trimpath -ldflags "-s -w -X main.version=$VER" \
    -o "$SCRATCH/ctx/linux/$arch/dra-inspect" ./cmd/dra-inspect)
  cp "$REPO/Dockerfile" "$SCRATCH/ctx/"
  BUILT_IMAGE=1   # set first: a half-built image is still removed
  podman build --platform "linux/$arch" --build-arg "TARGETPLATFORM=linux/$arch" -t "$IMAGE" "$SCRATCH/ctx"
  (cd "$REPO" && hack/check-image.sh "$IMAGE" "$VER")
}
