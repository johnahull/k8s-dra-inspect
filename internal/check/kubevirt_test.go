package check

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/johnahull/k8s-dra-inspect/internal/adapters/kubevirt"
	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/testutil"
)

func kvSnap(t *testing.T) model.Snapshot {
	t.Helper()
	return testutil.LoadFixture(t, "testdata/fixtures/kubevirt")
}

func kvOne(t *testing.T, snap model.Snapshot, target string) model.Result {
	t.Helper()
	return one(t, KubeVirt(target, ""), snap)
}

func TestKubeVirtBranches(t *testing.T) {
	snap := kvSnap(t)
	for _, tc := range []struct {
		vmi    string
		status model.Status
		reason string
	}{
		{"vm-ok", model.StatusPass, ""},
		{"vm-secrets", model.StatusPass, ""},
		{"vm-garbage", model.StatusPass, ""},
		{"vm-missing-claim", model.StatusFail, "claim demo/no-such-claim not found"},
		{"vm-no-allocation", model.StatusFail, "request absent has no allocation result in claim demo/claim-ok"},
		{"vm-wrong-node", model.StatusFail, "node node-b.example.com, but the VMI runs on node-a.example.com"},
		{"vm-pending", model.StatusUnavailable, "VMI is not scheduled to a node"},
		{"vm-split", model.StatusWarn, "NUMA nodes 0,1 without guestMappingPassthrough"},
	} {
		t.Run(tc.vmi, func(t *testing.T) {
			r := kvOne(t, snap, "demo/"+tc.vmi)
			if r.Check != NameKubeVirt || r.Subject != "demo/"+tc.vmi {
				t.Fatalf("%+v", r)
			}
			if tc.status == model.StatusPass {
				if r.Status != tc.status {
					t.Fatalf("%+v", r)
				}
				return
			}
			wantStatus(t, r, tc.status, tc.reason)
		})
	}
}

func TestKubeVirtDetails(t *testing.T) {
	r := kvOne(t, kvSnap(t), "vm-ok")
	if !reflect.DeepEqual(r.Details["numa"], []string{"0", "1"}) || r.Details["guestNUMAPassthrough"] != true || r.Details["dedicatedCPUPlacement"] != true {
		t.Fatalf("%+v", r.Details)
	}
	devs, _ := r.Details["devices"].([]map[string]any)
	if len(devs) != 3 || devs[0]["device"] != "gpu-0" || devs[0]["pciBusID"] != "0000:3b:00.0" || devs[2]["request"] != "gpu2/sub" || devs[2]["driver"] != "gpu.example.com" || devs[2]["pool"] != "node-a.example.com" {
		t.Fatalf("%+v", devs)
	}
}

func TestKubeVirtSplitWithPassthroughIsPass(t *testing.T) {
	snap := kvSnap(t)
	// vm-split without passthrough warns (above); the same devices with it do not.
	for i := range snap.VMIs {
		if snap.VMIs[i].GetName() == "vm-split" {
			snap.VMIs[i].Object["spec"].(map[string]any)["domain"].(map[string]any)["cpu"].(map[string]any)["numa"] = map[string]any{"guestMappingPassthrough": map[string]any{}}
		}
	}
	if r := kvOne(t, snap, "vm-split"); r.Status != model.StatusPass {
		t.Fatalf("%+v", r)
	}
}

func TestKubeVirtAllVMIsAndTargets(t *testing.T) {
	snap := kvSnap(t)
	all := run(KubeVirt("", ""), snap)
	if len(all) != 8 {
		t.Fatalf("want 8 results, got %d", len(all))
	}
	if n := len(run(KubeVirt("", "other"), snap)); n != 1 {
		t.Fatalf("namespace other: %d results", n)
	}
	r := kvOne(t, snap, "nope")
	wantStatus(t, r, model.StatusUnavailable, "no VMI named nope")
	if got := kvOne(t, snap, "demo/vm-ok").Subject; got != "demo/vm-ok" {
		t.Fatal(got)
	}
	if n := len(run(KubeVirt("vm-ok", "demo"), snap)); n != 1 {
		t.Fatal(n)
	}
}

func TestKubeVirtNoVMIs(t *testing.T) {
	snap := kvSnap(t)
	snap.VMIs = nil
	r := kvOne(t, snap, "")
	if r.Status != model.StatusPass || !strings.Contains(fmt.Sprint(r.Details["summary"]), "no VirtualMachineInstances") {
		t.Fatalf("%+v", r)
	}
}

