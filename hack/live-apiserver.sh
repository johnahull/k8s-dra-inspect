#!/usr/bin/env bash
# API-server-level live verification of dra-inspect: a REAL kube-apiserver + etcd run
# as plain host processes on 127.0.0.1 (no kubelet, scheduler or controllers), with the
# binaries taken from the already-cached kindest/node image. Use it where `kind` cannot
# create a cluster. What it can and cannot show: docs/harness-test-plan.md.
#
#   hack/live-apiserver.sh [--keep] [--red] [--scratch DIR]
#
#   --red   corrupt every expectation (see check() in lib-live.sh): the run must FAIL on
#           every assertion, proving each one can fail. Exit code 0 means the proof broke.
#   --keep  leave the scratch dir (and print how to stop the processes if they are alive).
#
# Safe by construction: scratch kubeconfig only (never ~/.kube), 127.0.0.1 only, high
# ports, own uid, no sudo, nothing pulled or downloaded, and cleanup kills exactly the
# PIDs this run started and removes only the container/image this run created.
# Needs: podman (with the node image already cached), jq, openssl, curl, ss, tar, python3
# with PyYAML, helm, go, goreleaser; kubectl from $KUBECTL, PATH, or the node image.
# Offline self-test of the shared helpers: hack/test-live-helpers.sh
set -euo pipefail
# shellcheck source=hack/lib-live.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib-live.sh"

# ---- pinned inputs (change here only) ---------------------------------------
NODE_DIGEST=sha256:44e222ee2132dab25ff87301682f89eb82c7880ea3a1bf543bfe9708fd08d67d  # kindest/node:v1.34.11
BLOB_DIR=var/lib/containerd/io.containerd.content.v1.content/blobs/sha256
BLOB_APISERVER=0ada6ac81ce9b89c0d5d5de53042ada4749de04746aa101a53c6dfbf61080453      # layer holding usr/local/bin/kube-apiserver
BLOB_ETCD=22e7170f8acc95dc76018dc62ef3d2b73a2d06c1530ba5a5cfc16ccac6e830d4            # layer holding usr/local/bin/etcd
WANT_APISERVER=v1.34.11
WANT_ETCD=3.6.5
# Alpha gates the fixtures need (counters, consumable capacity); everything else is default.
FEATURE_GATES=DRAPartitionableDevices=true,DRAConsumableCapacity=true
IMAGE_REPO=localhost/dra-inspect
IMAGE_TAG=harness-$$   # unique per run: a pre-existing image is never touched
IMAGE=$IMAGE_REPO:$IMAGE_TAG
OPAQUE_MARKER=OPAQUE-MARKER-7c1e5d
# Fixture objects the 1.34 apiserver is expected to refuse: list-valued attributes
# (ints/strings) are not in its schema, and the xe9785l/xe9680 DeviceClass names overlap.
WANT_REJECTED_SLICES=6
WANT_DUP_CLASSES=5
# -----------------------------------------------------------------------------

FAILS=0
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

