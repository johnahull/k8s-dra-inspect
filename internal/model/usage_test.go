package model_test

import (
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/johnahull/k8s-dra-inspect/internal/analyze"
	"github.com/johnahull/k8s-dra-inspect/internal/model"
)

func usage(snap model.Snapshot) (map[model.DeviceKey]model.Usage, model.UsageScope) {
	users, _ := analyze.ClaimPodNames(snap)
	return model.DeviceUsage(snap, users)
}

func uclaim(ns, name string, reserved []string, devs ...string) resourcev1.ResourceClaim {
	c := resourcev1.ResourceClaim{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name}}
	if devs == nil {
		return c
	}
	al := &resourcev1.AllocationResult{}
	for _, d := range devs {
		al.Devices.Results = append(al.Devices.Results, resourcev1.DeviceRequestAllocationResult{Request: "r", Driver: "drv", Pool: "pool", Device: d})
	}
	c.Status.Allocation = al
	for _, r := range reserved {
		c.Status.ReservedFor = append(c.Status.ReservedFor, resourcev1.ResourceClaimConsumerReference{Resource: "pods", Name: r})
	}
	return c
}

func usnap(claims []resourcev1.ResourceClaim, pods []model.PodRef) model.Snapshot {
	ok := model.CollectionStatus{State: model.StateOK}
	return model.Snapshot{
		Collection:     map[string]model.CollectionStatus{"resourceclaims": ok, "pods": ok},
		ResourceClaims: claims, Pods: pods,
	}
}

func key(d string) model.DeviceKey { return model.DeviceKey{Driver: "drv", Pool: "pool", Device: d} }

func TestDeviceUsageInverts(t *testing.T) {
	cn := "c1"
	snap := usnap(
		[]resourcev1.ResourceClaim{uclaim("ns", "c1", []string{"reserved-pod"}, "gpu-0", "gpu-1"), uclaim("ns", "free", nil)},
		[]model.PodRef{{Namespace: "ns", Name: "p1", ResourceClaims: []corev1.PodResourceClaim{{Name: "x", ResourceClaimName: &cn}}}})
	u, sc := usage(snap)
	// reserved-pod is in reservedFor but no collected pod has that name and
	// UID, so it is a stale reservation, not a user.
	want := model.Usage{Claims: []string{"ns/c1"}, Pods: []string{"ns/p1"}}
	for _, d := range []string{"gpu-0", "gpu-1"} {
		if !reflect.DeepEqual(u[key(d)], want) {
			t.Errorf("%s: got %+v want %+v", d, u[key(d)], want)
		}
	}
	if len(u) != 2 {
		t.Errorf("unallocated claim leaked: %+v", u)
	}
	if !sc.Available || !sc.PodsAvailable || sc.Reason != "" {
		t.Errorf("scope %+v", sc)
	}
}

func TestDeviceUsageAllocatedWithoutReservation(t *testing.T) {
	u, _ := usage(usnap([]resourcev1.ResourceClaim{uclaim("ns", "c", nil, "gpu-0")}, nil))
	if got := u[key("gpu-0")]; !reflect.DeepEqual(got.Claims, []string{"ns/c"}) || len(got.Pods) != 0 {
		t.Errorf("got %+v", got)
	}
}

func TestDeviceUsageDedupesRepeatedPairs(t *testing.T) {
	u, _ := usage(usnap([]resourcev1.ResourceClaim{uclaim("ns", "c", nil, "gpu-0", "gpu-0")}, nil))
	if got := u[key("gpu-0")].Claims; !reflect.DeepEqual(got, []string{"ns/c"}) {
		t.Errorf("got %v", got)
	}
}