func TestKubeVirtCollectionNotOK(t *testing.T) {
	snap := kvSnap(t)
	snap.Collection["virtualmachineinstances"] = model.CollectionStatus{State: model.StateNotFound, Reason: "kubevirt.io/v1 not served"}
	wantStatus(t, kvOne(t, snap, ""), model.StatusUnavailable, "kubevirt.io/v1 not served")
	snap.Collection["virtualmachineinstances"] = model.CollectionStatus{State: model.StateForbidden}
	wantStatus(t, kvOne(t, snap, ""), model.StatusUnavailable, "virtualmachineinstances collection state: forbidden")
	// Missing DRA collections make device checks unavailable, not pass.
	snap = kvSnap(t)
	snap.Collection["resourceclaims"] = model.CollectionStatus{State: model.StateForbidden, Reason: "no access"}
	wantStatus(t, kvOne(t, snap, "vm-ok"), model.StatusUnavailable, "no access")
}

func TestKubeVirtUnresolvedClaims(t *testing.T) {
	snap := kvSnap(t)
	u := &snap.VMIs[0]
	for i := range snap.VMIs {
		if snap.VMIs[i].GetName() == "vm-ok" {
			u = &snap.VMIs[i]
		}
	}
	spec := u.Object["spec"].(map[string]any)
	spec["resourceClaims"] = []any{map[string]any{"name": "partitions", "resourceClaimTemplateName": "tmpl"}}
	wantStatus(t, kvOne(t, snap, "vm-ok"), model.StatusUnavailable, "claim partitions comes from a template")
	spec["resourceClaims"] = []any{}
	wantStatus(t, kvOne(t, snap, "vm-ok"), model.StatusFail, "claim alias partitions is not defined in spec.resourceClaims")
}

func TestKubeVirtUsesEvidenceFromSnapshot(t *testing.T) {
	snap := kvSnap(t)
	want := run(KubeVirt("", ""), snap)
	raw, err := kubevirt.New(kubevirt.Config{VMIs: snap.VMIs, State: snap.Collection["virtualmachineinstances"]}).Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	snap.VMIs = nil // only the evidence can supply them now
	snap.Evidence = map[string]json.RawMessage{kubevirt.Name: raw}
	if got := run(KubeVirt("", ""), snap); !reflect.DeepEqual(got, want) {
		t.Fatalf("evidence replay differs\n got %+v\nwant %+v", got, want)
	}
	snap.Evidence[kubevirt.Name] = json.RawMessage(`{"version":99}`)
	wantStatus(t, kvOne(t, snap, ""), model.StatusUnavailable, "unsupported kubevirt evidence version 99")
	snap.Evidence[kubevirt.Name] = json.RawMessage(`{`)
	wantStatus(t, kvOne(t, snap, ""), model.StatusUnavailable, "invalid kubevirt evidence in snapshot")
}

func TestKubeVirtDetailsNeverCarrySecrets(t *testing.T) {
	b, err := json.Marshal(run(KubeVirt("", ""), kvSnap(t)))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "SECRET-") || strings.Contains(string(b), "2001:db8") {
		t.Fatalf("secret in results: %s", b)
	}
	if !strings.Contains(string(b), "warnings") {
		t.Fatalf("garbage VMI warnings missing: %s", b)
	}
}

func TestKubeVirtDetailsListsAreNeverNull(t *testing.T) {
	b, _ := json.Marshal(kvOne(t, kvSnap(t), "vm-missing-claim").Details)
	if strings.Contains(string(b), "null") {
		t.Fatalf("null in details: %s", b)
	}
}

func TestKubeVirtDeviceWithoutNodeIsUnavailable(t *testing.T) {
	snap := kvSnap(t)
	for i := range snap.ResourceSlices {
		snap.ResourceSlices[i].Spec.NodeName = nil // e.g. a network-attached pool
	}
	wantStatus(t, kvOne(t, snap, "vm-ok"), model.StatusUnavailable, "has no known node")
}

func TestKubeVirtClaimInOtherNamespaceDoesNotSatisfy(t *testing.T) {
	snap := kvSnap(t)
	for i := range snap.ResourceClaims {
		if snap.ResourceClaims[i].Name == "claim-b" {
			snap.ResourceClaims[i].Namespace = "elsewhere"
		}
	}
	wantStatus(t, kvOne(t, snap, "demo/vm-wrong-node"), model.StatusFail, "claim demo/claim-b not found")
}

func TestKubeVirtRequestPrefixRule(t *testing.T) {
	setRequest := func(req string) model.Snapshot {
		snap := kvSnap(t)
		for i := range snap.ResourceClaims {
			if snap.ResourceClaims[i].Name == "claim-ok" {
				snap.ResourceClaims[i].Status.Allocation.Devices.Results[0].Request = req
			}
		}
		return snap
	}
	// vm-ok's hostDevice request gpu0 must not match a result named gpu01 ...
	wantStatus(t, kvOne(t, setRequest("gpu01"), "demo/vm-ok"), model.StatusFail, "request gpu0 has no allocation result")
	// ... but does match the sub-request gpu0/sub.
	if r := kvOne(t, setRequest("gpu0/sub"), "demo/vm-ok"); r.Status != model.StatusPass {
		t.Fatalf("%+v", r)
	}
}

