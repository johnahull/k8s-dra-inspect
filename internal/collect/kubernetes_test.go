package collect

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/utils/ptr"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
)

var now = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

func slice(name string) *resourcev1.ResourceSlice {
	return &resourcev1.ResourceSlice{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: resourcev1.ResourceSliceSpec{
			Driver: "gpu.example.com",
			Pool:   resourcev1.ResourcePool{Name: "node-a", Generation: 2},
			Devices: []resourcev1.Device{{
				Name: "gpu-0",
				Attributes: map[resourcev1.QualifiedName]resourcev1.DeviceAttribute{
					"index":                           {IntValue: ptr.To(int64(0))},
					"resource.kubernetes.io/numaNode": {IntValues: []int64{0, 1}},
					"resource.kubernetes.io/pcieRoot": {StringValue: ptr.To("pci0000:00")},
				},
				Capacity: map[resourcev1.QualifiedName]resourcev1.DeviceCapacity{"memory": {Value: resource.MustParse("64Gi")}},
			}},
			SharedCounters: []resourcev1.CounterSet{{Name: "cs"}},
		},
	}
}

func claim(ns, name string) *resourcev1.ResourceClaim {
	return &resourcev1.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Status: resourcev1.ResourceClaimStatus{Allocation: &resourcev1.AllocationResult{Devices: resourcev1.DeviceAllocationResult{
			Results: []resourcev1.DeviceRequestAllocationResult{{Request: "r", Driver: "gpu.example.com", Pool: "node-a", Device: "gpu-0"}},
		}}},
	}
}

func pod(ns, name string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, UID: "u-" + "x"},
		Spec:       corev1.PodSpec{NodeName: "node-a", ResourceClaims: []corev1.PodResourceClaim{{Name: "rc"}}},
		Status:     corev1.PodStatus{ResourceClaimStatuses: []corev1.PodResourceClaimStatus{{Name: "rc", ResourceClaimName: ptr.To("c1")}}},
	}
}

func newLive(cs *fake.Clientset, objs ...runtime.Object) *Live {
	scheme := runtime.NewScheme()
	dyn := dynfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{vmiGVR: "VirtualMachineInstanceList"}, objs...)
	return &Live{Client: cs, Dynamic: dyn, Probe: func(_ context.Context, gv string) error {
		_, err := cs.Discovery().ServerResourcesForGroupVersion(gv)
		return err
	}, Now: func() time.Time { return now }}
}

func serveKubeVirt(cs *fake.Clientset) {
	cs.Resources = []*metav1.APIResourceList{{
		GroupVersion: "kubevirt.io/v1",
		APIResources: []metav1.APIResource{{Name: "virtualmachineinstances", Kind: "VirtualMachineInstance", Namespaced: true}},
	}}
}

func vmi(ns, name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "kubevirt.io/v1", "kind": "VirtualMachineInstance",
		"metadata": map[string]any{"namespace": ns, "name": name},
	}}
}

func TestCollectTyped(t *testing.T) {
	cs := fake.NewClientset(
		slice("s1"),
		&resourcev1.DeviceClass{ObjectMeta: metav1.ObjectMeta{Name: "gpu.example.com"}},
		claim("a", "c1"), claim("b", "c2"),
		pod("a", "p1"), pod("b", "p2"),
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}},
	)
	serveKubeVirt(cs)
	snap, err := newLive(cs, vmi("a", "v1")).Collect(context.Background(), Scope{})
	if err != nil {
		t.Fatal(err)
	}
	if snap.SchemaVersion != model.SchemaVersion || snap.Source != model.SourceLive || !snap.GeneratedAt.Equal(now) || snap.Redacted {
		t.Fatalf("header: %+v", snap)
	}
	for _, k := range []string{"resourceslices", "deviceclasses", "resourceclaims", "pods", "nodes", "virtualmachineinstances"} {
		if snap.Collection[k].State != model.StateOK {
			t.Errorf("%s: %+v", k, snap.Collection[k])
		}
	}
	if len(snap.ResourceClaims) != 2 || len(snap.Pods) != 2 || len(snap.Nodes) != 1 || len(snap.DeviceClasses) != 1 || len(snap.VMIs) != 1 {
		t.Fatalf("counts: %d %d %d %d %d", len(snap.ResourceClaims), len(snap.Pods), len(snap.Nodes), len(snap.DeviceClasses), len(snap.VMIs))
	}
	d := snap.ResourceSlices[0].Spec.Devices[0]
	if *d.Attributes["index"].IntValue != 0 || len(d.Attributes["resource.kubernetes.io/numaNode"].IntValues) != 2 ||
		*d.Attributes["resource.kubernetes.io/pcieRoot"].StringValue != "pci0000:00" {
		t.Errorf("attributes lost: %+v", d.Attributes)
	}
	q := d.Capacity["memory"].Value
	if q.Cmp(resource.MustParse("64Gi")) != 0 || snap.ResourceSlices[0].Spec.Pool.Generation != 2 || len(snap.ResourceSlices[0].Spec.SharedCounters) != 1 {
		t.Errorf("capacity/pool/counters lost")
	}
	if r := snap.ResourceClaims[0].Status.Allocation.Devices.Results[0]; r.Driver != "gpu.example.com" || r.Pool != "node-a" || r.Device != "gpu-0" || r.Request != "r" {
		t.Errorf("allocation: %+v", r)
	}
	if p := snap.Pods[0]; p.NodeName != "node-a" || len(p.ResourceClaimStatuses) != 1 {
		t.Errorf("pod: %+v", p)
	}
}

