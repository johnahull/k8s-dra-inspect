package kubevirt

import (
	"context"
	"encoding/json"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"reflect"
	"strings"
	"testing"

	"github.com/johnahull/k8s-dra-inspect/internal/adapters"
	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/testutil"
)

var _ adapters.Adapter = (*Adapter)(nil)

func fixture(t *testing.T) Config {
	t.Helper()
	s := testutil.LoadFixture(t, "testdata/fixtures/kubevirt")
	return Config{VMIs: s.VMIs, State: s.Collection["virtualmachineinstances"]}
}

func collect(t *testing.T, cfg Config) Evidence {
	t.Helper()
	raw, err := New(cfg).Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var ev Evidence
	if err := json.Unmarshal(raw, &ev); err != nil {
		t.Fatalf("%v: %s", err, raw)
	}
	return ev
}

func vmi(ev Evidence, name string) VMI {
	for _, v := range ev.VMIs {
		if v.Name == name {
			return v
		}
	}
	return VMI{}
}

func TestAvailable(t *testing.T) {
	for _, tc := range []struct {
		state  model.CollectionStatus
		ok     bool
		reason string
	}{
		{model.CollectionStatus{State: model.StateOK}, true, ""},
		{model.CollectionStatus{State: model.StateNotFound, Reason: "kubevirt.io/v1 not served"}, false, "kubevirt.io/v1 not served"},
		{model.CollectionStatus{State: model.StateNotFound}, false, "kubevirt.io/v1 not served"},
		{model.CollectionStatus{State: model.StateForbidden, Reason: "cannot list virtualmachineinstances"}, false, "cannot list virtualmachineinstances"},
		{model.CollectionStatus{State: model.StateError, Reason: "timeout"}, false, "timeout"},
		{model.CollectionStatus{State: model.StateError}, false, "virtualmachineinstances collection state: error"},
		{model.CollectionStatus{}, false, "virtualmachineinstances collection state: not collected"},
	} {
		ok, reason, err := New(Config{State: tc.state}).Available(context.Background())
		if err != nil || ok != tc.ok || reason != tc.reason {
			t.Errorf("%+v: got (%v, %q, %v)", tc.state, ok, reason, err)
		}
	}
}

func TestEvidenceContentAndSort(t *testing.T) {
	cfg := fixture(t)
	// Reverse the input: output order must not depend on it.
	for i, j := 0, len(cfg.VMIs)-1; i < j; i, j = i+1, j-1 {
		cfg.VMIs[i], cfg.VMIs[j] = cfg.VMIs[j], cfg.VMIs[i]
	}
	ev := collect(t, cfg)
	if ev.Version != 1 {
		t.Fatalf("version %d", ev.Version)
	}
	var names []string
	for _, v := range ev.VMIs {
		names = append(names, v.Namespace+"/"+v.Name)
	}
	want := []string{"demo/vm-garbage", "demo/vm-missing-claim", "demo/vm-no-allocation", "demo/vm-ok", "demo/vm-pending", "demo/vm-secrets", "demo/vm-split", "demo/vm-wrong-node"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("order %v", names)
	}
	got := vmi(ev, "vm-ok")
	exp := VMI{
		Namespace: "demo", Name: "vm-ok", NodeName: "node-a.example.com", Phase: "Running",
		Claims: map[string]string{"partitions": "claim-ok"},
		HostDevices: []HostDevice{
			{"gpu0", "partitions", "claim-ok", "gpu0"}, {"nic0", "partitions", "claim-ok", "nic0"}, {"gpu2", "partitions", "claim-ok", "gpu2"},
		},
		GuestNUMAPassthrough: true, DedicatedCPUPlacement: true, Sockets: 2, Cores: 4, Threads: 1,
	}
	if !reflect.DeepEqual(got, exp) {
		t.Fatalf("vm-ok:\n got %+v\nwant %+v", got, exp)
	}
	if s := vmi(ev, "vm-split"); s.GuestNUMAPassthrough || s.DedicatedCPUPlacement || s.Threads != 2 {
		t.Fatalf("vm-split %+v", s)
	}
	if p := vmi(ev, "vm-pending"); p.NodeName != "" || p.Phase != "Pending" {
		t.Fatalf("vm-pending %+v", p)
	}
}

func TestTemplateClaimIsUnresolved(t *testing.T) {
	s := testutil.LoadFixture(t, "testdata/fixtures/kubevirt")
	u := s.VMIs[0].DeepCopy()
	u.Object["spec"] = map[string]any{
		"resourceClaims": []any{map[string]any{"name": "a", "resourceClaimTemplateName": "tmpl"}},
		"domain":         map[string]any{"devices": map[string]any{"hostDevices": []any{map[string]any{"name": "d", "claimName": "a", "requestName": "r"}}}},
	}
	v := collect(t, Config{VMIs: []unstructured.Unstructured{*u}, State: model.CollectionStatus{State: model.StateOK}}).VMIs[0]
	if v.Claims["a"] != "" || v.HostDevices[0].ClaimName != "" || v.HostDevices[0].ClaimAlias != "a" {
		t.Fatalf("%+v", v)
	}
}

func TestEvidenceNeverCarriesSecrets(t *testing.T) {
	raw, err := New(fixture(t)).Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"SECRET-", "2001:db8", "cloud-config", "accessCredentials", "volumes", "annotations", "interfaces"} {
		if strings.Contains(string(raw), m) {
			t.Errorf("evidence contains %q: %s", m, raw)
		}
	}
}

func TestGarbageVMIsAreTolerated(t *testing.T) {
	ev := collect(t, fixture(t))
	g := vmi(ev, "vm-garbage")
	if g.Name == "" {
		t.Fatal("garbage VMI dropped")
	}
	if len(g.Warnings) == 0 {
		t.Fatal("no warnings")
	}
	if g.GuestNUMAPassthrough || g.DedicatedCPUPlacement || g.Sockets != 0 || g.NodeName != "" || g.Phase != "" {
		t.Fatalf("garbage produced values: %+v", g)
	}
	raw, _ := json.Marshal(g)
	if strings.Contains(string(raw), "SECRET-GARBAGE") {
		t.Fatalf("raw value echoed: %s", raw)
	}
	for _, w := range g.Warnings {
		if strings.ContainsAny(w, "%") || len(w) > 80 {
			t.Errorf("warning %q is not fixed short text", w)
		}
	}
}

func TestWholeObjectGarbageDoesNotPanic(t *testing.T) {
	for _, obj := range []map[string]any{
		{}, {"spec": nil, "status": nil, "metadata": nil}, {"spec": []any{1}, "status": "x"},
		{"spec": map[string]any{"domain": "x", "resourceClaims": []any{nil, 3, map[string]any{"name": 4}}}},
		{"spec": map[string]any{"domain": map[string]any{"devices": "x", "cpu": []any{}}}},
		{"spec": map[string]any{"domain": map[string]any{"devices": map[string]any{"hostDevices": "x"}}}},
	} {
		collect(t, Config{VMIs: []unstructured.Unstructured{{Object: obj}}, State: model.CollectionStatus{State: model.StateOK}})
	}
}
