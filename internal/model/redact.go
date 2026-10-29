package model

import (
	"encoding/json"
	"maps"
	"slices"

	corev1 "k8s.io/api/core/v1"
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
// cloud-init data and access credentials. Nodes keep only name, labels and
// status.allocatable; VMIs keep only the allow-list in redactVMI. Evidence is
// left as is. It is idempotent.
func Redact(s Snapshot) Snapshot {
	out := s
	out.Redacted = true
	out.Collection = maps.Clone(s.Collection)
	out.ResourceSlices = deepCopy(s.ResourceSlices)
	out.DeviceClasses = deepCopy(s.DeviceClasses)
	out.ResourceClaims = deepCopy(s.ResourceClaims)
	out.Nodes = deepCopy(s.Nodes)
	out.VMIs = nil
	if s.VMIs != nil {
		out.VMIs = make([]unstructured.Unstructured, len(s.VMIs))
	}
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
		n := &out.Nodes[i]
		// Allow-list: addresses, machine IDs, images, volumes and annotations never survive.
		*n = corev1.Node{
			TypeMeta:   n.TypeMeta,
			ObjectMeta: metav1.ObjectMeta{Name: n.Name, Labels: n.Labels},
			Status:     corev1.NodeStatus{Allocatable: n.Status.Allocatable},
		}
	}
	for i := range s.VMIs {
		out.VMIs[i] = unstructured.Unstructured{Object: redactVMI(s.VMIs[i].Object)}
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

// redactVMI rebuilds a VMI from an allow-list of what replay and the kubevirt
// adapter read. Everything else (labels, annotations, owners, networks,
// volumes, cloud-init, credentials, status details) is never copied, and a
// value of an unexpected type is dropped. The result shares nothing with obj.
func redactVMI(obj map[string]any) map[string]any {
	out := map[string]any{}
	keep(out, obj, "apiVersion", "kind")
	if m := sub(obj, "metadata"); m != nil {
		put(out, "metadata", pick(m, "name", "namespace", "uid"))
	}
	if st := sub(obj, "status"); st != nil {
		put(out, "status", pick(st, "nodeName", "phase"))
	}
	spec := map[string]any{}
	if sp := sub(obj, "spec"); sp != nil {
		if l, ok := sp["resourceClaims"].([]any); ok {
			var cs []any
			for _, e := range l {
				if m, ok := e.(map[string]any); ok {
					cs = append(cs, pick(m, "name", "resourceClaimName", "resourceClaimTemplateName"))
				}
			}
			if cs != nil {
				spec["resourceClaims"] = cs
			}
		}
		dom := map[string]any{}
		d := sub(sp, "domain")
		if cpu := sub(d, "cpu"); cpu != nil {
			c := map[string]any{}
			for _, k := range []string{"sockets", "cores", "threads"} {
				switch n := cpu[k].(type) {
				case int64, float64, int:
					c[k] = n
				}
			}
			if b, ok := cpu["dedicatedCpuPlacement"].(bool); ok {
				c["dedicatedCpuPlacement"] = b
			}
			if n := sub(cpu, "numa"); n != nil {
				numa := map[string]any{}
				if n["guestMappingPassthrough"] != nil {
					numa["guestMappingPassthrough"] = map[string]any{} // only presence is read
				}
				put(c, "numa", numa)
			}
			put(dom, "cpu", c)
		}
		if l, ok := sub(d, "devices")["hostDevices"].([]any); ok {
			var hs []any
			for _, e := range l {
				if m, ok := e.(map[string]any); ok {
					hs = append(hs, pick(m, "name", "claimName", "requestName"))
				}
			}
			if hs != nil {
				put(dom, "devices", map[string]any{"hostDevices": hs})
			}
		}
		put(spec, "domain", dom)
	}
	put(out, "spec", spec)
	return out
}

// sub returns m[k] as a map, or nil; a nil m is fine.
func sub(m map[string]any, k string) map[string]any {
	x, _ := m[k].(map[string]any)
	return x
}

// put sets m[k]=v unless v is empty.
func put(m map[string]any, k string, v map[string]any) {
	if len(v) > 0 {
		m[k] = v
	}
}

// pick returns the string values of src under keys.
func pick(src map[string]any, keys ...string) map[string]any {
	out := map[string]any{}
	keep(out, src, keys...)
	return out
}

func keep(dst, src map[string]any, keys ...string) {
	for _, k := range keys {
		if s, ok := src[k].(string); ok {
			dst[k] = s
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
