package view_test

import (
	"strings"
	"testing"

	resourcev1 "k8s.io/api/resource/v1"

	"github.com/johnahull/k8s-dra-inspect/internal/inspect"
	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/view"
)

func topoOut(t *testing.T, snap model.Snapshot, compact bool) string {
	return show(t, snap, inspect.Topology(compact), view.Topology)
}

func joinLines(l ...string) string { return strings.Join(l, "\n") + "\n" }

func TestTopologyExactTree(t *testing.T) {
	got := topoOut(t, fx(t, "classes-claims"), false)
	want := joinLines(
		"═══ DRA Topology ═══",
		"",
		"Node: node-a.example.com",
		"├─ NUMA 0",
		"│  └─ pcieRoot pci0000:00",
		"│     └─ gpu  gpu-0",
		"└─ NUMA 1",
		"   └─ pcieRoot pci0000:80",
		"      └─ gpu  gpu-1",
	)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestTopologyCompactCountsOnly(t *testing.T) {
	got := topoOut(t, fx(t, "xe9785l"), true)
	for _, want := range []string{
		"├─ NUMA 0,1,2,3",
		"│  ├─ pcieRoot pci0000:00  gpu=1",
		"│  ├─ pcieRoot pci0000:32,pci0000:66  cpu=1",
		"│  └─ memory=4",
	} {
		if !hasLine(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "gpu-vfio") || strings.Contains(got, "cpudevnuma") {
		t.Errorf("compact must not list devices:\n%s", got)
	}
}

func TestTopologyRootlessDevicesAreDirectChildrenOfNUMA(t *testing.T) {
	snap := oneSliceSnap(
		devWith("nic", []int64{0}, "pci0000:88"),
		devWith("cpu", []int64{0}),
		devWith("memory", []int64{0}),
	)
	got := topoOut(t, snap, false)
	for _, want := range []string{
		"└─ NUMA 0",
		"   ├─ pcieRoot pci0000:88",
		"   │  └─ d.example.com  nic",
		"   ├─ d.example.com  cpu",
		"   └─ d.example.com  memory",
	} {
		if !hasLine(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "pcieRoot unknown") {
		t.Errorf("rootless devices must not be shown under an unknown PCIe root:\n%s", got)
	}
}

func TestTopologyCompactRootlessCountsAreDirectChildrenOfNUMA(t *testing.T) {
	snap := oneSliceSnap(
		devWith("nic", []int64{0}, "pci0000:88"),
		devWith("cpu", []int64{0}),
		devWith("memory", []int64{0}),
	)
	got := topoOut(t, snap, true)
	if !hasLine(got, "   └─ d.example.com=2") || strings.Contains(got, "pcieRoot unknown") {
		t.Errorf("compact rootless counts must be direct children:\n%s", got)
	}
}

func TestTopologyMixedPoolReportsMissingRootAfterFlattening(t *testing.T) {
	snap := oneSliceSnap(
		devWith("nic", []int64{0}, "pci0000:88"),
		devWith("cpu", []int64{0}),
	)
	got := topoOut(t, snap, false)
	if !strings.Contains(got, "! NUMA 0, PCIe root not published: missing attribute: pcieRoot") {
		t.Errorf("missing root diagnostic lost when the rootless branch is flattened:\n%s", got)
	}
}

func TestTopologyEveryDeviceExactlyOnceAndSetsExact(t *testing.T) {
	snap := fx(t, "xe9785l")
	got := topoOut(t, snap, false)
	cur, _ := model.CurrentSlices(snap)
	n := 0
	for _, s := range cur {
		for _, d := range s.Spec.Devices {
			n++
			if c := strings.Count(got, " "+d.Name+"\n") + strings.Count(got, " "+d.Name+" ("); c != 1 {
				t.Errorf("device %s appears %d times", d.Name, c)
			}
		}
	}
	if n != 32 {
		t.Fatalf("fixture has %d devices, want 32", n)
	}
	for _, want := range []string{
		"├─ NUMA 0,1,2,3",
		"└─ NUMA 4,5,6,7",
		"│  ├─ pcieRoot pci0000:32,pci0000:66",
		"│  ├─ memory  memory-bjd9hw",
		"│  ├─ memory  memory-fk8nlr",
		"   │  └─ gpu  gpu-vfio-0 (0001:dc:02.0)",
	} {
		if !hasLine(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "NUMA unknown") {
		t.Errorf("every xe9785l device has a NUMA set:\n%s", got)
	}
}

func TestTopologyUnknownBranchesComeLast(t *testing.T) {
	got := topoOut(t, fx(t, "missing-attr"), false)
	want := joinLines(
		"═══ DRA Topology ═══",
		"",
		"Node: node-a.example.com",
		"├─ NUMA 0",
		"│  └─ gpu  gpu-0",
		"└─ NUMA unknown",
		"   └─ gpu  gpu-1 [example-gpu]",
	)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestTopologyNumbersSortNumericallyInsideASet(t *testing.T) {
	snap := fx(t, "classes-claims")
	for i := range snap.ResourceSlices {
		snap.ResourceSlices[i].Spec.Devices[0].Attributes["resource.kubernetes.io/numaNode"] = resourcev1.DeviceAttribute{IntValues: []int64{10, 2, 1}}
	}
	got := topoOut(t, snap, false)
	if !hasLine(got, "└─ NUMA 1,2,10") {
		t.Errorf("want numeric set order 1,2,10:\n%s", got)
	}
}

func TestTopologyDriverNameOnlyWhenAmbiguous(t *testing.T) {
	snap := fx(t, "classes-claims")
	other := *snap.ResourceSlices[0].DeepCopy()
	other.Name, other.Spec.Driver = "other-slice", "dra.cpu"
	other.Spec.Devices = other.Spec.Devices[:1] // gpu-0 again, now under another driver
	snap.ResourceSlices = append(snap.ResourceSlices, other)
	got := topoOut(t, snap, false)
	for _, want := range []string{"├─ cpu  dra.cpu/gpu-0", "└─ gpu  gpu.example.com/gpu-0"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if !strings.Contains(got, " gpu-1\n") {
		t.Errorf("unambiguous device must stay bare:\n%s", got)
	}
}

func TestTopologyLabelFallsBackToDriverName(t *testing.T) {
	snap := fx(t, "classes-claims")
	for i := range snap.ResourceSlices {
		snap.ResourceSlices[i].Spec.Driver = "weird.example.org"
	}
	if got := topoOut(t, snap, false); !strings.Contains(got, "└─ weird.example.org  gpu-0") {
		t.Errorf("fallback label:\n%s", got)
	}
}

func TestTopologyNotesIncompletePool(t *testing.T) {
	snap := fx(t, "classes-claims")
	snap.ResourceSlices[0].Spec.Pool.ResourceSliceCount = 2
	got := topoOut(t, snap, false)
	if !strings.Contains(got, "! NUMA 0, pcieRoot pci0000:00: pool has fewer ResourceSlices than expected for its newest generation: node-a.example.com") {
		t.Errorf("incomplete note missing:\n%s", got)
	}
}
