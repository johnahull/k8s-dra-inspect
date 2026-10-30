package inspect

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
)

func mkVMI(ns, name string) unstructured.Unstructured {
	u := unstructured.Unstructured{}
	u.SetNamespace(ns)
	u.SetName(name)
	return u
}

func TestVMIOfDashSuffixGuard(t *testing.T) {
	snap := model.Snapshot{VMIs: []unstructured.Unstructured{mkVMI("ns", "vm1"), mkVMI("ns", "vm1-x"), mkVMI("other", "vm9")}}
	for pod, want := range map[string]string{
		"virt-launcher-vm1-x-abcde": "vm1-x",
		"virt-launcher-vm1-abcde":   "vm1",
		"virt-launcher-vm9-abcde":   "", // VMI lives in another namespace
	} {
		if got := vmiOf(snap, "ns", []string{pod}); got != want {
			t.Errorf("%s -> %q, want %q", pod, got, want)
		}
	}
}

func claimWithPod() model.Snapshot {
	claim := resourcev1.ResourceClaim{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "c"}}
	return model.Snapshot{
		ResourceClaims: []resourcev1.ResourceClaim{claim},
		Pods: []model.PodRef{{Namespace: "ns", Name: "p",
			ResourceClaims: []corev1.PodResourceClaim{{Name: "x", ResourceClaimName: &claim.Name}}}},
		Collection: map[string]model.CollectionStatus{
			"resourceclaims": {State: model.StateOK}, "resourceslices": {State: model.StateOK},
			"virtualmachineinstances": {State: model.StateOK}, "pods": {State: model.StateOK},
		},
	}
}

func TestClaimsPodsUnavailableNotShownAsUnused(t *testing.T) {
	snap := claimWithPod()
	snap.Pods = nil
	snap.Collection["pods"] = model.CollectionStatus{State: model.StateForbidden, Reason: "forbidden: pods"}
	in := Claims()
	r := in.Run(snap)[0]
	if got := str(r, "usedByState"); got != "unavailable: forbidden: pods" {
		t.Errorf("usedByState = %q", got)
	}
	if row := in.Row(r); row[4] != "unavailable" {
		t.Errorf("USED BY cell = %q", row[4])
	}
	// pods ok: no state key, normal listing.
	ok := Claims().Run(claimWithPod())[0]
	if _, has := ok.Details["usedByState"]; has || str(ok, "usedBy") != "[p]" {
		t.Errorf("%+v", ok.Details)
	}
}

func TestClaimsMissingDeviceWarns(t *testing.T) {
	snap := snapOf(mkSlice("s", "p", 1, 1, dev("a")))
	snap.Collection["resourceclaims"] = model.CollectionStatus{State: model.StateOK}
	snap.ResourceClaims = []resourcev1.ResourceClaim{{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "c"},
		Status: resourcev1.ResourceClaimStatus{Allocation: &resourcev1.AllocationResult{
			Devices: resourcev1.DeviceAllocationResult{Results: []resourcev1.DeviceRequestAllocationResult{
				{Driver: "d.example.com", Pool: "p", Device: "a"}, {Driver: "d.example.com", Pool: "p", Device: "gone"}}}}}}}
	r := Claims().Run(snap)[0]
	missing, _ := r.Details["missingDevices"].([]string)
	if r.Status != model.StatusWarn || len(missing) != 1 || missing[0] != "d.example.com/p/gone" ||
		!strings.Contains(str(r, "reason"), "d.example.com/p/gone") {
		t.Errorf("%+v", r)
	}
}
