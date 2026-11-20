package analyze

import (
	"fmt"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
	resourcev1 "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// FromModel adapts the persisted dra-inspect snapshot to the pointer-based
// Kubernetes objects consumed by the pure analyzers.
func FromModel(s model.Snapshot) *Snapshot {
	out := &Snapshot{}
	pluginGroups := map[string]string{}
	for i := range s.Nodes {
		out.Nodes = append(out.Nodes, s.Nodes[i].DeepCopy())
	}
	for i := range s.Pods {
		out.Pods = append(out.Pods, s.Pods[i].Pod())
	}
	for i := range s.NodePluginPods {
		pod := s.NodePluginPods[i].Pod()
		if s.Namespace != "" && pod.Namespace != s.Namespace {
			pod.Namespace, pod.Name, pod.UID = "outside namespace", fmt.Sprintf("plugin-%d", i+1), ""
			if owner := metav1.GetControllerOfNoCopy(pod); owner != nil {
				key := string(owner.UID)
				if key == "" {
					key = fmt.Sprintf("%s/%s/%s/%s", owner.APIVersion, owner.Kind, s.NodePluginPods[i].Namespace, owner.Name)
				}
				group, ok := pluginGroups[key]
				if !ok {
					group = fmt.Sprintf("plugin-group-%d", len(pluginGroups)+1)
					pluginGroups[key] = group
				}
				maskedOwner := *owner.DeepCopy()
				maskedOwner.Name, maskedOwner.UID = group, ""
				pod.OwnerReferences = []metav1.OwnerReference{maskedOwner}
			} else {
				pod.OwnerReferences = nil
			}
		}
		out.NodePluginPods = append(out.NodePluginPods, pod)
	}
	for i := range s.Events {
		out.Events = append(out.Events, s.Events[i].DeepCopy())
	}
	for i := range s.DeviceClasses {
		out.DeviceClasses = append(out.DeviceClasses, s.DeviceClasses[i].DeepCopy())
	}
	for i := range s.ResourceSlices {
		out.ResourceSlices = append(out.ResourceSlices, s.ResourceSlices[i].DeepCopy())
	}
	for i := range s.ResourceClaims {
		out.ResourceClaims = append(out.ResourceClaims, s.ResourceClaims[i].DeepCopy())
	}
	for i := range s.GlobalAllocations {
		out.ResourceClaims = append(out.ResourceClaims, &resourcev1.ResourceClaim{
			ObjectMeta: metav1.ObjectMeta{Name: "allocated outside namespace"},
			Status:     resourcev1.ResourceClaimStatus{Allocation: s.GlobalAllocations[i].DeepCopy()},
		})
		out.outsideClaims++
	}
	for i := range s.DeviceTaintRules {
		out.DeviceTaintRules = append(out.DeviceTaintRules, s.DeviceTaintRules[i].DeepCopy())
	}
	return out
}
