package view_test

import (
	"strings"
	"testing"

	resourcev1 "k8s.io/api/resource/v1"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
)

// addSpanning appends a per-device-selection slice with one device per bind
// (each sets that device's node fields), in pool pool at generation gen with
// the given slice count.
func addSpanning(snap *model.Snapshot, name, pool string, gen, count int64, binds ...func(*resourcev1.Device)) {
	addSlice(snap, name, pool, func(s *resourcev1.ResourceSliceSpec) {
		yes := true
		s.PerDeviceNodeSelection = &yes
		s.Pool.Generation, s.Pool.ResourceSliceCount = gen, count
		dev := s.Devices[0]
		s.Devices = nil
		for i, b := range binds {
			d := *dev.DeepCopy()
			d.Name = name + "-d" + string(rune('0'+i))
			b(&d)
			s.Devices = append(s.Devices, d)
		}
	})
}

func onNode(n string) func(*resourcev1.Device) {
	return func(d *resourcev1.Device) { d.NodeName = strp(n) }
}
func onAll(d *resourcev1.Device) { yes := true; d.AllNodes = &yes }

func countOf(s, sub string) int { return strings.Count(s, sub) }

func TestSliceNoteForMultiNodeStaleSliceGoesToOtherNotes(t *testing.T) {
	snap := mixedNodes(t)
	addSpanning(&snap, "span-old", "pm", 1, 1, onNode("node-10"), onNode("node-2"))
	addSpanning(&snap, "span-new", "pm", 2, 1, onNode("node-10"))
	got := slicesOut(t, snap)
	if g := nodeTitles(got); g != "Node: node-10|Node: node-2|Node: (no node)|Node: (all nodes)|Node: (selector)" {
		t.Errorf("bogus node block: %s\n%s", g, got)
	}
	if n := countOf(got, "span-old (generation 1) is stale"); n != 1 || !strings.Contains(got, "\n\nOther notes\n  ! span-old: slice span-old (generation 1) is stale") {
		t.Errorf("want one note under Other notes, got %d:\n%s", n, got)
	}
	if !strings.HasSuffix(strings.TrimRight(got, "\n"), "pool pm is at generation 2") {
		t.Errorf("Other notes not last:\n%s", got)
	}
}

func TestSliceNoteForMultiNodeIncompletePoolGoesToOtherNotes(t *testing.T) {
	snap := mixedNodes(t)
	addSpanning(&snap, "span-inc", "pi", 1, 2, onNode("node-10"), onNode("node-2"))
	got := slicesOut(t, snap)
	if strings.Contains(got, "Node: node-10, node-2") || nodeTitles(got) != "Node: node-10|Node: node-2|Node: (no node)|Node: (all nodes)|Node: (selector)" {
		t.Errorf("bogus node block:\n%s", got)
	}
	if n := countOf(got, "span-inc (generation 1)"); n != 1 || !strings.Contains(got, "\n\nOther notes\n  ! span-inc: slice span-inc (generation 1)") {
		t.Errorf("want one note under Other notes, got %d:\n%s", n, got)
	}
}

func TestSliceNoteForSingleNodeSliceStaysUnderItsNode(t *testing.T) {
	snap := mixedNodes(t)
	// A per-device slice whose devices all sit on node-2 is single-node.
	addSpanning(&snap, "one-old", "pm", 1, 1, onNode("node-2"), onNode("node-2"))
	addSpanning(&snap, "one-new", "pm", 2, 1, onNode("node-10"))
	got := slicesOut(t, snap)
	if strings.Contains(got, "Other notes") || !strings.Contains(got, "Node: node-2\n") {
		t.Fatalf("single-node note must not go to Other notes:\n%s", got)
	}
	i, j, k := strings.Index(got, "Node: node-2\n"), strings.Index(got, "! slice one-old"), strings.Index(got, "Node: (no node)")
	if i < 0 || i >= j || j >= k {
		t.Errorf("note not under node-2:\n%s", got)
	}
}

func TestSliceNoteForPlaceholderSlices(t *testing.T) {
	snap := mixedNodes(t)
	// whole slice on (all nodes): note sits under that block
	addSpanning(&snap, "all-old", "pa", 1, 1, onAll)
	addSpanning(&snap, "all-new", "pa", 2, 1, onAll)
	// mixed placeholder + named node: Other notes
	addSpanning(&snap, "mix-old", "px", 1, 1, onAll, onNode("node-2"))
	addSpanning(&snap, "mix-new", "px", 2, 1, onAll)
	got := slicesOut(t, snap)
	i, j, k := strings.Index(got, "Node: (all nodes)"), strings.Index(got, "! slice all-old"), strings.Index(got, "Node: (selector)")
	if i < 0 || i >= j || j >= k {
		t.Errorf("all-old note not under (all nodes):\n%s", got)
	}
	if !strings.Contains(got, "\n\nOther notes\n  ! mix-old: slice mix-old") || countOf(got, "slice mix-old") != 1 {
		t.Errorf("mixed note not once under Other notes:\n%s", got)
	}
}

func TestSliceOtherNotesNeutraliseHostileNames(t *testing.T) {
	snap := mixedNodes(t)
	bad := "ev\x1b[2Jil\nNode: fake"
	addSpanning(&snap, bad, "pm", 1, 1, onNode("node-10"), onNode("node-2\x1b[31m"))
	addSpanning(&snap, "good", "pm", 2, 1, onNode("node-10"))
	got := slicesOut(t, snap)
	if strings.ContainsAny(got, "\x1b") || strings.Contains(got, "\nNode: fake") {
		t.Errorf("hostile text not neutralised: %q", got)
	}
	if !strings.Contains(got, "Other notes\n  ! ") {
		t.Errorf("missing Other notes:\n%s", got)
	}
}
