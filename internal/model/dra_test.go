package model_test

import (
	"reflect"
	"testing"

	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/utils/ptr"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/testutil"
)

func TestAttrSet(t *testing.T) {
	tests := []struct {
		name string
		in   resourcev1.DeviceAttribute
		want []string
	}{
		{"int", resourcev1.DeviceAttribute{IntValue: ptr.To(int64(2))}, []string{"2"}},
		{"bool", resourcev1.DeviceAttribute{BoolValue: ptr.To(true)}, []string{"true"}},
		{"string", resourcev1.DeviceAttribute{StringValue: ptr.To("pci0000:98")}, []string{"pci0000:98"}},
		{"version", resourcev1.DeviceAttribute{VersionValue: ptr.To("1.2.3")}, []string{"1.2.3"}},
		{"ints sorted deduped", resourcev1.DeviceAttribute{IntValues: []int64{3, 1, 3, 0}}, []string{"0", "1", "3"}},
		{"bools", resourcev1.DeviceAttribute{BoolValues: []bool{true, false}}, []string{"false", "true"}},
		{"strings", resourcev1.DeviceAttribute{StringValues: []string{"b", "a"}}, []string{"a", "b"}},
		{"versions", resourcev1.DeviceAttribute{VersionValues: []string{"2.0.0", "1.0.0"}}, []string{"1.0.0", "2.0.0"}},
		{"empty", resourcev1.DeviceAttribute{}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := model.AttrSet(tt.in); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v want %v", got, tt.want)
			}
		})
	}
}

func intAttr(v int64) resourcev1.DeviceAttribute  { return resourcev1.DeviceAttribute{IntValue: &v} }
func strAttr(v string) resourcev1.DeviceAttribute { return resourcev1.DeviceAttribute{StringValue: &v} }

func dev(attrs map[string]resourcev1.DeviceAttribute) resourcev1.Device {
	d := resourcev1.Device{Name: "d", Attributes: map[resourcev1.QualifiedName]resourcev1.DeviceAttribute{}}
	for k, v := range attrs {
		d.Attributes[resourcev1.QualifiedName(k)] = v
	}
	return d
}

func TestTopologyOf(t *testing.T) {
	tests := []struct {
		name  string
		attrs map[string]resourcev1.DeviceAttribute
		numa  model.Resolved
		pcie  model.Resolved
	}{
		{"none", nil, model.Resolved{}, model.Resolved{}},
		{"standard wins over everything",
			map[string]resourcev1.DeviceAttribute{
				"resource.kubernetes.io/numaNode": intAttr(1), "gpu.example.com/numaNode": intAttr(0), "x.io/numa": intAttr(5),
				"resource.kubernetes.io/pcieRoot": strAttr("pci0000:00"), "pcieRoot": strAttr("zzz"),
			},
			model.Resolved{Values: []string{"1"}, Key: "resource.kubernetes.io/numaNode"},
			model.Resolved{Values: []string{"pci0000:00"}, Key: "resource.kubernetes.io/pcieRoot"}},
		{"standard list",
			map[string]resourcev1.DeviceAttribute{"resource.kubernetes.io/numaNode": {IntValues: []int64{1, 0}}},
			model.Resolved{Values: []string{"0", "1"}, Key: "resource.kubernetes.io/numaNode"}, model.Resolved{}},
		{"tier2 driver-qualified beats other vendor",
			map[string]resourcev1.DeviceAttribute{"other.io/numaNode": intAttr(9), "gpu.example.com/numaNode": intAttr(2)},
			model.Resolved{Values: []string{"2"}, Key: "gpu.example.com/numaNode"}, model.Resolved{}},
		{"tier2 unqualified, name order",
			map[string]resourcev1.DeviceAttribute{"numaNodeID": intAttr(3), "numa": intAttr(3), "pcieRoot": strAttr("r")},
			model.Resolved{Values: []string{"3"}, Key: "numa"}, model.Resolved{Values: []string{"r"}, Key: "pcieRoot"}},
		{"tier2 conflict",
			map[string]resourcev1.DeviceAttribute{"gpu.example.com/numaNode": intAttr(0), "numaNode": intAttr(1)},
			model.Resolved{Values: []string{"0"}, Key: "gpu.example.com/numaNode", Conflict: true}, model.Resolved{}},
		{"tier3 any vendor, lexical, no conflict",
			map[string]resourcev1.DeviceAttribute{"dra.net/numaNode": intAttr(1), "dra.memory/numaNode": intAttr(1)},
			model.Resolved{Values: []string{"1"}, Key: "dra.memory/numaNode"}, model.Resolved{}},
		{"tier3 conflict across vendors and names (dra.memory + dra.net + dra.cpu)",
			map[string]resourcev1.DeviceAttribute{"dra.memory/numaNode": intAttr(0), "dra.net/numaNode": intAttr(1), "dra.cpu/numaNodeID": intAttr(0), "dra.cpu/socketID": intAttr(7)},
			model.Resolved{Values: []string{"0"}, Key: "dra.memory/numaNode", Conflict: true}, model.Resolved{}},
		{"cpuSocketID ignored",
			map[string]resourcev1.DeviceAttribute{"cpuSocketID": intAttr(1), "x/cpuSocketID": intAttr(1)},
			model.Resolved{}, model.Resolved{}},
		{"tier3 pcie",
			map[string]resourcev1.DeviceAttribute{"a.io/pcieRoot": strAttr("p1"), "b.io/pcieRoot": strAttr("p2")},
			model.Resolved{}, model.Resolved{Values: []string{"p1"}, Key: "a.io/pcieRoot", Conflict: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for range 50 {
				got, err := model.TopologyOf(dev(tt.attrs), "gpu.example.com")
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got.NUMA, tt.numa) || !reflect.DeepEqual(got.PCIeRoot, tt.pcie) {
					t.Fatalf("numa=%+v pcie=%+v", got.NUMA, got.PCIeRoot)
				}
			}
		})
	}
}

