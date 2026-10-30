package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/testutil"
)

// textCase is one check or report command run against one snapshot.
type textCase struct {
	golden string // testdata/golden/<golden>.text
	snap   func(t *testing.T) model.Snapshot
	host   func(t *testing.T) NewHostFunc
	args   []string
	exit   int
}

func fixtureSnap(name string) func(*testing.T) model.Snapshot {
	return func(t *testing.T) model.Snapshot { return testutil.LoadFixture(t, "testdata/fixtures/"+name) }
}

func hostCase(good bool) func(*testing.T) model.Snapshot {
	return func(t *testing.T) model.Snapshot { return hostSnap(t, hostFixture(t, good)) }
}

// runText runs one case at the given width and TTY flag (colour off unless tty).
func (c textCase) run(t *testing.T, width int, tty bool, extra ...string) (string, int) {
	t.Helper()
	snap := c.snap(t)
	nh := NewHostFunc(nil)
	if c.host != nil {
		nh = c.host(t)
	}
	deps := Deps{
		NewSource: func(*Options) (model.Source, error) { return fakeSource{snap}, nil },
		NewHost:   nh,
		Now:       func() time.Time { return time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC) },
		Width:     width,
		IsTTY:     tty,
	}
	var stdout, stderr bytes.Buffer
	code := run(append(append([]string{}, c.args...), extra...), &stdout, &stderr, "v", deps)
	if stderr.Len() > 0 {
		t.Fatalf("stderr: %s", stderr.String())
	}
	return stdout.String(), code
}

func alignWithPod(t *testing.T) model.Snapshot {
	snap := testutil.LoadFixture(t, "testdata/fixtures/numa-split")
	snap.Pods = []model.PodRef{{Name: "two-gpus", Namespace: "demo",
		ResourceClaimStatuses: []corev1.PodResourceClaimStatus{{Name: "gpus", ResourceClaimName: ptr.To("two-gpus")}}}}
	return snap
}

func noKV(t *testing.T) model.Snapshot {
	s := kvFixture(t)
	s.VMIs = nil
	s.Collection["virtualmachineinstances"] = model.CollectionStatus{State: model.StateNotFound, Reason: "kubevirt.io/v1 not served"}
	return s
}

func emptyKV(t *testing.T) model.Snapshot {
	s := kvFixture(t)
	s.VMIs = nil
	return s
}

// checkTextCases covers every check and report command over the same fixture
// matrix as the table and JSON goldens, plus argument variants.
func checkTextCases(t *testing.T) []textCase {
	var cs []textCase
	for _, c := range checkCases {
		cs = append(cs, textCase{
			golden: "check-" + c.check + "-" + strings.ReplaceAll(c.fixture, "/", "-"),
			snap:   fixtureSnap(c.fixture), args: []string{"check", c.check, "-o", "text"}, exit: c.wantExit,
		})
	}
	cs = append(cs,
		textCase{golden: "check-alignment-arg-pod-and-claim", snap: alignWithPod, args: []string{"check", "alignment", "two-gpus", "-o", "text"}},
		textCase{golden: "check-alignment-arg-claim-namespace", snap: fixtureSnap("numa-split"), args: []string{"check", "alignment", "two-gpus", "-n", "demo", "-o", "text"}},
		textCase{golden: "check-kubevirt-fixture", snap: kvFixture, host: noHost, args: []string{"check", "kubevirt", "-o", "text"}, exit: 1},
		textCase{golden: "check-kubevirt-one", snap: kvFixture, host: noHost, args: []string{"check", "kubevirt", "demo/vm-ok", "-o", "text"}},
		textCase{golden: "check-kubevirt-notfound", snap: noKV, host: noHost, args: []string{"check", "kubevirt", "-o", "text"}},
		textCase{golden: "check-kubevirt-none", snap: emptyKV, host: noHost, args: []string{"check", "kubevirt", "-o", "text"}},
	)
	for _, fx := range []string{"xe9785l", "xe9680"} {
		cs = append(cs, textCase{golden: "report-cluster-" + fx, snap: fixtureSnap(fx), args: []string{"report", "cluster", "-o", "text"}})
	}
	live := kvHost(t).Root
	for _, sc := range []struct{ name, root string }{{"live", live}, {"nohost", "/nonexistent/host-root"}} {
		cs = append(cs, textCase{golden: "report-support-" + sc.name, snap: kvFixture, host: func(*testing.T) NewHostFunc { return (&recorder{}).New },
			args: []string{"report", "support", "--host-root", sc.root, "--node-name", kvNode, "-o", "text"}, exit: 1})
	}
	for _, good := range []bool{true, false} {
		name, vfioExit := "bad", 1
		if good {
			name, vfioExit = "good", 0
		}
		cs = append(cs,
			textCase{golden: "check-cpupinning-host-" + name, snap: hostCase(good), host: noHost, args: []string{"check", "cpupinning", "app", "-n", "demo", "--from", "x.json", "-o", "text"}},
			textCase{golden: "check-vfio-host-" + name, snap: hostCase(good), host: noHost, args: []string{"check", "vfio", "--from", "x.json", "-o", "text"}, exit: vfioExit},
		)
	}
	return cs
}

