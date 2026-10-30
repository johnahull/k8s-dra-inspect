package model_test

import (
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
)

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
	u, sc := model.DeviceUsage(snap)
	want := model.Usage{Claims: []string{"ns/c1"}, Pods: []string{"ns/p1", "ns/reserved-pod"}}
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
	u, _ := model.DeviceUsage(usnap([]resourcev1.ResourceClaim{uclaim("ns", "c", nil, "gpu-0")}, nil))
	if got := u[key("gpu-0")]; !reflect.DeepEqual(got.Claims, []string{"ns/c"}) || len(got.Pods) != 0 {
		t.Errorf("got %+v", got)
	}
}

func TestDeviceUsageDedupesRepeatedPairs(t *testing.T) {
	u, _ := model.DeviceUsage(usnap([]resourcev1.ResourceClaim{uclaim("ns", "c", nil, "gpu-0", "gpu-0")}, nil))
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
	u, _ := model.DeviceUsage(snap)
	want := model.Usage{Claims: []string{"ns1/a", "ns2/b"}, Pods: []string{"ns1/pa", "ns2/pb"}}
	if !reflect.DeepEqual(u[key("gpu-0")], want) {
		t.Errorf("got %+v want %+v", u[key("gpu-0")], want)
	}
}

func TestDeviceUsageTemplateClaimPodViaStatus(t *testing.T) {
	gen := "pod-tmpl-abcde"
	snap := usnap([]resourcev1.ResourceClaim{uclaim("ns", gen, nil, "gpu-0")},
		[]model.PodRef{{Namespace: "ns", Name: "p", ResourceClaimStatuses: []corev1.PodResourceClaimStatus{{Name: "x", ResourceClaimName: &gen}}}})
	u, _ := model.DeviceUsage(snap)
	if got := u[key("gpu-0")].Pods; !reflect.DeepEqual(got, []string{"ns/p"}) {
		t.Errorf("got %v", got)
	}
}

func TestDeviceUsageClaimsForbidden(t *testing.T) {
	snap := usnap([]resourcev1.ResourceClaim{uclaim("ns", "c", nil, "gpu-0")}, nil)
	snap.Collection["resourceclaims"] = model.CollectionStatus{State: model.StateForbidden, Reason: "forbidden: list resourceclaims"}
	u, sc := model.DeviceUsage(snap)
	if sc.Available || sc.Reason != "forbidden: list resourceclaims" || len(u) != 0 {
		t.Errorf("scope %+v usage %+v", sc, u)
	}
	delete(snap.Collection, "resourceclaims")
	if _, sc = model.DeviceUsage(snap); sc.Available || sc.Reason != "resourceclaims collection state: not collected" {
		t.Errorf("scope %+v", sc)
	}
}

func TestDeviceUsageNamespaceAndPodsNotCollected(t *testing.T) {
	snap := usnap([]resourcev1.ResourceClaim{uclaim("ns", "c", []string{"rp"}, "gpu-0")}, nil)
	snap.Namespace = "ns"
	snap.Collection["pods"] = model.CollectionStatus{State: model.StateForbidden}
	u, sc := model.DeviceUsage(snap)
	if sc.Namespace != "ns" || sc.PodsAvailable || !sc.Available {
		t.Errorf("scope %+v", sc)
	}
	if got := u[key("gpu-0")].Pods; !reflect.DeepEqual(got, []string{"ns/rp"}) {
		t.Errorf("reservedFor pods lost: %v", got)
	}
}

func TestPodsUsingKeepsBareNames(t *testing.T) {
	cn := "c"
	snap := usnap(nil, []model.PodRef{
		{Namespace: "ns", Name: "p", ResourceClaims: []corev1.PodResourceClaim{{ResourceClaimName: &cn}, {ResourceClaimName: &cn}}},
		{Namespace: "x", Name: "q", ResourceClaims: []corev1.PodResourceClaim{{ResourceClaimName: &cn}}}})
	c := uclaim("ns", "c", []string{"r", "p"}, "d")
	if got := model.PodsUsing(snap, c); !reflect.DeepEqual(got, []string{"p", "r"}) {
		t.Errorf("got %v", got)
	}
}

func TestAttrString(t *testing.T) {
	d := dev(map[string]resourcev1.DeviceAttribute{
		"resource.kubernetes.io/pciBusID": strAttr("std"), "gpu.example.com/pciBusID": strAttr("drv"), "pciBusID": strAttr("bare"),
		"gpu.example.com/model": strAttr("drvmodel"), "model": strAttr("baremodel"),
		"productName": strAttr("bareprod"), "other.io/vendor": strAttr("x"), "n": intAttr(1),
	})
	for _, c := range []struct {
		name, want string
		ok         bool
	}{
		{"pciBusID", "std", true},   // standard key wins
		{"model", "drvmodel", true}, // then <driver>/<name>
		{"productName", "bareprod", true},
		{"vendor", "", false}, // other vendors' keys never match
		{"missing", "", false},
		{"n", "", false}, // not a string attribute
	} {
		got, ok := model.AttrString(d, "gpu.example.com", c.name)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: got %q,%v want %q,%v", c.name, got, ok, c.want, c.ok)
		}
	}
	delete(d.Attributes, "resource.kubernetes.io/pciBusID")
	if got, _ := model.AttrString(d, "gpu.example.com", "pciBusID"); got != "drv" {
		t.Errorf("driver key: %q", got)
	}
	delete(d.Attributes, "gpu.example.com/pciBusID")
	if got, _ := model.AttrString(d, "gpu.example.com", "pciBusID"); got != "bare" {
		t.Errorf("bare key: %q", got)
	}
}
