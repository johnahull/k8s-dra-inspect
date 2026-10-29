# Live verification plan (kind)

**Status: RUN 2026-10-29, live cluster part BLOCKED on this environment.**
`kind create cluster` failed on rootless podman 3.4.4 (CNI error, exact text in
[Findings](#findings)), so nothing that needs a cluster ran: steps 2 to 10 below
are unexecuted. What did run: kind, kubectl and the sample-driver chart were
fetched and pinned, the image was built and passed `hack/check-image.sh`
(step 1), the chart renders and lints, the workstation CLI was exercised
against unreachable and hanging API servers, the assertion helpers of
`hack/live-kind.sh` were proven able to fail, and the script itself was written.
Re-run everything with `hack/live-kind.sh` on a host with podman 4+ (netavark)
or Docker.

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
| kind provider | podman 4 or newer with netavark (`KIND_EXPERIMENTAL_PROVIDER=podman`), cgroups v2 with `cpu cpuset memory pids` delegated to the user for rootless; Docker also works. podman 3.4.4 (Ubuntu 22.04) does not: see finding F1. |
| Node image | `kindest/node:v1.34.11@sha256:44e222ee2132dab25ff87301682f89eb82c7880ea3a1bf543bfe9708fd08d67d` (from the kind v0.33.0 release notes; Kubernetes 1.34 or newer so `resource.k8s.io/v1` is served, DRA is GA, no feature gates; containerd 2.3.4 so CDI is on by default). |
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
- **Cleanup always:** `kind delete cluster --name dra-inspect-test`, remove
  `$SCRATCH`, and `podman rmi localhost/dra-inspect:harness`. Run it even when a
  step fails.
- Only the commands below are run against the cluster; no `kubectl delete` of
  anything outside the harness namespaces.

```bash
export SCRATCH=$(mktemp -d)
export KUBECONFIG="$SCRATCH/kubeconfig"
export KIND_EXPERIMENTAL_PROVIDER=podman
export NODE_IMAGE='kindest/node:v1.34.11@sha256:44e222ee2132dab25ff87301682f89eb82c7880ea3a1bf543bfe9708fd08d67d'
export DRIVER_TAG='v0.5.0'   # commit 93ff610f3c3bfd603c592d1c281a236c1463edb9
```

## Steps

Each step lists the commands and its acceptance criteria. Stop at the first
failed criterion, record it, and clean up.

### 1. Build the binary and a local image

```bash
go build -o "$SCRATCH/dra-inspect" ./cmd/dra-inspect
mkdir -p "$SCRATCH/ctx/linux/amd64"
hack/check-release.sh        # goreleaser snapshot binary in dist/
cp "$(find dist -type f -name dra-inspect | head -n1)" "$SCRATCH/ctx/linux/amd64/dra-inspect"
cp Dockerfile "$SCRATCH/ctx/"
podman build --platform linux/amd64 --build-arg TARGETPLATFORM=linux/amd64 \
  -t localhost/dra-inspect:harness "$SCRATCH/ctx"
hack/check-image.sh localhost/dra-inspect:harness     # expects the dist/metadata.json snapshot version
```

Accept: `check-image: ok`, non-root user, no shell. **Result: pass**
(`check-image: ok (0.0.0-SNAPSHOT-4683a0e, user 65532:65532)`, image 32.6 MB).

### 2. Create the cluster

```bash
kind create cluster --name dra-inspect-test --image "$NODE_IMAGE" --kubeconfig "$KUBECONFIG"
kubectl config current-context        # must be kind-dra-inspect-test
kubectl api-resources --api-group=resource.k8s.io | grep -E 'resourceslices|deviceclasses|resourceclaims'
podman save localhost/dra-inspect:harness -o "$SCRATCH/img.tar"
kind load image-archive "$SCRATCH/img.tar" --name dra-inspect-test
```

Accept: three `resource.k8s.io` resources listed; image loaded.

### 3. Install the sample driver

Install `dra-example-driver` at `$DRIVER_TAG` following its README for kind
(its Helm chart from the checked-out tag; record the exact command used).

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
  --set image.repository=localhost/dra-inspect --set image.tag=harness \
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
  --set image.repository=localhost/dra-inspect --set image.tag=harness --set image.pullPolicy=Never
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
helm uninstall dra-inspect -n team-a || true
kind delete cluster --name dra-inspect-test
podman rmi localhost/dra-inspect:harness || true
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
| kubectl | v1.35.10 (downloaded to the scratch dir, sha256 verified) |
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
| F1 | environment (blocks live phase) | kind v0.33.0 cannot create a cluster on rootless podman 3.4.4: the `kind` CNI network conflist uses config version 1.0.0 which the installed `firewall` plugin rejects. Also only `memory pids` cgroup controllers are delegated to the user (kind rootless docs need `cpu cpuset` too), so a CNI fix alone would probably not suffice. No workaround attempted (would need system config changes). | `KIND_EXPERIMENTAL_PROVIDER=podman kind create cluster --name dra-inspect-test --kubeconfig $S/kind.kubeconfig --image kindest/node:v1.34.11@sha256:44e2...d67d` fails with `Error: error configuring network namespace for container ...: CNI network "kind" not found` after `plugin firewall does not support config version "1.0.0"` |
| F2 | medium (fixed: `--timeout` is now one overall deadline, kinds past it report `timed out after <timeout>`) | `--timeout` was a per-request timeout and collection is sequential over 6 resource kinds, so wall time against a hung API server is about 6 x timeout (3 s gave 18 s for a single `inspect slices`; the 30 s default gives about 3 min). Neither README nor `--help` says so. | hang listener, `dra-inspect --kubeconfig hang.kubeconfig --timeout 3s inspect slices` takes 18 s |
| F3 | low | Total API failure exits 0 and prints `UNAVAILABLE: ...` unless `--strict` (documented behavior, but surprising for a single-purpose `inspect slices`; the message carries the raw client-go URL including `?limit=500`). Exit is 3 with `--strict`, as documented. | `dra-inspect --kubeconfig closed-port.kubeconfig inspect slices; echo $?` prints 0 |
| F4 | low (doc) | Job template comment said the in-cluster fallback was "verified live in Task 8"; it was not. Comment reworded. | `deploy/helm/dra-inspect/templates/job.yaml` |
| F5 | low (doc) | The plan named the cluster `dra-inspect-harness`, built the image from a plain `go build` binary, and listed "rootless works" as the only podman prerequisite. Corrected in this document. | |

Observation for the next live run: the `v0.5.0` example driver targets
Kubernetes 1.37 in its own demo (kind config with feature gates and a 1.37 node
image). The chart declares `kubeVersion >=1.33.0-0`, so the 1.34.11 node image
is expected to work but that is untested; if the plugin or admission policy
misbehaves, retry with `kindest/node:v1.37.0@sha256:a1ed56cfb0e7b93589bdf97c8cd566405a265939e3620fc4f5de89adff580ae5`.

## Re-running

```bash
go install sigs.k8s.io/kind@v0.33.0   # plus podman 4+/Docker, kubectl, helm, jq, goreleaser
hack/live-kind.sh            # creates dra-inspect-test, runs steps 1-7, always cleans up
hack/live-kind.sh --keep     # keep the cluster for poking around
```

The script refuses to run when `KUBECONFIG` is set or the cluster name is
taken, only ever uses its scratch kubeconfig, and sources cleanly
(`source hack/live-kind.sh`) so the assertion helpers can be tested. It was
syntax-checked (`bash -n`) but could not be run end to end here (F1);
`shellcheck` is not installed on this machine.
