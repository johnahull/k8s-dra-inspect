package check

import (
	"strings"
	"testing"

	resourcev1 "k8s.io/api/resource/v1"

	"github.com/johnahull/k8s-dra-inspect/internal/adapters/host"
	"github.com/johnahull/k8s-dra-inspect/internal/model"
)

const (
	bdf0 = "0000:3b:00.0"
	bdf1 = "0000:3b:00.1"
)

// vfioHost: gpu-0 (bdf0) bound to vfio-pci alone in group 7 with /dev/vfio/7.
func vfioHost() host.Evidence {
	ev := hostEv()
	ev.PCI[bdf0] = host.PCIDevice{Driver: "vfio-pci", IOMMUGroup: "7", Vendor: "0x1", Device: "0x2"}
	ev.IOMMUGroups["7"] = []string{bdf0}
	ev.VFIODevNodes = []string{"7", "vfio"}
	return ev
}

func vfioSnap(ev host.Evidence, results ...resourcev1.DeviceRequestAllocationResult) model.Snapshot {
	if len(results) == 0 {
		results = []resourcev1.DeviceRequestAllocationResult{res("r", "gpu-0")}
	}
	s := snapOf([]resourcev1.ResourceSlice{slice("s", "p", 1, 1,
		dev("gpu-0", attrs{pciKey: str(bdf0)}),
		dev("gpu-1", attrs{pciKey: str(bdf1)}),
		dev("bare", attrs{"pciBusID": str(bdf0)}),
		dev("foreign", attrs{"other.example.com/pciBusID": str(bdf0)}),
		dev("nopci", attrs{numa: ints(0)}))},
		claim("demo", "c1", nil, results...))
	return withHost(s, ev)
}