func TestCollectNamespaceScope(t *testing.T) {
	cs := fake.NewClientset(slice("s1"), claim("a", "c1"), claim("b", "c2"), pod("a", "p1"), pod("b", "p2"),
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}})
	snap, err := newLive(cs).Collect(context.Background(), Scope{Namespace: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.ResourceClaims) != 1 || snap.ResourceClaims[0].Namespace != "a" || len(snap.Pods) != 1 || snap.Pods[0].Namespace != "a" {
		t.Fatalf("claims=%d pods=%d", len(snap.ResourceClaims), len(snap.Pods))
	}
	if len(snap.ResourceSlices) != 1 || len(snap.Nodes) != 1 {
		t.Error("cluster-scoped resources must stay cluster-wide")
	}
	if snap.Namespace != "a" {
		t.Errorf("namespace = %q", snap.Namespace)
	}
	want := model.CollectionStatus{State: model.StateOK, Reason: "namespace a only"}
	for _, k := range []string{"resourceclaims", "pods"} {
		if snap.Collection[k] != want {
			t.Errorf("%s: %+v", k, snap.Collection[k])
		}
	}
	if snap.Collection["nodes"].Reason != "" {
		t.Error("cluster-scoped reason must stay empty")
	}
}

func TestCollectForbiddenDoesNotAbort(t *testing.T) {
	cs := fake.NewClientset(slice("s1"), claim("a", "c1"), &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}})
	cs.PrependReactor("list", "resourceslices", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Group: "resource.k8s.io", Resource: "resourceslices"}, "", nil)
	})
	cs.PrependReactor("list", "nodes", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewNotFound(schema.GroupResource{Resource: "nodes"}, "")
	})
	cs.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, context.DeadlineExceeded
	})
	snap, err := newLive(cs).Collect(context.Background(), Scope{})
	if err != nil {
		t.Fatal(err)
	}
	if st := snap.Collection["resourceslices"]; st.State != model.StateForbidden || st.Reason == "" || len(snap.ResourceSlices) != 0 {
		t.Errorf("slices: %+v n=%d", st, len(snap.ResourceSlices))
	}
	if snap.Collection["nodes"].State != model.StateNotFound {
		t.Errorf("nodes: %+v", snap.Collection["nodes"])
	}
	if st := snap.Collection["pods"]; st.State != model.StateError || st.Reason != "timed out" {
		t.Errorf("pods: %+v", st)
	}
	if snap.Collection["resourceclaims"].State != model.StateOK || len(snap.ResourceClaims) != 1 {
		t.Error("claims should still be collected")
	}
	if snap.Collection["deviceclasses"].State != model.StateOK {
		t.Error("empty successful list must be ok")
	}
}

