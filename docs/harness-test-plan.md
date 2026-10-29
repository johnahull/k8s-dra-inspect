# Live verification plan (kind)

**Status.** The kind plan below (steps 2 to 10) is still **NOT RUN**: `kind create
cluster` fails on this host's rootless podman 3.4.4 (finding F1). The substitute
that did run is [API-server-level live verification](#api-server-level-live-verification-no-kubelet)
(RUN 2026-10-29): a real kube-apiserver and etcd without kubelet, scheduler or
controllers. It covers the CLI, replay, chart RBAC, Pod Security admission and the
in-cluster auth path; it cannot show anything that needs a node (see "What this
route cannot show"). Keep this kind plan as the full-fidelity path; re-run it with
`hack/live-kind.sh` on a host with podman 4+ (netavark). Docker is untested and not
supported by that script.

Related: [README](../README.md), [RBAC reference](rbac.md),
[migration guide](migration-from-dra-verify.md).

## Goals

1. **In-cluster auth fallback.** The Job runs with no kubeconfig and must fall
   back to the pod's service account (in-cluster config).
2. **Pod Security `restricted` admission.** The Job pod is admitted into a
   namespace that enforces `restricted`.
3. **Namespace versus cluster RBAC.** The cluster profile reads everything; the
   namespace profile reports slices, DeviceClasses and nodes as `unavailable`
   (never a cluster-wide claim from a namespaced grant).
4. **Read-only proof.** `kubectl auth can-i` shows get/list only, no mutating
   verb and no `pods/exec`, for the Job's service account.
5. **Live versus replay equality.** `snapshot capture` then `--from` gives the
   same results as the live command.
6. **Adapter behavior.** The host adapter is `unavailable` from a workstation;
   KubeVirt is `notFound`/unavailable on a cluster without the CRD; neither
   blocks the core results.

## Prerequisites

| Item | Requirement |
|---|---|
| Tools | `podman`, `kind`, `kubectl`, `helm`, `jq`, Go toolchain from `go.mod` |
| kind provider | podman 4 or newer with netavark (`KIND_EXPERIMENTAL_PROVIDER=podman`), cgroups v2 with `cpu cpuset memory pids` delegated to the user for rootless (`cpu cpuset io memory pids`). Docker is untested and not supported by `hack/live-kind.sh`, which forces podman. podman 3.4.4 (Ubuntu 22.04) does not: see finding F1. |
| Node image | `kindest/node:v1.34.11@sha256:44e222ee2132dab25ff87301682f89eb82c7880ea3a1bf543bfe9708fd08d67d` (from the kind v0.33.0 release notes; Kubernetes 1.34 or newer so `resource.k8s.io/v1` is served, DRA is GA, no feature gates; containerd 2.3.4 so CDI is on by default). **The example driver injects devices through CDI, so the node's containerd must have CDI enabled** (the default in this node image); on an older node image enable it in the containerd config first. This is taken from the driver's design, not verified against its chart here. |
| Sample driver | `kubernetes-sigs/dra-example-driver` checked out at the pinned release tag `v0.5.0` (commit `93ff610f3c3bfd603c592d1c281a236c1463edb9`; chart `deployments/helm/dra-example-driver`, image `registry.k8s.io/dra-example-driver/dra-example-driver:v0.5.0`). It publishes simulated GPUs without NUMA attributes, so topology findings are `warn`, which is expected. |
| dra-inspect | Built from the commit under test: `go build -o "$SCRATCH/dra-inspect" ./cmd/dra-inspect` and a local image (below). |

## Safety rules

- **Scratch kubeconfig only.** Always `export KUBECONFIG="$SCRATCH/kubeconfig"`
  and pass `--kubeconfig "$SCRATCH/kubeconfig"`. Never read or write
  `~/.kube/config`; run `kubectl config current-context` first and abort unless
  it is `kind-dra-inspect-test`.
- **One cluster name:** `dra-inspect-test`. Never touch other clusters.
- **No sudo.** Rootless podman only.
- **Pin and record** every image tag, digest and chart commit used.
- **Cleanup always:** delete the cluster, remove `$SCRATCH`, and remove the
  image this run built (below). Run it even when a step fails: install the
  trap before the first step.
- **Guard the context** before every cluster command; abort on any other:
  `[ "$(kubectl config current-context)" = kind-dra-inspect-test ]`.
- Only the commands below are run against the cluster; no `kubectl delete` of
  anything outside the harness namespaces.

```bash
export SCRATCH=$(mktemp -d)
export KUBECONFIG="$SCRATCH/kubeconfig"
export KIND_EXPERIMENTAL_PROVIDER=podman
export NODE_IMAGE='kindest/node:v1.34.11@sha256:44e222ee2132dab25ff87301682f89eb82c7880ea3a1bf543bfe9708fd08d67d'
export DRIVER_TAG='v0.5.0'   # commit 93ff610f3c3bfd603c592d1c281a236c1463edb9
export IMAGE="localhost/dra-inspect:harness-$$"   # unique: never remove a pre-existing :harness image

cleanup() {
  set +e
  kind delete cluster --name dra-inspect-test --kubeconfig "$KUBECONFIG"
  podman rmi "$IMAGE"
  rm -rf "$SCRATCH"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
```

## Steps

Each step lists the commands and its acceptance criteria. Stop at the first
failed criterion, record it, and clean up.

### 1. Build the binary and a local image

```bash
go build -o "$SCRATCH/dra-inspect" ./cmd/dra-inspect
ARCH=$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')   # kind nodes follow the host
hack/check-release.sh        # release properties; writes dist/metadata.json
VER=$(grep -o '"version":"[^"]*"' dist/metadata.json | cut -d'"' -f4)
mkdir -p "$SCRATCH/ctx/linux/$ARCH"
CGO_ENABLED=0 GOOS=linux GOARCH=$ARCH go build -trimpath -ldflags "-s -w -X main.version=$VER" \
  -o "$SCRATCH/ctx/linux/$ARCH/dra-inspect" ./cmd/dra-inspect
cp Dockerfile "$SCRATCH/ctx/"
podman build --platform "linux/$ARCH" --build-arg "TARGETPLATFORM=linux/$ARCH" \
  -t "$IMAGE" "$SCRATCH/ctx"
hack/check-image.sh "$IMAGE" "$VER"     # expects the dist/metadata.json snapshot version
```

Accept: `check-image: ok`, non-root user, no shell. **Result: pass**
(`check-image: ok (0.0.0-SNAPSHOT-4683a0e, user 65532:65532)`, image 32.6 MB).

### 2. Create the cluster

```bash
kind create cluster --name dra-inspect-test --image "$NODE_IMAGE" --kubeconfig "$KUBECONFIG"
kubectl config current-context        # must be kind-dra-inspect-test
kubectl api-resources --api-group=resource.k8s.io | grep -E 'resourceslices|deviceclasses|resourceclaims'
podman save "$IMAGE" -o "$SCRATCH/img.tar"
kind load image-archive "$SCRATCH/img.tar" --name dra-inspect-test
```

Accept: three `resource.k8s.io` resources listed; image loaded.

### 3. Install the sample driver

Install `dra-example-driver` at `$DRIVER_TAG` with an explicit `helm install`
of its chart from a checkout of that tag, against the existing cluster and the
scratch `$KUBECONFIG`. Do NOT follow the driver's README for kind and do not
run its `create-cluster` scripts: they create their own cluster and may touch
the default kubeconfig.

```bash
[ "$(kubectl config current-context)" = kind-dra-inspect-test ] || exit 1
git clone --depth 1 --branch "$DRIVER_TAG" https://github.com/kubernetes-sigs/dra-example-driver.git "$SCRATCH/driver"
helm install dra-example-driver "$SCRATCH/driver/deployments/helm/dra-example-driver" \
  --kubeconfig "$KUBECONFIG" -n dra-example-driver --create-namespace --wait --timeout 5m
```

```bash
kubectl get resourceslices -o json | jq '[.items[].spec.devices | length] | add'
```

Accept: the driver pod is `Running` and the device count is greater than zero.

### 4. Workstation CLI against the live cluster

```bash
B="$SCRATCH/dra-inspect --kubeconfig $KUBECONFIG"
$B inspect slices -o json  | jq '.results | length'
$B inspect drivers -o json | jq -r '.results[] | [.subject, .details.devices] | @tsv'
$B inspect topology; echo "exit=$?"
$B check alignment; echo "exit=$?"
$B report cluster -o json > "$SCRATCH/live-report.json"; echo "exit=$?"
```

Accept: slice results match `kubectl get resourceslices | wc -l` minus the
header; `inspect drivers` device count equals the step 3 count; topology rows
are `warn` with `missing attribute` (the example driver publishes no NUMA
attributes); exit codes are 0 (warn does not fail).

### 5. Allocate a claim and inspect it

Apply the driver's own demo manifest for one GPU (its `gpu-test1`-style example
at the pinned tag; record the file), wait for the pod `Running`.

```bash
$B inspect claims -o json | jq '.results[] | {subject, allocated: .details.allocated, usedBy: .details.usedBy}'
$B check alignment -o json | jq '.results[] | {subject, status}'
```

Accept: the claim is `allocated: true` with the pod in `usedBy`; its alignment
status is `pass` (single device) with exit 0.

### 6. Capture and replay equality

```bash
$B snapshot capture --file "$SCRATCH/snap.json"
for c in "inspect slices" "inspect drivers" "inspect claims" "check alignment" "check counters"; do
  $B $c -o json | jq -S 'del(.generatedAt, .source)' > "$SCRATCH/live.json"
  $SCRATCH/dra-inspect $c --from "$SCRATCH/snap.json" -o json | jq -S 'del(.generatedAt, .source)' > "$SCRATCH/replay.json"
  diff -q "$SCRATCH/live.json" "$SCRATCH/replay.json" || echo "DIFF in: $c"
done
stat -c '%a' "$SCRATCH/snap.json"
```

Accept: no `DIFF` line (the cluster is idle, so live equals the capture);
snapshot mode `600`; replay needs no `--kubeconfig`.

### 7. Cluster-profile Job under Pod Security `restricted`

```bash
kubectl create namespace dra-inspect
kubectl label namespace dra-inspect pod-security.kubernetes.io/enforce=restricted \
  pod-security.kubernetes.io/enforce-version=latest
helm install dra-inspect deploy/helm/dra-inspect -n dra-inspect \
  --set image.repository=localhost/dra-inspect --set image.tag="${IMAGE##*:}" \
  --set image.pullPolicy=Never --set job.enabled=true
kubectl -n dra-inspect wait --for=condition=complete job/dra-inspect --timeout=120s
kubectl -n dra-inspect logs job/dra-inspect | sed -n '/^{$/,/^}$/p' > "$SCRATCH/job-report.json"
jq -r '.source' "$SCRATCH/job-report.json"
diff <(jq -S '[.results[] | {check,status,subject}]' "$SCRATCH/job-report.json") \
     <(jq -S '[.results[] | {check,status,subject}]' "$SCRATCH/live-report.json") && echo SAME
```

Accept: the pod is admitted (no `violates PodSecurity` event), the Job
completes, the log holds valid JSON, the Job had no kubeconfig (in-cluster
fallback worked: results are not all `unavailable`), and the check/status/subject
triples equal the workstation `report cluster` from step 4.

### 8. Read-only proof with `auth can-i`

```bash
SA=system:serviceaccount:dra-inspect:dra-inspect
for r in resourceslices.resource.k8s.io deviceclasses.resource.k8s.io resourceclaims.resource.k8s.io pods nodes; do
  for v in get list; do echo "$v $r: $(kubectl auth can-i $v $r --as=$SA)"; done
  for v in create update patch delete deletecollection; do echo "$v $r: $(kubectl auth can-i $v $r --as=$SA)"; done
done
kubectl auth can-i create pods --subresource=exec --as=$SA
kubectl auth can-i get secrets --as=$SA
kubectl auth can-i --list --as=$SA | grep -v '^\s*$'
```

Accept: `get`/`list` print `yes`; every other verb, `pods/exec` and `secrets`
print `no`; `--list` shows no verb outside get/list for the rules in
[rbac.md](rbac.md).

### 9. Namespace profile

```bash
helm uninstall dra-inspect -n dra-inspect
kubectl create namespace team-a
kubectl label namespace team-a pod-security.kubernetes.io/enforce=restricted
helm install dra-inspect deploy/helm/dra-inspect -n team-a \
  --set rbac.profile=namespace --set job.enabled=true \
  --set image.repository=localhost/dra-inspect --set image.tag="${IMAGE##*:}" --set image.pullPolicy=Never
kubectl -n team-a wait --for=condition=complete job/dra-inspect --timeout=120s
kubectl -n team-a logs job/dra-inspect | sed -n '/^{$/,/^}$/p' | jq -r '.results[] | select(.status=="unavailable") | .check'
kubectl auth can-i list resourceslices.resource.k8s.io --as=system:serviceaccount:team-a:dra-inspect
```

Accept: the Job completes (exit 0 without `job.strict`); slice, DeviceClass and
node based results are `unavailable`; no result claims cluster-wide coverage;
`can-i list resourceslices` prints `no`. Re-run with `--set job.strict=true`
(after deleting the finished Job): the Job fails with exit 3.

### 10. Optional adapters from a workstation

```bash
$B report support -o json | jq -r '.results[] | select(.check|startswith("adapter.")) | [.check,.status,.details.reason] | @tsv'
$B report support --strict; echo "exit=$?"
$B check vfio; echo "exit=$?"
```

Expected: `adapter.host` is `unavailable` (not running on the node, with the
reason); `adapter.kubevirt` is `unavailable` with a `notFound` reason
(`kubevirt.io` is not installed on kind); core results are unaffected; with
`--strict` the exit code is 3; `check vfio` is `unavailable`, never a failure.
Accept: exactly this behavior.

### 11. Cleanup

```bash
# normally done by the cleanup trap above; the same commands by hand:
helm uninstall dra-inspect -n team-a --kubeconfig "$KUBECONFIG" || true
kind delete cluster --name dra-inspect-test --kubeconfig "$KUBECONFIG"
podman rmi "$IMAGE" || true
rm -rf "$SCRATCH"
```

Accept: `kind get clusters` no longer lists `dra-inspect-test`.

## Results

Run 2026-10-29 by the Task 8b agent (operator: John Hull's workstation).

| Field | Value |
|---|---|
| dra-inspect commit | `4683a0e` (branch `feat/dra-inspect-v1`) |
| kind | v0.33.0 (`go install sigs.k8s.io/kind@v0.33.0`, go1.25.5 build) |
| kindest/node | `kindest/node:v1.34.11@sha256:44e222ee2132dab25ff87301682f89eb82c7880ea3a1bf543bfe9708fd08d67d` (pulled, 908 MB, left cached) |
| dra-example-driver | tag `v0.5.0`, commit `93ff610f3c3bfd603c592d1c281a236c1463edb9` (chart read and rendered with `helm template`; not installed) |
| podman | 3.4.4 (rootless, crun 0.17, cgroup v2, cgroupManager systemd, delegated controllers `memory pids`) |
| helm | v4.3.1+gc0d08d0 |
| kubectl | v1.35.10 (a one-off download to the scratch dir by the agent; the script itself uses whatever `kubectl` is on PATH) |
| go | go1.26.10 |

| Step | Result | Evidence / notes |
|---|---|---|
| 1 Build and image | pass | `check-release: ok (0.0.0-SNAPSHOT-4683a0e)`, `check-image: ok (0.0.0-SNAPSHOT-4683a0e, user 65532:65532)`; `helm lint` ok for both profiles |
| 2 Cluster | **BLOCKED (environment)** | `kind create cluster` failed, see F1 |
| 3 Sample driver | not run | needs a cluster; chart and demo specs read, see `hack/live-kind.sh` |
| 4 Workstation CLI | not run | needs a cluster |
| 5 Claim and alignment | not run | needs a cluster |
| 6 Capture and replay equality | not run | helper assertions proven red-first offline (below) |
| 7 Job, PSA restricted, in-cluster auth | not run | still unverified; the claim in the Job template comment was reworded |
| 8 `auth can-i` read-only proof | not run | |
| 9 Namespace profile | not run | |
| 10 Adapters from a workstation | not run | |
| Failure modes (offline subset) | pass | unreachable and hanging API server: see F2, F3 |
| 11 Cleanup | pass | see below |

### Assertion red-first (helpers in `hack/live-kind.sh`, run against `testdata/snapshots/xe9785l.json`)

| Assertion | Failing output seen | Green |
|---|---|---|
| replay==live results[] | `FAIL  replay==live: inspect slices: want [live 4 results] got [replay differs: 3c3 <     "check": "inspect.drivers", --- ]` | `PASS  replay==live: inspect slices` |
| results non-empty (guards against vacuous equality) | `FAIL  non-empty results: inspect slices: want [true] got [false]` | `PASS  non-empty results: check alignment` |
| marker absent from snapshot/outputs | `FAIL  no secret marker (marker planted): want [absent] got [present]` | `PASS  no secret marker` |
| can-i / generic `check` | `FAIL  can-i style compare: want [no] got [yes]` | `PASS  can-i style compare` |

These prove the comparison helpers can fail. The cluster-dependent assertions
(`can-i` yes/no against a real API server, Job exit codes, impersonated
`Forbidden` probes) use the same `check` helper but were not executed.

### Commands run (offline subset)

```bash
S=<scratch>; go build -o $S/dra-inspect ./cmd/dra-inspect
$S/dra-inspect --kubeconfig closed-port.kubeconfig inspect slices      # exit 0, UNAVAILABLE ... connection refused
$S/dra-inspect --kubeconfig closed-port.kubeconfig report cluster --strict  # exit 3
$S/dra-inspect --kubeconfig hang.kubeconfig --timeout 3s inspect slices # exit 0 after 18 s, "context deadline exceeded" (before fix F2; now one overall deadline)
```

`closed-port.kubeconfig` points at `https://127.0.0.1:1`; `hang.kubeconfig` at a
local socket that accepts and never answers.

### Cleanup proof

After the failed `kind create`, kind itself deleted the half-created node
container. This run created and removed: the `kind` podman network
(`podman network rm kind`), the `localhost/dra-inspect:harness` image. Final
state: `kind get clusters` prints `No kind clusters found.`; `podman ps -a`
lists only `nginx` and `openclaw-gateway` (both pre-existing, untouched);
`podman network ls` lists only `podman`; `podman volume ls` is empty. Left
cached on purpose: `docker.io/kindest/node` (908 MB) and
`gcr.io/distroless/static-debian12` base (3.19 MB). `~/.kube` was never read
or written; no cluster ever held a kubeconfig. Scratch dir is under `/tmp`
(127 MB).

### Findings

| ID | Severity | Finding | Minimal repro |
|---|---|---|---|
| F1 | environment (blocks live phase) | kind v0.33.0 cannot create a cluster on rootless podman 3.4.4: the `kind` CNI network conflist uses config version 1.0.0 which the installed `firewall` plugin rejects. Also only `memory pids` cgroup controllers are delegated to the user (kind rootless docs need `cpu cpuset io` too, i.e. `cpu cpuset io memory pids`), so a CNI fix alone would probably not suffice. No workaround attempted (would need system config changes). | `KIND_EXPERIMENTAL_PROVIDER=podman kind create cluster --name dra-inspect-test --kubeconfig $S/kind.kubeconfig --image kindest/node:v1.34.11@sha256:44e2...d67d` fails with `Error: error configuring network namespace for container ...: CNI network "kind" not found` after `plugin firewall does not support config version "1.0.0"` |
| F2 | medium (fixed: `--timeout` is now one overall deadline, kinds past it report `timed out after <timeout>`) | `--timeout` was a per-request timeout and collection is sequential over 6 resource kinds, so wall time against a hung API server is about 6 x timeout (3 s gave 18 s for a single `inspect slices`; the 30 s default gives about 3 min). Neither README nor `--help` says so. | hang listener, `dra-inspect --kubeconfig hang.kubeconfig --timeout 3s inspect slices` takes 18 s |
| F3 | low | Total API failure exits 0 and prints `UNAVAILABLE: ...` unless `--strict` (documented behavior, but surprising for a single-purpose `inspect slices`; the message carries the raw client-go URL including `?limit=500`). Exit is 3 with `--strict`, as documented. | `dra-inspect --kubeconfig closed-port.kubeconfig inspect slices; echo $?` prints 0 |
| F4 | low (doc) | Job template comment said the in-cluster fallback was "verified live in Task 8"; it was not. Comment reworded. | `deploy/helm/dra-inspect/templates/job.yaml` |
| F5 | low (doc) | The plan named the cluster `dra-inspect-harness`, built the image from a plain `go build` binary, and listed "rootless works" as the only podman prerequisite. Corrected in this document. | |

Observation for the next live run: the `v0.5.0` example driver targets
Kubernetes 1.37 in its own demo (kind config with feature gates and a 1.37 node
image). The chart declares `kubeVersion >=1.33.0-0`, so the 1.34.11 node image
is expected to work but that is untested; if the plugin or admission policy
misbehaves, retry with `kindest/node:v1.37.0@sha256:a1ed56cfb0e7b93589bdf97c8cd566405a265939e3620fc4f5de89adff580ae5`.

## API-server-level live verification (no kubelet)

**Status: RUN 2026-10-29** with `hack/live-apiserver.sh` (final run: 190 PASS, 0 FAIL,
3 XFAIL known findings, exit 0).
It starts `etcd` and `kube-apiserver` as plain host processes on `127.0.0.1`
(high random ports, own uid, no sudo). The two binaries are not downloaded: the
script `podman create`s (never starts) a container from the already-cached
`docker.io/kindest/node` image (`sha256:44e222ee...d67d`), `podman export`s it, and
untars the two content-addressed layer blobs that hold them (the script checks each
blob's sha256 equals its name). Nothing is pulled; if the image is not cached the
script stops. All scratch state lives under one directory; `~/.kube` is neither read
nor written (`KUBECACHEDIR` is redirected because kubectl would otherwise write its
discovery cache there; the script compares `~/.kube`'s mtime before and after).

### Versions

| Component | Version |
|---|---|
| kube-apiserver | v1.34.11 (from the kind node image) |
| etcd | 3.6.5 (same image) |
| kubectl | v1.35.10 (one-off download from the earlier run; the script uses `$KUBECTL`, then `PATH`, then the node image's own) |
| helm | v4.3.1+gc0d08d0 |
| podman | 3.4.4 (rootless) |
| go | go1.26.10 |
| dra-inspect | branch `feat/dra-inspect-v1` at `d911d63` plus this change (image `0.0.0-SNAPSHOT-d911d63`) |

kube-apiserver flags that matter: `--authorization-mode=Node,RBAC`, a static admin
token in `system:masters`, a generated RSA service-account key pair and issuer (so
`kubectl create token` works without controllers), default admission plugins
(PodSecurity included), `--feature-gates=DRAPartitionableDevices=true,DRAConsumableCapacity=true`
(alpha in 1.34; the fixtures use counters and consumable capacity; the script was not
run without them), `--endpoint-reconciler-type=none` (the loopback advertise address
cannot be published as the `kubernetes` Endpoints and only logs an error).
`resource.k8s.io/v1` is served by default.

### Commands

```bash
KUBECTL=/path/to/kubectl hack/live-apiserver.sh --scratch DIR      # the real run
KUBECTL=/path/to/kubectl hack/live-apiserver.sh --red --scratch DIR # every expectation corrupted: must fail everywhere
hack/test-live-helpers.sh                                           # offline proof the shared helpers can fail
```

`--keep` leaves the processes and scratch dir. The script refuses an inherited
`KUBECONFIG`, picks three free ports (collision-checked with `ss`), and its EXIT trap
CONTs and kills exactly the two PIDs it started, removes the extraction container and
the image it built, and prints the cleanup proof below.

### Results

| Step | Exit | Result |
|---|---|---|
| 0 Extract apiserver, etcd from the cached node image | 0 | pass; versions pinned and checked |
| A Build binary and goreleaser-checked image | 0 | pass; `check-release: ok`, `check-image: ok (0.0.0-SNAPSHOT-d911d63, user 65532:65532)` |
| 1 Start etcd and apiserver | 0 | pass; `/readyz` ok; serving cert SAN has `127.0.0.1`; `resource.k8s.io/v1` served; server `v1.34.11` |
| B Load fixtures as real objects | 0 | classes-claims 10/10 and xe9680 20/20 created (status subresource written for claims and pods); multinode and xe9785l partly rejected, see F3 and F4 |
| B golden comparison, live vs committed golden `results[]` | 0 | classes-claims topology, deviceclasses (plain and `--selector`), claims (ignoring the KubeVirt `vmi` field) and xe9680 slices, drivers, attributes, drivers `--attributes`, topology, deviceclasses, `check topology`, `report cluster`: **all identical** |
| C `inspect` (6, table and JSON), `check` (5), `report cluster`, `report support` as admin | inspect 0; `check alignment` 1, `check capacity` 1, `report *` 1, `check counters`/`topology`/`kubevirt` 0 | pass: exit 1 is a genuine `fail` result because the combined dataset has claims whose devices are not published (rejected slices); `check kubevirt` is `unavailable` with exit 0 |
| D `snapshot capture --with-adapters`, then 14 commands `--from` | 0 | live `results[]` equals replay `results[]` for all (including `report support`); snapshot mode 600; collection `ok ok ok ok` plus `virtualmachineinstances: notFound`; opaque marker present in the cluster, absent from snapshot and every output |
| I `-n demo` | 0 | live equals replay for `report cluster` and `inspect claims`; only `demo/` subjects |
| E RBAC (`helm template` piped to `kubectl apply`, both profiles, `job.enabled=true`) | cluster SA 1, namespace SA 0, namespace SA `--strict` 3 | cluster SA results equal the admin's, collections ok; namespace SA (`rbac.namespaces={demo}`, `-n demo`): slices, deviceclasses, nodes `forbidden`, eight slice-based results `unavailable`, claims and pods scoped `namespace demo only`; without `-n`, claims `unavailable` |
| E read-only proof | n/a | `auth can-i` get/list `yes` and create/update/patch/delete/deletecollection `no` on slices, deviceclasses, claims, pods, nodes; `pods/exec` and `secrets` `no`; `can-i --list` has only get/list rows (selfsubject* and non-resource URL rows excluded); create/delete/exec/patch as the SA (impersonated and with its real token) are `Forbidden`; the target object survived |
| F Pod Security `restricted` | 0 | the chart's Job pod template is admitted in both namespaces by server dry run and real create with no warning; applying the Job objects prints no warning; a pod with `hostNetwork` and privilege escalation is rejected in the same namespaces (`violates PodSecurity "restricted:latest": host namespaces (hostNetwork=true), ...`) and is not created |
| G In-cluster auth under podman | cluster SA 1, namespace SA 0 / `--strict` 3 | `podman run --network host --read-only --user 65532:65532 --cap-drop=ALL --security-opt no-new-privileges` with the token, CA and namespace files mounted and `KUBERNETES_SERVICE_HOST/PORT` set: client-go's in-cluster path works with no kubeconfig; exit codes and `results[]` equal the workstation run as the same SA; the README's `sed -n '/^{$/,/^}$/p' | jq` extraction yields the JSON |
| H SIGSTOP the apiserver, `--timeout 3s` | 0 (`--strict` 3) | `UNAVAILABLE: inspect.slices: timed out after 3s`, no URL in the text, but **6.02 s** elapsed, see F1; the server recovers after SIGCONT |
| H closed port, bad CA, bad token | 0 (`--strict` 3 each) | `connection refused`, `x509: certificate signed by unknown authority`, `Unauthorized`; the first two echo the raw URL, see F2 |

### Findings

| ID | Severity | Finding | Minimal repro |
|---|---|---|---|
| F1 | medium (product) | `--timeout` is not the total on a hung server: a 3 s deadline costs 6.02 s. `Collect` binds the five list calls to the context, but `collectVMIs` calls `Discovery.ServerResourcesForGroupVersion` without it, so after the deadline fires it still waits for the client-go HTTP timeout (`cfg.Timeout`, the same 3 s). README and `--help` say "overall deadline". The `xfail` in the script flips to `XPASS` (a failure) once fixed. | `kill -STOP <kube-apiserver pid>; time dra-inspect --kubeconfig K --timeout 3s inspect slices` prints `UNAVAILABLE: inspect.slices: timed out after 3s` after 6.02 s |
| F2 | low-medium (product) | Only the timeout reason is sanitized. Connection and TLS errors still print the raw client-go URL including `?limit=500`. | `dra-inspect --kubeconfig <closed port> inspect slices` prints `UNAVAILABLE: inspect.slices: Get "https://127.0.0.1:PORT/apis/resource.k8s.io/v1/resourceslices?limit=500": dial tcp ...: connect: connection refused`; same with a wrong CA (`tls: failed to verify certificate: x509: ...`) |
| F3 | medium (fixture validity) | The fixtures with list-valued attributes (`ints`, `strings`: `multinode` and `xe9785l` slices) are not valid for a 1.34.11 apiserver: 6 ResourceSlices are rejected (`spec.devices[0].attributes[resource.kubernetes.io/numaNode]: Required value: exactly one value must be specified`; kubectl strict validation says `unknown field "ints" in io.k8s.api.resource.v1.DeviceAttribute`). The code builds against `k8s.io/api` v0.37 so it parses them, but the 1.34 node image the kind plan pins cannot publish them, so xe9785l topology and alignment cannot be reproduced live on 1.34 and no golden comparison exists for xe9785l or multinode. A server new enough to serve list attributes is needed (not available offline here). `docs/rbac.md` says unknown fields "come back empty"; that is right for reading (a 1.34 server never holds them) and does not cover this write-side rejection. | `kubectl create -f testdata/fixtures/multinode/resourceslices.yaml` (after stripping server fields) against 1.34.11 |
| F4 | low (fixture hygiene) | 5 DeviceClass names (`dra.cpu`, `dra.hugepages-2m`, `dra.memory`, `gpu.amd.com`, `sriovnetwork.k8snetworkplumbingwg.io`) exist in both the `xe9680` and `xe9785l` fixtures, so the sets cannot coexist in one cluster (`AlreadyExists`). The xe9785l DeviceClass `opaque.parameters.alignments: null` makes kubectl's client-side check say `unknown object type "nil"`; the apiserver accepts it. | create both sets in one cluster |
| F5 | info | The golden `inspect claims` for `classes-claims` carries `vmi: vm1` from a KubeVirt fixture; live without the CRD says `vmi: unavailable: kubevirt.io/v1 not served`. Expected; the script compares with that field removed and asserts the live text. | |
| F6 | info (harness lesson) | Without controllers: namespaces need a hand-made `default` ServiceAccount or any Pod is refused; `--allow-privileged` is false by default so a `privileged: true` negative control is rejected by validation, not by Pod Security (use `hostNetwork` and privilege escalation); kubectl writes its discovery cache under `~/.kube` unless `KUBECACHEDIR` is set (`hack/live-kind.sh` now sets it too). | |
| F7 | info | `report cluster`, `check alignment` and `check capacity` exit 1 on the combined dataset: genuine `fail` results, not tool errors. `can-i --list` also prints the discovery/health non-resource URL rows every authenticated user gets; they are not ours and the helpers skip them. | |

### What this route cannot show

All of the following stay **NOT RUN** (they are the kind plan's job):

- Pods created by a kubelet or run by it: the Job objects were created and the
  Pod spec admitted, but no controller creates a Job's Pod and no kubelet starts
  one. The podman run in step G emulates only the container side (hardening flags,
  mounted token and CA, env vars), not a real service-account projected volume or
  the pod network.
- Job controller behavior: completion, `backoffLimit`, `ttlSecondsAfterFinished`,
  `activeDeadlineSeconds`, the Job pod's exit code as `kubectl` reports it.
- A real DRA driver publishing slices, the kubelet plugin, CDI injection, claim
  allocation by the scheduler (allocations here are written by hand through the
  status subresource), and real Node objects.
- The kind node's containerd/CNI environment, arm64 and darwin hosts, and
  Kubernetes versions other than 1.34.11 (F3 needs a newer server).
- KubeVirt: the CRD was absent, so only the `notFound`/`unavailable` path ran.
- The host adapter on a real node (it ran on this workstation and its output was
  compared live against replay only).

### Assertion red-first

`--red` makes `check()` compare against `RED-WRONG-<expected>`: all 190 assertions of
a full end-to-end run failed (190 `FAIL`, 0 `PASS`, exit 0 meaning "every assertion
can fail"). Some assertions also failed for real while the script was written (also
verbatim): the privileged-pod negative control (`The Pod "psa-bad-dic" is invalid:
spec.containers[0].securityContext.privileged: Forbidden: disallowed by cluster
policy`, rejected before Pod Security ran), the configmap probe (`error: failed to
create configmap: configmaps is forbidden: ...`, lower-case spelling, now matched),
and the hang timing (F1). Whether the *comparison logic* of the equality and
absence assertions can fail on different data is proven offline by
`hack/test-live-helpers.sh` (for example `same_results: one status differs`,
`golden_results: differing field fails`, `absent: marker in file`,
`xfail: a fixed finding (equal) fails the run`), run in CI.

| Assertion family | Failing output seen (`--red`) | Green |
|---|---|---|
| apiserver ready / SAN / `resource.k8s.io/v1` / version | `FAIL  server version: want [RED-WRONG-v1.34.11] got [v1.34.11]` | PASS |
| fixture load counts, rejections | `FAIL  rejected ResourceSlices (list-valued attributes need a newer apiserver): want [RED-WRONG-6] got [6]` | PASS |
| golden equality | `FAIL  golden: inspect topology classes-claims: want [RED-WRONG-ok] got [ok]` | PASS |
| CLI exit codes (C, E, G, H) | `FAIL  exit closed-strict: want [RED-WRONG-3] got [3]` | PASS |
| replay equals live | `FAIL  replay==live: inspect slices: want [RED-WRONG-ok] got [ok]` | PASS |
| results non-empty | `FAIL  non-empty results: inspect slices: want [RED-WRONG-true] got [true]` | PASS |
| snapshot mode and collection states | `FAIL  snapshot mode: want [RED-WRONG-600] got [600]` | PASS |
| marker absent | `FAIL  no opaque marker in snapshot: want [RED-WRONG-absent] got [absent]` | PASS |
| namespace scoping | `FAIL  -n demo claims: only demo/ subjects: want [RED-WRONG-0] got [0]` | PASS |
| chart applied, no PSA warning | `FAIL  no PodSecurity warnings on applying the Jobs: want [RED-WRONG-0] got [0]` | PASS |
| SA results equal admin / in-cluster equal workstation | `FAIL  in-cluster: results == workstation results as the same SA: want [RED-WRONG-d6c5cb1d8294a1a741a3b72e669eab14  -] got [d6c5cb1d8294a1a741a3b72e669eab14  -]` | PASS |
| namespace SA unavailable set | `FAIL  ns SA: claims collected, none unavailable: want [RED-WRONG-true] got [true]` | PASS |
| can-i matrix and `--list` | `FAIL  can-i create pods as system:serviceaccount:dic:di-c-dra-inspect: want [RED-WRONG-no] got [no]` | PASS |
| mutations are Forbidden | `FAIL  forbidden as cluster SA: kubectl delete pod web-0 -n demo: want [RED-WRONG-true] got [true]` | PASS |
| PSA admit / reject | `FAIL  negative control: hostNetwork+privilege-escalation pod rejected in dic (real create): want [RED-WRONG-true] got [true]` | PASS |
| README `sed` extraction | `FAIL  in-cluster: README sed extraction yields valid JSON: want [RED-WRONG-true] got [true]` | PASS |
| hang text, no URL, recovery | `FAIL  hang: reason does not echo a URL: want [RED-WRONG-0] got [0]` | PASS |
| closed port / bad CA / bad token | `FAIL  bad CA: x509 unknown authority: want [RED-WRONG-true] got [true]` | PASS |
| `~/.kube` untouched | `FAIL  ~/.kube untouched: want [RED-WRONG-1763065414] got [1763065414]` | PASS |

The three known findings are `XFAIL` lines in the real run, not assertions:
`XFAIL F1 hang: --timeout 3s should take about 3 s in total: want [true] got [false]`,
`XFAIL F2 closed: reason should not echo the request URL: want [0] got [1]`, and the
same for `badca`.

### Cleanup proof

Printed by the script's trap after the final run, then re-checked by hand:

```
pid 1737717 gone
pid 1737716 gone
port 27554 free
port 39409 free
port 37066 free
podman ps -a: [nginx openclaw-gateway ]      # both pre-existing, untouched
image localhost/dra-inspect:harness-1736488: removed
~/.kube mtime unchanged
```

`ps` shows no `kube-apiserver` or `etcd`, `ss -ltnp` no listener of ours, and the
extraction container (`dra-apisrv-extract-*`) was removed right after `podman
export`. The cached `kindest/node` image was left alone.

## Re-running

```bash
go install sigs.k8s.io/kind@v0.33.0   # plus podman 4+, kubectl, helm, jq, goreleaser
hack/live-kind.sh            # creates dra-inspect-test, runs steps 1-10, always cleans up
hack/live-kind.sh --keep     # keep the cluster for poking around
```

The script refuses to run when an inherited `KUBECONFIG` points anywhere other
than its own scratch file, or when the cluster name is taken; it then exports its
scratch kubeconfig and passes it explicitly. It builds the image for the host
architecture under a unique tag, pins and checks the kind version, and aborts
before installing if the sample-driver commit differs. Its pure helpers are
tested offline by `hack/test-live-helpers.sh` (run in CI). The script itself was
syntax-checked (`bash -n`) but could not be run end to end here (F1);
`shellcheck` is not installed on this machine.
