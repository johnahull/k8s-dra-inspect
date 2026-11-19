package model

import (
	"cmp"
	"encoding/json"
	"slices"
	"time"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

// SchemaVersion identifies the snapshot and JSON envelope format.
const SchemaVersion = "dra-inspect/v1alpha1"

// Snapshot sources.
const (
	SourceLive     = "live"
	SourceSnapshot = "snapshot"
)

// Collection states.
const (
	StateOK        = "ok"
	StateForbidden = "forbidden"
	StateNotFound  = "notFound"
	StateError     = "error"
)

// CollectionStatus records whether one resource type was collected.
type CollectionStatus struct {
	State  string `json:"state"` // ok|forbidden|notFound|error
	Reason string `json:"reason,omitempty"`
}

// PodRef keeps only the pod fields DRA inspection needs.
type PodRef struct {
	Name                  string                          `json:"name"`
	Namespace             string                          `json:"namespace"`
	UID                   types.UID                       `json:"uid,omitempty"`
	NodeName              string                          `json:"nodeName,omitempty"`
	ResourceClaims        []corev1.PodResourceClaim       `json:"resourceClaims,omitempty"`
	ResourceClaimStatuses []corev1.PodResourceClaimStatus `json:"resourceClaimStatuses,omitempty"`
	NodeSelector          map[string]string               `json:"nodeSelector,omitempty"`
	Affinity              *corev1.Affinity                `json:"affinity,omitempty"`
	Tolerations           []corev1.Toleration             `json:"tolerations,omitempty"`
	Volumes               []corev1.Volume                 `json:"volumes,omitempty"`
	OwnerReferences       []metav1.OwnerReference         `json:"ownerReferences,omitempty"`
	Phase                 corev1.PodPhase                 `json:"phase,omitempty"`
	Conditions            []corev1.PodCondition           `json:"conditions,omitempty"`
}

// NewPodRef slims a Pod down to a PodRef.
func NewPodRef(p corev1.Pod) PodRef {
	return PodRef{
		Name: p.Name, Namespace: p.Namespace, UID: p.UID, NodeName: p.Spec.NodeName,
		ResourceClaims:        p.Spec.ResourceClaims,
		ResourceClaimStatuses: p.Status.ResourceClaimStatuses,
		NodeSelector:          p.Spec.NodeSelector,
		Affinity:              p.Spec.Affinity,
		Tolerations:           p.Spec.Tolerations,
		Volumes:               p.Spec.Volumes,
		OwnerReferences:       p.OwnerReferences,
		Phase:                 p.Status.Phase,
		Conditions:            p.Status.Conditions,
	}
}

// NewNodePluginPodRef keeps only fields used to identify kubelet plugin pods.
func NewNodePluginPodRef(p corev1.Pod) PodRef {
	var conditions []corev1.PodCondition
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady {
			conditions = append(conditions, corev1.PodCondition{Type: c.Type, Status: c.Status})
		}
	}
	return PodRef{Name: p.Name, Namespace: p.Namespace, NodeName: p.Spec.NodeName,
		Volumes: hostPathVolumes(p.Spec.Volumes), OwnerReferences: controllerRefs(p.OwnerReferences),
		Phase: p.Status.Phase, Conditions: conditions}
}

// Pod rebuilds the minimum Pod object used by DRA analysis.
func (p PodRef) Pod() *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: p.Name, Namespace: p.Namespace, UID: p.UID, OwnerReferences: p.OwnerReferences},
		Spec: corev1.PodSpec{NodeName: p.NodeName, ResourceClaims: p.ResourceClaims, NodeSelector: p.NodeSelector,
			Affinity: p.Affinity, Tolerations: p.Tolerations, Volumes: p.Volumes},
		Status: corev1.PodStatus{ResourceClaimStatuses: p.ResourceClaimStatuses, Phase: p.Phase, Conditions: p.Conditions},
	}
}

// Snapshot is everything dra-inspect knows about a cluster.
type Snapshot struct {
	SchemaVersion  string                      `json:"schemaVersion"`
	GeneratedAt    time.Time                   `json:"generatedAt"`
	Source         string                      `json:"source"`              // SourceLive|SourceSnapshot
	Namespace      string                      `json:"namespace,omitempty"` // set when collection was namespace-scoped
	Redacted       bool                        `json:"redacted"`
	Collection     map[string]CollectionStatus `json:"collection"`
	ResourceSlices []resourcev1.ResourceSlice  `json:"resourceSlices"`
	DeviceClasses  []resourcev1.DeviceClass    `json:"deviceClasses"`
	ResourceClaims []resourcev1.ResourceClaim  `json:"resourceClaims"`
	// GlobalAllocations retains only allocations from other namespaces when
	// Namespace is set, so device occupancy stays global without exposing names.
	GlobalAllocations []resourcev1.AllocationResult `json:"globalAllocations,omitempty"`
	DeviceTaintRules  []resourcev1.DeviceTaintRule  `json:"deviceTaintRules,omitempty"`
	Pods              []PodRef                      `json:"pods"`
	Events            []corev1.Event                `json:"events,omitempty"`
	NodePluginPods    []PodRef                      `json:"nodePluginPods,omitempty"`
	Nodes             []corev1.Node                 `json:"nodes"`
	VMIs              []unstructured.Unstructured   `json:"virtualMachineInstances"`
	// Evidence holds adapter output. Redact does not touch it, so adapter
	// producers must redact their own evidence before storing it here.
	Evidence map[string]json.RawMessage `json:"evidence"`
}

type namer interface {
	GetNamespace() string
	GetName() string
}

func byKey[T any, P interface {
	*T
	namer
}](s []T) {
	slices.SortFunc(s, func(a, b T) int {
		return cmp.Or(cmp.Compare(P(&a).GetNamespace(), P(&b).GetNamespace()), cmp.Compare(P(&a).GetName(), P(&b).GetName()))
	})
}

// Normalize sorts every list by (namespace, name) for deterministic output.
func (s *Snapshot) Normalize() {
	byKey(s.ResourceSlices)
	byKey(s.DeviceClasses)
	byKey(s.ResourceClaims)
	byKey(s.DeviceTaintRules)
	byKey(s.Events)
	slices.SortFunc(s.GlobalAllocations, func(a, b resourcev1.AllocationResult) int {
		aJSON, _ := json.Marshal(a)
		bJSON, _ := json.Marshal(b)
		return cmp.Compare(string(aJSON), string(bJSON))
	})
	slices.SortFunc(s.NodePluginPods, func(a, b PodRef) int {
		return cmp.Or(cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Name, b.Name))
	})
	byKey(s.Nodes)
	byKey(s.VMIs)
	slices.SortFunc(s.Pods, func(a, b PodRef) int {
		return cmp.Or(cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Name, b.Name))
	})
}

// AllocationSummary keeps only the fields needed to account for device occupancy.
func AllocationSummary(in resourcev1.AllocationResult) resourcev1.AllocationResult {
	out := resourcev1.AllocationResult{Devices: *in.Devices.DeepCopy()}
	out.Devices.Config = nil
	for i := range out.Devices.Results {
		out.Devices.Results[i].Request = ""
	}
	return out
}

// Evidence is one adapter's output, stored under Snapshot.Evidence[adapter name].
type Evidence = json.RawMessage
