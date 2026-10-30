package view_test

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"

	"github.com/johnahull/k8s-dra-inspect/internal/inspect"
	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/view"
)

// addSlice appends a one-device slice (a copy of slice 0) named name, in pool
// pool, for the given node binding.
func addSlice(snap *model.Snapshot, name, pool string, bind func(*resourcev1.ResourceSliceSpec)) {
	c := snap.ResourceSlices[0].DeepCopy()
	c.Name, c.Spec.Pool.Name = name, pool
	c.Spec.NodeName, c.Spec.Devices = nil, c.Spec.Devices[:1]
	c.Spec.Devices[0].Name = name + "-dev"
	bind(&c.Spec)
	snap.ResourceSlices = append(snap.ResourceSlices, *c)
}

// mixedNodes has named nodes node-10 and node-2 plus an all-nodes slice, a
// selector slice and a slice with no node field at all.
func mixedNodes(t *testing.T) model.Snapshot {
	snap := fx(t, "multinode")
	snap.ResourceSlices[0].Spec.NodeName = strp("node-10")
	snap.ResourceSlices[1].Spec.NodeName = strp("node-2")
	all := true
	addSlice(&snap, "s-all", "p-all", func(s *resourcev1.ResourceSliceSpec) { s.AllNodes = &all })
	addSlice(&snap, "s-sel", "p-sel", func(s *resourcev1.ResourceSliceSpec) { s.NodeSelector = &corev1.NodeSelector{} })
	addSlice(&snap, "s-none", "p-none", func(*resourcev1.ResourceSliceSpec) {})
	return snap
}

func nodeTitles(out string) string {
	var l []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "Node:") {
			l = append(l, line)
		}
	}
	return strings.Join(l, "|")
}

// All four views order and title nodes alike: named nodes in plain string
// order (node-10 before node-2), then the placeholders.
func TestAllViewsOrderAndTitleNodesAlike(t *testing.T) {
	want := "Node: node-10|Node: node-2|Node: (no node)|Node: (all nodes)|Node: (selector)"
	for name, out := range map[string]func(model.Snapshot) string{
		"slices":     func(s model.Snapshot) string { return slicesOut(t, s) },
		"topology":   func(s model.Snapshot) string { return topoOut(t, s, false) },
		"drivers":    func(s model.Snapshot) string { return show(t, s, inspect.Drivers(false), view.Drivers) },
		"attributes": func(s model.Snapshot) string { return show(t, s, inspect.Attributes(false, false), view.Attributes) },
	} {
		t.Run(name, func(t *testing.T) {
			got := out(mixedNodes(t))
			if g := nodeTitles(got); g != want {
				t.Errorf("node blocks\n got %s\nwant %s\n%s", g, want, got)
			}
		})
	}
}

