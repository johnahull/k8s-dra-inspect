package model

import (
	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/utils/ptr"
)

// Placeholder labels for devices that are not tied to one named node.
const (
	AllNodesLabel = "(all nodes)"
	SelectorLabel = "(selector)"
)

// NodeRef is a real node name or, when Name is empty, a placeholder Label.
// The zero value means the device has no node binding at all.
type NodeRef struct{ Name, Label string }

func (n NodeRef) String() string { return n.Name + n.Label }

// Ref is the scope as a node name or a placeholder label; the zero NodeRef
// when no node selection is set.
func (s NodeScope) Ref() NodeRef {
	switch {
	case s.NodeName != "":
		return NodeRef{Name: s.NodeName}
	case s.AllNodes:
		return NodeRef{Label: AllNodesLabel}
	case s.NodeSelector != nil:
		return NodeRef{Label: SelectorLabel}
	}
	return NodeRef{}
}

// NodeRefOf resolves one nodeName/nodeSelector/allNodes triple.
func NodeRefOf(name *string, selector *corev1.NodeSelector, all *bool) (NodeRef, bool) {
	s := NodeScope{NodeName: ptr.Deref(name, ""), AllNodes: ptr.Deref(all, false), NodeSelector: selector}
	return s.Ref(), s.Known()
}

// DeviceNode resolves where one device is available, honouring
// PerDeviceNodeSelection.
func DeviceNode(s resourcev1.ResourceSlice, d resourcev1.Device) NodeRef {
	return ScopeOf(&s, &d).Ref()
}
