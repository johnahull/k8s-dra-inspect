package inspect_test

import (
	"context"
	"maps"
	"slices"
	"testing"

	resourcev1 "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"github.com/johnahull/k8s-dra-inspect/internal/check"
	"github.com/johnahull/k8s-dra-inspect/internal/inspect"
	"github.com/johnahull/k8s-dra-inspect/internal/model"
)

func poolSlice(name, pool string, count int64, devs ...string) resourcev1.ResourceSlice {
	s := resourcev1.ResourceSlice{ObjectMeta: metav1.ObjectMeta{Name: name}, Spec: resourcev1.ResourceSliceSpec{
		Driver: "gpu.example.com", Pool: resourcev1.ResourcePool{Name: pool, Generation: 1, ResourceSliceCount: count},
		NodeName: ptr.To("node-a"),
	}}
	for _, d := range devs {
		s.Spec.Devices = append(s.Spec.Devices, resourcev1.Device{Name: d})
	}
	return s
}

// Every command that judges pool validity must agree with inspect pools.
func TestPoolValidityAgreesAcrossCommands(t *testing.T) {
	ok := model.CollectionStatus{State: model.StateOK}
	snap := model.Snapshot{Collection: map[string]model.CollectionStatus{"resourceslices": ok, "resourceclaims": ok},
		ResourceSlices: []resourcev1.ResourceSlice{
			poolSlice("dup-a", "dup", 2, "gpu-0"), poolSlice("dup-b", "dup", 2, "gpu-0"),
			poolSlice("over-a", "over", 2, "a"), poolSlice("over-b", "over", 2, "b"), poolSlice("over-c", "over", 2, "c"),
			poolSlice("good", "good", 1, "gpu-0"),
		}}
	invalid := []string{"gpu.example.com/dup", "gpu.example.com/over"}

	var pools []string
	for _, r := range inspect.Pools().Run(snap) {
		if r.Details["state"] == "Invalid" {
			pools = append(pools, r.Subject)
		}
	}
	if !slices.Equal(pools, invalid) {
		t.Errorf("inspect pools invalid = %v, want %v", pools, invalid)
	}

	seen := map[string]bool{}
	for _, r := range inspect.Slices().Run(snap) {
		if r.Details["state"] == "invalid" {
			seen["gpu.example.com/"+r.Details["pool"].(string)] = true
		}
	}
	if got := slices.Sorted(maps.Keys(seen)); !slices.Equal(got, invalid) {
		t.Errorf("inspect slices invalid pools = %v, want %v", got, invalid)
	}

	var topo []string
	for _, r := range check.Topology()(context.Background(), snap) {
		for _, f := range r.Findings {
			if f.Kind == model.KindInvalid {
				topo = append(topo, r.Subject)
				break
			}
		}
	}
	slices.Sort(topo)
	if !slices.Equal(topo, invalid) {
		t.Errorf("check topology invalid = %v, want %v", topo, invalid)
	}
}
