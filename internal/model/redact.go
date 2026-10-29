package model

import (
	"encoding/json"
	"maps"
	"slices"

	resourcev1 "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

const lastAppliedAnnotation = "kubectl.kubernetes.io/last-applied-configuration"

func deepCopy[T any, P interface {
	*T
	DeepCopy() *T
}](in []T) []T {
	if in == nil {
		return nil
	}
	out := make([]T, len(in))
	for i := range in {
		out[i] = *P(&in[i]).DeepCopy()
	}
	return out
}

// Redact returns a deep copy of s without opaque driver parameters, device
// status data, last-applied annotations, managedFields, or inline VMI
// cloud-init data and access credentials. Evidence is left as is. It is idempotent.
func Redact(s Snapshot) Snapshot {
	out := s
	out.Redacted = true
	out.Collection = maps.Clone(s.Collection)
	out.ResourceSlices = deepCopy(s.ResourceSlices)
	out.DeviceClasses = deepCopy(s.DeviceClasses)
	out.ResourceClaims = deepCopy(s.ResourceClaims)
	out.Nodes = deepCopy(s.Nodes)
	out.VMIs = deepCopy(s.VMIs)
	out.Pods = slices.Clone(s.Pods)
	for i := range out.Pods {
		out.Pods[i].ResourceClaims = deepCopy(out.Pods[i].ResourceClaims)
		out.Pods[i].ResourceClaimStatuses = deepCopy(out.Pods[i].ResourceClaimStatuses)
	}
	if s.Evidence != nil {
		out.Evidence = make(map[string]json.RawMessage, len(s.Evidence))
		for k, v := range s.Evidence {
			out.Evidence[k] = slices.Clone(v)
		}
	}

	for i := range out.ResourceSlices {
		scrubMeta(&out.ResourceSlices[i])
	}
	for i := range out.Nodes {
		scrubMeta(&out.Nodes[i])
	}
	for i := range out.VMIs {
		scrubMeta(&out.VMIs[i])
		for _, base := range [][]string{{"spec"}, {"spec", "template", "spec"}} {
			scrubVMISpec(out.VMIs[i].Object, base)
		}
	}
	for i := range out.DeviceClasses {
		dc := &out.DeviceClasses[i]
		scrubMeta(dc)
		for j := range dc.Spec.Config {
			dropParameters(&dc.Spec.Config[j].DeviceConfiguration)
		}
	}
	for i := range out.ResourceClaims {
		c := &out.ResourceClaims[i]
		scrubMeta(c)
		for j := range c.Spec.Devices.Config {
			dropParameters(&c.Spec.Devices.Config[j].DeviceConfiguration)
		}
		if a := c.Status.Allocation; a != nil {
			for j := range a.Devices.Config {
				dropParameters(&a.Devices.Config[j].DeviceConfiguration)
			}
		}
		for j := range c.Status.Devices {
			c.Status.Devices[j].Data = nil
		}
	}
	return out
}

// scrubVMISpec drops inline cloud-init data and accessCredentials under the
// map at path; secret references (names only) stay.
func scrubVMISpec(obj map[string]any, path []string) {
	spec, ok, _ := unstructured.NestedFieldNoCopy(obj, path...)
	m, isMap := spec.(map[string]any)
	if !ok || !isMap {
		return
	}
	delete(m, "accessCredentials")
	vols, _ := m["volumes"].([]any)
	for _, v := range vols {
		vm, _ := v.(map[string]any)
		for _, k := range []string{"cloudInitNoCloud", "cloudInitConfigDrive"} {
			if ci, _ := vm[k].(map[string]any); ci != nil {
				for _, f := range []string{"userData", "userDataBase64", "networkData", "networkDataBase64"} {
					delete(ci, f)
				}
			}
		}
	}
}

func dropParameters(c *resourcev1.DeviceConfiguration) {
	if c.Opaque != nil {
		c.Opaque.Parameters = runtime.RawExtension{}
	}
}

func scrubMeta(o metav1.Object) {
	o.SetManagedFields(nil)
	if a := o.GetAnnotations(); a != nil {
		delete(a, lastAppliedAnnotation)
		if len(a) == 0 {
			a = nil
		}
		o.SetAnnotations(a)
	}
}