func setDedicated(t *testing.T, snap model.Snapshot, vmi string, dedicated bool) {
	t.Helper()
	for i := range snap.VMIs {
		if snap.VMIs[i].GetName() == vmi {
			snap.VMIs[i].Object["spec"].(map[string]any)["domain"].(map[string]any)["cpu"].(map[string]any)["dedicatedCpuPlacement"] = dedicated
		}
	}
}

func TestKubeVirtPassthroughNeedsDedicatedCPUs(t *testing.T) {
	snap := kvSnap(t)
	if r := kvOne(t, snap, "demo/vm-ok"); r.Status != model.StatusPass {
		t.Fatalf("dedicated: %+v", r)
	}
	setDedicated(t, snap, "vm-ok", false)
	wantStatus(t, kvOne(t, snap, "demo/vm-ok"), model.StatusWarn, "NUMA passthrough requires dedicated CPU placement; KubeVirt will not schedule such a VMI")
}

func kvEvidenceSnap(t *testing.T, ev kubevirt.Evidence) model.Snapshot {
	t.Helper()
	snap := kvSnap(t)
	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	snap.Evidence = map[string]json.RawMessage{kubevirt.Name: raw}
	return snap
}

func TestKubeVirtFromEvidenceDuplicateVMIs(t *testing.T) {
	v := kubevirt.VMI{Namespace: "demo", Name: "dup", Claims: map[string]string{}}
	rs := run(KubeVirt("", ""), kvEvidenceSnap(t, kubevirt.Evidence{Version: 1, VMIs: []kubevirt.VMI{v, v}}))
	if len(rs) != 1 || fmt.Sprint(rs[0].Details["warnings"]) != "[duplicate VMIs ignored]" {
		t.Fatalf("%+v", rs)
	}
}

func TestKubeVirtFromEvidenceBoundsHugeStrings(t *testing.T) {
	long := strings.Repeat("x", 10<<20)
	v := kubevirt.VMI{Namespace: "demo", Name: long, NodeName: "node-a.example.com",
		Claims:      map[string]string{long: long},
		HostDevices: []kubevirt.HostDevice{{Name: long, ClaimAlias: long, ClaimName: long, RequestName: long}}}
	rs := run(KubeVirt("", ""), kvEvidenceSnap(t, kubevirt.Evidence{Version: 1, VMIs: []kubevirt.VMI{v}}))
	raw, _ := json.Marshal(rs)
	if len(rs) != 1 || len(raw) > 16<<10 {
		t.Fatalf("%d results, %d bytes of output", len(rs), len(raw))
	}
}

func TestKubeVirtFromEvidenceCapsMaps(t *testing.T) {
	claims := map[string]string{}
	for i := 0; i < 5000; i++ {
		claims[fmt.Sprint("a", i)] = "c"
	}
	var hds []kubevirt.HostDevice
	for i := 0; i < 5000; i++ {
		hds = append(hds, kubevirt.HostDevice{Name: "h", ClaimAlias: "a0", ClaimName: "c", RequestName: "r"})
	}
	v := kubevirt.VMI{Namespace: "demo", Name: "big", Claims: claims, HostDevices: hds}
	rs := run(KubeVirt("", ""), kvEvidenceSnap(t, kubevirt.Evidence{Version: 1, VMIs: []kubevirt.VMI{v}}))
	w := fmt.Sprint(rs[0].Details["warnings"])
	if len(rs) != 1 || !strings.Contains(w, "claims truncated at 1024") || !strings.Contains(w, "hostDevices truncated at 1024") {
		t.Fatalf("warnings %s", w)
	}
	if d, _ := rs[0].Details["devices"].([]map[string]any); len(d) > 1024 {
		t.Fatalf("%d devices", len(d))
	}
}

// perDevice moves every slice's node binding down to its devices
// (PerDeviceNodeSelection); node, when set, replaces the slice's node.
func perDevice(snap model.Snapshot, node string) {
	for i := range snap.ResourceSlices {
		sp := &snap.ResourceSlices[i].Spec
		n := node
		if n == "" && sp.NodeName != nil {
			n = *sp.NodeName
		}
		t := true
		sp.PerDeviceNodeSelection, sp.NodeName = &t, nil
		for j := range sp.Devices {
			sp.Devices[j].NodeName = &n
		}
	}
}

func TestKubeVirtPerDeviceNodeSelection(t *testing.T) {
	snap := kvSnap(t)
	perDevice(snap, "")
	if r := kvOne(t, snap, "vm-ok"); r.Status != model.StatusPass {
		t.Fatalf("device on the VMI's node must pass: %+v", r)
	}
	snap = kvSnap(t)
	perDevice(snap, "node-z.example.com")
	wantStatus(t, kvOne(t, snap, "vm-ok"), model.StatusFail, "node node-z.example.com, but the VMI runs on node-a.example.com")
}
