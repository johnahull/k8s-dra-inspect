#!/usr/bin/env bash
# Live verification of dra-inspect on a throwaway kind cluster (rootless podman).
# Plan and results: docs/harness-test-plan.md. Step numbers below match the plan.
# podman only: Docker is untested and not supported by this script.
#
#   hack/live-kind.sh [--keep] [--scratch DIR]
#
# Safe by construction: scratch kubeconfig only (never ~/.kube), one cluster
# name, no sudo, nothing deleted that this run did not create. --keep leaves the
# cluster (and scratch dir) in place; cleanup commands are printed.
# Needs: podman, kind, kubectl, helm, jq, go, goreleaser, git.
# Offline self-test of the helpers below: hack/test-live-helpers.sh
set -euo pipefail

# ---- pinned versions (change here only) -------------------------------------
KIND_VERSION=v0.33.0   # go install sigs.k8s.io/kind@v0.33.0
NODE_IMAGE='kindest/node:v1.34.11@sha256:44e222ee2132dab25ff87301682f89eb82c7880ea3a1bf543bfe9708fd08d67d'
DRIVER_REPO=https://github.com/kubernetes-sigs/dra-example-driver.git
DRIVER_TAG=v0.5.0
DRIVER_COMMIT=93ff610f3c3bfd603c592d1c281a236c1463edb9
CLUSTER=dra-inspect-test
IMAGE_REPO=localhost/dra-inspect
IMAGE_TAG=harness-$$   # unique per run: a pre-existing :harness image is never touched
IMAGE=$IMAGE_REPO:$IMAGE_TAG
OPAQUE_MARKER=OPAQUE-MARKER-7c1e5d
SECRET_MARKER=SECRET-MARKER-42b9aa
# -----------------------------------------------------------------------------

