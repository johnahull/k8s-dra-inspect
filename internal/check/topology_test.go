package check

import (
	"fmt"
	"testing"

	resourcev1 "k8s.io/api/resource/v1"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
)

func full(name string) resourcev1.Device {
	return dev(name, attrs{numa: ints(0), pcie: str("pci0000:00")})
}

func TestTopologyCleanPoolPassesWithCounts(t *testing.T) {
	r := one(t, Topology(), snapOf([]resourcev1.ResourceSlice{slice("s", "p", 3, 1, full("a"), full("b"))}))
	if r.Status != model.StatusPass || r.Subject != "gpu.example.com/p" {
		t.Fatalf("%+v", r)
	}
	if r.Details["devices"] != 2 || r.Details["generation"] != int64(3) || fmt.Sprint(r.Details["missingNUMA"]) != "[]" {
		t.Errorf("details = %v", r.Details)
	}
}

func TestTopologyMissingAttributesWarnNamingDevices(t *testing.T) {
	r := one(t, Topology(), snapOf([]resourcev1.ResourceSlice{slice("s", "p", 1, 1, full("a"), dev("b", attrs{pcie: str("x")}), dev("c", attrs{numa: ints(1)}))}))
	wantStatus(t, r, model.StatusWarn, "NUMA node missing on b")
	if fmt.Sprint(r.Details["missingNUMA"]) != "[b]" || fmt.Sprint(r.Details["missingPCIeRoot"]) != "[c]" {
		t.Errorf("details = %v", r.Details)
	}
	wantStatus(t, r, model.StatusWarn, "PCIe root missing on c")
}

func TestTopologyRootlessPoolDoesNotRequirePCIeRoot(t *testing.T) {
	r := one(t, Topology(), snapOf([]resourcev1.ResourceSlice{
		slice("s", "p", 1, 1,
			dev("cpu", attrs{numa: ints(0)}),
			dev("memory", attrs{numa: ints(0)}),
		),
	}))
	if r.Status != model.StatusPass || fmt.Sprint(r.Details["missingPCIeRoot"]) != "[]" {
		t.Fatalf("rootless pool should not require PCIe roots: %+v", r)
	}
}

func TestTopologyMixedPoolStillRequiresPCIeRoot(t *testing.T) {
	r := one(t, Topology(), snapOf([]resourcev1.ResourceSlice{
		slice("s", "p", 1, 1,
			full("nic"),
			dev("cpu", attrs{numa: ints(0)}),
		),
	}))
	wantStatus(t, r, model.StatusWarn, "PCIe root missing on cpu")
}

func TestTopologyConflictWarns(t *testing.T) {
	d := dev("a", attrs{"x.example.com/numaNode": ints(0), "y.example.com/numaNode": ints(1), pcie: str("r")})
	r := one(t, Topology(), snapOf([]resourcev1.ResourceSlice{slice("s", "p", 1, 1, d)}))
	wantStatus(t, r, model.StatusWarn, "conflicting NUMA attributes on a")
}

func TestTopologyInvalidAttributeWarns(t *testing.T) {
	d := resourcev1.Device{Name: "a", Attributes: map[resourcev1.QualifiedName]resourcev1.DeviceAttribute{numa: {}}}
	r := one(t, Topology(), snapOf([]resourcev1.ResourceSlice{slice("s", "p", 1, 1, d)}))
	wantStatus(t, r, model.StatusWarn, "has no value")
}

func TestTopologyIncompletePoolWarns(t *testing.T) {
	r := one(t, Topology(), snapOf([]resourcev1.ResourceSlice{slice("s", "p", 1, 2, full("a"))}))
	wantStatus(t, r, model.StatusWarn, "fewer ResourceSlices")
	if r.Details["slicesObserved"] != 1 || r.Details["slicesExpected"] != int64(2) {
		t.Errorf("details = %v", r.Details)
	}
}

func TestTopologyOneResultPerPoolCurrentGenerationOnly(t *testing.T) {
	sl := []resourcev1.ResourceSlice{
		slice("old", "p", 1, 1, dev("stale-bad", nil)), slice("new", "p", 2, 1, full("a")),
		slicePool("q", "nic.example.com", "q", 1, 1, full("n")),
	}
	rs := run(Topology(), snapOf(sl))
	if len(rs) != 2 || rs[0].Subject != "gpu.example.com/p" || rs[1].Subject != "nic.example.com/q" {
		t.Fatalf("%+v", rs)
	}
	if rs[0].Status != model.StatusPass {
		t.Errorf("stale generation devices must be ignored: %+v", rs[0])
	}
}

func TestTopologyNoSlicesIsOnePass(t *testing.T) {
	wantStatus(t, one(t, Topology(), snapOf(nil)), model.StatusPass, "no ResourceSlices")
}
