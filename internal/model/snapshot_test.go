package model_test

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/utils/ptr"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
)

func meta(ns, name string) metav1.ObjectMeta { return metav1.ObjectMeta{Namespace: ns, Name: name} }

func TestNormalizeSorts(t *testing.T) {
	vmi := func(ns, name string) unstructured.Unstructured {
		return unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "kubevirt.io/v1", "kind": "VirtualMachineInstance",
			"metadata": map[string]any{"namespace": ns, "name": name},
		}}
	}
	s := model.Snapshot{
		ResourceSlices: []resourcev1.ResourceSlice{{ObjectMeta: meta("", "b")}, {ObjectMeta: meta("", "a")}},
		DeviceClasses:  []resourcev1.DeviceClass{{ObjectMeta: meta("", "z")}, {ObjectMeta: meta("", "y")}},
		ResourceClaims: []resourcev1.ResourceClaim{{ObjectMeta: meta("b", "a")}, {ObjectMeta: meta("a", "z")}, {ObjectMeta: meta("a", "b")}},
		Pods:           []model.PodRef{{Namespace: "b", Name: "a"}, {Namespace: "a", Name: "a"}},
		Nodes:          []corev1.Node{{ObjectMeta: meta("", "n2")}, {ObjectMeta: meta("", "n1")}},
		VMIs:           []unstructured.Unstructured{vmi("b", "x"), vmi("a", "y")},
	}
	s.Normalize()
	if s.ResourceSlices[0].Name != "a" || s.DeviceClasses[0].Name != "y" || s.Nodes[0].Name != "n1" {
		t.Fatal("cluster-scoped not sorted")
	}
	got := []string{s.ResourceClaims[0].Namespace + "/" + s.ResourceClaims[0].Name, s.ResourceClaims[1].Namespace + "/" + s.ResourceClaims[1].Name, s.ResourceClaims[2].Namespace + "/" + s.ResourceClaims[2].Name}
	if got[0] != "a/b" || got[1] != "a/z" || got[2] != "b/a" {
		t.Fatalf("claims: %v", got)
	}
	if s.Pods[0].Namespace != "a" || s.VMIs[0].GetNamespace() != "a" {
		t.Fatal("pods/vmis not sorted")
	}
}

func TestNewPodRefSlims(t *testing.T) {
	p := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "p", Namespace: "ns", UID: "u1",
			Annotations: map[string]string{"secret": "x"},
		},
		Spec: corev1.PodSpec{
			NodeName:       "n1",
			Containers:     []corev1.Container{{Name: "c", Env: []corev1.EnvVar{{Name: "TOKEN", Value: "s3cr3t"}}}},
			ResourceClaims: []corev1.PodResourceClaim{{Name: "rc", ResourceClaimName: ptr.To("claim-a")}},
		},
		Status: corev1.PodStatus{
			Phase:                 corev1.PodRunning,
			ResourceClaimStatuses: []corev1.PodResourceClaimStatus{{Name: "rc", ResourceClaimName: ptr.To("claim-a")}},
		},
	}
	ref := model.NewPodRef(p)
	b, err := json.Marshal(ref)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte("s3cr3t")) || bytes.Contains(b, []byte("secret")) {
		t.Fatalf("leaked: %s", b)
	}
	if ref.Name != "p" || ref.Namespace != "ns" || ref.UID != "u1" || ref.NodeName != "n1" ||
		len(ref.ResourceClaims) != 1 || len(ref.ResourceClaimStatuses) != 1 ||
		*ref.ResourceClaimStatuses[0].ResourceClaimName != "claim-a" {
		t.Fatalf("ref=%+v", ref)
	}
}

func TestSnapshotJSONRoundTrip(t *testing.T) {
	s := model.Snapshot{
		SchemaVersion: model.SchemaVersion,
		GeneratedAt:   time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Source:        "live",
		Collection:    map[string]model.CollectionStatus{"nodes": {State: "forbidden", Reason: "nope"}},
		ResourceSlices: []resourcev1.ResourceSlice{{
			ObjectMeta: meta("", "s1"),
			Spec: resourcev1.ResourceSliceSpec{Driver: "d", Pool: resourcev1.ResourcePool{Name: "p", Generation: 3}, Devices: []resourcev1.Device{{
				Name: "dev",
				Attributes: map[resourcev1.QualifiedName]resourcev1.DeviceAttribute{
					"a": {IntValue: ptr.To(int64(1))}, "b": {StringValues: []string{"x", "y"}}, "c": {BoolValue: ptr.To(true)}, "d": {IntValues: []int64{1, 2}},
				},
				Capacity: map[resourcev1.QualifiedName]resourcev1.DeviceCapacity{"mem": {Value: resource.MustParse("16Gi")}},
			}}},
		}},
		Pods:     []model.PodRef{{Name: "p", Namespace: "ns", UID: "u", NodeName: "n"}},
		VMIs:     []unstructured.Unstructured{{Object: map[string]any{"apiVersion": "kubevirt.io/v1", "kind": "VirtualMachineInstance", "metadata": map[string]any{"name": "v"}}}},
		Evidence: map[string]json.RawMessage{"x": json.RawMessage(`{"k":1}`)},
	}
	b1, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var back model.Snapshot
	if err := json.Unmarshal(b1, &back); err != nil {
		t.Fatal(err)
	}
	b2, _ := json.Marshal(back)
	if !bytes.Equal(b1, b2) {
		t.Fatalf("not lossless:\n%s\n%s", b1, b2)
	}
	d := back.ResourceSlices[0].Spec.Devices[0]
	q := d.Capacity["mem"].Value
	if d.Attributes["b"].StringValues[1] != "y" || q.Cmp(resource.MustParse("16Gi")) != 0 || !back.GeneratedAt.Equal(s.GeneratedAt) {
		t.Fatalf("fields lost: %+v", d)
	}
}
