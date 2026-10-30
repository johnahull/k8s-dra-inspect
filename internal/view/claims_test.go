package view_test

import (
	"strings"
	"testing"

	"github.com/johnahull/k8s-dra-inspect/internal/inspect"
	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/view"
	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/resource"
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
		"  gpu",
		"    ! gpu.example.com/gpu-9 not found in ResourceSlices (pool node-a.example.com)",
		"",
		"demo/pending → pending (not allocated)",
		"",
		"demo/shared-gpu → pod virt-launcher-vm1-abcde (VM vm1)",
		"  gpu",
		"    gpu-0  gpu.example.com",
		"    NUMA 0  pcieRoot pci0000:00",
		"    node node-a.example.com",
		"",
		"demo/web-0-gpu-xyz12 → pod web-0",
		"  gpu",
		"    gpu-1  gpu.example.com",
		"    NUMA 1  pcieRoot pci0000:80",
		"    node node-a.example.com",
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
	want := "    gpu-0 (0000:41:00.0) [AMD MI355X]  gpu.example.com"
	if !hasLine(got, want) {
		t.Errorf("missing %q in:\n%s", want, got)
	}
}

func TestClaimsConsumableDeviceShowsConsumedCapacity(t *testing.T) {
	got := claimsOut(t, fx(t, "capacity/pass"))
	for _, want := range []string{
		"    capacity size: 40Gi of 100Gi consumed",
		"    capacity size: 60Gi of 100Gi consumed",
	} {
		if !hasLine(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestClaimsConsumableDeviceResolvesQualifiedCapacityName(t *testing.T) {
	snap := fx(t, "capacity/pass")
	for i := range snap.ResourceClaims {
		for j := range snap.ResourceClaims[i].Status.Allocation.Devices.Results {
			r := &snap.ResourceClaims[i].Status.Allocation.Devices.Results[j]
			value := r.ConsumedCapacity["size"]
			r.ConsumedCapacity = map[resourcev1.QualifiedName]resource.Quantity{"mem.example.com/size": value}
		}
	}
	got := claimsOut(t, snap)
	for _, want := range []string{
		"    capacity mem.example.com/size: 40Gi of 100Gi consumed",
		"    capacity mem.example.com/size: 60Gi of 100Gi consumed",
	} {
		if !hasLine(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestClaimsShowsConsumedCapacityWhenPublishedCapacityIsMissing(t *testing.T) {
	snap := fx(t, "capacity/pass")
	for i := range snap.ResourceSlices {
		for j := range snap.ResourceSlices[i].Spec.Devices {
			snap.ResourceSlices[i].Spec.Devices[j].Capacity = nil
		}
	}
	got := claimsOut(t, snap)
	for _, want := range []string{
		"    capacity size: 40Gi consumed (not published)",
		"    capacity size: 60Gi consumed (not published)",
	} {
		if !hasLine(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestClaimsShowsConsumedCapacityWhenSlicesAreUnavailable(t *testing.T) {
	snap := fx(t, "capacity/pass")
	snap.Collection["resourceslices"] = model.CollectionStatus{State: model.StateForbidden, Reason: "forbidden: resource slices"}
	snap.ResourceSlices = nil
	got := claimsOut(t, snap)
	for _, want := range []string{
		"    mem-0  mem.example.com",
		"    capacity size: 40Gi consumed (not published)",
		"    capacity size: 60Gi consumed (not published)",
	} {
		if !hasLine(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestClaimsShowsConsumedCapacityWhenDeviceIsMissing(t *testing.T) {
	snap := fx(t, "capacity/pass")
	snap.ResourceSlices[0].Spec.Devices = nil
	got := claimsOut(t, snap)
	for _, want := range []string{
		"    ! mem.example.com/mem-0 not found in ResourceSlices (pool node-a.example.com)",
		"    capacity size: 40Gi consumed (not published)",
		"    capacity size: 60Gi consumed (not published)",
	} {
		if !hasLine(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
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
	if want := "    NUMA 0,1,2,3  pcieRoot pci0000:00"; !hasLine(got, want) {
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
		"? pods unavailable: forbidden: cannot list pods",
		"? vmi unavailable: kubevirt.io/v1 not served",
	} {
		if !hasLine(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	// The reason is said once for the whole run, not once per claim.
	for _, once := range []string{"? pods unavailable", "? vmi unavailable"} {
		if n := strings.Count(got, once); n != 1 {
			t.Errorf("%q printed %d times, want 1:\n%s", once, n, got)
		}
	}
}
