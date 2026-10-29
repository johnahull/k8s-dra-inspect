package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/johnahull/k8s-dra-inspect/internal/adapters/host"
	"github.com/johnahull/k8s-dra-inspect/internal/adapters/kubevirt"
	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/testutil"
)

const kvNode = "node-a.example.com"

func kvFixture(t *testing.T) model.Snapshot {
	t.Helper()
	return testutil.LoadFixture(t, "testdata/fixtures/kubevirt")
}

// kvHost is a fake node whose three fixture devices are bound to vfio-pci.
func kvHost(t *testing.T) *testutil.FakeHost {
	t.Helper()
	h := testutil.NewFakeHost(t)
	h.NUMA("0", "0-7")
	h.NUMA("1", "8-15")
	for i, bdf := range []string{"0000:3b:00.0", "0000:3c:00.0", "0000:af:00.0"} {
		g := string(rune('7' + i))
		h.PCI(bdf, "0", "0x1234", "0x5678", "vfio-pci", g)
		h.VFIONode(g)
	}
	h.VFIONode("vfio")
	return h
}

func supportRun(t *testing.T, snap model.Snapshot, nh NewHostFunc, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	return runHost(t, snap, nh, args...)
}

func resultsOf(t *testing.T, out string) []model.Result { t.Helper(); return decode(t, out).Results }

func find(rs []model.Result, check string) []model.Result {
	var out []model.Result
	for _, r := range rs {
		if r.Check == check {
			out = append(out, r)
		}
	}
	return out
}

func TestCheckKubeVirtGoldens(t *testing.T) {
	notFound := kvFixture(t)
	notFound.VMIs = nil
	notFound.Collection["virtualmachineinstances"] = model.CollectionStatus{State: model.StateNotFound, Reason: "kubevirt.io/v1 not served"}
	none := kvFixture(t)
	none.VMIs = nil
	for _, tc := range []struct {
		name string
		snap model.Snapshot
		args []string
		exit int
	}{
		{"fixture", kvFixture(t), []string{"check", "kubevirt"}, 1},
		{"one", kvFixture(t), []string{"check", "kubevirt", "demo/vm-ok"}, 0},
		{"notfound", notFound, []string{"check", "kubevirt"}, 0},
		{"none", none, []string{"check", "kubevirt"}, 0},
	} {
		for _, f := range []struct{ format, ext string }{{"table", "txt"}, {"json", "json"}} {
			t.Run(tc.name+"/"+f.format, func(t *testing.T) {
				out, errOut, code := runHost(t, tc.snap, noHost(t), append(tc.args, "-o", f.format)...)
				if errOut != "" || code != tc.exit {
					t.Fatalf("exit %d (want %d) stderr %q\n%s", code, tc.exit, errOut, out)
				}
				testutil.Golden(t, "check-kubevirt-"+tc.name+"."+f.ext, []byte(out))
			})
		}
	}
}

func TestCheckKubeVirtArgsAndNamespace(t *testing.T) {
	snap := kvFixture(t)
	if _, errOut, code := runHost(t, snap, noHost(t), "check", "kubevirt", "a", "b"); code != 2 || !strings.Contains(errOut, "accepts at most 1 arg") {
		t.Errorf("two args: exit %d %q", code, errOut)
	}
	out, _, code := runHost(t, snap, noHost(t), "check", "kubevirt", "vm-ok", "-n", "demo", "-o", "json")
	if rs := resultsOf(t, out); code != 0 || len(rs) != 1 || rs[0].Subject != "demo/vm-ok" {
		t.Errorf("exit %d %+v", code, rs)
	}
}

func TestReportSupportGoldens(t *testing.T) {
	h := kvHost(t)
	for _, tc := range []struct {
		name string
		root string
	}{{"live", h.Root}, {"nohost", "/nonexistent/host-root"}} {
		for _, f := range []struct{ format, ext string }{{"table", "txt"}, {"json", "json"}} {
			t.Run(tc.name+"/"+f.format, func(t *testing.T) {
				rec := &recorder{}
				out, errOut, code := supportRun(t, kvFixture(t), rec.New, "report", "support", "--host-root", tc.root, "--node-name", kvNode, "-o", f.format)
				if errOut != "" || code != 1 || len(rec.cfgs) != 1 {
					t.Fatalf("exit %d stderr %q cfgs %+v\n%s", code, errOut, rec.cfgs, out)
				}
				testutil.Golden(t, "report-support-"+tc.name+"."+f.ext, []byte(out))
			})
		}
	}
}

