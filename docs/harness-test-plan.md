# Live verification plan (kind)

**Status: NOT RUN - pending approval.** Nothing in this document has been
executed. The offline half of the migration work (fixtures, parity tests,
sample bundle) is covered by `go test ./...`; this plan is the live half and
needs an explicit go-ahead because it creates a cluster, pulls container
images and downloads a chart. It fills in the results table at the end; until
then every cell is empty.

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
| kind provider | podman (`KIND_EXPERIMENTAL_PROVIDER=podman`), rootless works with cgroups v2 |
| Node image | `kindest/node:v1.34.<patch>@sha256:<digest>`, Kubernetes 1.34 or newer so `resource.k8s.io/v1` is served (DRA is GA, no feature gates). Record the exact tag and digest in the results table before running. |
| Sample driver | `kubernetes-sigs/dra-example-driver` checked out at a pinned **release tag** (record tag and commit). It publishes simulated GPUs without NUMA attributes, so topology findings are `warn`, which is expected. |
| dra-inspect | Built from the commit under test: `go build -o "$SCRATCH/dra-inspect" ./cmd/dra-inspect` and a local image (below). |

## Safety rules

- **Scratch kubeconfig only.** Always `export KUBECONFIG="$SCRATCH/kubeconfig"`
  and pass `--kubeconfig "$SCRATCH/kubeconfig"`. Never read or write
  `~/.kube/config`; run `kubectl config current-context` first and abort unless
  it is `kind-dra-inspect-harness`.
- **One cluster name:** `dra-inspect-harness`. Never touch other clusters.
- **No sudo.** Rootless podman only.
- **Pin and record** every image tag, digest and chart commit used.
- **Cleanup always:** `kind delete cluster --name dra-inspect-harness`, remove
  `$SCRATCH`, and `podman rmi localhost/dra-inspect:harness`. Run it even when a
  step fails.
- Only the commands below are run against the cluster; no `kubectl delete` of
  anything outside the harness namespaces.

```bash
export SCRATCH=$(mktemp -d)
export KUBECONFIG="$SCRATCH/kubeconfig"
export KIND_EXPERIMENTAL_PROVIDER=podman
export NODE_IMAGE='kindest/node:v1.34.<patch>@sha256:<digest>'   # fill in and record
export DRIVER_TAG='<release tag of dra-example-driver>'          # fill in and record
```

## Steps

Each step lists the commands and its acceptance criteria. Stop at the first
failed criterion, record it, and clean up.

### 1. Build the binary and a local image

```bash
go build -o "$SCRATCH/dra-inspect" ./cmd/dra-inspect
mkdir -p "$SCRATCH/ctx/linux/amd64"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o "$SCRATCH/ctx/linux/amd64/dra-inspect" ./cmd/dra-inspect
cp Dockerfile "$SCRATCH/ctx/"
podman build --platform linux/amd64 --build-arg TARGETPLATFORM=linux/amd64 \
  -t localhost/dra-inspect:harness "$SCRATCH/ctx"
hack/check-image.sh localhost/dra-inspect:harness "$("$SCRATCH/dra-inspect" version)"
```

Accept: `check-image: ok`, non-root user, no shell.

### 2. Create the cluster

```bash
kind create cluster --name dra-inspect-harness --image "$NODE_IMAGE" --kubeconfig "$KUBECONFIG"
kubectl config current-context        # must be kind-dra-inspect-harness
kubectl api-resources --api-group=resource.k8s.io | grep -E 'resourceslices|deviceclasses|resourceclaims'
podman save localhost/dra-inspect:harness -o "$SCRATCH/img.tar"
kind load image-archive "$SCRATCH/img.tar" --name dra-inspect-harness
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
kind delete cluster --name dra-inspect-harness
podman rmi localhost/dra-inspect:harness || true
rm -rf "$SCRATCH"
```

Accept: `kind get clusters` no longer lists `dra-inspect-harness`.

## Results

Fill in after the run; leave empty until then.

| Field | Value |
|---|---|
| Date / operator | |
| dra-inspect commit | |
| kindest/node tag and digest | |
| dra-example-driver tag and commit | |

| Step | Result (pass/fail) | Evidence / notes |
|---|---|---|
| 1 Build and image | | |
| 2 Cluster | | |
| 3 Sample driver | | |
| 4 Workstation CLI | | |
| 5 Claim and alignment | | |
| 6 Capture and replay equality | | |
| 7 Job, PSA restricted, in-cluster auth | | |
| 8 `auth can-i` read-only proof | | |
| 9 Namespace profile | | |
| 10 Adapters from a workstation | | |
| 11 Cleanup | | |
