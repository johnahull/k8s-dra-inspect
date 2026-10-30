package view_test

import (
	"regexp"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/johnahull/k8s-dra-inspect/internal/inspect"
	"github.com/johnahull/k8s-dra-inspect/internal/view"
)

// Node order is plain string order (as in slices.go), real nodes before the
// (all nodes)/(selector) headings; drivers and devices sort by name.

func TestAttributesLayoutGroupsByNode(t *testing.T) {
	got := show(t, fx(t, "multinode"), inspect.Attributes(false, false), view.Attributes)
	want := joinLines(
		"═══ DRA Device Attributes ═══",
		"",
		"Node: node-a.example.com",
		"  gpu.example.com (2 devices)",
		"    a-gpu-0",
		"      resource.kubernetes.io/numaNode = [0, 1, 2, 3]",
		"      resource.kubernetes.io/pcieRoot = pci0000:00",
		"    a-gpu-1",
		"      resource.kubernetes.io/numaNode = [0, 1, 2, 3]",
		"      resource.kubernetes.io/pcieRoot = pci0000:00",
		"",
		"Node: node-b.example.com",
		"  gpu.example.com (2 devices)",
		"    b-gpu-0",
		"      resource.kubernetes.io/numaNode = 2",
		"      resource.kubernetes.io/pcieRoot = pci0000:40",
		"    b-gpu-1",
		"      resource.kubernetes.io/numaNode = [2, 3]",
		"      resource.kubernetes.io/pcieRoot = pci0000:c0",
	)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestAttributesSingleNodeHasOneNodeBlock(t *testing.T) {
	got := show(t, fx(t, "xe9785l"), inspect.Attributes(false, false), view.Attributes)
	if n := strings.Count(got, "Node: "); n != 1 {
		t.Errorf("want one Node: block, got %d", n)
	}
	if strings.Contains(got, "(pool ") {
		t.Errorf("pool equals node: no pool in device headings:\n%.600s", got)
	}
}

func TestAttributesHeadingShowsPoolOnlyWhenItDiffersFromNode(t *testing.T) {
	snap := fx(t, "multinode")
	snap.ResourceSlices[1].Spec.Pool.Name = "pool-b"
	got := show(t, snap, inspect.Attributes(false, false), view.Attributes)
	for _, want := range []string{"    a-gpu-0", "    b-gpu-0  (pool pool-b)", "    b-gpu-1  (pool pool-b)"} {
		if !hasLine(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if contains(got, "a-gpu-0  (pool") || contains(got, "node node-") {
		t.Errorf("pool and node are not repeated when equal:\n%s", got)
	}
}

func TestAttributesPlaceholderNodesComeAfterRealNodes(t *testing.T) {
	snap := fx(t, "multinode")
	all := true
	s0 := &snap.ResourceSlices[0].Spec
	s0.PerDeviceNodeSelection = &all
	s0.NodeName = nil
	s0.Devices[0].AllNodes = &all
	s0.Devices[1].NodeName = strp("node-a.example.com")
	s1 := &snap.ResourceSlices[1].Spec
	s1.NodeName, s1.AllNodes = nil, &all
	got := show(t, snap, inspect.Attributes(false, false), view.Attributes)
	want := joinLines(
		"═══ DRA Device Attributes ═══",
		"",
		"Node: node-a.example.com",
		"  gpu.example.com (1 device)",
		"    a-gpu-1",
		"      resource.kubernetes.io/numaNode = [0, 1, 2, 3]",
		"      resource.kubernetes.io/pcieRoot = pci0000:00",
		"",
		"Node: (all nodes)",
		"  gpu.example.com (3 devices)",
		"    a-gpu-0  (pool node-a.example.com)",
		"      resource.kubernetes.io/numaNode = [0, 1, 2, 3]",
		"      resource.kubernetes.io/pcieRoot = pci0000:00",
		"    b-gpu-0  (pool node-b.example.com)",
		"      resource.kubernetes.io/numaNode = 2",
		"      resource.kubernetes.io/pcieRoot = pci0000:40",
		"    b-gpu-1  (pool node-b.example.com)",
		"      resource.kubernetes.io/numaNode = [2, 3]",
		"      resource.kubernetes.io/pcieRoot = pci0000:c0",
	)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestAttributesSelectorHeading(t *testing.T) {
	snap := fx(t, "multinode")
	snap.ResourceSlices[0].Spec.NodeName = nil
	snap.ResourceSlices[0].Spec.NodeSelector = &corev1.NodeSelector{}
	got := show(t, snap, inspect.Attributes(false, false), view.Attributes)
	var heads []string
	for _, l := range strings.Split(got, "\n") {
		if strings.HasPrefix(l, "Node: ") {
			heads = append(heads, l)
		}
	}
	if strings.Join(heads, "|") != "Node: node-b.example.com|Node: (selector)" {
		t.Errorf("headings %q in:\n%s", heads, got)
	}
}

func TestAttributesOrderIsPlainStringOrder(t *testing.T) {
	snap := fx(t, "multinode")
	snap.ResourceSlices[0].Spec.NodeName = strp("node-10")
	snap.ResourceSlices[1].Spec.NodeName = strp("node-2")
	snap.ResourceSlices[0].Spec.Driver = "z.example.com"
	snap.ResourceSlices[1].Spec.Driver = "z.example.com"
	// a second driver on node-10 sorts before z.example.com; devices sort by name.
	snap.ResourceSlices[0].Spec.Devices[0].Name = "b-dev"
	snap.ResourceSlices[0].Spec.Devices[1].Name = "a-dev"
	extra := snap.ResourceSlices[0].DeepCopy()
	extra.Name, extra.Spec.Driver = "extra", "a.example.com"
	extra.Spec.Devices = extra.Spec.Devices[:1]
	snap.ResourceSlices = append(snap.ResourceSlices, *extra)
	got := show(t, snap, inspect.Attributes(false, false), view.Attributes)
	var order []string
	for _, l := range strings.Split(got, "\n") {
		if t := strings.TrimSpace(l); l != "" && !strings.Contains(l, "=") && !strings.HasPrefix(l, "═") {
			order = append(order, t)
		}
	}
	// The pools keep their old names, so each heading names its pool.
	want := "Node: node-10|a.example.com (1 device)|b-dev  (pool node-a.example.com)|z.example.com (2 devices)|a-dev  (pool node-a.example.com)|b-dev  (pool node-a.example.com)|" +
		"Node: node-2|z.example.com (2 devices)|b-gpu-0  (pool node-b.example.com)|b-gpu-1  (pool node-b.example.com)"
	if strings.Join(order, "|") != want {
		t.Errorf("order\n got %s\nwant %s\n%s", strings.Join(order, "|"), want, got)
	}
}

func TestAttributesListValuesAndCapacityPerNode(t *testing.T) {
	got := show(t, fx(t, "unicode-long"), inspect.Attributes(true, true), view.Attributes)
	// Keys are padded to align the "="; squash the padding to compare content.
	squashed := regexp.MustCompile(` {2,}= `).ReplaceAllString(got, " = ")
	for _, want := range []string{
		"      resource.kubernetes.io/pcieRoot = [pci0000:00, pci0000:80]",
		"      resource.kubernetes.io/numaNode = 1",
		"      capacity memory = 64Gi",
	} {
		if !hasLine(squashed, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if !contains(got, "/channels") || !contains(got, "= [2, 4]") {
		t.Errorf("--all-attributes list value missing:\n%s", got)
	}
	if !hasLine(got, "Node: node-a.example.com") || !hasLine(got, "Node: node-b.example.com") {
		t.Errorf("both nodes expected:\n%s", got)
	}
	if off := show(t, fx(t, "unicode-long"), inspect.Attributes(false, false), view.Attributes); contains(off, "capacity") || contains(off, "channels") {
		t.Errorf("flags off must hide capacity and non-topology attributes:\n%s", off)
	}
}

func TestAttributesIncompletePoolIsFlaggedUnderItsDevices(t *testing.T) {
	snap := fx(t, "classes-claims")
	snap.ResourceSlices[0].Spec.Pool.ResourceSliceCount = 2
	got := show(t, snap, inspect.Attributes(false, false), view.Attributes)
	want := "      ! pool has fewer ResourceSlices than expected for its newest generation: node-a.example.com"
	if n := strings.Count(got, want+"\n"); n != 2 {
		t.Errorf("want the note under each of 2 devices, got %d in:\n%s", n, got)
	}
}

func TestAttributesLongNamesStayWhole(t *testing.T) {
	got := show(t, fx(t, "unicode-long"), inspect.Attributes(false, false), view.Attributes)
	long := "  gpu.accelerators.extremely-long-driver-domain-name-for-layout-testing.example.com (2 devices)"
	if !hasLine(got, long) {
		t.Errorf("long driver heading cut:\n%s", got)
	}
}

func TestAttributesEmpty(t *testing.T) {
	snap := fx(t, "multinode")
	snap.ResourceSlices = nil
	got := show(t, snap, inspect.Attributes(false, false), view.Attributes)
	if want := joinLines("═══ DRA Device Attributes ═══", "", "no devices"); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// Devices sort by name within a driver, whichever pool they are in.
func TestAttributesDevicesSortByNameAcrossPools(t *testing.T) {
	snap := fx(t, "multinode")
	snap.ResourceSlices[1].Spec.NodeName = strp("node-a.example.com") // pool node-b now also on node-a
	got := show(t, snap, inspect.Attributes(false, false), view.Attributes)
	var devs []string
	for _, l := range strings.Split(got, "\n") {
		if strings.HasPrefix(l, "    ") && !strings.HasPrefix(l, "      ") {
			devs = append(devs, strings.Fields(l)[0])
		}
	}
	if strings.Join(devs, " ") != "a-gpu-0 a-gpu-1 b-gpu-0 b-gpu-1" {
		t.Errorf("device order %v\n%s", devs, got)
	}
}
