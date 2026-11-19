package inspect_test

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"github.com/johnahull/k8s-dra-inspect/internal/inspect"
	"github.com/johnahull/k8s-dra-inspect/internal/model"
)

func TestMapLinksPodToClaimAndReportsMissingReference(t *testing.T) {
	ok := model.CollectionStatus{State: model.StateOK}
	claim := resourcev1.ResourceClaim{ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "gpu"},
		Status: resourcev1.ResourceClaimStatus{Allocation: &resourcev1.AllocationResult{Devices: resourcev1.DeviceAllocationResult{
			Results: []resourcev1.DeviceRequestAllocationResult{{Driver: "gpu.example.com", Pool: "node-a", Device: "gpu-0"}},
		}}}}
	ref := "gpu"
	snap := model.Snapshot{Collection: map[string]model.CollectionStatus{"resourceclaims": ok, "pods": ok},
		ResourceClaims: []resourcev1.ResourceClaim{claim}, Pods: []model.PodRef{{Namespace: "team-a", Name: "trainer", ResourceClaims: []corev1.PodResourceClaim{
			{Name: "bound", ResourceClaimName: &ref}, {Name: "missing", ResourceClaimName: ptr.To("absent")},
		}}}}
	results := inspect.Map().Run(snap)
	if len(results) != 2 || results[0].Subject != "team-a/gpu" || results[0].Details["state"] != "allocated" {
		t.Fatalf("mapped results = %+v", results)
	}
	if results[1].Status != model.StatusWarn || results[1].Details["state"] != "missing" {
		t.Fatalf("missing reference = %+v", results[1])
	}
}

func TestDevicesMasksForeignAllocationIdentity(t *testing.T) {
	ok := model.CollectionStatus{State: model.StateOK}
	slice := resourcev1.ResourceSlice{ObjectMeta: metav1.ObjectMeta{Name: "slice"}, Spec: resourcev1.ResourceSliceSpec{
		Driver: "gpu.example.com", Pool: resourcev1.ResourcePool{Name: "node-a", Generation: 2, ResourceSliceCount: 1},
		Devices: []resourcev1.Device{{Name: "gpu-0"}},
	}}
	snap := model.Snapshot{Namespace: "team-a", Collection: map[string]model.CollectionStatus{
		"resourceslices": ok, "resourceclaims": ok, "globalallocations": ok, "devicetaintrules": ok,
	}, ResourceSlices: []resourcev1.ResourceSlice{slice}, GlobalAllocations: []resourcev1.AllocationResult{{
		Devices: resourcev1.DeviceAllocationResult{Results: []resourcev1.DeviceRequestAllocationResult{{Driver: "gpu.example.com", Pool: "node-a", Device: "gpu-0"}}},
	}}}
	results := inspect.Devices().Run(snap)
	if len(results) != 1 || results[0].Details["state"] != "Allocated" {
		t.Fatalf("device results = %+v", results)
	}
	holders, _ := results[0].Details["holders"].([]string)
	if len(holders) != 1 || holders[0] != "allocated outside namespace" || strings.Contains(strings.Join(holders, " "), "foreign") {
		t.Fatalf("foreign holder identity was not masked: %v", holders)
	}
}

func TestPoolsUsesNewestGenerationEvenWhenIncomplete(t *testing.T) {
	old := resourcev1.ResourceSlice{ObjectMeta: metav1.ObjectMeta{Name: "old"}, Spec: resourcev1.ResourceSliceSpec{
		Driver: "gpu.example.com", Pool: resourcev1.ResourcePool{Name: "p", Generation: 1, ResourceSliceCount: 1},
		Devices: []resourcev1.Device{{Name: "old-device"}},
	}}
	newest := old.DeepCopy()
	newest.Name = "new"
	newest.Spec.Pool.Generation, newest.Spec.Pool.ResourceSliceCount = 2, 2
	newest.Spec.Devices[0].Name = "new-device"
	results := inspect.Pools().Run(model.Snapshot{Collection: map[string]model.CollectionStatus{
		"resourceslices": {State: model.StateOK},
	}, ResourceSlices: []resourcev1.ResourceSlice{old, *newest}})
	if len(results) != 1 || results[0].Details["generation"] != int64(2) || results[0].Details["state"] != "Incomplete" || results[0].Details["outdatedSlices"] != 1 {
		t.Fatalf("pool results = %+v", results)
	}
}

func TestNodesRequiresClusterwidePluginEvidence(t *testing.T) {
	results := inspect.Nodes().Run(model.Snapshot{Collection: map[string]model.CollectionStatus{
		"resourceslices": {State: model.StateOK}, "nodes": {State: model.StateOK}, "resourceclaims": {State: model.StateOK},
		"globalallocations": {State: model.StateOK}, "devicetaintrules": {State: model.StateOK},
	}})
	if len(results) != 1 || results[0].Status != model.StatusUnavailable || !strings.Contains(results[0].Details["reason"].(string), "nodepluginpods") {
		t.Fatalf("nodes without plugin evidence = %+v", results)
	}
}
