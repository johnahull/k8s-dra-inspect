# `dra-inspect` live diagnosis recording

![Demo](demo.gif)

This is a recording-only operator demo of `dra-inspect v0.1.3` on the live
AMD MI300X DRA cluster. It demonstrates the tool itself: published drivers,
hardware inventory, topology, device attributes, claim allocation, alignment,
shared counters, consumable capacity, and topology validation.

The recording creates a temporary `ResourceClaim` and pause Pod offscreen so
the allocation checks have real data to inspect. The same
`pod-example.yaml` is used for setup and is the only workload configuration in
the demo. The YAML is intentionally kept offscreen so the recording stays
focused on diagnostic output.

## Prerequisites

- Kubernetes access to `10.6.62.52` in the default namespace
- `dra-inspect v0.1.3` on `PATH`
- The topology coordinator and `mi300x-inference-numa` offering already deployed
- AMD GPU, CPU, memory, and network DRA drivers already running
- VHS and `ffmpeg` installed for recording

The demo does not create or modify offerings, DeviceClasses, drivers, or
existing workloads.

## Rehearsal

Run this on `jhull@10.6.62.52`:

```bash
export PATH=$HOME/.local/bin:$PATH
export DEMO=$HOME/devel/k8s-dra-inspect/demos/live-diagnosis
kubectl delete pod demo-mi300x-inference-pod -n default --ignore-not-found --wait=true --timeout=180s
kubectl delete resourceclaim demo-mi300x-inference -n default --ignore-not-found --wait=true --timeout=60s
kubectl apply -n default -f $DEMO/pod-example.yaml
kubectl wait --for=condition=Ready pod/demo-mi300x-inference-pod -n default --timeout=180s
```

Then run the commands in `demo.tape` interactively. Expected results are:

- `inspect drivers` lists the deployed DRA drivers.
- `inspect slices` and `inspect topology` show the MI300X, CPU, memory, and
  SR-IOV resources.
- `inspect attributes --topology` shows NUMA and PCIe-root attributes.
- `inspect claims` shows `demo-mi300x-inference` allocated to the Pod.
- `check alignment`, `check counters`, `check capacity`, and `check topology`
  report passing results.

## Recording

```bash
vhs demo.tape
```

The tape uses readable text output, clears between commands, pauses several
seconds after every command, and uses `more` for the attributes view. It does
not show CLI versions, DeviceClasses, table output, snapshots, host checks,
KubeVirt checks, or support reports.

Cleanup removes only `demo-mi300x-inference-pod` and
`demo-mi300x-inference`. Verify both are gone after recording before reusing
the cluster.

## Files

| File | Description |
|------|-------------|
| `demo.tape` | VHS recording script |
| `pod-example.yaml` | Temporary ResourceClaim and Pod used by setup |
| `demo.gif` | Generated recording |
| `demo.mp4` | Generated recording |