func TestCheckTextGolden(t *testing.T) {
	for _, c := range checkTextCases(t) {
		t.Run(c.golden, func(t *testing.T) {
			out, code := c.run(t, 0, false)
			if code != c.exit {
				t.Fatalf("exit %d, want %d\n%s", code, c.exit, out)
			}
			testutil.Golden(t, c.golden+".text", []byte(out))
		})
	}
}

// Without -o the check commands print the text view, not the table.
func TestCheckDefaultOutputIsTextView(t *testing.T) {
	for _, c := range checkTextCases(t) {
		out, code := c.run(t, 0, false)
		args := c.args[:len(c.args)-2] // drop -o text
		def, dcode := textCase{snap: c.snap, host: c.host, args: args}.run(t, 0, false)
		tbl, _ := textCase{snap: c.snap, host: c.host, args: append(append([]string{}, args...), "-o", "table")}.run(t, 0, false)
		if def != out || dcode != code {
			t.Errorf("%s: default differs from -o text", c.golden)
		}
		if strings.HasPrefix(out, "═══ ") && def == tbl {
			t.Errorf("%s: default equals the table", c.golden)
		}
		if !strings.Contains(out, "═══ ") && !strings.HasPrefix(out, "UNAVAILABLE: ") {
			t.Errorf("%s: neither a banner nor UNAVAILABLE lines:\n%s", c.golden, out)
		}
	}
}

// -o table and -o json keep their exact shape (the existing goldens prove the bytes).
func TestCheckTextLayoutProperties(t *testing.T) {
	for _, c := range checkTextCases(t) {
		out, _ := c.run(t, 0, false)
		if again, _ := c.run(t, 0, false); again != out {
			t.Errorf("%s: not deterministic", c.golden)
		}
		if out == "" {
			t.Errorf("%s: empty", c.golden)
		}
		for _, l := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
			if !utf8.ValidString(l) || strings.ContainsAny(l, "\x1b\t…") || strings.HasSuffix(l, " ") {
				t.Errorf("%s: bad line %q", c.golden, l)
			}
		}
		wrapped, _ := c.run(t, testWidth, false)
		for _, l := range strings.Split(wrapped, "\n") {
			s := strings.TrimLeft(l, " ")
			if strings.HasPrefix(s, "UNAVAILABLE: ") {
				continue
			}
			for _, m := range []string{"! ", "✗ ", "? ", "· ", "✓ ", "E "} {
				if strings.HasPrefix(s, m) && runewidth.StringWidth(l) > testWidth && strings.Contains(s[len(m):], " ") {
					t.Errorf("%s: prose line wider than %d: %q", c.golden, testWidth, l)
				}
			}
		}
	}
}