FAILS=0
check() { # NAME WANT GOT -- one assertion; prints a FAIL line instead of aborting
  if [ "$2" = "$3" ]; then echo "PASS  $1"; else echo "FAIL  $1: want [$2] got [$3]"; FAILS=$((FAILS + 1)); fi
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
kind_version_ok() { # `kind version` OUTPUT -- must be the pinned KIND_VERSION
  [ "$(awk '{print $2}' <<<"$1")" = "$KIND_VERSION" ] || { echo "kind version [$1] != $KIND_VERSION" >&2; return 1; }
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
wait_job() { # NS NAME -- waits for Complete or Failed, prints the outcome
  local i s f
  for i in $(seq 1 90); do
    s=$(kubectl -n "$1" get job "$2" -o jsonpath='{.status.succeeded}' 2>/dev/null || true)
    f=$(kubectl -n "$1" get job "$2" -o jsonpath='{.status.failed}' 2>/dev/null || true)
    if [ -n "$s" ]; then echo Complete; return; fi
    if [ -n "$f" ]; then echo Failed; return; fi
    sleep 2
  done
  echo Timeout
}
job_exit() { # NS NAME
  kubectl -n "$1" get pod -l "job-name=$2" -o jsonpath='{.items[0].status.containerStatuses[0].state.terminated.exitCode}'
}
job_json() { # NS NAME -- the README extraction
  kubectl -n "$1" logs "job/$2" | sed -n '/^{$/,/^}$/p'
}
run() { # NAME ARGS... -- run the CLI, save stdout+stderr to $OUT/NAME.txt, record exit code
  local name=$1; shift
  set +e; "$DI" "$@" >"$OUT/$name.txt" 2>&1; local rc=$?; set -e
  printf '%-40s exit=%s\n' "$name" "$rc" | tee -a "$OUT/exit-codes.txt"
}

cleanup() {
  local rc=$?
  set +e
  if [ "${KEEP:-0}" = 1 ]; then
    echo "--keep: cluster kept. Remove with:"
    echo "  KIND_EXPERIMENTAL_PROVIDER=podman kind delete cluster --name $CLUSTER --kubeconfig $SCRATCH/kind.kubeconfig"
    [ "${BUILT_IMAGE:-0}" = 1 ] && echo "  podman rmi $IMAGE"
    exit "$rc"
  fi
  if [ "${CREATED_CLUSTER:-0}" = 1 ]; then
    kind delete cluster --name "$CLUSTER" --kubeconfig "$SCRATCH/kind.kubeconfig"
  fi
  # remove only what this run created
  [ "${BUILT_IMAGE:-0}" = 1 ] && podman rmi "$IMAGE" >/dev/null 2>&1
  [ "${NET_PREEXISTED:-1}" = 0 ] && [ "${CREATED_CLUSTER:-0}" = 1 ] && ! podman ps -a --filter label=io.x-k8s.kind.cluster --format '{{.Names}}' | grep -q . &&
    podman network rm kind >/dev/null 2>&1
  [ "${MADE_SCRATCH:-0}" = 1 ] && rm -rf "$SCRATCH"
  command -v kind >/dev/null && echo "cleanup done: kind clusters: [$(kind get clusters 2>/dev/null | tr '\n' ' ')]"
  exit "$rc"
}

main() {
  KEEP=0; SCRATCH=''; MADE_SCRATCH=0; CREATED_CLUSTER=0; BUILT_IMAGE=0; NET_PREEXISTED=1
  # Traps first, so even an early refusal removes the mktemp dir. INT/TERM exit
  # explicitly so the EXIT trap (cleanup) always runs.
  trap cleanup EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  while [ $# -gt 0 ]; do
    case $1 in
      --keep) KEEP=1 ;;
      --scratch) SCRATCH=${2:?--scratch DIR}; shift ;;
      *) echo "usage: $0 [--keep] [--scratch DIR]" >&2; exit 2 ;;
    esac
    shift
  done
  cd "$(dirname "$0")/.."
  REPO=$PWD
  if [ -z "$SCRATCH" ]; then SCRATCH=$(mktemp -d); MADE_SCRATCH=1; else mkdir -p "$SCRATCH"; fi
  SCRATCH=$(cd "$SCRATCH" && pwd)
  KUBECONFIG_FILE=$SCRATCH/kind.kubeconfig
  # Refuse an inherited KUBECONFIG that could point at a real cluster; the run
  # then exports its own scratch KUBECONFIG and passes --kubeconfig everywhere.
  if [ -n "${KUBECONFIG:-}" ] && [ "$KUBECONFIG" != "$KUBECONFIG_FILE" ]; then
    echo "refusing to run: KUBECONFIG is set to $KUBECONFIG; unset it" >&2; exit 2
  fi
  export KUBECONFIG=$KUBECONFIG_FILE KIND_EXPERIMENTAL_PROVIDER=podman
  for t in podman kind kubectl helm jq go goreleaser git; do
    command -v "$t" >/dev/null || { echo "missing tool: $t (kind: go install sigs.k8s.io/kind@$KIND_VERSION)" >&2; exit 2; }
  done
  GRAPHROOT=$(podman info --format '{{.Store.GraphRoot}}' 2>/dev/null || true)
  for d in "$SCRATCH" "$HOME" ${GRAPHROOT:+"$GRAPHROOT"}; do
    [ "$(free_kb "$d")" -ge 15000000 ] || { echo "need >= 15 GB free on $d" >&2; exit 2; }
  done
  kind_version_ok "$(kind version)" || exit 2
  GOARCH_HOST=$(host_goarch "$(uname -m)") || exit 2
  if podman image exists "$IMAGE"; then echo "refusing: image $IMAGE already exists" >&2; exit 2; fi
  if kind get clusters 2>/dev/null | grep -qx "$CLUSTER"; then
    echo "refusing: kind cluster $CLUSTER already exists (not created by this run)" >&2; exit 2
  fi
  podman network ls --format '{{.Name}}' | grep -qx kind && NET_PREEXISTED=1 || NET_PREEXISTED=0
  OUT=$SCRATCH/out; mkdir -p "$OUT"; : >"$OUT/exit-codes.txt"

  echo "== 1. build binary and image ($GOARCH_HOST)"
  DI=$SCRATCH/dra-inspect
  go build -o "$DI" ./cmd/dra-inspect
  hack/check-release.sh
  # kind nodes follow the host architecture; build for it explicitly (not --single-target's guess).
  VER=$(grep -o '"version":"[^"]*"' dist/metadata.json | cut -d'"' -f4)
  mkdir -p "$SCRATCH/ctx/linux/$GOARCH_HOST"
  CGO_ENABLED=0 GOOS=linux GOARCH=$GOARCH_HOST go build -trimpath -ldflags "-s -w -X main.version=$VER" \
    -o "$SCRATCH/ctx/linux/$GOARCH_HOST/dra-inspect" ./cmd/dra-inspect
  cp Dockerfile "$SCRATCH/ctx/"
  BUILT_IMAGE=1
  podman build --platform "linux/$GOARCH_HOST" --build-arg "TARGETPLATFORM=linux/$GOARCH_HOST" -t "$IMAGE" "$SCRATCH/ctx"
  hack/check-image.sh "$IMAGE" "$VER"

  echo "== 2. cluster $CLUSTER ($NODE_IMAGE)"
  CREATED_CLUSTER=1   # set first: a half-created cluster is still deleted
  kind create cluster --name "$CLUSTER" --image "$NODE_IMAGE" --kubeconfig "$KUBECONFIG_FILE" --wait 120s
  check "current-context" "kind-$CLUSTER" "$(kubectl config current-context)"
  kubectl api-versions | grep -x 'resource.k8s.io/v1'
  podman save "$IMAGE" -o "$SCRATCH/img.tar"
  kind load image-archive "$SCRATCH/img.tar" --name "$CLUSTER"

  echo "== 3. sample driver $DRIVER_TAG"
  git clone -q --depth 1 --branch "$DRIVER_TAG" "$DRIVER_REPO" "$SCRATCH/driver"
  require_eq "driver commit" "$DRIVER_COMMIT" "$(git -C "$SCRATCH/driver" rev-parse HEAD)"
  # Only the chart is used; the plugin image is the published registry.k8s.io one (chart appVersion).
  helm install dra-example-driver "$SCRATCH/driver/deployments/helm/dra-example-driver" \
    -n dra-example-driver --create-namespace --wait --timeout 5m
  for _ in $(seq 1 90); do
    DEVS=$(kubectl get resourceslices -o json | jq '[.items[].spec.devices | length] | add // 0')
    [ "$DEVS" -gt 0 ] && break; sleep 2
  done
  check "slices published devices>0" true "$([ "${DEVS:-0}" -gt 0 ] && echo true || echo false)"
  echo "== 4. workstation CLI"
  for c in slices drivers attributes topology deviceclasses claims; do
    run "inspect-$c" --kubeconfig "$KUBECONFIG_FILE" inspect "$c"
    run "inspect-$c-json" --kubeconfig "$KUBECONFIG_FILE" inspect "$c" -o json
  done
  for c in alignment counters capacity topology kubevirt; do
    run "check-$c" --kubeconfig "$KUBECONFIG_FILE" check "$c"
  done
  run report-cluster --kubeconfig "$KUBECONFIG_FILE" report cluster
  run report-cluster-json --kubeconfig "$KUBECONFIG_FILE" report cluster -o json
  run report-support --kubeconfig "$KUBECONFIG_FILE" report support -o json
  for n in $(awk '$1 ~ /^(inspect|report)-/ {print $1}' "$OUT/exit-codes.txt"); do check "exit $n" 0 "$(exit_code "$n")"; done

  echo "== 5. allocate a claim"
  cat <<EOF | kubectl apply -f -
apiVersion: resource.k8s.io/v1
kind: ResourceClaim
metadata: {name: live-claim, namespace: default}
spec:
  devices:
    requests:
    - {name: gpu, exactly: {deviceClassName: gpu.example.com}}
---
apiVersion: v1
kind: Pod
metadata: {name: live-pod, namespace: default}
spec:
  containers:
  - name: ctr
    image: ubuntu:22.04
    command: ["bash", "-c"]
    args: ["trap 'exit 0' TERM; sleep 9999 & wait"]
    resources: {claims: [{name: gpu}]}
  resourceClaims:
  - {name: gpu, resourceClaimName: live-claim}
---
apiVersion: resource.k8s.io/v1
kind: ResourceClaim
metadata: {name: marker-claim, namespace: default}
spec:
  devices:
    requests:
    - {name: gpu, exactly: {deviceClassName: gpu.example.com}}
    config:
    - requests: [gpu]
      opaque:
        driver: marker.example.com
        parameters: {marker: $OPAQUE_MARKER}
---
apiVersion: v1
kind: Secret
metadata: {name: marker-secret, namespace: default}
stringData: {marker: $SECRET_MARKER}
EOF
  kubectl -n default wait --for=condition=Ready pod/live-pod --timeout=240s

  run inspect-claims-allocated --kubeconfig "$KUBECONFIG_FILE" inspect claims -o json
  check "claims: live-claim allocated" true \
    "$(jq '[.results[] | select(.subject | test("live-claim")) | .details.allocated] | any' "$OUT/inspect-claims-allocated.txt" 2>/dev/null || echo false)"

  echo "== 6. capture and replay"
  SNAP=$SCRATCH/S.json
  "$DI" --kubeconfig "$KUBECONFIG_FILE" snapshot capture --file "$SNAP" --with-adapters
  DI_ORIG=$DI
  # same_results passes --from to replay and --kubeconfig to live via a wrapper
  DI=$SCRATCH/di-wrap
  printf '#!/usr/bin/env bash\ncase " $* " in *" --from "*) exec %q "$@" ;; *) exec %q --kubeconfig %q "$@" ;; esac\n' \
    "$DI_ORIG" "$DI_ORIG" "$KUBECONFIG_FILE" >"$DI"
  chmod +x "$DI"
  for c in "inspect slices" "inspect drivers" "inspect attributes" "inspect topology" "inspect deviceclasses" \
           "inspect claims" "check alignment" "check counters" "check capacity" "check topology"; do
    # shellcheck disable=SC2086
    same_results $c
  done
  absent "no opaque marker in snapshot" "$OPAQUE_MARKER" "$SNAP"
  absent "no secret marker in snapshot" "$SECRET_MARKER" "$SNAP"
  absent "no markers in any CLI output" "MARKER-" "$OUT"
  check "snapshot mode" 600 "$(stat -c %a "$SNAP")"

  echo "== 7. cluster-profile Job under restricted PSA"
  for ns in dra-inspect-c dra-inspect-n; do
    kubectl create namespace "$ns"
    kubectl label namespace "$ns" pod-security.kubernetes.io/enforce=restricted
  done
  H=(deploy/helm/dra-inspect --set image.repository="$IMAGE_REPO" --set image.tag="$IMAGE_TAG" --set image.pullPolicy=Never)
  helm install dra-inspect "${H[@]}" -n dra-inspect-c --set job.enabled=true --set job.strict=true
  r=$(wait_job dra-inspect-c dra-inspect)
  check "cluster-profile strict Job completes with exit 0" "Complete 0" "$r $(job_exit dra-inspect-c dra-inspect)"
  kubectl -n dra-inspect-c get job dra-inspect -o jsonpath='{.status.conditions}'; echo
  job_json dra-inspect-c dra-inspect | tee "$OUT/job-cluster.json" | jq -e '.results | length > 0' >/dev/null
  check "job cluster: some result not unavailable" true \
    "$(jq '[.results[] | select(.status != "unavailable")] | length > 0' "$OUT/job-cluster.json")"
  echo "== 8. read-only proof with auth can-i"
  SA=system:serviceaccount:dra-inspect-c:dra-inspect
  for r in resourceslices.resource.k8s.io deviceclasses.resource.k8s.io resourceclaims.resource.k8s.io pods nodes; do
    for v in get list; do can_i yes "$v" "$r"; done
    for v in create update patch delete deletecollection; do can_i no "$v" "$r"; done
  done
  can_i no create pods --subresource=exec
  can_i no get secrets
    kubectl auth can-i --list --as="$SA" | tee "$OUT/can-i-list.txt"
  check "can-i --list: only get/list on pods, nodes and *.k8s.io (selfsubject* excluded; non-empty; pods and resourceslices present)" "" \
    "$(canilist_offenders "$OUT/can-i-list.txt")"
  for verb in "create cm x --from-literal=a=b" "delete pod live-pod" "exec live-pod -- true"; do
    # shellcheck disable=SC2086  # $verb is intentionally word-split into a kubectl command line
    out=$(kubectl --as="$SA" -n default $verb 2>&1 || true)   # expected-denied probes on the test cluster only
    check "forbidden: kubectl $verb" "true" "$(grep -q 'Forbidden\|forbidden' <<<"$out" && echo true || echo false)"
  done

  echo "== 9. namespace profile"
  helm install dra-inspect "${H[@]}" -n dra-inspect-n --set rbac.profile=namespace --set 'rbac.namespaces={default}' --set job.enabled=true
  r=$(wait_job dra-inspect-n dra-inspect)
  check "namespace profile non-strict Job completes with exit 0" "Complete 0" "$r $(job_exit dra-inspect-n dra-inspect)"
  job_json dra-inspect-n dra-inspect >"$OUT/job-ns.json"
  jq -r '.results[] | select(.status=="unavailable") | .check' "$OUT/job-ns.json" | sort -u
  SA=system:serviceaccount:dra-inspect-n:dra-inspect
  can_i no list resourceslices.resource.k8s.io
  can_i yes list resourceclaims.resource.k8s.io -n default
  helm uninstall dra-inspect -n dra-inspect-n
  wait_gone dra-inspect-n dra-inspect
  check "previous Job pod deleted" 0 "$(kubectl -n dra-inspect-n get pod -l job-name=dra-inspect -o name | wc -l)"
  helm install dra-inspect "${H[@]}" -n dra-inspect-n --set rbac.profile=namespace --set 'rbac.namespaces={default}' \
    --set job.enabled=true --set job.strict=true
  r=$(wait_job dra-inspect-n dra-inspect)
  check "namespace profile + strict Job fails with exit 3" "Failed 3" "$r $(job_exit dra-inspect-n dra-inspect)"

  echo "== 10. adapters and failure modes"
  TOKEN=$(kubectl -n dra-inspect-n create token dra-inspect)
  kubectl config view --raw --minify -o jsonpath='{.clusters[0].cluster.certificate-authority-data}' | base64 -d >"$SCRATCH/ca.crt"
  SRV=$(kubectl config view --raw --minify -o jsonpath='{.clusters[0].cluster.server}')
  SAK=$SCRATCH/sa.kubeconfig
  kubectl config set-cluster k --server="$SRV" --certificate-authority="$SCRATCH/ca.crt" --embed-certs --kubeconfig "$SAK" >/dev/null
  kubectl config set-credentials u --token="$TOKEN" --kubeconfig "$SAK" >/dev/null
  kubectl config set-context c --cluster=k --user=u --kubeconfig "$SAK" >/dev/null
  kubectl config use-context c --kubeconfig "$SAK" >/dev/null
  DI=$DI_ORIG
  run forbidden-report --kubeconfig "$SAK" report cluster
  run forbidden-report-ns --kubeconfig "$SAK" -n default report cluster
  run forbidden-report-ns-strict --kubeconfig "$SAK" -n default report cluster --strict
  printf 'apiVersion: v1\nkind: Config\nclusters: [{name: c, cluster: {server: "https://127.0.0.1:1", insecure-skip-tls-verify: true}}]\nusers: [{name: u, user: {token: x}}]\ncontexts: [{name: c, context: {cluster: c, user: u}}]\ncurrent-context: c\n' >"$SCRATCH/closed.kubeconfig"
  run unreachable --kubeconfig "$SCRATCH/closed.kubeconfig" report cluster
  run unreachable-strict --kubeconfig "$SCRATCH/closed.kubeconfig" report cluster --strict
  run timeout-1ns --kubeconfig "$KUBECONFIG_FILE" --timeout 1ns inspect slices
  for want in forbidden-report:0 forbidden-report-ns:0 forbidden-report-ns-strict:3 unreachable:0 unreachable-strict:3 timeout-1ns:0; do
    check "exit ${want%%:*}" "${want##*:}" "$(exit_code "${want%%:*}")"
  done

  echo "== done: $FAILS failed assertion(s); outputs in $OUT"
  [ "$FAILS" -eq 0 ]
}

# Sourcing the file (for tests of the helpers) does not run main.
[[ "${BASH_SOURCE[0]}" != "$0" ]] || main "$@"
