package view_test

import (
	"strings"
	"testing"

	"github.com/johnahull/k8s-dra-inspect/internal/inspect"
	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/view"
)

func slicesOut(t *testing.T, snap model.Snapshot) string {
	return show(t, snap, inspect.Slices(), view.Slices)
}

func TestSlicesExactLayout(t *testing.T) {
	got := slicesOut(t, fx(t, "classes-claims"))
	want := strings.Join([]string{
		"═══ ResourceSlice Hardware Summary ═══",
		"",
		"Node: node-a.example.com",
		"  gpu.example.com (2 devices, 2 in use, 0 free)",
		"    NUMA 0  gpu-0  →demo/virt-launcher-vm1-abcde",
		"    NUMA 1  gpu-1  →demo/web-0",
		"",
	}, "\n")
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestSlicesDeviceLabelAndFreeDevices(t *testing.T) {
	snap := fx(t, "classes-claims")
	setAttr(&snap, "gpu-0", "resource.kubernetes.io/pciBusID", "0000:41:00.0")
	setAttr(&snap, "gpu-0", "productName", "AMD MI355X")
	setAttr(&snap, "gpu-1", "model", "Fallback Model")
	snap.ResourceClaims = nil
	got := slicesOut(t, snap)
	for _, want := range []string{
		"  gpu.example.com (2 devices, 0 in use, 2 free)",
		"    NUMA 0  gpu-0 (0000:41:00.0) [AMD MI355X]",
		"    NUMA 1  gpu-1 [Fallback Model]",
	} {
		if !hasLine(got, want) {
			t.Errorf("missing line %q in:\n%s", want, got)
		}
	}
}

func TestSlicesNamespaceScopeAnnotated(t *testing.T) {
	snap := fx(t, "classes-claims")
	snap.Namespace = "demo"
	got := slicesOut(t, snap)
	if !hasLine(got, "  gpu.example.com (2 devices, 2 in use (namespace demo only))") {
		t.Errorf("scope not annotated:\n%s", got)
	}
	if strings.Contains(got, " free") {
		t.Errorf("a namespace-scoped view must not claim devices are free:\n%s", got)
	}
}

func TestSlicesUsageUnavailable(t *testing.T) {
	snap := fx(t, "classes-claims")
	snap.Collection["resourceclaims"] = model.CollectionStatus{State: model.StateForbidden, Reason: "forbidden: cannot list resourceclaims"}
	got := slicesOut(t, snap)
	for _, want := range []string{
		"? usage unavailable: forbidden: cannot list resourceclaims",
		"  gpu.example.com (2 devices)",
		"    NUMA 0  gpu-0",
	} {
		if !hasLine(got, want) {
			t.Errorf("missing line %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "in use") || strings.Contains(got, "→") {
		t.Errorf("usage must not be shown when unavailable:\n%s", got)
	}
}

func TestSlicesPodsNotCollectedShowsClaims(t *testing.T) {
	snap := fx(t, "classes-claims")
	snap.Collection["pods"] = model.CollectionStatus{State: model.StateForbidden, Reason: "forbidden: cannot list pods"}
	snap.Pods = nil
	for i := range snap.ResourceClaims {
		snap.ResourceClaims[i].Status.ReservedFor = nil
	}
	got := slicesOut(t, snap)
	for _, want := range []string{
		"? pods not collected: arrows show claims, not pods",
		"    NUMA 0  gpu-0  →claim demo/shared-gpu",
		"    NUMA 1  gpu-1  →claim demo/web-0-gpu-xyz12",
	} {
		if !hasLine(got, want) {
			t.Errorf("missing line %q in:\n%s", want, got)
		}
	}
}

func TestSlicesStaleAndIncompleteGenerations(t *testing.T) {
	// Generation 2 is complete: the generation 1 slice is stale.
	snap := fx(t, "classes-claims")
	g2 := *snap.ResourceSlices[0].DeepCopy()
	g2.Name = "gpu-node-a-new"
	g2.Spec.Pool.Generation = 2
	snap.ResourceSlices = append(snap.ResourceSlices, g2)
	got := slicesOut(t, snap)
	if want := "! slice gpu-node-a (generation 1) is stale: pool node-a.example.com is at generation 2"; !hasLine(got, "  "+want) {
		t.Errorf("stale line missing:\n%s", got)
	}

	// Generation 2 lists 2 slices but only 1 was seen: generation 1 stays current.
	snap = fx(t, "classes-claims")
	g2 = *snap.ResourceSlices[0].DeepCopy()
	g2.Name = "gpu-node-a-new"
	g2.Spec.Pool.Generation, g2.Spec.Pool.ResourceSliceCount = 2, 2
	snap.ResourceSlices = append(snap.ResourceSlices, g2)
	got = slicesOut(t, snap)
	want := "! slice gpu-node-a-new (generation 2) is ignored: newer than current generation 1 but incomplete (1 of 2 slices observed)"
	if !hasLine(got, "  "+want) {
		t.Errorf("newer-incomplete line missing:\n%s", got)
	}

	// No complete generation at all: the newest is shown and flagged.
	snap = fx(t, "classes-claims")
	snap.ResourceSlices[0].Spec.Pool.ResourceSliceCount = 2
	got = slicesOut(t, snap)
	want = "! slice gpu-node-a (generation 1): pool node-a.example.com has 1 of 2 slices for its newest generation"
	if !hasLine(got, "  "+want) {
		t.Errorf("incomplete line missing:\n%s", got)
	}
}

func TestSlicesAcrossNodesAlignsNumaColumn(t *testing.T) {
	got := slicesOut(t, fx(t, "multinode"))
	for _, want := range []string{
		"Node: node-a.example.com",
		"Node: node-b.example.com",
		"    NUMA 0,1,2,3  a-gpu-0  →demo/pair-pod",
		"    NUMA 2    b-gpu-0",
		"    NUMA 2,3  b-gpu-1",
	} {
		if !hasLine(got, want) {
			t.Errorf("missing line %q in:\n%s", want, got)
		}
	}
}
