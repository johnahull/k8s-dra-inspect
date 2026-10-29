package inspect

import (
	"fmt"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
)

func snapOf(slices ...resourcev1.ResourceSlice) model.Snapshot {
	return model.Snapshot{
		ResourceSlices: slices,
		Collection:     map[string]model.CollectionStatus{"resourceslices": {State: model.StateOK}},
	}
}

func mkSlice(name, pool string, gen, count int64, devs ...resourcev1.Device) resourcev1.ResourceSlice {
	return resourcev1.ResourceSlice{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: resourcev1.ResourceSliceSpec{
			Driver: "d.example.com", NodeName: ptr.To("n1"), Devices: devs,
			Pool: resourcev1.ResourcePool{Name: pool, Generation: gen, ResourceSliceCount: count},
		},
	}
}

func dev(name string) resourcev1.Device { return resourcev1.Device{Name: name} }

func TestPerDeviceNodeSelection(t *testing.T) {
	sel := &corev1.NodeSelector{}
	s := mkSlice("s", "p", 1, 1,
		resourcev1.Device{Name: "a", NodeName: ptr.To("n1")},
		resourcev1.Device{Name: "b", NodeName: ptr.To("n2")},
		resourcev1.Device{Name: "c", NodeName: ptr.To("n1")},
		resourcev1.Device{Name: "d", AllNodes: ptr.To(true)},
		resourcev1.Device{Name: "e", NodeSelector: sel},
	)
	s.Spec.NodeName = nil
	s.Spec.PerDeviceNodeSelection = ptr.To(true)
	snap := snapOf(s)

	r := Drivers(false).Run(snap)[0]
	if r.Details["nodes"] != 2 {
		t.Errorf("nodes = %v, want 2 real node names only", r.Details["nodes"])
	}
	if got := fmt.Sprint(r.Details["nodeSelection"]); got != "[(all nodes) (selector)]" {
		t.Errorf("nodeSelection = %s", got)
	}
	want := map[string]string{"a": "n1", "b": "n2", "c": "n1", "d": "(all nodes)", "e": "(selector)"}
	for _, r := range Attributes(false, false).Run(snap) {
		if got := str(r, "node"); got != want[r.Subject[strings.LastIndex(r.Subject, "/")+1:]] {
			t.Errorf("%s node = %q", r.Subject, got)
		}
	}
	if got := str(Slices().Run(snap)[0], "node"); got != "n1, n2, (all nodes), (selector)" {
		t.Errorf("slice node = %q", got)
	}
}

func TestSliceLevelNodeSelection(t *testing.T) {
	all := mkSlice("a", "pa", 1, 1, dev("x"))
	all.Spec.NodeName, all.Spec.AllNodes = nil, ptr.To(true)
	selr := mkSlice("s", "ps", 1, 1, dev("x"))
	selr.Spec.NodeName, selr.Spec.NodeSelector = nil, &corev1.NodeSelector{}
	named := mkSlice("n", "pn", 1, 1, dev("x"))
	r := Drivers(false).Run(snapOf(all, selr, named))[0]
	if r.Details["nodes"] != 1 {
		t.Errorf("nodes = %v, want 1", r.Details["nodes"])
	}
	if got := fmt.Sprint(r.Details["nodeSelection"]); got != "[(all nodes) (selector)]" {
		t.Errorf("nodeSelection = %s", got)
	}
}

func stateOf(snap model.Snapshot) map[string]string {
	m := map[string]string{}
	for _, r := range Slices().Run(snap) {
		m[r.Subject] = str(r, "state")
	}
	return m
}

func TestStaleOlderGeneration(t *testing.T) {
	snap := snapOf(mkSlice("old", "p", 1, 1, dev("a")), mkSlice("new", "p", 2, 1, dev("b")))
	if got := stateOf(snap); got["old"] != "stale" || got["new"] != "current" {
		t.Errorf("states = %v", got)
	}
	for _, r := range Slices().Run(snap) {
		if (r.Subject == "old") != (r.Details["stale"] == true) {
			t.Errorf("%s stale detail = %v", r.Subject, r.Details["stale"])
		}
	}
	if r := Drivers(false).Run(snap)[0]; r.Details["devices"] != 1 || r.Status != model.StatusPass {
		t.Errorf("drivers = %+v", r)
	}
}

func TestIncompleteNewestKeepsOlderComplete(t *testing.T) {
	snap := snapOf(mkSlice("old", "p", 1, 1, dev("a")), mkSlice("new", "p", 2, 2, dev("b")))
	if got := stateOf(snap); got["old"] != "current" || got["new"] != "stale" {
		t.Errorf("states = %v", got)
	}
	if r := Drivers(false).Run(snap)[0]; r.Details["devices"] != 1 || r.Status != model.StatusPass {
		t.Errorf("drivers = %+v", r)
	}
}

func TestNoCompleteGenerationUsesNewestAndWarns(t *testing.T) {
	snap := snapOf(mkSlice("old", "p", 1, 2, dev("a")), mkSlice("new", "p", 2, 3, dev("b")))
	if got := stateOf(snap); got["old"] != "stale" || got["new"] != "incomplete" {
		t.Errorf("states = %v", got)
	}
	r := Drivers(false).Run(snap)[0]
	if r.Status != model.StatusWarn || r.Details["incompletePools"] == nil || str(r, "reason") == "" {
		t.Errorf("drivers = %+v", r)
	}
	a := Attributes(false, false).Run(snap)
	if len(a) != 1 || a[0].Status != model.StatusWarn || a[0].Details["incomplete"] != true || str(a[0], "reason") == "" {
		t.Errorf("attributes = %+v", a)
	}
}

func TestAllCompleteSingleGeneration(t *testing.T) {
	snap := snapOf(mkSlice("a", "p", 3, 2, dev("x")), mkSlice("b", "p", 3, 2, dev("y")))
	for n, s := range stateOf(snap) {
		if s != "current" {
			t.Errorf("%s = %s", n, s)
		}
	}
	for _, r := range Slices().Run(snap) {
		if r.Status != model.StatusPass || r.Details["stale"] == true {
			t.Errorf("%+v", r)
		}
	}
	if r := Drivers(false).Run(snap)[0]; r.Status != model.StatusPass || r.Details["devices"] != 2 {
		t.Errorf("drivers = %+v", r)
	}
}
