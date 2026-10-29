package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/johnahull/k8s-dra-inspect/internal/adapters"
	"github.com/johnahull/k8s-dra-inspect/internal/adapters/host"
	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/testutil"
)

const (
	hostNode = "node-a.example.com"
	hostUID  = "00000000-0000-4000-8000-000000000001"
)

// hostFixture builds a fake root. good: pod pinned to NUMA 0 with the GPU
// bound to vfio-pci alone. bad: pod pinned to NUMA 1, GPU on nvidia, with a
// non-vfio IOMMU peer.
func hostFixture(t *testing.T, good bool) *testutil.FakeHost {
	t.Helper()
	h := testutil.NewFakeHost(t)
	h.NUMA("0", "0-7")
	h.NUMA("1", "8-15")
	h.VFIONode("vfio")
	h.VFIONode("7")
	if good {
		h.CgroupSystemd(hostUID, "guaranteed", "2-5")
		h.PCI("0000:3b:00.0", "0", "0x1234", "0x5678", "vfio-pci", "7")
	} else {
		h.CgroupFS(hostUID, "guaranteed", "8-11")
		h.PCI("0000:3b:00.0", "0", "0x1234", "0x5678", "nvidia", "7")
		h.PCI("0000:3b:00.1", "0", "0x1234", "0x9abc", "snd_hda_intel", "7")
	}
	return h
}

func hostEvidence(t *testing.T, h *testutil.FakeHost) model.Evidence {
	t.Helper()
	ev, err := host.New(host.Config{Root: h.Root, NodeName: hostNode}).Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return ev
}

func hostSnap(t *testing.T, h *testutil.FakeHost) model.Snapshot {
	t.Helper()
	s := testutil.LoadFixture(t, "testdata/fixtures/host")
	if h != nil {
		s.Evidence = map[string]json.RawMessage{host.Name: hostEvidence(t, h)}
	}
	return s
}

// recorder is a NewHost that builds the real adapter and remembers its config.
type recorder struct{ cfgs []host.Config }

func (r *recorder) New(c host.Config) adapters.Adapter {
	r.cfgs = append(r.cfgs, c)
	return host.New(c)
}

func runHost(t *testing.T, snap model.Snapshot, nh NewHostFunc, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	deps := Deps{
		NewSource: func(*Options) (model.Source, error) { return fakeSource{snap}, nil },
		NewHost:   nh,
		Now:       func() time.Time { return time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC) },
		Width:     testWidth,
	}
	var o, e bytes.Buffer
	code = run(args, &o, &e, "v", deps)
	return o.String(), e.String(), code
}

func noHost(t *testing.T) NewHostFunc {
	return func(host.Config) adapters.Adapter {
		t.Helper()
		t.Fatal("host adapter must not be constructed")
		return nil
	}
}

func TestHostChecksGoldenAndExit(t *testing.T) {
	for _, v := range []struct {
		name string
		good bool
	}{{"good", true}, {"bad", false}} {
		snap := hostSnap(t, hostFixture(t, v.good))
		for _, c := range []struct {
			name string
			args []string
			exit int
		}{
			{"cpupinning", []string{"check", "cpupinning", "app", "-n", "demo", "--from", "x.json"}, 0},
			{"vfio", []string{"check", "vfio", "--from", "x.json"}, map[bool]int{true: 0, false: 1}[v.good]},
		} {
			for _, format := range []string{"table", "json"} {
				t.Run(c.name+"/"+v.name+"/"+format, func(t *testing.T) {
					out, errOut, code := runHost(t, snap, noHost(t), append(c.args, "-o", format)...)
					if errOut != "" || code != c.exit {
						t.Fatalf("exit %d (want %d) stderr %q\n%s", code, c.exit, errOut, out)
					}
					testutil.Golden(t, "check-"+c.name+"-host-"+v.name+"."+map[string]string{"table": "txt", "json": "json"}[format], []byte(out))
				})
			}
		}
	}
}

func TestHostChecksFromWithoutEvidenceIsUnavailable(t *testing.T) {
	snap := hostSnap(t, nil)
	for _, args := range [][]string{{"check", "cpupinning", "app", "-n", "demo"}, {"check", "vfio"}} {
		args = append(args, "--from", "x.json")
		out, _, code := runHost(t, snap, noHost(t), args...)
		if code != 0 || !strings.Contains(out, "UNAVAILABLE: ") || !strings.Contains(out, "no host evidence in snapshot; run on the node") {
			t.Errorf("%v: exit %d\n%s", args, code, out)
		}
		if _, _, code := runHost(t, snap, noHost(t), append(args, "--strict")...); code != 3 {
			t.Errorf("%v --strict: exit %d, want 3", args, code)
		}
	}
}

