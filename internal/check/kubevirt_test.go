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
