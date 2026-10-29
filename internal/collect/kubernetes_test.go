package collect

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/utils/ptr"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
)

var now = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

func slice(name string) *resourcev1.ResourceSlice {
	return &resourcev1.ResourceSlice{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: resourcev1.ResourceSliceSpec{
			Driver: "gpu.example.com",
			Pool:   resourcev1.ResourcePool{Name: "node-a", Generation: 2},
			Devices: []resourcev1.Device{{
				Name: "gpu-0",
				Attributes: map[resourcev1.QualifiedName]resourcev1.DeviceAttribute{
					"index":                           {IntValue: ptr.To(int64(0))},
					"resource.kubernetes.io/numaNode": {IntValues: []int64{0, 1}},
					"resource.kubernetes.io/pcieRoot": {StringValue: ptr.To("pci0000:00")},
				},
				Capacity: map[resourcev1.QualifiedName]resourcev1.DeviceCapacity{"memory": {Value: resource.MustParse("64Gi")}},
			}},
			SharedCounters: []resourcev1.CounterSet{{Name: "cs"}},
		},
	}
}

func claim(ns, name string) *resourcev1.ResourceClaim {
	return &resourcev1.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Status: resourcev1.ResourceClaimStatus{Allocation: &resourcev1.AllocationResult{Devices: resourcev1.DeviceAllocationResult{
			Results: []resourcev1.DeviceRequestAllocationResult{{Request: "r", Driver: "gpu.example.com", Pool: "node-a", Device: "gpu-0"}},
		}}},
	}
}

func pod(ns, name string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, UID: "u-" + "x"},
		Spec:       corev1.PodSpec{NodeName: "node-a", ResourceClaims: []corev1.PodResourceClaim{{Name: "rc"}}},
		Status:     corev1.PodStatus{ResourceClaimStatuses: []corev1.PodResourceClaimStatus{{Name: "rc", ResourceClaimName: ptr.To("c1")}}},
	}
}

func newLive(cs *fake.Clientset, objs ...runtime.Object) *Live {
	scheme := runtime.NewScheme()
	dyn := dynfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{vmiGVR: "VirtualMachineInstanceList"}, objs...)
	return &Live{Client: cs, Dynamic: dyn, Discovery: cs.Discovery(), Now: func() time.Time { return now }}
}

func serveKubeVirt(cs *fake.Clientset) {
	cs.Resources = []*metav1.APIResourceList{{
		GroupVersion: "kubevirt.io/v1",
		APIResources: []metav1.APIResource{{Name: "virtualmachineinstances", Kind: "VirtualMachineInstance", Namespaced: true}},
	}}
}

func vmi(ns, name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "kubevirt.io/v1", "kind": "VirtualMachineInstance",
		"metadata": map[string]any{"namespace": ns, "name": name},
	}}
}

func TestCollectTyped(t *testing.T) {
	cs := fake.NewClientset(
		slice("s1"),
		&resourcev1.DeviceClass{ObjectMeta: metav1.ObjectMeta{Name: "gpu.example.com"}},
		claim("a", "c1"), claim("b", "c2"),
		pod("a", "p1"), pod("b", "p2"),
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}},
	)
	serveKubeVirt(cs)
	snap, err := newLive(cs, vmi("a", "v1")).Collect(context.Background(), Scope{})
	if err != nil {
		t.Fatal(err)
	}
	if snap.SchemaVersion != model.SchemaVersion || snap.Source != "live" || !snap.GeneratedAt.Equal(now) || snap.Redacted {
		t.Fatalf("header: %+v", snap)
	}
	for _, k := range []string{"resourceslices", "deviceclasses", "resourceclaims", "pods", "nodes", "virtualmachineinstances"} {
		if snap.Collection[k].State != model.StateOK {
			t.Errorf("%s: %+v", k, snap.Collection[k])
		}
	}
	if len(snap.ResourceClaims) != 2 || len(snap.Pods) != 2 || len(snap.Nodes) != 1 || len(snap.DeviceClasses) != 1 || len(snap.VMIs) != 1 {
		t.Fatalf("counts: %d %d %d %d %d", len(snap.ResourceClaims), len(snap.Pods), len(snap.Nodes), len(snap.DeviceClasses), len(snap.VMIs))
	}
	d := snap.ResourceSlices[0].Spec.Devices[0]
	if *d.Attributes["index"].IntValue != 0 || len(d.Attributes["resource.kubernetes.io/numaNode"].IntValues) != 2 ||
		*d.Attributes["resource.kubernetes.io/pcieRoot"].StringValue != "pci0000:00" {
		t.Errorf("attributes lost: %+v", d.Attributes)
	}
	q := d.Capacity["memory"].Value
	if q.Cmp(resource.MustParse("64Gi")) != 0 || snap.ResourceSlices[0].Spec.Pool.Generation != 2 || len(snap.ResourceSlices[0].Spec.SharedCounters) != 1 {
		t.Errorf("capacity/pool/counters lost")
	}
	if r := snap.ResourceClaims[0].Status.Allocation.Devices.Results[0]; r.Driver != "gpu.example.com" || r.Pool != "node-a" || r.Device != "gpu-0" || r.Request != "r" {
		t.Errorf("allocation: %+v", r)
	}
	if p := snap.Pods[0]; p.NodeName != "node-a" || len(p.ResourceClaimStatuses) != 1 {
		t.Errorf("pod: %+v", p)
	}
}

