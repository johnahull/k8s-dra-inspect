package view_test

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/johnahull/k8s-dra-inspect/internal/inspect"
	"github.com/johnahull/k8s-dra-inspect/internal/view"
)

// Node order is plain string order (as in slices.go), real nodes before the
// (all nodes)/(selector) headings.

func TestDriversLayoutGroupsByNode(t *testing.T) {
	got := show(t, fx(t, "multinode"), inspect.Drivers(false), view.Drivers)
	want := joinLines(
		"═══ DRA Drivers ═══",
		"",
		"Node: node-a.example.com",
		"  gpu.example.com: 2 devices  (1 pool)",
		"",
		"Node: node-b.example.com",
		"  gpu.example.com: 2 devices  (1 pool)",
	)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestDriversSingleNodeHasOneNodeBlock(t *testing.T) {
	got := show(t, fx(t, "xe9785l"), inspect.Drivers(false), view.Drivers)
	if n := strings.Count(got, "Node: "); n != 1 {
		t.Errorf("want one Node: block, got %d:\n%s", n, got)
	}
	if !hasLine(got, "  dra.cpu: 8 devices  (1 pool)") {
		t.Errorf("driver line missing or not indented under the node:\n%s", got)
	}
}

func TestDriversAttributesPerNode(t *testing.T) {
	snap := fx(t, "multinode")
	setAttr(&snap, "b-gpu-1", "extra.example.com/only-b", "x")
	got := show(t, snap, inspect.Drivers(true), view.Drivers)
	want := joinLines(
		"═══ DRA Drivers ═══",
		"",
		"Node: node-a.example.com",
		"  gpu.example.com: 2 devices  (1 pool)",
		"    attributes:",
		"      resource.kubernetes.io/numaNode",
		"      resource.kubernetes.io/pcieRoot",
		"",
		"Node: node-b.example.com",
		"  gpu.example.com: 2 devices  (1 pool)",
		"    attributes:",
		"      extra.example.com/only-b",
		"      resource.kubernetes.io/numaNode",
		"      resource.kubernetes.io/pcieRoot",
	)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestDriversAttributeBlocksAreSeparated(t *testing.T) {
	got := show(t, fx(t, "xe9680"), inspect.Drivers(true), view.Drivers)
	if !contains(got, "    dra.net/numaNode\n\n  dra.memory: 8 devices") {
		t.Errorf("blank line between attribute blocks missing:\n%s", got)
	}
	if plain := show(t, fx(t, "xe9680"), inspect.Drivers(false), view.Drivers); contains(plain, "\n\n  dra.memory") {
		t.Errorf("no blank lines between one-line driver entries:\n%s", plain)
	}
}

func TestDriversNodeAndDriverOrderIsPlainStringOrder(t *testing.T) {
	snap := fx(t, "multinode")
	snap.ResourceSlices[0].Spec.NodeName = strp("node-10")
	snap.ResourceSlices[1].Spec.NodeName = strp("node-2")
	snap.ResourceSlices[1].Spec.Driver = "a.example.com"
	got := show(t, snap, inspect.Drivers(false), view.Drivers)
	want := joinLines(
		"═══ DRA Drivers ═══",
		"",
		"Node: node-10",
		"  gpu.example.com: 2 devices  (1 pool)",
		"",
		"Node: node-2",
		"  a.example.com: 2 devices  (1 pool)",
	)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestDriversPlaceholderNodesComeAfterRealNodes(t *testing.T) {
	snap := fx(t, "multinode")
	all := true
	// a-gpu-0 is selected per device by a selector-less allNodes; b slice has all devices on node-b;
	// the node-a slice becomes slice-level allNodes for its remaining device.
	s0 := &snap.ResourceSlices[0].Spec
	s0.PerDeviceNodeSelection = &all
	s0.Devices[0].AllNodes = &all
	s0.Devices[1].NodeName = strp("node-a.example.com")
	s0.NodeName = nil
	s1 := &snap.ResourceSlices[1].Spec
	s1.NodeName, s1.AllNodes = nil, &all
	snap.ResourceSlices[1].Spec.Pool.Name = "shared"
	got := show(t, snap, inspect.Drivers(false), view.Drivers)
	want := joinLines(
		"═══ DRA Drivers ═══",
		"",
		"Node: node-a.example.com",
		"  gpu.example.com: 1 device  (1 pool)",
		"",
		"Node: (all nodes)",
		"  gpu.example.com: 3 devices  (2 pools)",
	)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestDriversSelectorHeading(t *testing.T) {
	snap := fx(t, "multinode")
	snap.ResourceSlices[0].Spec.NodeName = nil
	snap.ResourceSlices[0].Spec.NodeSelector = &corev1.NodeSelector{}
	got := show(t, snap, inspect.Drivers(false), view.Drivers)
	want := joinLines(
		"═══ DRA Drivers ═══",
		"",
		"Node: node-b.example.com",
		"  gpu.example.com: 2 devices  (1 pool)",
		"",
		"Node: (selector)",
		"  gpu.example.com: 2 devices  (1 pool)",
	)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestDriversIncompletePoolNoteStaysUnderItsNode(t *testing.T) {
	snap := fx(t, "multinode")
	snap.ResourceSlices[1].Spec.Pool.ResourceSliceCount = 2
	got := show(t, snap, inspect.Drivers(false), view.Drivers)
	want := joinLines(
		"═══ DRA Drivers ═══",
		"",
		"Node: node-a.example.com",
		"  gpu.example.com: 2 devices  (1 pool)",
		"",
		"Node: node-b.example.com",
		"  gpu.example.com: 2 devices  (1 pool)",
		"    ! pool has fewer ResourceSlices than expected for its newest generation: node-b.example.com",
	)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestDriversIncompletePoolUnderPlaceholder(t *testing.T) {
	snap := fx(t, "classes-claims")
	all := true
	snap.ResourceSlices[0].Spec.NodeName = nil
	snap.ResourceSlices[0].Spec.AllNodes = &all
	snap.ResourceSlices[0].Spec.Pool.ResourceSliceCount = 2
	got := show(t, snap, inspect.Drivers(false), view.Drivers)
	want := joinLines(
		"═══ DRA Drivers ═══",
		"",
		"Node: (all nodes)",
		"  gpu.example.com: 2 devices  (1 pool)",
		"    ! pool has fewer ResourceSlices than expected for its newest generation: node-a.example.com",
	)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// A driver whose pool publishes no devices has no node; it still prints, and
// a warning that no node can own goes once under "Other notes".
func TestDriversWithoutDevicesAndUnattributedNotes(t *testing.T) {
	snap := fx(t, "multinode")
	snap.ResourceSlices[1].Spec.Driver = "empty.example.com"
	snap.ResourceSlices[1].Spec.Devices = nil
	snap.ResourceSlices[1].Spec.Pool.ResourceSliceCount = 2
	got := show(t, snap, inspect.Drivers(false), view.Drivers)
	want := joinLines(
		"═══ DRA Drivers ═══",
		"",
		"Node: node-a.example.com",
		"  gpu.example.com: 2 devices  (1 pool)",
		"",
		"empty.example.com: 0 devices  (1 pool)",
		"",
		"Other notes",
		"  ! empty.example.com: pool has fewer ResourceSlices than expected for its newest generation: node-b.example.com",
	)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestDriversEmpty(t *testing.T) {
	snap := fx(t, "multinode")
	snap.ResourceSlices = nil
	got := show(t, snap, inspect.Drivers(false), view.Drivers)
	if want := joinLines("═══ DRA Drivers ═══", "", "no drivers"); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestDriversLongNamesStayWhole(t *testing.T) {
	got := show(t, fx(t, "unicode-long"), inspect.Drivers(false), view.Drivers)
	long := "gpu.accelerators.extremely-long-driver-domain-name-for-layout-testing.example.com"
	if !hasLine(got, "  "+long+": 2 devices  (1 pool)") {
		t.Errorf("long driver name cut or reflowed:\n%s", got)
	}
}
