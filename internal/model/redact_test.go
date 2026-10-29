package model_test

import (
	"encoding/json"
	"strings"
	"testing"

	resourcev1 "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
)

const lastApplied = "kubectl.kubernetes.io/last-applied-configuration"

func secretMeta(name string) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Name:          name,
		Annotations:   map[string]string{lastApplied: "SECRET-APPLIED", "keep": "yes"},
		ManagedFields: []metav1.ManagedFieldsEntry{{Manager: "SECRET-MANAGER"}},
	}
}

func params() runtime.RawExtension {
	return runtime.RawExtension{Raw: []byte(`{"token":"SECRET-PARAM"}`)}
}

func redactInput() model.Snapshot {
	opaque := func() resourcev1.DeviceConfiguration {
		return resourcev1.DeviceConfiguration{Opaque: &resourcev1.OpaqueDeviceConfiguration{Driver: "gpu.example.com", Parameters: params()}}
	}
	vmi := unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "kubevirt.io/v1", "kind": "VirtualMachineInstance",
		"metadata": map[string]any{
			"name":          "vmi",
			"annotations":   map[string]any{lastApplied: "SECRET-APPLIED"},
			"managedFields": []any{map[string]any{"manager": "SECRET-MANAGER"}},
		},
	}}
	return model.Snapshot{
		SchemaVersion:  model.SchemaVersion,
		Source:         "live",
		ResourceSlices: []resourcev1.ResourceSlice{{ObjectMeta: secretMeta("s"), Spec: resourcev1.ResourceSliceSpec{Driver: "gpu.example.com"}}},
		DeviceClasses: []resourcev1.DeviceClass{{ObjectMeta: secretMeta("dc"), Spec: resourcev1.DeviceClassSpec{
			Config: []resourcev1.DeviceClassConfiguration{{DeviceConfiguration: opaque()}},
		}}},
		ResourceClaims: []resourcev1.ResourceClaim{{
			ObjectMeta: secretMeta("c"),
			Spec: resourcev1.ResourceClaimSpec{Devices: resourcev1.DeviceClaim{
				Requests: []resourcev1.DeviceRequest{{Name: "r"}},
				Config:   []resourcev1.DeviceClaimConfiguration{{Requests: []string{"r"}, DeviceConfiguration: opaque()}},
			}},
			Status: resourcev1.ResourceClaimStatus{
				Allocation: &resourcev1.AllocationResult{Devices: resourcev1.DeviceAllocationResult{
					Results: []resourcev1.DeviceRequestAllocationResult{{Request: "r", Driver: "gpu.example.com", Pool: "p", Device: "d"}},
					Config:  []resourcev1.DeviceAllocationConfiguration{{Source: resourcev1.AllocationConfigSourceClaim, DeviceConfiguration: opaque()}},
				}},
				Devices: []resourcev1.AllocatedDeviceStatus{{Driver: "gpu.example.com", Pool: "p", Device: "d",
					Data: &runtime.RawExtension{Raw: []byte(`{"ip":"SECRET-DATA"}`)}}},
			},
		}},
		VMIs: []unstructured.Unstructured{vmi},
	}
}

func TestRedact(t *testing.T) {
	in := redactInput()
	before, _ := json.Marshal(in)
	out := model.Redact(in)

	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "SECRET") {
		t.Fatalf("secret survived: %s", b)
	}
	if !out.Redacted {
		t.Error("Redacted not set")
	}
	// Everything else is kept.
	if out.ResourceSlices[0].Annotations["keep"] != "yes" || out.ResourceSlices[0].Spec.Driver != "gpu.example.com" {
		t.Error("slice data lost")
	}
	if o := out.DeviceClasses[0].Spec.Config[0].Opaque; o == nil || o.Driver != "gpu.example.com" {
		t.Error("deviceclass driver lost")
	}
	c := out.ResourceClaims[0]
	if c.Spec.Devices.Config[0].Opaque.Driver != "gpu.example.com" || c.Spec.Devices.Config[0].Requests[0] != "r" ||
		c.Status.Allocation.Devices.Config[0].Opaque.Driver != "gpu.example.com" ||
		c.Status.Allocation.Devices.Results[0].Device != "d" ||
		c.Status.Devices[0].Device != "d" || c.Status.Devices[0].Data != nil {
		t.Errorf("claim data lost or data kept: %+v", c.Status.Devices)
	}
	if out.VMIs[0].GetName() != "vmi" || len(out.VMIs[0].GetManagedFields()) != 0 || len(out.VMIs[0].GetAnnotations()) != 0 {
		t.Errorf("vmi: %v", out.VMIs[0].Object)
	}

	after, _ := json.Marshal(in)
	if string(before) != string(after) {
		t.Fatal("input mutated")
	}
	if in.Redacted {
		t.Error("input Redacted flipped")
	}

	twice, _ := json.Marshal(model.Redact(out))
	if string(twice) != string(b) {
		t.Fatalf("not idempotent:\n%s\n%s", b, twice)
	}
}

func TestRedactNilSafe(t *testing.T) {
	s := model.Snapshot{ResourceClaims: []resourcev1.ResourceClaim{{
		Spec: resourcev1.ResourceClaimSpec{Devices: resourcev1.DeviceClaim{
			Config: []resourcev1.DeviceClaimConfiguration{{DeviceConfiguration: resourcev1.DeviceConfiguration{}}}}},
		Status: resourcev1.ResourceClaimStatus{Devices: []resourcev1.AllocatedDeviceStatus{{Data: ptr.To(runtime.RawExtension{})}}},
	}}}
	_ = model.Redact(s)
}