func TestCollectNamespaceScope(t *testing.T) {
	cs := fake.NewClientset(slice("s1"), claim("a", "c1"), claim("b", "c2"), pod("a", "p1"), pod("b", "p2"),
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}})
	snap, err := newLive(cs).Collect(context.Background(), Scope{Namespace: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.ResourceClaims) != 1 || snap.ResourceClaims[0].Namespace != "a" || len(snap.Pods) != 1 || snap.Pods[0].Namespace != "a" {
		t.Fatalf("claims=%d pods=%d", len(snap.ResourceClaims), len(snap.Pods))
	}
	if len(snap.ResourceSlices) != 1 || len(snap.Nodes) != 1 {
		t.Error("cluster-scoped resources must stay cluster-wide")
	}
}

func TestCollectForbiddenDoesNotAbort(t *testing.T) {
	cs := fake.NewClientset(slice("s1"), claim("a", "c1"), &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}})
	cs.PrependReactor("list", "resourceslices", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Group: "resource.k8s.io", Resource: "resourceslices"}, "", nil)
	})
	cs.PrependReactor("list", "nodes", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewNotFound(schema.GroupResource{Resource: "nodes"}, "")
	})
	cs.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, context.DeadlineExceeded
	})
	snap, err := newLive(cs).Collect(context.Background(), Scope{})
	if err != nil {
		t.Fatal(err)
	}
	if st := snap.Collection["resourceslices"]; st.State != model.StateForbidden || st.Reason == "" || len(snap.ResourceSlices) != 0 {
		t.Errorf("slices: %+v n=%d", st, len(snap.ResourceSlices))
	}
	if snap.Collection["nodes"].State != model.StateNotFound {
		t.Errorf("nodes: %+v", snap.Collection["nodes"])
	}
	if st := snap.Collection["pods"]; st.State != model.StateError || st.Reason != context.DeadlineExceeded.Error() {
		t.Errorf("pods: %+v", st)
	}
	if snap.Collection["resourceclaims"].State != model.StateOK || len(snap.ResourceClaims) != 1 {
		t.Error("claims should still be collected")
	}
	if snap.Collection["deviceclasses"].State != model.StateOK {
		t.Error("empty successful list must be ok")
	}
}

func TestCollectKubeVirtAbsent(t *testing.T) {
	snap, err := newLive(fake.NewClientset()).Collect(context.Background(), Scope{})
	if err != nil {
		t.Fatal(err)
	}
	want := model.CollectionStatus{State: model.StateNotFound, Reason: "kubevirt.io/v1 not served"}
	if got := snap.Collection["virtualmachineinstances"]; got != want || len(snap.VMIs) != 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestCollectVMIsNamespaced(t *testing.T) {
	cs := fake.NewClientset()
	serveKubeVirt(cs)
	snap, err := newLive(cs, vmi("a", "v1"), vmi("b", "v2")).Collect(context.Background(), Scope{Namespace: "b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.VMIs) != 1 || snap.VMIs[0].GetName() != "v2" {
		t.Fatalf("vmis: %v", snap.VMIs)
	}
}

func TestSnapshotUsesScope(t *testing.T) {
	cs := fake.NewClientset(claim("a", "c1"), claim("b", "c2"))
	l := newLive(cs)
	l.Scope = Scope{Namespace: "a"}
	var src model.Source = l
	snap, err := src.Snapshot(context.Background())
	if err != nil || len(snap.ResourceClaims) != 1 {
		t.Fatalf("err=%v n=%d", err, len(snap.ResourceClaims))
	}
}