func TestTopologyOfEmptyValueIsError(t *testing.T) {
	_, err := model.TopologyOf(dev(map[string]resourcev1.DeviceAttribute{"resource.kubernetes.io/numaNode": {}}), "d")
	if err == nil {
		t.Fatal("want error")
	}
}

func TestFixturesRealDevices(t *testing.T) {
	for _, dir := range []string{"testdata/fixtures/xe9785l", "testdata/fixtures/xe9680"} {
		snap := testutil.LoadFixture(t, dir)
		if len(snap.ResourceSlices) == 0 {
			t.Fatalf("%s: no slices", dir)
		}
		n := 0
		for _, s := range snap.ResourceSlices {
			for _, d := range s.Spec.Devices {
				a, err := model.TopologyOf(d, s.Spec.Driver)
				if err != nil {
					t.Fatalf("%s/%s: %v", s.Name, d.Name, err)
				}
				b, _ := model.TopologyOf(d, s.Spec.Driver)
				if !reflect.DeepEqual(a, b) {
					t.Fatalf("%s: nondeterministic", d.Name)
				}
				if len(a.NUMA.Values) > 0 {
					n++
				}
			}
		}
		if n == 0 {
			t.Errorf("%s: no device resolved a NUMA node", dir)
		}
	}
}

func TestAllocatedTriplesExistInSlices(t *testing.T) {
	snap := testutil.LoadFixture(t, "testdata/fixtures/xe9785l")
	if len(snap.ResourceClaims) == 0 || snap.ResourceClaims[0].Status.Allocation == nil {
		t.Fatal("no allocated claim")
	}
	for _, r := range snap.ResourceClaims[0].Status.Allocation.Devices.Results {
		d, s, ok := model.FindDevice(snap.ResourceSlices, r.Driver, r.Pool, r.Device)
		if !ok || d.Name != r.Device || s.Spec.Driver != r.Driver {
			t.Errorf("missing %s/%s/%s", r.Driver, r.Pool, r.Device)
		}
	}
	if _, _, ok := model.FindDevice(snap.ResourceSlices, "nope", "p", "d"); ok {
		t.Error("found nonexistent")
	}
}

func TestSyntheticFixtures(t *testing.T) {
	numa := func(dir, dev string) []string {
		snap := testutil.LoadFixture(t, "testdata/fixtures/"+dir)
		d, s, ok := model.FindDevice(snap.ResourceSlices, snap.ResourceSlices[0].Spec.Driver, "node-a.example.com", dev)
		if !ok {
			t.Fatalf("%s/%s not found", dir, dev)
		}
		topo, err := model.TopologyOf(d, s.Spec.Driver)
		if err != nil {
			t.Fatal(err)
		}
		return topo.NUMA.Values
	}
	if !reflect.DeepEqual(numa("numa-split", "gpu-0"), []string{"0"}) || !reflect.DeepEqual(numa("numa-split", "gpu-1"), []string{"1"}) {
		t.Error("numa-split")
	}
	if numa("missing-attr", "gpu-1") != nil {
		t.Error("missing-attr gpu-1 should have no NUMA")
	}
	pcie := testutil.LoadFixture(t, "testdata/fixtures/pcie-split")
	if c := pcie.ResourceClaims[0].Spec.Devices.Constraints[0]; c.MatchAttribute == nil || *c.MatchAttribute != "resource.kubernetes.io/pcieRoot" {
		t.Error("pcie-split constraint missing")
	}
}
