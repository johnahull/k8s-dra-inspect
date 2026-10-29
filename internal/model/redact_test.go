package model_test

import (
	"encoding/json"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/resource"
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
		"spec": map[string]any{
			"accessCredentials": []any{map[string]any{"sshPublicKey": "SECRET-KEY"}},
			"volumes": []any{
				map[string]any{"name": "ci", "cloudInitNoCloud": map[string]any{
					"userData": "SECRET-UD", "userDataBase64": "SECRET-UD64", "networkData": "SECRET-ND", "networkDataBase64": "SECRET-ND64",
					"secretRef": map[string]any{"name": "keep-ref"}, "userDataSecretRef": map[string]any{"name": "keep-udref"},
				}},
				map[string]any{"name": "cd", "cloudInitConfigDrive": map[string]any{"userData": "SECRET-CD"}},
				map[string]any{"name": "disk", "containerDisk": map[string]any{"image": "img"}},
			},
			"template": map[string]any{"spec": map[string]any{
				"accessCredentials": []any{map[string]any{"sshPublicKey": "SECRET-TKEY"}},
				"volumes":           []any{map[string]any{"name": "ci", "cloudInitNoCloud": map[string]any{"userData": "SECRET-TUD"}}},
			}},
		},
	}}
	return model.Snapshot{
		SchemaVersion:  model.SchemaVersion,
		Source:         model.SourceLive,
		Evidence:       map[string]json.RawMessage{"adapter": json.RawMessage(`{"note":"SECRET-EVIDENCE"}`)},
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
	// Evidence is the adapter producers' responsibility: Redact must leave it alone.
	if string(out.Evidence["adapter"]) != `{"note":"SECRET-EVIDENCE"}` {
		t.Errorf("evidence changed: %s", out.Evidence["adapter"])
	}
	if rest := strings.ReplaceAll(string(b), "SECRET-EVIDENCE", ""); strings.Contains(rest, "SECRET") {
		t.Fatalf("secret survived: %s", b)
	}
	vols, _, _ := unstructured.NestedSlice(out.VMIs[0].Object, "spec", "volumes")
	if len(vols) != 3 {
		t.Fatalf("volumes lost: %v", vols)
	}
	ci := vols[0].(map[string]any)["cloudInitNoCloud"].(map[string]any)
	if len(ci) != 2 || ci["secretRef"] == nil || ci["userDataSecretRef"] == nil {
		t.Errorf("secret refs must stay, inline data go: %v", ci)
	}
	if cd := vols[1].(map[string]any)["cloudInitConfigDrive"].(map[string]any); len(cd) != 0 {
		t.Errorf("configdrive: %v", cd)
	}
	if vols[2].(map[string]any)["containerDisk"] == nil {
		t.Error("other volume lost")
	}
	if _, ok, _ := unstructured.NestedFieldNoCopy(out.VMIs[0].Object, "spec", "accessCredentials"); ok {
		t.Error("accessCredentials kept")
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

// Identifiers are built in code (documentation ranges only) so no fixture
// under testdata/ needs to carry them.
func identNode() corev1.Node {
	return corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node-a", UID: "uid-LEAK", ResourceVersion: "rv-LEAK",
			Labels:        map[string]string{"kubernetes.io/hostname": "node-a", "zone": "z1"},
			Annotations:   map[string]string{"csi.volume.kubernetes.io/nodeid": "ANNOT-LEAK", lastApplied: "SECRET-APPLIED"},
			ManagedFields: []metav1.ManagedFieldsEntry{{Manager: "SECRET-MANAGER"}},
		},
		Spec: corev1.NodeSpec{ProviderID: "aws:///PROVIDER-LEAK", PodCIDR: "192.0.2.0/24", PodCIDRs: []string{"192.0.2.0/24"},
			Unschedulable: true, Taints: []corev1.Taint{{Key: "TAINT-LEAK"}}},
		Status: corev1.NodeStatus{
			Allocatable:     corev1.ResourceList{"cpu": resource.MustParse("8")},
			Capacity:        corev1.ResourceList{"cpu": resource.MustParse("16")},
			Addresses:       []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "10." + "1.2.3"}, {Type: corev1.NodeHostName, Address: "host.example.com"}},
			NodeInfo:        corev1.NodeSystemInfo{MachineID: "MACHINEID-LEAK", SystemUUID: "SYSUUID-LEAK", BootID: "BOOTID-LEAK"},
			Images:          []corev1.ContainerImage{{Names: []string{"IMAGE-LEAK"}}},
			VolumesInUse:    []corev1.UniqueVolumeName{"VOLINUSE-LEAK"},
			VolumesAttached: []corev1.AttachedVolume{{Name: "VOLATTACHED-LEAK"}},
			DaemonEndpoints: corev1.NodeDaemonEndpoints{KubeletEndpoint: corev1.DaemonEndpoint{Port: 10250}},
			Conditions:      []corev1.NodeCondition{{Type: "Ready", Message: "COND-LEAK"}},
		},
	}
}

