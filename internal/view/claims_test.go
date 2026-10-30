package view_test

import (
	"strings"
	"testing"

	"github.com/johnahull/k8s-dra-inspect/internal/inspect"
	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/view"
	resourcev1 "k8s.io/api/resource/v1"
)

func claimsOut(t *testing.T, snap model.Snapshot) string {
	return show(t, snap, inspect.Claims(), view.Claims)
}

func TestClaimsExactLayout(t *testing.T) {
	got := claimsOut(t, fx(t, "classes-claims"))
	want := strings.Join([]string{
		"═══ ResourceClaims ═══",
		"",
		"demo/broken → no pods",
		"  ✗ request gpu: gpu.example.com/gpu-9 not found in ResourceSlices (pool node-a.example.com)",
		"",
		"demo/pending → pending (not allocated)",
		"",
		"demo/shared-gpu → pod virt-launcher-vm1-abcde (VM vm1)",
		"  request gpu: gpu.example.com/gpu-0  NUMA 0  pcieRoot pci0000:00  node node-a.example.com",
		"",
		"demo/web-0-gpu-xyz12 → pod web-0",
		"  request gpu: gpu.example.com/gpu-1  NUMA 1  pcieRoot pci0000:80  node node-a.example.com",
		"",
	}, "\n")
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestClaimsDeviceShowsBusIDAndProduct(t *testing.T) {
	snap := fx(t, "classes-claims")
	setAttr(&snap, "gpu-0", "resource.kubernetes.io/pciBusID", "0000:41:00.0")
	setAttr(&snap, "gpu-0", "productName", "AMD MI355X")
	got := claimsOut(t, snap)
	want := "  request gpu: gpu.example.com/gpu-0 (0000:41:00.0) [AMD MI355X]  NUMA 0  pcieRoot pci0000:00  node node-a.example.com"
	if !hasLine(got, want) {
		t.Errorf("missing %q in:\n%s", want, got)
	}
}

func TestClaimsSeveralPodsAndNumaSets(t *testing.T) {
	snap := fx(t, "classes-claims")
	for i := range snap.ResourceClaims {
		if snap.ResourceClaims[i].Name == "shared-gpu" {
			snap.ResourceClaims[i].Status.ReservedFor = append(snap.ResourceClaims[i].Status.ReservedFor, resourcev1.ResourceClaimConsumerReference{Resource: "pods", Name: "other"})
		}
	}
	got := claimsOut(t, snap)
	if want := "demo/shared-gpu → pods other, virt-launcher-vm1-abcde (VM vm1)"; !hasLine(got, want) {
		t.Errorf("missing %q in:\n%s", want, got)
	}
	got = claimsOut(t, fx(t, "multinode"))
	if want := "  request gpu: gpu.example.com/a-gpu-1  NUMA 0,1,2,3  pcieRoot pci0000:00  node node-a.example.com"; !hasLine(got, want) {
		t.Errorf("missing %q in:\n%s", want, got)
	}
}

func TestClaimsPodsAndVMIUnavailableInWords(t *testing.T) {
	snap := fx(t, "classes-claims")
	snap.Pods = []model.PodRef(nil)
	snap.Collection["pods"] = model.CollectionStatus{State: model.StateForbidden, Reason: "forbidden: cannot list pods"}
	snap.Collection["virtualmachineinstances"] = model.CollectionStatus{State: model.StateNotFound, Reason: "kubevirt.io/v1 not served"}
	got := claimsOut(t, snap)
	for _, want := range []string{
		"demo/shared-gpu → pods unavailable",
		"  ? pods unavailable: forbidden: cannot list pods",
		"  ? vmi unavailable: kubevirt.io/v1 not served",
	} {
		if !hasLine(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}