// A pool seen on two nodes keeps its warning on both, but each node lists only
// its own pools.
func TestDriversIncompleteNoteListsOnlyThePoolsOfThatNode(t *testing.T) {
	snap := fx(t, "multinode")
	for i, n := range []string{"node-10", "node-2"} { // pool multi spans both nodes
		snap.ResourceSlices[i].Spec.NodeName = strp(n)
		snap.ResourceSlices[i].Spec.Pool.Name = "multi"
		snap.ResourceSlices[i].Spec.Pool.ResourceSliceCount = 3
	}
	other := func(s *resourcev1.ResourceSliceSpec) { s.NodeName = strp("node-2"); s.Pool.ResourceSliceCount = 2 }
	addSlice(&snap, "s-other", "other", other)
	addSlice(&snap, "s-fine", "fine", func(s *resourcev1.ResourceSliceSpec) { s.NodeName = strp("node-10"); s.Pool.ResourceSliceCount = 1 })
	got := show(t, snap, inspect.Drivers(false), view.Drivers)
	const w = "    ! pool has fewer ResourceSlices than expected for its newest generation: "
	want := joinLines(
		"═══ DRA Drivers ═══",
		"",
		"Node: node-10",
		"  gpu.example.com: 3 devices  (2 pools)",
		w+"multi",
		"",
		"Node: node-2",
		"  gpu.example.com: 3 devices  (2 pools)",
		w+"multi, other",
	)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// Only a pool that no node holds devices for is left for "Other notes".
func TestDriversIncompleteNoteForPoolWithoutDevicesIsNotLost(t *testing.T) {
	snap := fx(t, "multinode")
	snap.ResourceSlices[0].Spec.Pool.ResourceSliceCount = 2
	addSlice(&snap, "s-empty", "empty", func(s *resourcev1.ResourceSliceSpec) { s.NodeName = strp("node-z"); s.Pool.ResourceSliceCount = 2 })
	snap.ResourceSlices[2].Spec.Devices = nil
	got := show(t, snap, inspect.Drivers(false), view.Drivers)
	if !contains(got, "Other notes\n  ! gpu.example.com: pool has fewer ResourceSlices than expected for its newest generation: empty\n") ||
		!contains(got, "newest generation: node-a.example.com\n") {
		t.Errorf("pools split between node note and Other notes wrongly:\n%s", got)
	}
}

// A slice with no node field is its own "(no node)" block, after real nodes.
func TestDriversAndAttributesNoNodeBlock(t *testing.T) {
	snap := fx(t, "multinode")
	addSlice(&snap, "s-none", "p-none", func(*resourcev1.ResourceSliceSpec) {})
	for _, got := range []string{
		show(t, snap, inspect.Drivers(false), view.Drivers),
		show(t, snap, inspect.Attributes(false, false), view.Attributes),
	} {
		if g := nodeTitles(got); g != "Node: node-a.example.com|Node: node-b.example.com|Node: (no node)" {
			t.Errorf("node blocks %s in:\n%s", g, got)
		}
	}
}

func TestAttributesSameDeviceNameInTwoPoolsSortsNameThenPool(t *testing.T) {
	snap := fx(t, "multinode")
	snap.ResourceSlices[1].Spec.NodeName = strp("node-a.example.com")
	snap.ResourceSlices[1].Spec.Devices[0].Name = "a-gpu-0"
	snap.ResourceSlices[1].Spec.Devices[1].Name = "a-gpu-1"
	got := show(t, snap, inspect.Attributes(false, false), view.Attributes)
	var devs []string
	for _, l := range strings.Split(got, "\n") {
		if strings.HasPrefix(l, "    ") && !strings.HasPrefix(l, "      ") {
			devs = append(devs, strings.TrimSpace(l))
		}
	}
	want := "a-gpu-0|a-gpu-0  (pool node-b.example.com)|a-gpu-1|a-gpu-1  (pool node-b.example.com)"
	if strings.Join(devs, "|") != want {
		t.Errorf("device order\n got %s\nwant %s\n%s", strings.Join(devs, "|"), want, got)
	}
}

// Only the current generation of a pool counts.
func TestDriversCountsOnlyCurrentGeneration(t *testing.T) {
	snap := fx(t, "multinode")
	snap.ResourceSlices[1].Spec.NodeName = strp("node-a.example.com")
	snap.ResourceSlices[1].Spec.Pool.Name = "node-a.example.com"
	snap.ResourceSlices[1].Spec.Pool.Generation = 2 // slice 0 (2 devices) is now stale
	snap.ResourceSlices[1].Spec.Devices = snap.ResourceSlices[1].Spec.Devices[:1]
	got := show(t, snap, inspect.Drivers(false), view.Drivers)
	if !hasLine(got, "  gpu.example.com: 1 device  (1 pool)") || contains(got, "3 devices") {
		t.Errorf("stale generation counted:\n%s", got)
	}
}

// Only an incomplete-pool finding is a pool note under a node; another kind
// that happens to name a pool is a note for the end.
func TestDriversOnlyIncompleteFindingsAttachToNodes(t *testing.T) {
	snap := fx(t, "multinode")
	r := model.Result{Check: "inspect.drivers", Status: model.StatusWarn, Subject: "gpu.example.com",
		Details:  map[string]any{"devices": 4, "pools": 2},
		Findings: []model.Finding{{Status: model.StatusWarn, Kind: model.KindOther, Text: "odd", Items: []string{"node-a.example.com"}}}}
	got := showResults(t, snap, []model.Result{r}, view.Drivers)
	want := joinLines(
		"═══ DRA Drivers ═══",
		"",
		"Node: node-a.example.com",
		"  gpu.example.com: 2 devices  (1 pool)",
		"",
		"Node: node-b.example.com",
		"  gpu.example.com: 2 devices  (1 pool)",
		"",
		"Other notes",
		"  ! gpu.example.com: odd",
	)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestDriversWithoutDevicesPrintInNameOrder(t *testing.T) {
	snap := fx(t, "multinode")
	snap.ResourceSlices = nil
	r := func(name string) model.Result {
		return model.Result{Check: "inspect.drivers", Status: model.StatusPass, Subject: name, Details: map[string]any{"devices": 0, "pools": 1}}
	}
	got := showResults(t, snap, []model.Result{r("e.x"), r("b.x"), r("d.x"), r("a.x"), r("c.x")}, view.Drivers)
	want := joinLines("═══ DRA Drivers ═══", "",
		"a.x: 0 devices  (1 pool)", "b.x: 0 devices  (1 pool)", "c.x: 0 devices  (1 pool)", "d.x: 0 devices  (1 pool)", "e.x: 0 devices  (1 pool)")
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestDriversPoolCountIsPerNode(t *testing.T) {
	snap := fx(t, "multinode")
	snap.ResourceSlices[1].Spec.NodeName = strp("node-a.example.com") // node-a: pools node-a and node-b
	addSlice(&snap, "s-c", "pc", func(s *resourcev1.ResourceSliceSpec) { s.NodeName = strp("node-c") })
	got := show(t, snap, inspect.Drivers(false), view.Drivers)
	for _, w := range []string{"  gpu.example.com: 4 devices  (2 pools)", "  gpu.example.com: 1 device  (1 pool)"} {
		if !hasLine(got, w) {
			t.Errorf("missing %q in:\n%s", w, got)
		}
	}
}
