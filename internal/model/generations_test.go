package model

import (
	"testing"

	resourcev1 "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func gslice(name, pool string, gen, count int64, devs ...string) resourcev1.ResourceSlice {
	s := resourcev1.ResourceSlice{ObjectMeta: metav1.ObjectMeta{Name: name}}
	s.Spec.Driver = "d.example.com"
	s.Spec.Pool = resourcev1.ResourcePool{Name: pool, Generation: gen, ResourceSliceCount: count}
	for _, d := range devs {
		s.Spec.Devices = append(s.Spec.Devices, resourcev1.Device{Name: d})
	}
	return s
}

func TestCurrentSlicesPicksNewestEvenWhenIncomplete(t *testing.T) {
	snap := Snapshot{ResourceSlices: []resourcev1.ResourceSlice{
		gslice("old", "p", 1, 1, "a"), gslice("new-partial", "p", 2, 2, "b"),
		gslice("only", "q", 5, 2, "c"),
	}}
	cur, sel := CurrentSlices(snap)
	names := map[string]bool{}
	for _, s := range cur {
		names[s.Name] = true
	}
	if names["old"] || !names["new-partial"] || !names["only"] || len(cur) != 2 {
		t.Errorf("current = %v", names)
	}
	if s := sel[PoolKey{"d.example.com", "p"}]; s.Gen != 2 || !s.Incomplete {
		t.Errorf("p = %+v", s)
	}
	if s := sel[PoolKey{"d.example.com", "q"}]; s.Gen != 5 || !s.Incomplete {
		t.Errorf("q = %+v", s)
	}
}

func TestSelectedGenerationInvalidWhenCountsDisagree(t *testing.T) {
	snap := Snapshot{ResourceSlices: []resourcev1.ResourceSlice{
		gslice("a", "p", 1, 2, "a"), gslice("b", "p", 1, 3, "b"),
	}}
	if got := SelectGenerations(snap)[PoolKey{"d.example.com", "p"}]; !got.Invalid || !got.Incomplete {
		t.Fatalf("selection = %+v, want invalid and incomplete", got)
	}
}

func TestSelectedGenerationInvalidWhenTooManySlicesObserved(t *testing.T) {
	snap := Snapshot{ResourceSlices: []resourcev1.ResourceSlice{
		gslice("a", "p", 1, 2, "a"), gslice("b", "p", 1, 2, "b"), gslice("c", "p", 1, 2, "c"),
	}}
	if got := SelectGenerations(snap)[PoolKey{"d.example.com", "p"}]; !got.Invalid || !got.Incomplete {
		t.Fatalf("selection = %+v, want invalid and incomplete", got)
	}
}

func TestAllocationDeviceIndexResolve(t *testing.T) {
	snap := Snapshot{ResourceSlices: []resourcev1.ResourceSlice{
		gslice("old", "p", 1, 1, "only-old", "both"),
		gslice("new", "p", 2, 1, "both", "only-new"),
	}}
	idx := NewAllocationDeviceIndex(snap)
	for _, tt := range []struct {
		dev   string
		state AllocationDeviceState
		slice string
	}{
		{"both", DeviceCurrent, "new"},
		{"only-new", DeviceCurrent, "new"},
		{"only-old", DeviceStale, "old"},
		{"absent", DeviceMissing, ""},
	} {
		got := idx.Resolve("d.example.com", "p", tt.dev)
		if got.State != tt.state {
			t.Errorf("%s: state %v, want %v", tt.dev, got.State, tt.state)
		}
		name := ""
		if got.Slice != nil {
			name = got.Slice.Name
		}
		if name != tt.slice {
			t.Errorf("%s: slice %q, want %q", tt.dev, name, tt.slice)
		}
	}
}

func TestSelectedGenerationProblems(t *testing.T) {
	snap := Snapshot{ResourceSlices: []resourcev1.ResourceSlice{
		gslice("b", "dup", 1, 2, "gpu-0"), gslice("a", "dup", 1, 2, "gpu-0", "gpu-1"),
		gslice("x", "over", 1, 2, "a"), gslice("y", "over", 1, 2, "b"), gslice("z", "over", 1, 2, "c"),
		gslice("m", "mixed", 1, 2, "a"), gslice("n", "mixed", 1, 3, "b"),
		gslice("ok", "ok", 1, 1, "a"), gslice("stale", "ok", 0, 1, "a"),
	}}
	sel := SelectGenerations(snap)
	for pool, want := range map[string]string{
		"dup":   `device name "gpu-0" is published by both a and b, which makes the pool invalid`,
		"over":  "3 slices observed at generation 1 but resourceSliceCount is 2, which makes the pool invalid",
		"mixed": "slices at generation 1 disagree on resourceSliceCount (2, 3), so the scheduler's view depends on slice order",
	} {
		got := sel[PoolKey{"d.example.com", pool}]
		if !got.Invalid || len(got.Problems) != 1 || got.Problems[0] != want {
			t.Errorf("%s = %+v, want invalid with %q", pool, got, want)
		}
	}
	if got := sel[PoolKey{"d.example.com", "dup"}]; got.Incomplete || got.Observed != 2 {
		t.Errorf("dup = %+v, want complete with 2 observed", got)
	}
	if got := sel[PoolKey{"d.example.com", "ok"}]; got.Invalid || got.Incomplete || got.Gen != 1 || got.Problems != nil {
		t.Errorf("ok = %+v, want valid generation 1", got)
	}
}