func TestDeviceUsageShareableDeviceTwoClaims(t *testing.T) {
	a, b := "a", "b"
	snap := usnap(
		[]resourcev1.ResourceClaim{uclaim("ns2", "b", nil, "gpu-0"), uclaim("ns1", "a", nil, "gpu-0")},
		[]model.PodRef{
			{Namespace: "ns1", Name: "pa", ResourceClaims: []corev1.PodResourceClaim{{ResourceClaimName: &a}}},
			{Namespace: "ns2", Name: "pb", ResourceClaims: []corev1.PodResourceClaim{{ResourceClaimName: &b}}},
			{Namespace: "ns1", Name: "other-ns-pod", ResourceClaims: []corev1.PodResourceClaim{{ResourceClaimName: &b}}},
		})
	u, _ := usage(snap)
	want := model.Usage{Claims: []string{"ns1/a", "ns2/b"}, Pods: []string{"ns1/pa", "ns2/pb"}}
	if !reflect.DeepEqual(u[key("gpu-0")], want) {
		t.Errorf("got %+v want %+v", u[key("gpu-0")], want)
	}
}

func TestDeviceUsageTemplateClaimPodViaStatus(t *testing.T) {
	gen, tmpl := "pod-tmpl-abcde", "tmpl"
	claim := uclaim("ns", gen, nil, "gpu-0")
	claim.OwnerReferences = []metav1.OwnerReference{{APIVersion: "v1", Kind: "Pod", Name: "p", UID: "uid-p", Controller: new(true)}}
	snap := usnap([]resourcev1.ResourceClaim{claim},
		[]model.PodRef{{Namespace: "ns", Name: "p", UID: "uid-p",
			ResourceClaims:        []corev1.PodResourceClaim{{Name: "x", ResourceClaimTemplateName: &tmpl}},
			ResourceClaimStatuses: []corev1.PodResourceClaimStatus{{Name: "x", ResourceClaimName: &gen}}}})
	u, _ := usage(snap)
	if got := u[key("gpu-0")].Pods; !reflect.DeepEqual(got, []string{"ns/p"}) {
		t.Errorf("got %v", got)
	}
}

func TestDeviceUsageClaimsForbidden(t *testing.T) {
	snap := usnap([]resourcev1.ResourceClaim{uclaim("ns", "c", nil, "gpu-0")}, nil)
	snap.Collection["resourceclaims"] = model.CollectionStatus{State: model.StateForbidden, Reason: "forbidden: list resourceclaims"}
	u, sc := usage(snap)
	if sc.Available || sc.Reason != "forbidden: list resourceclaims" || len(u) != 0 {
		t.Errorf("scope %+v usage %+v", sc, u)
	}
	delete(snap.Collection, "resourceclaims")
	if _, sc = usage(snap); sc.Available || sc.Reason != "resourceclaims collection state: not collected" {
		t.Errorf("scope %+v", sc)
	}
}

func TestDeviceUsageNamespaceAndPodsNotCollected(t *testing.T) {
	snap := usnap([]resourcev1.ResourceClaim{uclaim("ns", "c", []string{"rp"}, "gpu-0")}, nil)
	snap.Namespace = "ns"
	snap.Collection["globalallocations"] = model.CollectionStatus{State: model.StateOK}
	snap.Collection["pods"] = model.CollectionStatus{State: model.StateForbidden}
	u, sc := usage(snap)
	if sc.Namespace != "ns" || sc.PodsAvailable || !sc.Available {
		t.Errorf("scope %+v", sc)
	}
	if got := u[key("gpu-0")].Pods; !reflect.DeepEqual(got, []string{"ns/rp"}) {
		t.Errorf("reservedFor pods lost: %v", got)
	}
}

func TestDeviceUsageIncludesMaskedOutsideNamespaceAllocation(t *testing.T) {
	snap := usnap(nil, nil)
	snap.Namespace = "team-a"
	snap.Collection["globalallocations"] = model.CollectionStatus{State: model.StateOK}
	snap.GlobalAllocations = []resourcev1.AllocationResult{{Devices: resourcev1.DeviceAllocationResult{
		Results: []resourcev1.DeviceRequestAllocationResult{{Driver: "drv", Pool: "pool", Device: "gpu-0"}},
	}}}
	u, scope := usage(snap)
	if !scope.Available || !scope.ForeignClaims || !reflect.DeepEqual(u[key("gpu-0")].Claims, []string{"allocated outside namespace"}) {
		t.Fatalf("scope=%+v usage=%+v", scope, u)
	}
}