func TestCollectKubeVirtAbsent(t *testing.T) {
	snap, err := newLive(fake.NewClientset()).Collect(context.Background(), Scope{})
	if err != nil {
		t.Fatal(err)
	}
	want := model.CollectionStatus{State: model.StateNotFound, Reason: "kubevirt.io/v1 not served"}
	if got := snap.Collection["virtualmachineinstances"]; got != want || len(snap.VMIs) != 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestCollectVMIsNamespaced(t *testing.T) {
	cs := fake.NewClientset()
	serveKubeVirt(cs)
	snap, err := newLive(cs, vmi("a", "v1"), vmi("b", "v2")).Collect(context.Background(), Scope{Namespace: "b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.VMIs) != 1 || snap.VMIs[0].GetName() != "v2" {
		t.Fatalf("vmis: %v", snap.VMIs)
	}
	if want := (model.CollectionStatus{State: model.StateOK, Reason: "namespace b only"}); snap.Collection["virtualmachineinstances"] != want {
		t.Errorf("collection: %+v", snap.Collection["virtualmachineinstances"])
	}
}

func TestSnapshotUsesScope(t *testing.T) {
	cs := fake.NewClientset(claim("a", "c1"), claim("b", "c2"))
	l := newLive(cs)
	l.Scope = Scope{Namespace: "a"}
	var src model.Source = l
	snap, err := src.Snapshot(context.Background())
	if err != nil || len(snap.ResourceClaims) != 1 {
		t.Fatalf("err=%v n=%d", err, len(snap.ResourceClaims))
	}
}

func TestCollectPaginatesAll(t *testing.T) {
	var objs []runtime.Object
	for i := range 1200 {
		objs = append(objs, pod("a", fmt.Sprintf("p%04d", i)))
	}
	snap, err := newLive(fake.NewClientset(objs...)).Collect(context.Background(), Scope{})
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Pods) != 1200 || snap.Collection["pods"].State != model.StateOK {
		t.Fatalf("pods=%d %+v", len(snap.Pods), snap.Collection["pods"])
	}
}

const timedOutReason = "timed out after 300ms"

// hungKubeconfig serves a kubeconfig for a server that never answers until
// the client gives up.
func hungKubeconfig(t *testing.T, h http.Handler) string {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	cfg := fmt.Sprintf("apiVersion: v1\nkind: Config\nclusters:\n- name: c\n  cluster: {server: %q}\ncontexts:\n- name: x\n  context: {cluster: c, user: u}\nusers:\n- name: u\n  user: {}\ncurrent-context: x\n", srv.URL)
	p := filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(p, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func hang(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }

func TestCollectOverallDeadline(t *testing.T) {
	live, err := NewClients(hungKubeconfig(t, http.HandlerFunc(hang)), "", 300*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	snap, err := live.Collect(context.Background(), Scope{})
	if err != nil {
		t.Fatal(err)
	}
	// One overall deadline, not one per resource kind: a sequential collection
	// would need at least 6 x 300ms. The upper bound leaves room for a loaded
	// machine; the lower bound proves the deadline was actually waited for.
	if el := time.Since(start); el < 250*time.Millisecond || el > 1200*time.Millisecond {
		t.Errorf("Collect took %v, want between 250ms and 1.2s (timeout 300ms)", el)
	}
	if len(snap.Collection) == 0 {
		t.Fatal("no collection entries")
	}
	for k, st := range snap.Collection {
		if st.State == model.StateOK || st.Reason != timedOutReason {
			t.Errorf("%s: %+v, want non-ok with reason %q", k, st, timedOutReason)
		}
	}
}

func okHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case strings.HasPrefix(r.URL.Path, "/apis/kubevirt.io"):
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"NotFound","code":404}`))
	default:
		_, _ = w.Write([]byte(`{"kind":"List","apiVersion":"v1","items":[]}`))
	}
}

func TestCollectFastServerUnaffected(t *testing.T) {
	for _, to := range []time.Duration{0, 5 * time.Second} {
		live, err := NewClients(hungKubeconfig(t, http.HandlerFunc(okHandler)), "", to)
		if err != nil {
			t.Fatal(err)
		}
		snap, err := live.Collect(context.Background(), Scope{})
		if err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{"resourceslices", "deviceclasses", "resourceclaims", "pods", "nodes"} {
			if st := snap.Collection[k]; st.State != model.StateOK {
				t.Errorf("timeout %v: %s = %+v, want ok", to, k, st)
			}
		}
	}
}

// A server that is slow on every list and hangs on the KubeVirt discovery probe must still
// finish within the one overall deadline: the probe starts after the deadline has passed.
func TestCollectDeadlineCoversDiscovery(t *testing.T) {
	h := func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/apis/kubevirt.io") {
			hang(w, r)
			return
		}
		time.Sleep(70 * time.Millisecond)
		okHandler(w, r)
	}
	live, err := NewClients(hungKubeconfig(t, http.HandlerFunc(h)), "", 300*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	snap, err := live.Collect(context.Background(), Scope{})
	if err != nil {
		t.Fatal(err)
	}
	// One overall deadline, not one per resource kind: a sequential collection
	// would need at least 6 x 300ms. The upper bound leaves room for a loaded
	// machine; the lower bound proves the deadline was actually waited for.
	if el := time.Since(start); el < 250*time.Millisecond || el > 1200*time.Millisecond {
		t.Errorf("Collect took %v, want between 250ms and 1.2s (timeout 300ms)", el)
	}
	if st := snap.Collection["virtualmachineinstances"]; st.State != model.StateError || st.Reason != timedOutReason {
		t.Errorf("vmi = %+v, want error %q", st, timedOutReason)
	}
}

func statusHandler(code int, reason string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = fmt.Fprintf(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":%q,"code":%d}`, reason, code)
	}
}

