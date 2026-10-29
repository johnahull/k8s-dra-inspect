package model

import (
	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
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

// NodeRefOf resolves one nodeName/nodeSelector/allNodes triple.
func NodeRefOf(name *string, selector *corev1.NodeSelector, all *bool) (NodeRef, bool) {
	switch {
	case name != nil:
		return NodeRef{Name: *name}, true
	case all != nil && *all:
		return NodeRef{Label: AllNodesLabel}, true
	case selector != nil:
		return NodeRef{Label: SelectorLabel}, true
	}
	return NodeRef{}, false
}

// DeviceNode resolves where one device is available, honouring
// PerDeviceNodeSelection.
func DeviceNode(s resourcev1.ResourceSlice, d resourcev1.Device) NodeRef {
	var r NodeRef
	if s.Spec.PerDeviceNodeSelection != nil && *s.Spec.PerDeviceNodeSelection {
		r, _ = NodeRefOf(d.NodeName, d.NodeSelector, d.AllNodes)
	} else {
		r, _ = NodeRefOf(s.Spec.NodeName, s.Spec.NodeSelector, s.Spec.AllNodes)
	}
	return r
}
