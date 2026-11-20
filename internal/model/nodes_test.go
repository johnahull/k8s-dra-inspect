package model

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/utils/ptr"
)

func TestDeviceNodeMatchesScopeOf(t *testing.T) {
	sel := &corev1.NodeSelector{NodeSelectorTerms: []corev1.NodeSelectorTerm{{}}}
	tests := []struct {
		name  string
		slice resourcev1.ResourceSliceSpec
		dev   resourcev1.Device
		want  NodeRef
	}{
		{"slice nodeName", resourcev1.ResourceSliceSpec{NodeName: ptr.To("n1")}, resourcev1.Device{}, NodeRef{Name: "n1"}},
		{"slice allNodes", resourcev1.ResourceSliceSpec{AllNodes: ptr.To(true)}, resourcev1.Device{}, NodeRef{Label: AllNodesLabel}},
		{"slice selector", resourcev1.ResourceSliceSpec{NodeSelector: sel}, resourcev1.Device{}, NodeRef{Label: SelectorLabel}},
		{"no selection", resourcev1.ResourceSliceSpec{}, resourcev1.Device{}, NodeRef{}},
		{"allNodes false", resourcev1.ResourceSliceSpec{AllNodes: ptr.To(false)}, resourcev1.Device{}, NodeRef{}},
		{"per-device nodeName", resourcev1.ResourceSliceSpec{PerDeviceNodeSelection: ptr.To(true), NodeName: ptr.To("ignored")}, resourcev1.Device{NodeName: ptr.To("n2")}, NodeRef{Name: "n2"}},
		{"per-device allNodes", resourcev1.ResourceSliceSpec{PerDeviceNodeSelection: ptr.To(true)}, resourcev1.Device{AllNodes: ptr.To(true)}, NodeRef{Label: AllNodesLabel}},
		{"per-device selector", resourcev1.ResourceSliceSpec{PerDeviceNodeSelection: ptr.To(true)}, resourcev1.Device{NodeSelector: sel}, NodeRef{Label: SelectorLabel}},
		{"per-device none", resourcev1.ResourceSliceSpec{PerDeviceNodeSelection: ptr.To(true), NodeName: ptr.To("ignored")}, resourcev1.Device{}, NodeRef{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := resourcev1.ResourceSlice{Spec: tt.slice}
			if got := DeviceNode(s, tt.dev); got != tt.want {
				t.Errorf("DeviceNode = %+v, want %+v", got, tt.want)
			}
			if known := ScopeOf(&s, &tt.dev).Known(); known != (tt.want != NodeRef{}) {
				t.Errorf("ScopeOf.Known = %v, want %v", known, tt.want != NodeRef{})
			}
		})
	}
	if r, ok := NodeRefOf(ptr.To("n"), nil, nil); !ok || r != (NodeRef{Name: "n"}) {
		t.Errorf("NodeRefOf(name) = %+v, %v", r, ok)
	}
	if _, ok := NodeRefOf(nil, nil, ptr.To(false)); ok {
		t.Error("NodeRefOf with allNodes=false must report no binding")
	}
}