func TestReportSupportAdapterRows(t *testing.T) {
	out, _, _ := supportRun(t, kvFixture(t), (&recorder{}).New, "report", "support", "--host-root", kvHost(t).Root, "--node-name", kvNode, "-o", "json")
	rs := resultsOf(t, out)
	for _, name := range []string{"adapter.host", "adapter.kubevirt"} {
		got := find(rs, name)
		if len(got) != 1 || got[0].Status != model.StatusPass {
			t.Errorf("%s: %+v", name, got)
		}
	}
	for _, name := range []string{"check.vfio", "check.kubevirt", "check.alignment", "inspect.slices"} {
		if len(find(rs, name)) == 0 {
			t.Errorf("no %s results", name)
		}
	}
	if len(find(rs, "check.cpupinning")) != 0 {
		t.Error("cpupinning must not be in the support report")
	}
}

func TestReportSupportUnavailableAdaptersDoNotBlock(t *testing.T) {
	snap := kvFixture(t)
	snap.VMIs = nil
	snap.Collection["virtualmachineinstances"] = model.CollectionStatus{State: model.StateNotFound, Reason: "kubevirt.io/v1 not served"}
	empty := testutil.NewFakeHost(t)
	out, _, code := supportRun(t, snap, (&recorder{}).New, "report", "support", "--host-root", empty.Root, "--node-name", kvNode, "-o", "json")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	rs := resultsOf(t, out)
	h, k := find(rs, "adapter.host"), find(rs, "adapter.kubevirt")
	if len(h) != 1 || h[0].Status != model.StatusUnavailable || h[0].Details["reason"] != "no sysfs under "+empty.Root+"; run on the node (or pass --host-root)" {
		t.Errorf("host row %+v", h)
	}
	if len(k) != 1 || k[0].Status != model.StatusUnavailable || k[0].Details["reason"] != "kubevirt.io/v1 not served" {
		t.Errorf("kubevirt row %+v", k)
	}
	if g := find(rs, "check.alignment"); len(g) == 0 || g[0].Status != model.StatusPass {
		t.Errorf("alignment blocked: %+v", g)
	}
	if _, _, strict := supportRun(t, snap, (&recorder{}).New, "report", "support", "--host-root", empty.Root, "--node-name", kvNode, "--strict"); strict != 3 {
		t.Errorf("--strict exit %d, want 3", strict)
	}
}

func TestReportSupportHostFlagsWithFromAreUsageErrors(t *testing.T) {
	for _, flag := range [][]string{{"--host-root", "/x"}, {"--node-name", "n"}} {
		out, errOut, code := supportRun(t, kvFixture(t), noHost(t), append([]string{"report", "support", "--from", "x.json"}, flag...)...)
		if code != 2 || out != "" || !strings.Contains(errOut, flag[0]+" cannot be used with --from (host evidence comes from the snapshot)") {
			t.Errorf("%v: exit %d stdout %q stderr %q", flag, code, out, errOut)
		}
	}
}

func TestReportSupportFromWithoutEvidence(t *testing.T) {
	out, _, _ := supportRun(t, kvFixture(t), noHost(t), "report", "support", "--from", "x.json", "-o", "json")
	rs := resultsOf(t, out)
	if h := find(rs, "adapter.host"); len(h) != 1 || h[0].Status != model.StatusUnavailable || h[0].Details["reason"] != "no host evidence in snapshot; run on the node" {
		t.Errorf("host row %+v", h)
	}
	// kubevirt needs no outside evidence: it is derived from the snapshot's VMIs.
	if k := find(rs, "adapter.kubevirt"); len(k) != 1 || k[0].Status != model.StatusPass {
		t.Errorf("kubevirt row %+v", k)
	}
}