func TestVFIOMatrix(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mod       func(*host.Evidence)
		want      model.Status
		reasonHas string
		cdi       bool
	}{
		{"pass", func(*host.Evidence) {}, model.StatusPass, "", false},
		{"pass with cdi by device name", func(e *host.Evidence) {
			e.CDI = []host.CDISpec{{File: "a.json", Kind: "k/g", Devices: []string{"gpu-0"}}}
		}, model.StatusPass, "", true},
		{"pass with cdi by bdf", func(e *host.Evidence) { e.CDI = []host.CDISpec{{File: "a.json", Kind: "k/g", Devices: []string{bdf0}}} }, model.StatusPass, "", true},
		{"wrong driver", func(e *host.Evidence) {
			e.PCI[bdf0] = host.PCIDevice{Driver: "nvidia", IOMMUGroup: "7"}
		}, model.StatusFail, "bound to driver nvidia, want vfio-pci", false},
		{"unbound", func(e *host.Evidence) {
			e.PCI[bdf0] = host.PCIDevice{IOMMUGroup: "7"}
		}, model.StatusFail, "no driver bound, want vfio-pci", false},
		{"non-vfio peer", func(e *host.Evidence) {
			e.IOMMUGroups["7"] = []string{bdf0, bdf1}
			e.PCI[bdf1] = host.PCIDevice{Driver: "snd_hda_intel", IOMMUGroup: "7"}
		}, model.StatusWarn, "IOMMU group 7 also holds " + bdf1 + " (driver snd_hda_intel)", false},
		{"pci bridge peer is ignored", func(e *host.Evidence) {
			e.IOMMUGroups["7"] = []string{bdf0, bdf1}
			e.PCI[bdf1] = host.PCIDevice{Driver: "pcieport", IOMMUGroup: "7", Class: "060400"}
		}, model.StatusPass, "", false},
		{"host bridge peer without driver is ignored", func(e *host.Evidence) {
			e.IOMMUGroups["7"] = []string{bdf0, bdf1}
			e.PCI[bdf1] = host.PCIDevice{IOMMUGroup: "7", Class: "060000"}
		}, model.StatusPass, "", false},
		{"gpu audio function on a non-vfio driver warns", func(e *host.Evidence) {
			e.IOMMUGroups["7"] = []string{bdf0, bdf1}
			e.PCI[bdf1] = host.PCIDevice{Driver: "snd_hda_intel", IOMMUGroup: "7", Class: "040300"}
		}, model.StatusWarn, bdf1 + " (driver snd_hda_intel, GPU audio function sharing the group)", false},
		{"unbound gpu audio function is ignored", func(e *host.Evidence) {
			e.IOMMUGroups["7"] = []string{bdf0, bdf1}
			e.PCI[bdf1] = host.PCIDevice{IOMMUGroup: "7", Class: "040300"}
		}, model.StatusPass, "", false},
		{"gpu audio function on vfio-pci is fine", func(e *host.Evidence) {
			e.IOMMUGroups["7"] = []string{bdf0, bdf1}
			e.PCI[bdf1] = host.PCIDevice{Driver: "vfio-pci", IOMMUGroup: "7", Class: "040300"}
		}, model.StatusPass, "", false},
		{"non-vfio NIC peer follows the existing rules", func(e *host.Evidence) {
			e.IOMMUGroups["7"] = []string{bdf0, bdf1}
			e.PCI[bdf1] = host.PCIDevice{Driver: "ixgbe", IOMMUGroup: "7", Class: "020000"}
		}, model.StatusWarn, "also holds " + bdf1 + " (driver ixgbe)", false},
		{"version 1 evidence has no classes", func(e *host.Evidence) {
			e.Version = 1
			e.IOMMUGroups["7"] = []string{bdf0, bdf1}
			e.PCI[bdf1] = host.PCIDevice{Driver: "pcieport", IOMMUGroup: "7"}
		}, model.StatusWarn, bdf1 + " (driver pcieport)", false},
		{"unbound unknown peer", func(e *host.Evidence) { e.IOMMUGroups["7"] = []string{bdf0, bdf1} }, model.StatusWarn, bdf1 + " (no driver)", false},
		{"vfio peer is fine", func(e *host.Evidence) {
			e.IOMMUGroups["7"] = []string{bdf0, bdf1}
			e.PCI[bdf1] = host.PCIDevice{Driver: "vfio-pci", IOMMUGroup: "7"}
		}, model.StatusPass, "", false},
		{"missing group node", func(e *host.Evidence) { e.VFIODevNodes = []string{"vfio"} }, model.StatusWarn, "/dev/vfio/7 is missing", false},
		{"no iommu group", func(e *host.Evidence) { e.PCI[bdf0] = host.PCIDevice{Driver: "vfio-pci"} }, model.StatusWarn, "no IOMMU group", false},
		{"bdf not in evidence", func(e *host.Evidence) { delete(e.PCI, bdf0) }, model.StatusUnavailable, bdf0 + " not found in host evidence", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ev := vfioHost()
			tc.mod(&ev)
			r := one(t, VFIO(), vfioSnap(ev))
			wantStatus(t, r, tc.want, tc.reasonHas)
			if r.Subject != "gpu.example.com/p/gpu-0" || r.Details["bdf"] != bdf0 || r.Details["cdiPresent"] != tc.cdi {
				t.Errorf("%+v", r)
			}
		})
	}
}

func TestVFIOPCIBusIDKeyLookup(t *testing.T) {
	for dev, want := range map[string]model.Status{"bare": model.StatusPass, "foreign": model.StatusPass} {
		rs := run(VFIO(), vfioSnap(vfioHost(), res("r", dev)))
		got := rs[0]
		if dev == "bare" && (len(rs) != 1 || got.Status != want || got.Details["bdf"] != bdf0) {
			t.Errorf("bare driver-domain key not used: %+v", rs)
		}
		if dev == "foreign" && !strings.Contains(got.Details["reason"].(string), "no allocated device with a pciBusID") {
			t.Errorf("foreign-domain key must not match: %+v", rs)
		}
	}
}

func TestVFIONoCandidates(t *testing.T) {
	for _, s := range []model.Snapshot{
		vfioSnap(vfioHost(), res("r", "nopci")),
		withHost(snapOf(nil), vfioHost()),
	} {
		r := one(t, VFIO(), s)
		if r.Status != model.StatusPass || !strings.Contains(r.Details["reason"].(string), "no allocated device with a pciBusID on node node-a") {
			t.Errorf("%+v", r)
		}
	}
}