kubectl() { command "$KUBECTL" --kubeconfig "$ADMIN" "$@"; }   # also used by can_i in lib-live.sh
mk_kubeconfig() { # FILE TOKEN [CA] [SERVER]
  local f=$1 tok=$2 ca=${3:-$PKI/ca.crt} srv=${4:-https://127.0.0.1:$API_PORT}
  rm -f "$f"
  "$KUBECTL" config set-cluster k --server="$srv" --certificate-authority="$ca" --embed-certs --kubeconfig "$f" >/dev/null
  "$KUBECTL" config set-credentials u --token="$tok" --kubeconfig "$f" >/dev/null
  "$KUBECTL" config set-context c --cluster=k --user=u --kubeconfig "$f" >/dev/null
  "$KUBECTL" config use-context c --kubeconfig "$f" >/dev/null
  chmod 600 "$f"
}
now() { date +%s.%N; }
within() { # NAME LO HI VALUE -- numeric range assertion
  check "$1" "true" "$(awk -v v="$4" -v lo="$2" -v hi="$3" 'BEGIN{print (v>=lo && v<hi) ? "true" : "false"}')"
}
jqs() { jq -S "$@"; }
# Output the fixture objects stripped of server-owned fields, as List JSON (main) and the
# subset carrying status (status). Pods get a minimal container.
load_py() { python3 -I - "$@" <<'PY'
import sys, json, yaml, glob
out = sys.argv[1]; main = []; stat = []
for d in sys.argv[2:]:
    for f in sorted(glob.glob(d + "/*.yaml")):
        doc = yaml.safe_load(open(f))
        for o in (doc["items"] if doc.get("kind") == "List" else [doc]):
            if o["kind"] not in ("ResourceSlice", "DeviceClass", "ResourceClaim", "Pod"):
                continue
            m = o["metadata"]
            for k in ("uid", "resourceVersion", "creationTimestamp", "generation", "managedFields", "ownerReferences"):
                m.pop(k, None)
            st = o.pop("status", None)
            if o["kind"] == "Pod":
                o["spec"].setdefault("containers", [{"name": "c", "image": "registry.invalid/x:1"}])
            main.append(o)
            if st:
                stat.append(dict(o, status=st))
for n, items in (("main", main), ("status", stat)):
    json.dump({"apiVersion": "v1", "kind": "List", "items": items}, open("%s/%s.json" % (out, n), "w"))
print(len(main), len(stat))
PY
}
# Turn the Job in the chart's rendered job.yaml into a bare Pod (stdin -> stdout), optionally privileged.
job_to_pod() { python3 -I -c '
import sys, json, yaml
j = yaml.safe_load(sys.stdin); t = j["spec"]["template"]
p = {"apiVersion": "v1", "kind": "Pod", "metadata": {"name": sys.argv[1], "namespace": j["metadata"]["namespace"],
     "labels": t["metadata"].get("labels", {})}, "spec": t["spec"]}
if sys.argv[2] == "privileged":
    # not privileged=true: --allow-privileged=false makes the apiserver refuse that in validation, before PodSecurity runs.
    p["spec"]["hostNetwork"] = True
    p["spec"]["containers"][0]["securityContext"] = {"allowPrivilegeEscalation": True, "runAsNonRoot": False}
json.dump(p, sys.stdout)' "$@"; }

load_set() { # NAME FIXTURE_DIR... -- create the objects (+status subresource); sets L_CREATED L_INVALID L_EXISTS L_OTHER
  local name=$1 d; shift
  d=$SCRATCH/load/$name; mkdir -p "$d"
  load_py "$d" "$@" >"$d/counts.txt"
  # --validate=false: kubectl's client-side schema check would stop at the first unknown field.
  kubectl create -f "$d/main.json" --validate=false >"$d/create.txt" 2>&1 || true
  if [ "$(jq '.items | length' "$d/status.json")" -gt 0 ]; then
    kubectl replace --subresource=status -f "$d/status.json" >"$d/status.txt" 2>&1 || true
  else : >"$d/status.txt"; fi
  L_CREATED=$(grep -c ' created$' "$d/create.txt" || true)
  L_INVALID=$(grep -c 'Error from server (Invalid)' "$d/create.txt" || true)
  L_EXISTS=$(grep -c 'Error from server (AlreadyExists)' "$d/create.txt" || true)
  L_OTHER=$(grep -v ' created$' "$d/create.txt" | grep -vc 'Error from server (\(Invalid\|AlreadyExists\))' || true)
}
wipe_dra() { # delete every DRA fixture object (our own throwaway cluster)
  kubectl delete resourceslices,deviceclasses --all --wait=false >/dev/null 2>&1 || true
  kubectl delete resourceclaims,pods --all -A --force --grace-period=0 --wait=false >/dev/null 2>&1 || true
  local i
  for i in $(seq 1 30); do
    [ "$(kubectl get resourceslices,deviceclasses,resourceclaims,pods -A -o name 2>/dev/null | wc -l)" = 0 ] && return
    sleep 1
  done
}
setup_wrapper() { # DI that sends --from calls to the binary and live calls to the cluster as ADMIN
  printf '#!/usr/bin/env bash\ncase " $* " in *" --from "*) exec %q "$@" ;; *) exec %q --kubeconfig %q "$@" ;; esac\n' \
    "$DI_BIN" "$DI_BIN" "$ADMIN" >"$SCRATCH/di-wrap"
  chmod +x "$SCRATCH/di-wrap"
}
extract_binaries() {
  local id x=$SCRATCH/extract
  if [ -x "$BIN/kube-apiserver" ] && [ -x "$BIN/etcd" ] && [ -x "$KUBECTL" ]; then return; fi
  id=$(podman images --digests --format '{{.ID}} {{.Digest}}' | awk -v d="$NODE_DIGEST" '$2==d {print $1; exit}')
  [ -n "$id" ] || { echo "node image $NODE_DIGEST is not cached locally; this script never pulls" >&2; exit 2; }
  CTR=dra-apisrv-extract-$$   # created, never started: no entrypoint runs
  podman create --name "$CTR" "$id" >/dev/null
  mkdir -p "$x" "$BIN"
  podman export "$CTR" | tar -x -C "$x" "$BLOB_DIR/$BLOB_APISERVER" "$BLOB_DIR/$BLOB_ETCD" usr/bin/kubectl
  podman rm "$CTR" >/dev/null; CTR=
  for b in "$BLOB_APISERVER" "$BLOB_ETCD"; do   # blobs are content-addressed: the name is their sha256
    require_eq "blob sha256 $b" "$b" "$(sha256sum "$x/$BLOB_DIR/$b" | cut -d' ' -f1)"
  done
  tar -x -f "$x/$BLOB_DIR/$BLOB_APISERVER" -C "$BIN" --strip-components=3 usr/local/bin/kube-apiserver
  tar -x -f "$x/$BLOB_DIR/$BLOB_ETCD" -C "$BIN" --strip-components=3 usr/local/bin/etcd
  [ -x "$KUBECTL" ] || cp "$x/usr/bin/kubectl" "$BIN/kubectl"   # only when no kubectl was given or on PATH
  rm -rf "$x"
}
stop_pid() { # PID -- CONT (it may be stopped), TERM, wait up to 10 s, then KILL
  local pid=$1 i
  [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null || return 0
  kill -CONT "$pid" 2>/dev/null || true; kill "$pid" 2>/dev/null || true
  for i in $(seq 1 20); do kill -0 "$pid" 2>/dev/null || return 0; sleep 0.5; done
  kill -KILL "$pid" 2>/dev/null || true
}

cleanup() {
  local rc=$?
  set +e
  if [ "${KEEP:-0}" = 1 ]; then
    echo "--keep: processes etcd=$ETCD_PID apiserver=$API_PID (127.0.0.1:${API_PORT:-?}) left running; stop with:"
    echo "  kill $ETCD_PID $API_PID"
    [ "${BUILT_IMAGE:-0}" = 1 ] && echo "  podman rmi $IMAGE"
    echo "  scratch: $SCRATCH"
    exit "$rc"
  fi
  stop_pid "${API_PID:-}"; stop_pid "${ETCD_PID:-}"
  [ -n "${CTR:-}" ] && podman rm -f "$CTR" >/dev/null 2>&1
  [ "${BUILT_IMAGE:-0}" = 1 ] && podman rmi "$IMAGE" >/dev/null 2>&1
  echo "== cleanup proof"
  for p in "${API_PID:-}" "${ETCD_PID:-}"; do
    [ -z "$p" ] || { kill -0 "$p" 2>/dev/null && echo "STILL RUNNING pid $p" || echo "pid $p gone"; }
  done
  for p in ${API_PORT:-} ${ETCD_PORT:-} ${PEER_PORT:-}; do
    port_free "$p" && echo "port $p free" || echo "PORT $p STILL LISTENING"
  done
  echo "podman ps -a: [$(podman ps -a --format '{{.Names}}' | tr '\n' ' ')]"
  echo "image $IMAGE: $(podman image exists "$IMAGE" && echo STILL PRESENT || echo removed)"
  if [ -n "${KUBE_MTIME:-}" ]; then
    [ "$(stat -c %Y "$HOME/.kube" 2>/dev/null)" = "$KUBE_MTIME" ] && echo "~/.kube mtime unchanged" || echo "~/.kube CHANGED"
  fi
  [ "${MADE_SCRATCH:-0}" = 1 ] && rm -rf "$SCRATCH"
  exit "$rc"
}

main() {
  KEEP=0; RED=0; SCRATCH=''; MADE_SCRATCH=0; BUILT_IMAGE=0; CTR=''; ETCD_PID=''; API_PID=''
  trap cleanup EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  while [ $# -gt 0 ]; do
    case $1 in
      --keep) KEEP=1 ;;
      --red) RED=1 ;;
      --scratch) SCRATCH=${2:?--scratch DIR}; shift ;;
      *) echo "usage: $0 [--keep] [--red] [--scratch DIR]" >&2; exit 2 ;;
    esac
    shift
  done
  export RED
  cd "$HERE/.."
  REPO=$PWD
  if [ -z "$SCRATCH" ]; then SCRATCH=$(mktemp -d); MADE_SCRATCH=1; else mkdir -p "$SCRATCH"; fi
  SCRATCH=$(cd "$SCRATCH" && pwd)
  PKI=$SCRATCH/pki; BIN=$SCRATCH/bin; ADMIN=$SCRATCH/admin.kubeconfig
  # Refuse an inherited KUBECONFIG that could point at a real cluster.
  if [ -n "${KUBECONFIG:-}" ] && [ "$KUBECONFIG" != "$ADMIN" ]; then
    echo "refusing to run: KUBECONFIG is set to $KUBECONFIG; unset it" >&2; exit 2
  fi
  export KUBECONFIG=$ADMIN KUBECACHEDIR=$SCRATCH/kubecache   # kubectl's discovery cache would otherwise land in ~/.kube
  KUBE_MTIME=$(stat -c %Y "$HOME/.kube" 2>/dev/null || true)
  for t in podman jq openssl curl ss tar python3 helm go goreleaser; do
    command -v "$t" >/dev/null || { echo "missing tool: $t" >&2; exit 2; }
  done
  python3 -I -c 'import yaml' 2>/dev/null || { echo "python3 needs PyYAML" >&2; exit 2; }
  if podman image exists "$IMAGE"; then echo "refusing: image $IMAGE already exists" >&2; exit 2; fi
  rm -rf "$SCRATCH/etcd-data" "$SCRATCH/load" "$PKI" "$SCRATCH/kubecache"   # a reused --scratch starts clean
  OUT=$SCRATCH/out; rm -rf "$OUT"; mkdir -p "$OUT" "$BIN" "$PKI"; : >"$OUT/exit-codes.txt"

  echo "== 0. binaries from the cached node image ($NODE_DIGEST)"
  if [ -n "${KUBECTL:-}" ]; then :; elif type -P kubectl >/dev/null; then KUBECTL=$(type -P kubectl); else KUBECTL=$BIN/kubectl; fi
  extract_binaries
  require_eq "kube-apiserver version" "Kubernetes $WANT_APISERVER" "$("$BIN/kube-apiserver" --version)"
  require_eq "etcd version" "$WANT_ETCD" "$("$BIN/etcd" --version | awk '/etcd Version/ {print $3}')"
  {
    echo "kube-apiserver $("$BIN/kube-apiserver" --version)"; echo "etcd $("$BIN/etcd" --version | head -n1)"
    echo "kubectl $("$KUBECTL" version --client | head -n1)"; echo "helm $(helm version --short)"
    echo "podman $(podman --version)"; echo "go $(go version)"; echo "feature gates: $FEATURE_GATES"
  } | tee "$OUT/versions.txt"

  echo "== A. build"
  DI_BIN=$SCRATCH/dra-inspect; DI=$DI_BIN
  go build -o "$DI_BIN" ./cmd/dra-inspect
  build_image

  echo "== 1. start etcd and kube-apiserver on 127.0.0.1"
  ETCD_PORT=$(pick_port); PEER_PORT=$(pick_port "$ETCD_PORT"); API_PORT=$(pick_port "$ETCD_PORT" "$PEER_PORT")
  openssl genrsa -out "$PKI/sa.key" 2048 2>/dev/null
  openssl rsa -in "$PKI/sa.key" -pubout -out "$PKI/sa.pub" 2>/dev/null
  ADMIN_TOKEN=$(openssl rand -hex 16)
  printf '%s,admin,admin-uid,system:masters\n' "$ADMIN_TOKEN" >"$PKI/tokens.csv"; chmod 600 "$PKI/tokens.csv"
  "$BIN/etcd" --name default --data-dir "$SCRATCH/etcd-data" \
    --listen-client-urls "http://127.0.0.1:$ETCD_PORT" --advertise-client-urls "http://127.0.0.1:$ETCD_PORT" \
    --listen-peer-urls "http://127.0.0.1:$PEER_PORT" --initial-advertise-peer-urls "http://127.0.0.1:$PEER_PORT" \
    --initial-cluster "default=http://127.0.0.1:$PEER_PORT" >"$OUT/etcd.log" 2>&1 &
  ETCD_PID=$!
  # --endpoint-reconciler-type=none: the loopback --advertise-address cannot be published as
  # the "kubernetes" Endpoints and would only log an error every few seconds.
  "$BIN/kube-apiserver" --etcd-servers="http://127.0.0.1:$ETCD_PORT" \
    --secure-port="$API_PORT" --bind-address=127.0.0.1 --advertise-address=127.0.0.1 --cert-dir="$PKI/certs" \
    --authorization-mode=Node,RBAC --token-auth-file="$PKI/tokens.csv" \
    --service-account-issuer=https://kubernetes.default.svc --service-account-key-file="$PKI/sa.pub" \
    --service-account-signing-key-file="$PKI/sa.key" --api-audiences=https://kubernetes.default.svc \
    --service-cluster-ip-range=10.96.0.0/16 --endpoint-reconciler-type=none \
    --feature-gates="$FEATURE_GATES" >"$OUT/apiserver.log" 2>&1 &
  API_PID=$!
  ready=no
  for _ in $(seq 1 90); do
    kill -0 "$API_PID" 2>/dev/null || break
    [ "$(curl -sk "https://127.0.0.1:$API_PORT/readyz" 2>/dev/null)" = ok ] && { ready=yes; break; }
    sleep 1
  done
  check "apiserver ready" yes "$ready"
  [ "$ready" = yes ] || { tail -n 20 "$OUT/apiserver.log" >&2; exit 1; }
  cp "$PKI/certs/apiserver.crt" "$PKI/ca.crt"   # self-signed serving cert doubles as the CA
  mk_kubeconfig "$ADMIN" "$ADMIN_TOKEN"
  check "serving cert SAN has 127.0.0.1" true \
    "$(openssl x509 -in "$PKI/ca.crt" -noout -ext subjectAltName | grep -q 'IP Address:127.0.0.1' && echo true || echo false)"
  check "resource.k8s.io/v1 served" resource.k8s.io/v1 "$(kubectl api-versions | grep -x resource.k8s.io/v1 || true)"
  check "server version" "$WANT_APISERVER" "$(kubectl version -o json | jq -r .serverVersion.gitVersion)"
  # No controllers run, so the objects they would create must exist by hand.
  for ns in demo; do kubectl create namespace "$ns" >/dev/null; done
  for ns in default demo; do kubectl create serviceaccount default -n "$ns" >/dev/null 2>&1 || true; done

  echo "== B. real objects from the committed fixtures"
  FX=testdata/fixtures
  # B1: strict client validation shows the schema gap for list-valued attributes (evidence, not an assertion).
  load_py "$OUT" $FX/xe9785l >/dev/null
  kubectl create -f "$OUT/main.json" --dry-run=server --validate=strict 2>&1 | head -n 1 | cut -c1-400 | tee "$OUT/strict-xe9785l.txt" || true
  rm -f "$OUT/main.json" "$OUT/status.json"
  # B2: fixtures that must load completely, each compared with its committed golden.
  GOLD=testdata/golden
  load_set classes-claims $FX/classes-claims
  check "classes-claims: objects created" "10 0 0 0" "$L_CREATED $L_INVALID $L_EXISTS $L_OTHER"
  check "classes-claims: status written" 0 "$(grep -vc ' replaced$' "$SCRATCH/load/classes-claims/status.txt" || true)"
  setup_wrapper; DI=$SCRATCH/di-wrap
  golden_results "inspect topology classes-claims" "$GOLD/inspect-topology-classes-claims.json" . inspect topology
  golden_results "inspect deviceclasses classes-claims" "$GOLD/inspect-deviceclasses-classes-claims.json" . inspect deviceclasses
  golden_results "inspect deviceclasses --selector classes-claims" "$GOLD/inspect-deviceclasses-selector-classes-claims.json" . \
    inspect deviceclasses --selector 'tier in (gold,silver),vendor=example'
  # KubeVirt is not installed, so the golden's per-claim "vmi" is expected to read "unavailable": ignore that one field.
  golden_results "inspect claims classes-claims (minus vmi)" "$GOLD/inspect-claims-classes-claims.json" 'map(del(.details.vmi))' inspect claims
  check "claims: vmi reads unavailable (no CRD)" true \
    "$("$DI" inspect claims -o json | jq '[.results[].details.vmi] | all(. == "unavailable: kubevirt.io/v1 not served")')"
  wipe_dra
  load_set xe9680 $FX/xe9680
  check "xe9680: objects created" "$(jq '.items|length' "$SCRATCH/load/xe9680/main.json") 0 0 0" "$L_CREATED $L_INVALID $L_EXISTS $L_OTHER"
  for g in "inspect-slices-xe9680:inspect slices" "inspect-drivers-xe9680:inspect drivers" "inspect-attributes-xe9680:inspect attributes" \
           "inspect-topology-xe9680:inspect topology" "inspect-deviceclasses-xe9680:inspect deviceclasses" \
           "inspect-drivers-attributes-xe9680:inspect drivers --attributes" "check-topology-xe9680:check topology" \
           "report-cluster-xe9680:report cluster"; do
    # shellcheck disable=SC2086
    golden_results "${g%%:*}" "$GOLD/${g%%:*}.json" . ${g#*:}
  done
  wipe_dra
  # B3: everything together (the dataset for C-I), tolerating the documented rejections.
  for s in classes-claims xe9680 multinode xe9785l; do
    load_set "all-$s" $FX/$s
    printf '%-14s %s objects: created=%s invalid=%s exists=%s other=%s\n' "$s" "$(cut -d' ' -f1 "$SCRATCH/load/all-$s/counts.txt")" \
      "$L_CREATED" "$L_INVALID" "$L_EXISTS" "$L_OTHER" | tee -a "$OUT/load-summary.txt"
    TOT_INVALID=$(( ${TOT_INVALID:-0} + L_INVALID )); TOT_EXISTS=$(( ${TOT_EXISTS:-0} + L_EXISTS )); TOT_OTHER=$(( ${TOT_OTHER:-0} + L_OTHER ))
  done
  check "rejected ResourceSlices (list-valued attributes need a newer apiserver)" "$WANT_REJECTED_SLICES" "$TOT_INVALID"
  check "duplicate DeviceClass names across fixtures" "$WANT_DUP_CLASSES" "$TOT_EXISTS"
  check "no other load errors" 0 "$TOT_OTHER"
  check "every Invalid is the exactly-one-value rule" "$WANT_REJECTED_SLICES" \
    "$(cat "$SCRATCH"/load/all-*/create.txt | grep -c 'exactly one value must be specified' || true)"
  cat >"$SCRATCH/marker.yaml" <<EOF
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
EOF
  kubectl create -f "$SCRATCH/marker.yaml" >/dev/null
  check "opaque marker is stored in the cluster" 1 "$(kubectl get resourceclaim marker-claim -n default -o json | grep -c "$OPAQUE_MARKER")"
  kubectl get resourceslices,deviceclasses,resourceclaims,pods -A --no-headers | awk '{print $1}' | sort | uniq -c | tee "$OUT/object-counts.txt" >/dev/null

  echo "== C. workstation CLI against the real API (admin)"
  DI=$DI_BIN
  for c in slices drivers attributes topology deviceclasses claims; do
    run "inspect-$c" --kubeconfig "$ADMIN" inspect "$c"
    run "inspect-$c-json" --kubeconfig "$ADMIN" inspect "$c" -o json
  done
  for c in alignment counters capacity topology kubevirt; do run "check-$c" --kubeconfig "$ADMIN" check "$c"; done
  run report-cluster --kubeconfig "$ADMIN" report cluster
  run report-cluster-json --kubeconfig "$ADMIN" report cluster -o json
  run report-support --kubeconfig "$ADMIN" report support -o json
  for n in $(awk '$1 ~ /^inspect-/ {print $1}' "$OUT/exit-codes.txt"); do check "exit $n" 0 "$(exit_code "$n")"; done
  # check.alignment/report cluster exit 1 here: the combined dataset deliberately mixes fixtures whose claims reference
  # devices that are not published (rejected slices), which is an honest `fail`, not a tool error.
  check "exit check-alignment (fail results present)" 1 "$(exit_code check-alignment)"
  check "exit report-cluster" 1 "$(exit_code report-cluster)"
  check "exit report-support" 1 "$(exit_code report-support)"
  check "exit check-kubevirt (CRD absent => unavailable, not failure)" 0 "$(exit_code check-kubevirt)"
  check "inspect slices live count == kubectl count" "$(kubectl get resourceslices -o name | wc -l)" \
    "$(jq '.results | length' "$OUT/inspect-slices-json.txt")"

  echo "== D. snapshot and replay"
  SNAP=$SCRATCH/S.json
  "$DI_BIN" --kubeconfig "$ADMIN" snapshot capture --file "$SNAP" --with-adapters 2>"$OUT/snapshot-capture.err" || true
  cat "$OUT/snapshot-capture.err"
  check "snapshot mode" 600 "$(stat -c %a "$SNAP")"
  check "snapshot collection states" "ok ok ok ok notFound" \
    "$(jq -r '.collection | [.deviceclasses.state, .nodes.state, .pods.state, .resourceclaims.state, .virtualmachineinstances.state] | join(" ")' "$SNAP")"
  DI=$SCRATCH/di-wrap
  for c in "inspect slices" "inspect drivers" "inspect attributes" "inspect topology" "inspect deviceclasses" "inspect claims" \
           "check alignment" "check counters" "check capacity" "check topology" "check kubevirt" "check vfio" \
           "report cluster" "report support"; do
    # shellcheck disable=SC2086
    same_results $c
  done
  absent "no opaque marker in snapshot" "$OPAQUE_MARKER" "$SNAP"
  absent "no opaque marker in any CLI output" "$OPAQUE_MARKER" "$OUT"
  absent "no config knob text in snapshot" "opaque-parameter-marker" "$SNAP"

  echo "== I. namespace honesty: -n demo, live vs replay"
  same_results -n demo report cluster
  same_results -n demo inspect claims
  check "-n demo claims: only demo/ subjects" 0 \
    "$("$DI" -n demo inspect claims -o json | jq '[.results[].subject | select(startswith("demo/") | not)] | length')"
  check "-n demo does not hide the other namespaces' claims from cluster-wide" true \
    "$([ "$("$DI" inspect claims -o json | jq '.results | length')" -gt "$("$DI" -n demo inspect claims -o json | jq '.results | length')" ] && echo true || echo false)"
  DI=$DI_BIN

  echo "== E. RBAC for real: both chart profiles"
  for ns in dic dicn; do
    kubectl create namespace "$ns" >/dev/null
    kubectl label namespace "$ns" pod-security.kubernetes.io/enforce=restricted pod-security.kubernetes.io/warn=restricted \
      pod-security.kubernetes.io/audit=restricted >/dev/null
  done
  CH=deploy/helm/dra-inspect
  HSET=(--set "image.repository=$IMAGE_REPO" --set "image.tag=$IMAGE_TAG" --set image.pullPolicy=Never --set job.enabled=true)
  helm template di-c $CH -n dic "${HSET[@]}" >"$SCRATCH/c.yaml"
  helm template di-n $CH -n dicn --set rbac.profile=namespace --set 'rbac.namespaces={demo}' "${HSET[@]}" >"$SCRATCH/n.yaml"
  kubectl apply -f "$SCRATCH/c.yaml" >"$OUT/apply-cluster.txt" 2>&1
  kubectl apply -f "$SCRATCH/n.yaml" >"$OUT/apply-namespace.txt" 2>&1
  cat "$OUT/apply-cluster.txt" "$OUT/apply-namespace.txt"
  check "chart objects applied (incl. the Job object)" 8 "$(cat "$OUT"/apply-*.txt | grep -c ' created$')"
  check "no PodSecurity warnings on applying the Jobs" 0 "$(cat "$OUT"/apply-*.txt | grep -ci 'warning')"
  SAC=di-c-dra-inspect; SAN=di-n-dra-inspect
  CTOK=$(kubectl -n dic create token "$SAC" --duration=1h); NTOK=$(kubectl -n dicn create token "$SAN" --duration=1h)
  mk_kubeconfig "$SCRATCH/sac.kubeconfig" "$CTOK"; mk_kubeconfig "$SCRATCH/san.kubeconfig" "$NTOK"
  run report-admin --kubeconfig "$ADMIN" report cluster -o json
  run report-cluster-sa --kubeconfig "$SCRATCH/sac.kubeconfig" report cluster -o json
  check "cluster SA: same exit as admin" "$(exit_code report-admin)" "$(exit_code report-cluster-sa)"
  check "cluster SA: results == admin results" "$(jqs .results "$OUT/report-admin.txt" | md5sum)" "$(jqs .results "$OUT/report-cluster-sa.txt" | md5sum)"
  "$DI_BIN" --kubeconfig "$SCRATCH/sac.kubeconfig" snapshot capture --file "$SCRATCH/sac.json" --with-adapters 2>/dev/null || true
  check "cluster SA: collection states" "ok ok ok ok ok notFound" \
    "$(jq -r '.collection | [.deviceclasses.state, .nodes.state, .pods.state, .resourceclaims.state, .resourceslices.state, .virtualmachineinstances.state] | join(" ")' "$SCRATCH/sac.json")"
  run ns-sa-report --kubeconfig "$SCRATCH/san.kubeconfig" -n demo report cluster -o json
  run ns-sa-report-strict --kubeconfig "$SCRATCH/san.kubeconfig" -n demo report cluster -o json --strict
  run ns-sa-report-no-n --kubeconfig "$SCRATCH/san.kubeconfig" report cluster -o json
  check "ns SA with -n demo: exit 0" 0 "$(exit_code ns-sa-report)"
  check "ns SA with -n demo --strict: exit 3" 3 "$(exit_code ns-sa-report-strict)"
  check "ns SA without -n: exit 0" 0 "$(exit_code ns-sa-report-no-n)"
  NSR=$OUT/ns-sa-report.txt
  check "ns SA: slice/deviceclass based results are all unavailable" "check.alignment check.capacity check.counters check.topology inspect.deviceclasses inspect.drivers inspect.slices inspect.topology" \
    "$(jq -r '[.results[] | select(.status=="unavailable") | .check] | unique | join(" ")' "$NSR")"
  check "ns SA: unavailable reasons say forbidden" 0 \
    "$(jq '[.results[] | select(.status=="unavailable") | .details.reason | select(test("forbidden") | not)] | length' "$NSR")"
  check "ns SA: claims collected, none unavailable" "true" \
    "$(jq '[.results[] | select(.check=="inspect.claims")] | (length > 0) and all(.status != "unavailable")' "$NSR")"
  check "ns SA: claim subjects only in demo/" 0 "$(jq '[.results[] | select(.check=="inspect.claims") | .subject | select(startswith("demo/") | not)] | length' "$NSR")"
  check "ns SA without -n: claims unavailable (no cluster-wide claim from a namespaced grant)" "unavailable" \
    "$(jq -r '[.results[] | select(.check=="inspect.claims") | .status] | unique | join(",")' "$OUT/ns-sa-report-no-n.txt")"
  "$DI_BIN" --kubeconfig "$SCRATCH/san.kubeconfig" -n demo snapshot capture --file "$SCRATCH/san.json" --with-adapters 2>/dev/null || true
  check "ns SA snapshot: slices/deviceclasses/nodes forbidden, pods/claims scoped" \
    "forbidden forbidden forbidden ok:namespace demo only ok:namespace demo only" \
    "$(jq -r '.collection | [.resourceslices.state, .deviceclasses.state, .nodes.state, (.pods.state+":"+.pods.reason), (.resourceclaims.state+":"+.resourceclaims.reason)] | join(" ")' "$SCRATCH/san.json")"

  check "ns SA snapshot: slices reason carries the API server's RBAC detail" true \
    "$(jq '.collection.resourceslices.reason | contains("cannot list resource \"resourceslices\"")' "$SCRATCH/san.json")"
  jq -r '"ns SA reason: " + .collection.resourceslices.reason' "$SCRATCH/san.json"

  SA=system:serviceaccount:dic:$SAC
  for r in resourceslices.resource.k8s.io deviceclasses.resource.k8s.io resourceclaims.resource.k8s.io pods nodes; do
    for v in get list; do can_i yes "$v" "$r"; done
    for v in create update patch delete deletecollection; do can_i no "$v" "$r"; done
  done
  can_i no create pods --subresource=exec
  can_i no get secrets
  kubectl auth can-i --list --as="$SA" >"$OUT/can-i-list-cluster.txt"
  check "can-i --list (cluster profile): only get/list on pods, nodes and *.k8s.io" "" "$(canilist_offenders "$OUT/can-i-list-cluster.txt")"
  for verb in "create configmap x --from-literal=a=b -n demo" "delete pod web-0 -n demo" "exec web-0 -n demo -- true" \
              "patch resourceclaim shared-gpu -n demo --type=merge -p {}" "delete resourceslice gpu-node-a" "create namespace evil"; do
    # shellcheck disable=SC2086  # $verb is deliberately word-split into a kubectl command line
    out=$(kubectl --as="$SA" $verb 2>&1 || true)
    is_forbidden "forbidden as cluster SA: kubectl $verb" "$out"
  done
  out=$("$KUBECTL" --kubeconfig "$SCRATCH/sac.kubeconfig" delete resourceclaim shared-gpu -n demo 2>&1 || true)
  is_forbidden "forbidden with the SA's real token: delete resourceclaim" "$out"
  check "object survived the forbidden delete" 1 "$(kubectl get resourceclaim shared-gpu -n demo -o name | wc -l)"

  SA=system:serviceaccount:dicn:$SAN
  can_i no list resourceslices.resource.k8s.io
  can_i no list deviceclasses.resource.k8s.io
  can_i no list nodes
  can_i yes list resourceclaims.resource.k8s.io -n demo
  can_i yes get pods -n demo
  can_i no list resourceclaims.resource.k8s.io -n default
  can_i no list resourceclaims.resource.k8s.io
  can_i no delete resourceclaims.resource.k8s.io -n demo
  can_i no get secrets -n demo
  kubectl auth can-i --list --as="$SA" -n demo >"$OUT/can-i-list-ns.txt"
  check "can-i --list (namespace profile, -n demo): only get/list on pods and resourceclaims" "" "$(nsprofile_offenders "$OUT/can-i-list-ns.txt")"
  out=$(kubectl --as="$SA" -n demo delete pod web-0 2>&1 || true)
  is_forbidden "forbidden as namespace SA: delete pod" "$out"

  echo "== F. Pod Security restricted"
  for rel in "di-c:dic:" "di-n:dicn:--set rbac.profile=namespace --set rbac.namespaces={demo}"; do
    r=${rel%%:*}; ns=${rel#*:}; ns=${ns%%:*}; extra=${rel##*:}
    # shellcheck disable=SC2086
    helm template "$r" $CH -n "$ns" $extra "${HSET[@]}" --show-only templates/job.yaml | job_to_pod "psa-$ns" restricted >"$SCRATCH/pod-$ns.json"
    # shellcheck disable=SC2086
    helm template "$r" $CH -n "$ns" $extra "${HSET[@]}" --show-only templates/job.yaml | job_to_pod "psa-bad-$ns" privileged >"$SCRATCH/podbad-$ns.json"
    out=$(kubectl create -f "$SCRATCH/pod-$ns.json" --dry-run=server 2>&1 || true)
    check "PSA restricted admits the chart's pod in $ns (server dry-run, no warning)" "pod/psa-$ns created (server dry run)" "$out"
    out=$(kubectl create -f "$SCRATCH/pod-$ns.json" 2>&1 || true)
    check "PSA restricted admits the chart's pod in $ns (real create, no warning)" "pod/psa-$ns created" "$out"
    out=$(kubectl create -f "$SCRATCH/podbad-$ns.json" --dry-run=server 2>&1 || true)
    echo "$out" | cut -c1-300
    check "negative control: hostNetwork+privilege-escalation pod rejected in $ns (dry-run)" true "$(grep -q 'violates PodSecurity "restricted' <<<"$out" && echo true || echo false)"
    out=$(kubectl create -f "$SCRATCH/podbad-$ns.json" 2>&1 || true)
    check "negative control: hostNetwork+privilege-escalation pod rejected in $ns (real create)" true "$(grep -q 'violates PodSecurity "restricted' <<<"$out" && echo true || echo false)"
    check "bad pod was not created in $ns" 0 "$(kubectl -n "$ns" get pod "psa-bad-$ns" -o name 2>/dev/null | wc -l)"
  done
  check "Job objects exist (no controller runs them)" "di-c-dra-inspect di-n-dra-inspect" \
    "$(kubectl get jobs -A -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' | sort | tr '\n' ' ' | sed 's/ $//')"

  echo "== G. in-cluster auth emulation under podman (client-go in-cluster config)"
  SAVOL=$SCRATCH/sa-volume; mkdir -p "$SAVOL"
  printf '%s' "$CTOK" >"$SAVOL/token"; cp "$PKI/ca.crt" "$SAVOL/ca.crt"; printf 'dic' >"$SAVOL/namespace"; chmod 644 "$SAVOL"/*; chmod 755 "$SAVOL"
  incluster() { # OUTFILE ARGS... -- exactly the Job pod's hardening, host network so 127.0.0.1 reaches the apiserver
    local o=$1; shift
    set +e
    podman run --rm --network host --read-only --user 65532:65532 --cap-drop=ALL --security-opt no-new-privileges \
      -e KUBERNETES_SERVICE_HOST=127.0.0.1 -e "KUBERNETES_SERVICE_PORT=$API_PORT" \
      -v "$SAVOL/token:/var/run/secrets/kubernetes.io/serviceaccount/token:ro" \
      -v "$SAVOL/ca.crt:/var/run/secrets/kubernetes.io/serviceaccount/ca.crt:ro" \
      -v "$SAVOL/namespace:/var/run/secrets/kubernetes.io/serviceaccount/namespace:ro" \
      "$IMAGE" "$@" >"$o" 2>&1
    echo $? >"$o.rc"; set -e
  }
  incluster "$OUT/incluster-cluster.txt" report cluster -o json --timeout 60s
  check "in-cluster (cluster SA): exit == workstation exit as the same SA" "$(exit_code report-cluster-sa)" "$(cat "$OUT/incluster-cluster.txt.rc")"
  sed -n '/^{$/,/^}$/p' "$OUT/incluster-cluster.txt" >"$OUT/incluster-cluster.json"   # the README's extraction
  check "in-cluster: README sed extraction yields valid JSON" true "$(jq -e '.results | length > 0' "$OUT/incluster-cluster.json" >/dev/null 2>&1 && echo true || echo false)"
  check "in-cluster: results == workstation results as the same SA" "$(jqs .results "$OUT/report-cluster-sa.txt" | md5sum)" \
    "$(jqs .results "$OUT/incluster-cluster.json" 2>/dev/null | md5sum)"
  check "in-cluster: not all unavailable (auth worked without a kubeconfig)" true \
    "$(jq '[.results[] | select(.status != "unavailable")] | length > 0' "$OUT/incluster-cluster.json" 2>/dev/null || echo false)"
  printf '%s' "$NTOK" >"$SAVOL/token"; printf 'dicn' >"$SAVOL/namespace"
  incluster "$OUT/incluster-ns.txt" report cluster -o json --timeout 60s -n demo
  check "in-cluster (namespace SA, -n demo): exit 0" 0 "$(cat "$OUT/incluster-ns.txt.rc")"
  incluster "$OUT/incluster-ns-strict.txt" report cluster -o json --timeout 60s -n demo --strict
  check "in-cluster (namespace SA, --strict): exit 3" 3 "$(cat "$OUT/incluster-ns-strict.txt.rc")"
  check "in-cluster ns: results == workstation results as the same SA" "$(jqs .results "$OUT/ns-sa-report.txt" | md5sum)" \
    "$(sed -n '/^{$/,/^}$/p' "$OUT/incluster-ns.txt" | jqs .results 2>/dev/null | md5sum)"
  printf '%s' "$CTOK" >"$SAVOL/token"
  incluster "$OUT/incluster-version.txt" version
  check "in-cluster smoke: version prints without a cluster" "$VER" "$(cat "$OUT/incluster-version.txt")"

  echo "== H. failure modes against a REAL hung / closed / mistrusted server"
  openssl req -x509 -newkey rsa:2048 -nodes -subj /CN=other -keyout /dev/null -out "$PKI/other.crt" -days 1 2>/dev/null
  CLOSED=$(pick_port "$API_PORT" "$ETCD_PORT" "$PEER_PORT")
  mk_kubeconfig "$SCRATCH/closed.kubeconfig" x "$PKI/ca.crt" "https://127.0.0.1:$CLOSED"
  mk_kubeconfig "$SCRATCH/badca.kubeconfig" "$ADMIN_TOKEN" "$PKI/other.crt"
  mk_kubeconfig "$SCRATCH/badtoken.kubeconfig" not-a-token
  kill -STOP "$API_PID"
  t0=$(now); run hang --kubeconfig "$ADMIN" --timeout 3s inspect slices; t1=$(now)
  HANG_ELAPSED=$(awk -v a="$t0" -v b="$t1" 'BEGIN{printf "%.2f", b-a}')
  t0=$(now); run hang-strict --kubeconfig "$ADMIN" --timeout 3s --strict inspect slices; t1=$(now)
  HANG_STRICT_ELAPSED=$(awk -v a="$t0" -v b="$t1" 'BEGIN{printf "%.2f", b-a}')
  t0=$(now); run hang-report --kubeconfig "$ADMIN" --timeout 3s report cluster; t1=$(now)
  HANG_REPORT_ELAPSED=$(awk -v a="$t0" -v b="$t1" 'BEGIN{printf "%.2f", b-a}')
  kill -CONT "$API_PID"
  echo "elapsed: inspect slices ${HANG_ELAPSED}s, strict ${HANG_STRICT_ELAPSED}s, report cluster ${HANG_REPORT_ELAPSED}s"
  head -n 3 "$OUT/hang.txt" | cut -c1-300
  within "hang: --timeout 3s is far below the old ~18 s (6 kinds x 3 s)" 2.9 9 "$HANG_ELAPSED"
  within "hang: report cluster is also far below 18 s" 2.9 9 "$HANG_REPORT_ELAPSED"
  # F1 (fixed): the KubeVirt probe takes the context, so --timeout is the total (< 1.5 x timeout).
  check "hang: --timeout 3s finishes in < 4.5 s in total" true "$(awk -v v="$HANG_ELAPSED" 'BEGIN{print (v < 4.5) ? "true" : "false"}')"
  check "hang: exit 0" 0 "$(exit_code hang)"
  check "hang: --strict exit 3" 3 "$(exit_code hang-strict)"
  check "hang: says timed out after 3s" true "$(grep -q 'UNAVAILABLE.*timed out after 3s' "$OUT/hang.txt" && echo true || echo false)"
  check "hang: reason does not echo a URL" 0 "$(grep -cE 'https?://' "$OUT/hang.txt" || true)"
  for i in $(seq 1 20); do [ "$(curl -sk --max-time 2 "https://127.0.0.1:$API_PORT/readyz" 2>/dev/null)" = ok ] && break; sleep 1; done
  check "apiserver recovers after SIGCONT" ok "$(curl -sk --max-time 5 "https://127.0.0.1:$API_PORT/readyz")"
  run closed --kubeconfig "$SCRATCH/closed.kubeconfig" inspect slices
  run closed-strict --kubeconfig "$SCRATCH/closed.kubeconfig" inspect slices --strict
  run badca --kubeconfig "$SCRATCH/badca.kubeconfig" inspect slices
  run badca-strict --kubeconfig "$SCRATCH/badca.kubeconfig" inspect slices --strict
  run badtoken --kubeconfig "$SCRATCH/badtoken.kubeconfig" inspect slices
  run badtoken-strict --kubeconfig "$SCRATCH/badtoken.kubeconfig" inspect slices --strict
  for want in closed:0 closed-strict:3 badca:0 badca-strict:3 badtoken:0 badtoken-strict:3; do
    check "exit ${want%%:*}" "${want##*:}" "$(exit_code "${want%%:*}")"
  done
  check "closed port: connection refused" true "$(grep -q 'UNAVAILABLE.*connection refused' "$OUT/closed.txt" && echo true || echo false)"
  check "bad CA: x509 unknown authority" true "$(grep -q 'UNAVAILABLE.*x509' "$OUT/badca.txt" && echo true || echo false)"
  check "bad token: reported unavailable" true "$(grep -q 'UNAVAILABLE' "$OUT/badtoken.txt" && echo true || echo false)"
  check "bad token: reason does not echo a URL" 0 "$(grep -cE 'https?://' "$OUT/badtoken.txt" || true)"
  # F2 (fixed): every reason is "<resource>: <cause>" without method, URL or query.
  for f in closed badca; do
    check "$f: reason does not echo a URL" 0 "$(grep -cE 'https?://' "$OUT/$f.txt" || true)"
    check "$f: reason does not echo a query" 0 "$(grep -c '?limit' "$OUT/$f.txt" || true)"
  done
  check "bad token: reason does not echo a query" 0 "$(grep -c '?limit' "$OUT/badtoken.txt" || true)"
  for f in hang closed badca badtoken; do echo "-- $f:"; head -n 3 "$OUT/$f.txt" | cut -c1-260; done

  absent "no opaque marker in any output at the end" "$OPAQUE_MARKER" "$OUT" "$SNAP"
  if [ -n "$KUBE_MTIME" ]; then check "~/.kube untouched" "$KUBE_MTIME" "$(stat -c %Y "$HOME/.kube")"; fi
  echo "== done: $FAILS failed assertion(s); outputs in $OUT"
  if [ "$RED" = 1 ]; then
    # --red expects every assertion to fail; the run is a success only if none passed.
    [ "$FAILS" -gt 0 ] && { echo "red run: all assertions failed as intended"; return 0; }
    echo "red run: assertions passed against wrong expectations" >&2; return 1
  fi
  [ "$FAILS" -eq 0 ]
}

# Sourcing the file (for tests of the helpers) does not run main.
[[ "${BASH_SOURCE[0]}" != "$0" ]] || main "$@"