func TestHostChecksLiveCollectAndReplayAgree(t *testing.T) {
	h := hostFixture(t, false)
	for _, args := range [][]string{{"check", "cpupinning", "app", "-n", "demo"}, {"check", "vfio"}} {
		rec := &recorder{}
		live, errOut, liveCode := runHost(t, hostSnap(t, nil), rec.New,
			append(args, "--host-root", h.Root, "--node-name", hostNode, "-o", "json")...)
		if errOut != "" || len(rec.cfgs) != 1 || rec.cfgs[0] != (host.Config{Root: h.Root, NodeName: hostNode}) {
			t.Fatalf("%v: cfgs %+v stderr %q", args, rec.cfgs, errOut)
		}
		replay, _, replayCode := runHost(t, hostSnap(t, h), noHost(t), append(args, "--from", "x.json", "-o", "json")...)
		// The envelope's source differs (live vs snapshot); the results must not.
		if liveCode != replayCode || decodeResults(t, live) != decodeResults(t, replay) {
			t.Errorf("%v: live and replay differ\nlive:   %s\nreplay: %s", args, live, replay)
		}
	}
}

func decodeResults(t *testing.T, out string) string {
	t.Helper()
	b, err := json.Marshal(decode(t, out).Results)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestHostChecksLiveHostNotAvailable(t *testing.T) {
	empty := testutil.NewFakeHost(t)
	out, _, code := runHost(t, hostSnap(t, nil), (&recorder{}).New, "check", "vfio", "--host-root", empty.Root, "--node-name", hostNode)
	if code != 0 || !strings.Contains(out, "UNAVAILABLE: check.vfio: no sysfs under "+empty.Root+"; run on the node (or pass --host-root)\n") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

type failingAdapter struct{ adapters.Adapter }

func (failingAdapter) Available(context.Context) (bool, string, error) {
	return true, "", nil
}
func (failingAdapter) Collect(context.Context) (model.Evidence, error) {
	return nil, errors.New("boom")
}

func TestHostChecksLiveCollectErrorIsUnavailable(t *testing.T) {
	nh := func(host.Config) adapters.Adapter { return failingAdapter{} }
	out, _, code := runHost(t, hostSnap(t, nil), nh, "check", "vfio", "--node-name", hostNode)
	if code != 0 || !strings.Contains(out, "UNAVAILABLE: check.vfio: host evidence collection failed: boom") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func TestResolveNodeName(t *testing.T) {
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == "NODE_NAME" {
				return v
			}
			return ""
		}
	}
	hn := func() (string, error) { return "the-hostname", nil }
	for _, tc := range []struct{ flag, env, want string }{
		{"from-flag", "from-env", "from-flag"}, {"", "from-env", "from-env"}, {"", "", "the-hostname"},
	} {
		if got := resolveNodeName(tc.flag, env(tc.env), hn); got != tc.want {
			t.Errorf("%+v = %q", tc, got)
		}
	}
	if got := resolveNodeName("", env(""), func() (string, error) { return "", errors.New("x") }); got != "" {
		t.Errorf("hostname error = %q", got)
	}
}

func TestHostChecksNodeNameFromEnv(t *testing.T) {
	t.Setenv("NODE_NAME", "node-from-env")
	rec := &recorder{}
	_, _, _ = runHost(t, hostSnap(t, nil), rec.New, "check", "vfio", "--host-root", testutil.NewFakeHost(t).Root)
	if len(rec.cfgs) != 1 || rec.cfgs[0].NodeName != "node-from-env" {
		t.Fatalf("cfgs %+v", rec.cfgs)
	}
}

func TestHostFlagsOnlyOnHostChecks(t *testing.T) {
	for _, args := range [][]string{
		{"check", "alignment", "--host-root", "/x"}, {"check", "topology", "--node-name", "n"},
		{"inspect", "slices", "--host-root", "/x"}, {"report", "cluster", "--node-name", "n"},
	} {
		if _, errOut, code := runHost(t, hostSnap(t, nil), noHost(t), args...); code != 2 || !strings.Contains(errOut, "unknown flag") {
			t.Errorf("%v: exit %d stderr %q", args, code, errOut)
		}
	}
	if _, errOut, code := runHost(t, hostSnap(t, nil), noHost(t), "check", "cpupinning"); code != 2 || !strings.Contains(errOut, "accepts 1 arg") {
		t.Errorf("no pod: exit %d stderr %q", code, errOut)
	}
}

func TestCoreCommandsNeverTouchHostAdapter(t *testing.T) {
	snap := hostSnap(t, hostFixture(t, true))
	snapPath := t.TempDir() + "/s.json"
	for _, args := range [][]string{
		{"inspect", "slices"}, {"inspect", "drivers"}, {"inspect", "attributes"}, {"inspect", "topology"}, {"inspect", "claims"}, {"inspect", "deviceclasses"},
		{"check", "alignment"}, {"check", "counters"}, {"check", "capacity"}, {"check", "topology"},
		{"report", "cluster"}, {"snapshot", "capture"}, {"snapshot", "capture", "-f", snapPath}, {"version"},
	} {
		if _, errOut, code := runHost(t, snap, noHost(t), args...); code > 1 {
			t.Errorf("%v: exit %d stderr %q", args, code, errOut)
		}
	}
}