func TestVFIOSortedPerDeviceResults(t *testing.T) {
	ev := vfioHost()
	ev.PCI[bdf1] = host.PCIDevice{Driver: "nvidia"}
	rs := run(VFIO(), vfioSnap(ev, res("r", "gpu-1"), res("r", "gpu-0")))
	if len(rs) != 2 || rs[0].Subject != "gpu.example.com/p/gpu-0" || rs[0].Status != model.StatusPass || rs[1].Status != model.StatusFail {
		t.Fatalf("%+v", rs)
	}
}

func TestVFIOUnavailable(t *testing.T) {
	noEv := vfioSnap(vfioHost())
	noEv.Evidence = nil
	other := vfioHost()
	other.NodeName = "node-x"
	noNode := vfioSnap(vfioHost())
	noNode.ResourceSlices[0].Spec.NodeName = nil
	forbidden := vfioSnap(vfioHost())
	forbidden.Collection["resourceclaims"] = model.CollectionStatus{State: model.StateForbidden, Reason: "forbidden: cannot list resourceclaims"}
	for _, tc := range []struct {
		name string
		snap model.Snapshot
		want string
	}{
		{"no evidence", noEv, "no host evidence in snapshot; run on the node"},
		{"other node", vfioSnap(other), "1 device on node node-a: run on node node-a"},
		{"slice without node", noNode, "not tied to a node"},
		{"claims forbidden", forbidden, "forbidden: cannot list resourceclaims"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wantStatus(t, one(t, VFIO(), tc.snap), model.StatusUnavailable, tc.want)
		})
	}
}

func TestVFIOCollapsesOffNodeDevicesPerNode(t *testing.T) {
	s := vfioSnap(vfioHost(), res("r", "gpu-0"), res("r", "gpu-1"))
	remote := func(name, node, bdf string) {
		sl := slicePool("s-"+name, drv, "pool-"+node, 1, 1, dev(name, attrs{pciKey: str(bdf)}))
		sl.Spec.NodeName = &node
		s.ResourceSlices = append(s.ResourceSlices, sl)
		s.ResourceClaims[0].Status.Allocation.Devices.Results = append(s.ResourceClaims[0].Status.Allocation.Devices.Results,
			resourcev1.DeviceRequestAllocationResult{Request: "r", Driver: drv, Pool: "pool-" + node, Device: name})
	}
	remote("r1", "node-b", "0000:af:00.0")
	remote("r2", "node-b", "0000:af:00.1")
	remote("r3", "node-b", "0000:af:00.2")
	remote("r4", "node-c", "0000:bf:00.0")
	rs := run(VFIO(), s)
	var subjects []string
	by := map[string]model.Result{}
	for _, r := range rs {
		subjects = append(subjects, r.Subject)
		by[r.Subject] = r
	}
	if len(rs) != 4 || by["gpu.example.com/p/gpu-0"].Status != model.StatusPass || by["gpu.example.com/p/gpu-1"].Subject == "" {
		t.Fatalf("want 2 same-node results + 1 per other node, got %v: %+v", subjects, rs)
	}
	for node, want := range map[string]string{"node-b": "3 devices on node node-b: run on node node-b", "node-c": "1 device on node node-c: run on node node-c"} {
		wantStatus(t, by["node/"+node], model.StatusUnavailable, want)
	}
}

func TestVFIOMixedNodes(t *testing.T) {
	s := vfioSnap(vfioHost(), res("r", "gpu-0"), res("r", "gpu-1"))
	s.ResourceSlices = append(s.ResourceSlices, slicePool("t", drv, "q", 1, 1, dev("remote", attrs{pciKey: str("0000:af:00.0")})))
	s.ResourceSlices[1].Spec.NodeName = &[]string{"node-b"}[0]
	s.ResourceClaims[0].Status.Allocation.Devices.Results = append(s.ResourceClaims[0].Status.Allocation.Devices.Results,
		resourcev1.DeviceRequestAllocationResult{Request: "r", Driver: drv, Pool: "q", Device: "remote"})
	by := map[string]model.Result{}
	for _, r := range run(VFIO(), s) {
		by[r.Subject] = r
	}
	if len(by) != 3 || by["gpu.example.com/p/gpu-0"].Status != model.StatusPass {
		t.Fatalf("%+v", by)
	}
	wantStatus(t, by["node/node-b"], model.StatusUnavailable, "1 device on node node-b: run on node node-b")
}