func TestRedactNodeKeepsOnlyNameLabelsAllocatable(t *testing.T) {
	in := model.Snapshot{Nodes: []corev1.Node{identNode()}}
	before, _ := json.Marshal(in)
	out := model.Redact(in)
	b, _ := json.Marshal(out)
	for _, leak := range []string{"10." + "1.2.3", "host.example.com", "MACHINEID-LEAK", "SYSUUID-LEAK", "BOOTID-LEAK", "IMAGE-LEAK",
		"VOLINUSE-LEAK", "VOLATTACHED-LEAK", "10250", "ANNOT-LEAK", "SECRET-APPLIED", "SECRET-MANAGER", "PROVIDER-LEAK",
		"192.0.2.0", "uid-LEAK", "rv-LEAK", "TAINT-LEAK", "COND-LEAK"} {
		if strings.Contains(string(b), leak) {
			t.Errorf("redacted bytes contain %q", leak)
		}
	}
	n := out.Nodes[0]
	if n.Name != "node-a" || n.Labels["zone"] != "z1" || n.Labels["kubernetes.io/hostname"] != "node-a" || len(n.Labels) != 2 {
		t.Errorf("name/labels lost: %+v", n.ObjectMeta)
	}
	if cpu := n.Status.Allocatable["cpu"]; cpu.String() != "8" || len(n.Status.Allocatable) != 1 {
		t.Errorf("allocatable lost: %v", n.Status.Allocatable)
	}
	if n.Status.Capacity != nil || n.Spec.Unschedulable {
		t.Errorf("unexpected kept fields: %+v", n.Status.Capacity)
	}
	if after, _ := json.Marshal(in); string(before) != string(after) {
		t.Fatal("input mutated")
	}
	if twice, _ := json.Marshal(model.Redact(out)); string(twice) != string(b) {
		t.Fatalf("not idempotent:\n%s\n%s", b, twice)
	}
}

func TestRedactVMIDropsStatusIdentifiersAndFirmware(t *testing.T) {
	fw := func() map[string]any {
		return map[string]any{"domain": map[string]any{"firmware": map[string]any{"uuid": "FWUUID-LEAK", "serial": "FWSERIAL-LEAK", "bootloader": map[string]any{"efi": map[string]any{}}}, "devices": map[string]any{"gpus": []any{map[string]any{"name": "g"}}}}}
	}
	spec := fw()
	spec["template"] = map[string]any{"spec": fw()}
	vmi := unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": "vmi"},
		"spec":     spec,
		"status": map[string]any{
			"nodeName":     "node-a",
			"phase":        "Running",
			"interfaces":   []any{map[string]any{"ipAddress": "IFACE-LEAK", "mac": "MAC-LEAK"}},
			"guestOSInfo":  map[string]any{"name": "GUEST-LEAK"},
			"volumeStatus": []any{map[string]any{"name": "VOLSTATUS-LEAK"}},
		},
	}}
	in := model.Snapshot{VMIs: []unstructured.Unstructured{vmi}}
	before, _ := json.Marshal(in)
	out := model.Redact(in)
	b, _ := json.Marshal(out)
	for _, leak := range []string{"FWUUID-LEAK", "FWSERIAL-LEAK", "IFACE-LEAK", "MAC-LEAK", "GUEST-LEAK", "VOLSTATUS-LEAK"} {
		if strings.Contains(string(b), leak) {
			t.Errorf("redacted bytes contain %q", leak)
		}
	}
	o := out.VMIs[0].Object
	for _, p := range [][]string{{"status", "nodeName"}, {"status", "phase"}, {"spec", "domain", "devices", "gpus"}, {"spec", "domain", "firmware", "bootloader"},
		{"spec", "template", "spec", "domain", "devices", "gpus"}, {"spec", "template", "spec", "domain", "firmware", "bootloader"}} {
		if _, ok, _ := unstructured.NestedFieldNoCopy(o, p...); !ok {
			t.Errorf("%v lost", p)
		}
	}
	if after, _ := json.Marshal(in); string(before) != string(after) {
		t.Fatal("input mutated")
	}
	if twice, _ := json.Marshal(model.Redact(out)); string(twice) != string(b) {
		t.Fatal("not idempotent")
	}
}