func TestRecordReasonsAreSanitized(t *testing.T) {
	const token = "SECRET-TOKEN-xyz"
	tls := httptest.NewTLSServer(http.HandlerFunc(okHandler)) // cert not trusted by the client
	t.Cleanup(tls.Close)
	closed := httptest.NewServer(http.HandlerFunc(okHandler))
	closedAddr := closed.Listener.Addr().String()
	closed.Close()

	cases := []struct {
		name, server string
		h            http.Handler
		want         string // exact reason of resourceslices
		state        string
	}{
		{"closed port", "http://" + closedAddr, nil, "resourceslices: dial tcp " + closedAddr + ": connect: connection refused", model.StateError},
		{"bad CA", tls.URL, nil, "resourceslices: tls: failed to verify certificate: x509: certificate signed by unknown authority", model.StateError},
		{"401", "", statusHandler(401, "Unauthorized"), "resourceslices: unauthorized", model.StateError},
		{"403", "", statusHandler(403, "Forbidden"), "resourceslices: forbidden", model.StateForbidden},
		{"404", "", statusHandler(404, "NotFound"), "resourceslices: not found", model.StateNotFound},
		{"timeout", "", http.HandlerFunc(hang), "timed out after 300ms", model.StateError},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := c.server
			if c.h != nil {
				s := httptest.NewServer(c.h)
				t.Cleanup(s.Close)
				srv = s.URL
			}
			cfg := fmt.Sprintf("apiVersion: v1\nkind: Config\nclusters:\n- name: c\n  cluster: {server: %q}\ncontexts:\n- name: x\n  context: {cluster: c, user: u}\nusers:\n- name: u\n  user: {token: %s}\ncurrent-context: x\n", srv, token)
			p := filepath.Join(t.TempDir(), "kubeconfig")
			if err := os.WriteFile(p, []byte(cfg), 0o600); err != nil {
				t.Fatal(err)
			}
			live, err := NewClients(p, "", 300*time.Millisecond)
			if err != nil {
				t.Fatal(err)
			}
			snap, err := live.Collect(context.Background(), Scope{})
			if err != nil {
				t.Fatal(err)
			}
			st := snap.Collection["resourceslices"]
			if st.State != c.state || st.Reason != c.want {
				t.Errorf("got %+v, want %s %q", st, c.state, c.want)
			}
			for k, st := range snap.Collection {
				for _, bad := range []string{"http://", "https://", "?limit", token, "Get \""} {
					if strings.Contains(st.Reason, bad) {
						t.Errorf("%s reason %q contains %q", k, st.Reason, bad)
					}
				}
			}
		})
	}
}

func TestRecordForbiddenKeepsServerDetail(t *testing.T) {
	forbidden := func(msg string) error {
		return &apierrors.StatusError{ErrStatus: metav1.Status{Status: metav1.StatusFailure, Code: 403, Reason: metav1.StatusReasonForbidden, Message: msg}}
	}
	real := `resourceslices.resource.k8s.io is forbidden: User "system:serviceaccount:ns:sa" cannot list resource "resourceslices" in API group "resource.k8s.io" at the cluster scope`
	cases := []struct{ name, msg, want string }{
		{"real shape", real, "resourceslices: forbidden: " + real},
		{"truncated at 512 runes", strings.Repeat("é", 600), "resourceslices: forbidden: " + strings.Repeat("é", 512)},
		{"control chars", "a\nb\r\n\tc\x00d\x1b[31me  f", "resourceslices: forbidden: a b c d [31me f"},
		{"url scrubbed", "bad GET https://x/y?limit=500 end", "resourceslices: forbidden: bad GET end"},
		{"bearer scrubbed", "hdr Bearer abc123 end", "resourceslices: forbidden: hdr end"},
		{"both", "https://x/y?limit=500 Bearer abc123", "resourceslices: forbidden"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snap := &model.Snapshot{Collection: map[string]model.CollectionStatus{}}
			(&Live{}).record(snap, "resourceslices", forbidden(tc.msg))
			st := snap.Collection["resourceslices"]
			if st.Reason != tc.want || st.State != model.StateForbidden {
				t.Fatalf("got %q (%s)\nwant %q", st.Reason, st.State, tc.want)
			}
		})
	}
}