func captureWithAdapters(t *testing.T, snap model.Snapshot, root string, extra ...string) (path string, raw []byte) {
	t.Helper()
	path = tmpFile(t)
	args := append([]string{"snapshot", "capture", "-f", path, "--with-adapters", "--host-root", root, "--node-name", kvNode}, extra...)
	if _, errOut, code := runHost(t, snap, (&recorder{}).New, args...); code != 0 || errOut != "" && !strings.HasPrefix(errOut, "collection incomplete") {
		t.Fatalf("capture exit %d stderr %q", code, errOut)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, raw
}

func TestCaptureWithAdaptersReplaysLikeLive(t *testing.T) {
	h := kvHost(t)
	live, _, liveCode := supportRun(t, kvFixture(t), (&recorder{}).New, "report", "support", "--host-root", h.Root, "--node-name", kvNode, "-o", "json")
	path, _ := captureWithAdapters(t, kvFixture(t), h.Root)
	o, e, code := replaySupport(t, path)
	if e != "" || code != liveCode || decodeResults(t, o) != decodeResults(t, live) {
		t.Fatalf("replay differs (exit %d vs %d, stderr %q)\nlive:   %s\nreplay: %s", code, liveCode, e, live, o)
	}
}

// replaySupport replays a captured file; the host adapter must not be built.
func replaySupport(t *testing.T, path string) (stdout, stderr string, code int) {
	t.Helper()
	deps := snapDeps(t, model.Snapshot{})
	deps.NewHost = noHost(t)
	return runSnap(t, deps, "report", "support", "--from", path, "-o", "json")
}

func TestCaptureWithAdaptersStoresRedactedEvidence(t *testing.T) {
	_, raw := captureWithAdapters(t, kvFixture(t), kvHost(t).Root)
	var s model.Snapshot
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	if len(s.Evidence[host.Name]) == 0 || len(s.Evidence[kubevirt.Name]) == 0 {
		t.Fatalf("evidence keys: %v", s.Evidence)
	}
	for _, m := range []string{"SECRET-CLOUDINIT-MARKER", "SECRET-ACCESSCRED-MARKER", "SECRET-GUESTOS-MARKER", "2001:db8"} {
		if bytes.Contains(raw, []byte(m)) {
			t.Errorf("captured file contains %q", m)
		}
	}
	for _, k := range []string{kubevirt.Name, host.Name} {
		if bytes.Contains(s.Evidence[k], []byte("SECRET-")) {
			t.Errorf("%s evidence contains a secret marker: %s", k, s.Evidence[k])
		}
	}
}

func TestCaptureWithoutFlagHasNoEvidence(t *testing.T) {
	path := tmpFile(t)
	if _, errOut, code := runHost(t, kvFixture(t), noHost(t), "snapshot", "capture", "-f", path); code != 0 || errOut != "" {
		t.Fatalf("exit %d %q", code, errOut)
	}
	raw, _ := os.ReadFile(path)
	var s model.Snapshot
	if err := json.Unmarshal(raw, &s); err != nil || len(s.Evidence) != 0 {
		t.Fatalf("err %v evidence %v", err, s.Evidence)
	}
	if _, errOut, code := runHost(t, kvFixture(t), noHost(t), "snapshot", "capture", "--host-root", "/x"); code != 2 || !strings.Contains(errOut, "--host-root requires --with-adapters") {
		t.Errorf("host-root without flag: exit %d %q", code, errOut)
	}
}

func TestCaptureWithAdaptersUnavailableHostStillCaptures(t *testing.T) {
	snap := kvFixture(t)
	snap.Collection["virtualmachineinstances"] = model.CollectionStatus{State: model.StateForbidden, Reason: "cannot list"}
	snap.VMIs = nil
	path, _ := captureWithAdapters(t, snap, testutil.NewFakeHost(t).Root)
	out, e, _ := replaySupport(t, path)
	rs := resultsOf(t, out)
	h, k := find(rs, "adapter.host"), find(rs, "adapter.kubevirt")
	if e != "" || len(h) != 1 || h[0].Status != model.StatusUnavailable || !strings.Contains(h[0].Details["reason"].(string), "no sysfs under") {
		t.Errorf("host row %+v stderr %q", h, e)
	}
	if len(k) != 1 || k[0].Status != model.StatusUnavailable || k[0].Details["reason"] != "cannot list" {
		t.Errorf("kubevirt row %+v", k)
	}
}

func TestReportSupportNamespaceScopesKubeVirt(t *testing.T) {
	out, _, _ := supportRun(t, kvFixture(t), (&recorder{}).New, "report", "support", "-n", "other", "--host-root", kvHost(t).Root, "--node-name", kvNode, "-o", "json")
	kv := find(resultsOf(t, out), "check.kubevirt")
	if len(kv) != 1 || kv[0].Status != model.StatusPass || kv[0].Details["summary"] != "no VirtualMachineInstances" {
		t.Fatalf("%+v", kv)
	}
}
