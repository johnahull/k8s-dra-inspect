package analyze

import (
	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
)

// Snapshot is the minimal typed view consumed by the ported pure analyzers.
type Snapshot struct {
	Nodes            []*corev1.Node
	Pods             []*corev1.Pod
	NodePluginPods   []*corev1.Pod
	Events           []*corev1.Event
	DeviceClasses    []*resourcev1.DeviceClass
	ResourceSlices   []*resourcev1.ResourceSlice
	ResourceClaims   []*resourcev1.ResourceClaim
	DeviceTaintRules []*resourcev1.DeviceTaintRule
}
