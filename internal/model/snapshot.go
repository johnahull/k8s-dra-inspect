package model

import (
	"cmp"
	"encoding/json"
	"slices"
	"time"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

// SchemaVersion identifies the snapshot and JSON envelope format.
const SchemaVersion = "dra-inspect/v1alpha1"

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
}

// NewPodRef slims a Pod down to a PodRef.
func NewPodRef(p corev1.Pod) PodRef {
	return PodRef{
		Name: p.Name, Namespace: p.Namespace, UID: p.UID, NodeName: p.Spec.NodeName,
		ResourceClaims:        p.Spec.ResourceClaims,
		ResourceClaimStatuses: p.Status.ResourceClaimStatuses,
	}
}

// Snapshot is everything dra-inspect knows about a cluster.
type Snapshot struct {
	SchemaVersion  string                      `json:"schemaVersion"`
	GeneratedAt    time.Time                   `json:"generatedAt"`
	Source         string                      `json:"source"` // live|snapshot
	Redacted       bool                        `json:"redacted"`
	Collection     map[string]CollectionStatus `json:"collection"`
	ResourceSlices []resourcev1.ResourceSlice  `json:"resourceSlices"`
	DeviceClasses  []resourcev1.DeviceClass    `json:"deviceClasses"`
	ResourceClaims []resourcev1.ResourceClaim  `json:"resourceClaims"`
	Pods           []PodRef                    `json:"pods"`
	Nodes          []corev1.Node               `json:"nodes"`
	VMIs           []unstructured.Unstructured `json:"virtualMachineInstances"`
	Evidence       map[string]json.RawMessage  `json:"evidence"`
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
	byKey(s.Nodes)
	byKey(s.VMIs)
	slices.SortFunc(s.Pods, func(a, b PodRef) int {
		return cmp.Or(cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Name, b.Name))
	})
}