func TestCheckTextColourOnlyOnATerminal(t *testing.T) {
	for _, c := range checkTextCases(t) {
		t.Setenv("NO_COLOR", "")
		out, _ := c.run(t, 0, true)
		if strings.Contains(out, "═══ ") && !strings.Contains(out, "\x1b[1;36m═══ ") {
			t.Errorf("%s: no colour on a TTY:\n%s", c.golden, out)
		}
		if off, _ := c.run(t, 0, false); strings.Contains(off, "\x1b") {
			t.Errorf("%s: colour off a TTY", c.golden)
		}
		t.Setenv("NO_COLOR", "1")
		if nc, _ := c.run(t, 0, true); strings.Contains(nc, "\x1b") {
			t.Errorf("%s: NO_COLOR ignored", c.golden)
		}
	}
}

// A mix of unavailable and available results prints the UNAVAILABLE lines
// first, then the view of the available ones; the exit code is unchanged.
func TestCheckTextMixedUnavailableThenView(t *testing.T) {
	c := textCase{snap: kvFixture, host: noHost, args: []string{"check", "kubevirt", "-o", "text"}}
	out, code := c.run(t, 0, false)
	if !strings.HasPrefix(out, "UNAVAILABLE: check.kubevirt demo/vm-pending: VMI is not scheduled to a node\n═══ KubeVirt Check ═══\n") ||
		!strings.Contains(out, "demo/vm-ok\n") || code != 1 {
		t.Errorf("exit %d\n%s", code, out)
	}
	_, strict := c.run(t, 0, false, "--strict")
	if _, tbl := c.run(t, 0, false, "-o", "table", "--strict"); strict != tbl {
		t.Errorf("--strict exit %d, table %d", strict, tbl)
	}
}

func TestReportTextSummaryCountsWorstStatusPerMember(t *testing.T) {
	// xe9680: rootless CPU/memory pools are not topology warnings; each member counts once.
	c := textCase{snap: fixtureSnap("xe9680"), args: []string{"report", "cluster"}}
	out, code := c.run(t, 0, false)
	if want := "Summary: 9 checks: 9 ok, 0 warnings, 0 failures, 0 unavailable, 0 errors\n"; !strings.Contains(out, want) || code != 0 {
		t.Errorf("exit %d\n%s", code, out)
	}
	snap := testutil.LoadFixture(t, "testdata/fixtures/xe9680")
	snap.Collection["resourceslices"] = model.CollectionStatus{State: model.StateForbidden, Reason: "forbidden: cannot list resourceslices"}
	un := textCase{snap: func(*testing.T) model.Snapshot { return snap }, args: []string{"report", "cluster"}}
	out, code = un.run(t, 0, false)
	if !strings.HasPrefix(out, "UNAVAILABLE: ") || !strings.Contains(out, "═══ Cluster Report ═══") || !strings.Contains(out, ", 0 failures, ") {
		t.Errorf("exit %d\n%s", code, out)
	}
	for _, args := range [][]string{{"report", "cluster", "--strict"}, {"report", "cluster"}} {
		_, text := textCase{snap: un.snap, args: args}.run(t, 0, false)
		_, tbl := textCase{snap: un.snap, args: append(append([]string{}, args...), "-o", "table")}.run(t, 0, false)
		if text != tbl {
			t.Errorf("%v: exit %d (text) vs %d (table)", args, text, tbl)
		}
	}
	if _, strict := un.run(t, 0, false, "--strict"); strict != 3 {
		t.Errorf("strict exit %d, want 3", strict)
	}
}

func TestReportSupportTextAdapterRowsAndCounts(t *testing.T) {
	for _, c := range checkTextCases(t) {
		if c.golden != "report-support-nohost" {
			continue
		}
		out, code := c.run(t, 0, false)
		for _, want := range []string{
			"Summary: 13 checks: 9 ok, 1 warning, 1 failure, 2 unavailable, 0 errors\n",
			"adapters\n  ? adapter.host: no sysfs under /nonexistent/host-root; run on the node (or pass --host-root)\n  ✓ adapter.kubevirt: 8 VMIs\n",
			"\nDetails: run the individual check or inspect commands, or use -o json.\n",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("missing %q in:\n%s", want, out)
			}
		}
		if strict := func() int { _, c := c.run(t, 0, false, "--strict"); return c }(); code != 1 || strict != 1 {
			t.Errorf("exit %d, strict %d", code, strict)
		}
	}
}
