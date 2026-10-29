package kubevirt

import (
	"context"
	"encoding/json"
	"fmt"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

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
	if s := vmi(ev, "vm-split"); s.GuestNUMAPassthrough || !s.DedicatedCPUPlacement || s.Threads != 2 {
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

// jsonFields lists the JSON keys of struct type t, sorted.
func jsonFields(t reflect.Type) []string {
	var f []string
	for i := 0; i < t.NumField(); i++ {
		f = append(f, strings.Split(t.Field(i).Tag.Get("json"), ",")[0])
	}
	slices.Sort(f)
	return f
}

// Adding a field to the evidence types must be a conscious privacy decision:
// update the pinned lists (and the spec) together with the field.
func TestEvidenceFieldAllowList(t *testing.T) {
	for _, tc := range []struct {
		typ  reflect.Type
		want []string
	}{
		{reflect.TypeOf(Evidence{}), []string{"version", "vmis", "warnings"}},
		{reflect.TypeOf(VMI{}), []string{"claims", "cores", "dedicatedCPUPlacement", "guestNUMAPassthrough", "hostDevices", "name", "namespace", "nodeName", "phase", "sockets", "threads", "warnings"}},
		{reflect.TypeOf(HostDevice{}), []string{"claimAlias", "claimName", "name", "requestName"}},
	} {
		if got := jsonFields(tc.typ); !slices.Equal(got, tc.want) {
			t.Errorf("%s fields %v, pinned %v", tc.typ.Name(), got, tc.want)
		}
	}
}

func okState() model.CollectionStatus { return model.CollectionStatus{State: model.StateOK} }

func hasWarning(ws []string, w string) bool { return slices.Contains(ws, w) }

func allocDelta(f func()) uint64 {
	var a, b runtime.MemStats
	runtime.ReadMemStats(&a)
	f()
	runtime.ReadMemStats(&b)
	return b.TotalAlloc - a.TotalAlloc
}

func TestHostDevicesCappedPerVMI(t *testing.T) {
	var hds []any
	for i := 0; i < 5000; i++ {
		hds = append(hds, map[string]any{"name": fmt.Sprint("d", i), "claimName": "a", "requestName": "r"})
	}
	obj := map[string]any{"metadata": map[string]any{"name": "big", "namespace": "ns"},
		"spec": map[string]any{"domain": map[string]any{"devices": map[string]any{"hostDevices": hds}}}}
	var ev Evidence
	start := time.Now()
	if d := allocDelta(func() { ev = collect(t, Config{VMIs: []unstructured.Unstructured{{Object: obj}}, State: okState()}) }); d > 256<<20 {
		t.Errorf("allocated %d bytes", d)
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("took %v", time.Since(start))
	}
	if n := len(ev.VMIs[0].HostDevices); n != 1024 {
		t.Errorf("hostDevices %d, want 1024", n)
	}
	if !hasWarning(ev.VMIs[0].Warnings, "hostDevices truncated at 1024") {
		t.Errorf("warnings %v", ev.VMIs[0].Warnings)
	}
}

func TestVMIsCapped(t *testing.T) {
	vmis := make([]unstructured.Unstructured, 20000)
	for i := range vmis {
		vmis[i] = unstructured.Unstructured{Object: map[string]any{"metadata": map[string]any{"name": fmt.Sprintf("v%05d", i), "namespace": "ns"}}}
	}
	var ev Evidence
	start := time.Now()
	if d := allocDelta(func() { ev = collect(t, Config{VMIs: vmis, State: okState()}) }); d > 256<<20 {
		t.Errorf("allocated %d bytes", d)
	}
	if time.Since(start) > 10*time.Second {
		t.Errorf("took %v", time.Since(start))
	}
	if len(ev.VMIs) != 10000 || ev.VMIs[0].Name != "v00000" || !hasWarning(ev.Warnings, "vmis truncated at 10000") {
		t.Errorf("vmis %d warnings %v", len(ev.VMIs), ev.Warnings)
	}
}

func TestBoundDedupesTruncatesAndCaps(t *testing.T) {
	long := strings.Repeat("é", 10<<20) // 10M runes
	claims := map[string]string{}
	for i := 0; i < 5000; i++ {
		claims[fmt.Sprint("a", i)] = "c"
	}
	ev := Evidence{Version: 1, VMIs: []VMI{
		{Namespace: "ns", Name: "dup", Phase: "first"},
		{Namespace: "ns", Name: "dup", Phase: "second"},
		{Namespace: long, Name: long, NodeName: long, Claims: map[string]string{long: long}, HostDevices: []HostDevice{{Name: long, ClaimAlias: long, ClaimName: long, RequestName: long}}},
		{Namespace: "ns", Name: "many", Claims: claims},
	}}
	ev.Bound()
	if len(ev.VMIs) != 3 || ev.VMIs[0].Phase != "first" {
		t.Fatalf("dedupe: %+v", ev.VMIs[:1])
	}
	if !hasWarning(ev.Warnings, "duplicate VMIs ignored") {
		t.Errorf("warnings %v", ev.Warnings)
	}
	if n := len(ev.VMIs[2].Claims); n != 1024 || !hasWarning(ev.VMIs[2].Warnings, "claims truncated at 1024") {
		t.Errorf("claims %d warnings %v", n, ev.VMIs[2].Warnings)
	}
	raw, _ := json.Marshal(ev)
	if len(raw) > 64<<10 {
		t.Fatalf("bounded evidence is %d bytes", len(raw))
	}
	if n := len([]rune(ev.VMIs[1].Name)); n != MaxString+1 || !strings.HasSuffix(ev.VMIs[1].Name, "…") {
		t.Errorf("name has %d runes", n)
	}
}
