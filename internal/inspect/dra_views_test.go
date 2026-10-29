package inspect

import (
	"fmt"
	"strings"
	"testing"

	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/utils/ptr"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/render"
)

func numaDev(name string, attrs map[string]int64) resourcev1.Device {
	d := resourcev1.Device{Name: name, Attributes: map[resourcev1.QualifiedName]resourcev1.DeviceAttribute{}}
	for k, v := range attrs {
		d.Attributes[resourcev1.QualifiedName(k)] = resourcev1.DeviceAttribute{IntValue: ptr.To(v)}
	}
	return d
}

func TestTopologyMissingAttributeWarns(t *testing.T) {
	snap := snapOf(mkSlice("s", "p", 1, 1,
		numaDev("a", map[string]int64{"resource.kubernetes.io/numaNode": 0}), dev("b")))
	var got []string
	for _, r := range Topology(false).Run(snap) {
		got = append(got, fmt.Sprintf("%s|%s|%s", r.Subject, r.Status, str(r, "reason")))
	}
	want := []string{
		"n1/numa=-/pcieRoot=-|warn|missing attribute: numa, missing attribute: pcieRoot",
		"n1/numa=0/pcieRoot=-|warn|missing attribute: pcieRoot",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n%s", strings.Join(got, "\n"))
	}
}

func TestTopologyConflictWarns(t *testing.T) {
	snap := snapOf(mkSlice("s", "p", 1, 1, numaDev("a", map[string]int64{
		"d.example.com/numaNode": 0, "numaNode": 1,
	})))
	r := Topology(true).Run(snap)[0]
	if r.Status != model.StatusWarn || !strings.Contains(str(r, "reason"), "conflicting numa attributes") ||
		!strings.Contains(str(r, "reason"), "d.example.com/numaNode") {
		t.Errorf("%+v", r)
	}
}

func TestTopologyCompactOmitsDevices(t *testing.T) {
	snap := snapOf(mkSlice("s", "p", 1, 1, dev("a")))
	if _, ok := Topology(true).Run(snap)[0].Details["devices"]; ok {
		t.Error("compact lists devices")
	}
	if _, ok := Topology(false).Run(snap)[0].Details["devices"]; !ok {
		t.Error("non-compact omits devices")
	}
}

func TestUnavailablePaths(t *testing.T) {
	for _, c := range []struct {
		in  Inspector
		key string
	}{
		{Topology(false), "resourceslices"},
		{DeviceClasses(labels.Everything()), "deviceclasses"},
		{Claims(), "resourceclaims"},
	} {
		snap := model.Snapshot{Collection: map[string]model.CollectionStatus{c.key: {State: model.StateForbidden, Reason: "forbidden: " + c.key}}}
		res := c.in.Run(snap)
		if len(res) != 1 || res[0].Status != model.StatusUnavailable || str(res[0], "reason") != "forbidden: "+c.key {
			t.Errorf("%s: %+v", c.key, res)
		}
	}
}

func TestClaimsWithoutSlicesStillList(t *testing.T) {
	snap := model.Snapshot{
		Collection: map[string]model.CollectionStatus{
			"resourceclaims": {State: model.StateOK}, "resourceslices": {State: model.StateForbidden},
			"virtualmachineinstances": {State: model.StateNotFound, Reason: "kubevirt.io/v1 not served"},
		},
		ResourceClaims: []resourcev1.ResourceClaim{{Status: resourcev1.ResourceClaimStatus{Allocation: &resourcev1.AllocationResult{
			Devices: resourcev1.DeviceAllocationResult{Results: []resourcev1.DeviceRequestAllocationResult{{Driver: "d", Pool: "p", Device: "x"}}},
		}}}},
	}
	r := Claims().Run(snap)[0]
	if r.Status != model.StatusPass || fmt.Sprint(r.Details["nodes"]) != "[unavailable]" ||
		r.Details["vmi"] != "unavailable: kubevirt.io/v1 not served" {
		t.Errorf("%+v", r)
	}
}

func TestTopologyNoteColumn(t *testing.T) {
	in := Topology(true)
	snap := snapOf(mkSlice("s", "p", 1, 1, numaDev("a", map[string]int64{"resource.kubernetes.io/numaNode": 0}), dev("b")))
	res := in.Run(snap)
	last := in.Columns[len(in.Columns)-1]
	if last.Header != "NOTE" || last.Truncate != render.TruncateTail {
		t.Fatalf("last column %+v", last)
	}
	if row := in.Row(res[1]); row[len(row)-1] != "missing attribute: pcieRoot" {
		t.Errorf("warn row %v", row)
	}
}
