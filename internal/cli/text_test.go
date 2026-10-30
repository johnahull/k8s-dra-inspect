package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/testutil"
)

// textRun runs one command with the given terminal width and TTY flag.
func textRun(t *testing.T, snap model.Snapshot, width int, tty bool, args ...string) string {
	t.Helper()
	deps := Deps{
		NewSource: func(*Options) (model.Source, error) { return fakeSource{snap}, nil },
		Now:       func() time.Time { return time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC) },
		Width:     width,
		IsTTY:     tty,
	}
	var stdout, stderr bytes.Buffer
	run(args, &stdout, &stderr, "v", deps)
	if stderr.Len() > 0 {
		t.Fatalf("stderr: %s", stderr.String())
	}
	return stdout.String()
}

var allFixtures = []string{"xe9785l", "xe9680", "unicode-long", "classes-claims", "multinode", "numa-split", "pcie-split", "missing-attr"}

// textCases is every command with a text view; each runs over its fixtures
// and is stored as testdata/golden/inspect-<name>-<fixture>.text.
var textCases = []struct {
	name     string
	args     []string
	fixtures []string
}{
	{"slices", []string{"slices"}, allFixtures},
	{"claims", []string{"claims"}, []string{"xe9785l", "classes-claims", "multinode", "numa-split", "missing-attr"}},
	{"drivers", []string{"drivers"}, []string{"xe9785l", "xe9680", "unicode-long", "multinode", "classes-claims"}},
	{"drivers-attributes", []string{"drivers", "--attributes"}, []string{"xe9785l", "xe9680", "unicode-long"}},
	{"attributes", []string{"attributes"}, []string{"xe9785l", "xe9680", "unicode-long", "multinode", "classes-claims"}},
	{"attributes-all-capacity", []string{"attributes", "--all-attributes", "--include-capacity"}, []string{"xe9785l", "xe9680", "unicode-long"}},
	{"topology", []string{"topology"}, allFixtures},
	{"topology-compact", []string{"topology", "--compact"}, []string{"xe9785l", "xe9680", "multinode"}},
	{"deviceclasses", []string{"deviceclasses"}, []string{"xe9785l", "xe9680", "classes-claims"}},
	{"deviceclasses-selector", []string{"deviceclasses", "--selector", "tier in (gold,silver),vendor=example"}, []string{"classes-claims"}},
}

func TestInspectTextGolden(t *testing.T) {
	for _, c := range textCases {
		for _, fx := range c.fixtures {
			t.Run(c.name+"/"+fx, func(t *testing.T) {
				snap := testutil.LoadFixture(t, "testdata/fixtures/"+fx)
				out := textRun(t, snap, 0, false, append([]string{"-o", "text", "inspect"}, c.args...)...)
				testutil.Golden(t, "inspect-"+c.name+"-"+fx+".text", []byte(out))
			})
		}
	}
}

func TestInspectDefaultOutputIsTextView(t *testing.T) {
	snap := testutil.LoadFixture(t, "testdata/fixtures/classes-claims")
	for _, c := range textCases {
		def := textRun(t, snap, 0, false, append([]string{"inspect"}, c.args...)...)
		txt := textRun(t, snap, 0, false, append([]string{"-o", "text", "inspect"}, c.args...)...)
		tbl := textRun(t, snap, 0, false, append([]string{"-o", "table", "inspect"}, c.args...)...)
		if def != txt || def == tbl || !strings.Contains(def, "═══ ") {
			t.Errorf("%s: default must be the text view, not the table:\n%s", c.name, def)
		}
	}
}

// Layout properties hold for every text output.
func TestTextLayoutProperties(t *testing.T) {
	for _, c := range textCases {
		for _, fx := range c.fixtures {
			snap := testutil.LoadFixture(t, "testdata/fixtures/"+fx)
			args := append([]string{"-o", "text", "inspect"}, c.args...)
			out := textRun(t, snap, 0, false, args...)
			if again := textRun(t, snap, 0, false, args...); again != out {
				t.Errorf("%s/%s: not deterministic", c.name, fx)
			}
			if out == "" {
				t.Errorf("%s/%s: empty", c.name, fx)
			}
			for _, l := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
				if !utf8.ValidString(l) || strings.ContainsAny(l, "\x1b\t…") || strings.HasSuffix(l, " ") {
					t.Errorf("%s/%s: bad line %q", c.name, fx, l)
				}
			}
			// Prose (marker) lines wrap at the terminal width unless a single token overflows.
			for _, l := range strings.Split(textRun(t, snap, testWidth, false, args...), "\n") {
				if s := strings.TrimLeft(l, " "); (strings.HasPrefix(s, "! ") || strings.HasPrefix(s, "✗ ") || strings.HasPrefix(s, "? ")) &&
					runewidth.StringWidth(l) > testWidth && strings.Contains(strings.TrimLeft(s[len("! "):], " "), " ") {
					t.Errorf("%s/%s: prose line wider than %d: %q", c.name, fx, testWidth, l)
				}
			}
		}
	}
}

func TestTextColourOnlyOnATerminal(t *testing.T) {
	snap := testutil.LoadFixture(t, "testdata/fixtures/classes-claims")
	for _, c := range textCases {
		args := append([]string{"inspect"}, c.args...)
		t.Setenv("NO_COLOR", "")
		if out := textRun(t, snap, 0, true, args...); !strings.Contains(out, "\x1b[1;36m═══ ") {
			t.Errorf("%s: no colour on a TTY:\n%s", c.name, out)
		}
		if out := textRun(t, snap, 0, false, args...); strings.Contains(out, "\x1b") {
			t.Errorf("%s: colour off a TTY", c.name)
		}
		t.Setenv("NO_COLOR", "1")
		if out := textRun(t, snap, 0, true, args...); strings.Contains(out, "\x1b") {
			t.Errorf("%s: NO_COLOR ignored", c.name)
		}
	}
}

func TestTextUsageScopeFromTheCLI(t *testing.T) {
	snap := testutil.LoadFixture(t, "testdata/fixtures/classes-claims")
	snap.Namespace = "demo"
	if out := textRun(t, snap, 0, false, "inspect", "slices"); !strings.Contains(out, "2 in use (namespace demo only)") {
		t.Errorf("-n scope missing:\n%s", out)
	}
	snap = testutil.LoadFixture(t, "testdata/fixtures/classes-claims")
	snap.Collection["resourceclaims"] = model.CollectionStatus{State: model.StateForbidden, Reason: "forbidden: cannot list resourceclaims"}
	if out := textRun(t, snap, 0, false, "inspect", "slices"); !strings.Contains(out, "usage unavailable: forbidden: cannot list resourceclaims") || strings.Contains(out, "in use") {
		t.Errorf("forbidden claims:\n%s", out)
	}
	snap = testutil.LoadFixture(t, "testdata/fixtures/classes-claims")
	snap.Collection["pods"] = model.CollectionStatus{State: model.StateForbidden, Reason: "forbidden: cannot list pods"}
	if out := textRun(t, snap, 0, false, "inspect", "claims"); !strings.Contains(out, "demo/web-0-gpu-xyz12 → pods unavailable") {
		t.Errorf("pods not collected:\n%s", out)
	}
}
