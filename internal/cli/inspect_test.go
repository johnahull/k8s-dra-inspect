package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/testutil"
)

type fakeSource struct{ snap model.Snapshot }

func (f fakeSource) Snapshot(context.Context) (model.Snapshot, error) { return f.snap, nil }

const testWidth = 100

func inspectRun(t *testing.T, snap model.Snapshot, args ...string) (string, int) {
	t.Helper()
	deps := Deps{
		NewSource: func(*Options) (model.Source, error) { return fakeSource{snap}, nil },
		Now:       func() time.Time { return time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC) },
		Width:     testWidth,
	}
	var stdout, stderr bytes.Buffer
	code := run(append([]string{"inspect"}, args...), &stdout, &stderr, "v", deps)
	if stderr.Len() > 0 {
		t.Fatalf("stderr: %s", stderr.String())
	}
	return stdout.String(), code
}

var inspectCases = []struct {
	name string
	args []string
}{
	{"slices", []string{"slices"}},
	{"drivers", []string{"drivers"}},
	{"drivers-attributes", []string{"drivers", "--attributes"}},
	{"attributes", []string{"attributes"}},
	{"attributes-all-capacity", []string{"attributes", "--all-attributes", "--include-capacity"}},
}

func TestInspectGolden(t *testing.T) {
	for _, fx := range []string{"xe9785l", "xe9680", "unicode-long"} {
		snap := testutil.LoadFixture(t, "testdata/fixtures/"+fx)
		for _, c := range inspectCases {
			for _, format := range []string{"table", "json"} {
				t.Run(c.name+"/"+fx+"/"+format, func(t *testing.T) {
					out, code := inspectRun(t, snap, append([]string{"-o", format}, c.args...)...)
					if code != 0 {
						t.Fatalf("exit %d", code)
					}
					ext := map[string]string{"table": "txt", "json": "json"}[format]
					testutil.Golden(t, "inspect-"+c.name+"-"+fx+"."+ext, []byte(out))
				})
			}
		}
	}
}

func TestInspectTableLayout(t *testing.T) {
	for _, fx := range []string{"xe9785l", "xe9680", "unicode-long"} {
		snap := testutil.LoadFixture(t, "testdata/fixtures/"+fx)
		for _, c := range inspectCases {
			out, _ := inspectRun(t, snap, c.args...)
			lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
			if len(lines) < 2 || strings.ToUpper(lines[0]) != lines[0] {
				t.Fatalf("%s/%s: header missing:\n%s", c.name, fx, out)
			}
			for _, l := range lines {
				if w := runewidth.StringWidth(l); w > testWidth {
					t.Errorf("%s/%s: width %d: %q", c.name, fx, w, l)
				}
				if !utf8.ValidString(l) || strings.ContainsAny(l, "\x1b\t") || strings.HasSuffix(l, " ") {
					t.Errorf("%s/%s: bad line %q", c.name, fx, l)
				}
			}
		}
	}
	snap := testutil.LoadFixture(t, "testdata/fixtures/unicode-long")
	out, _ := inspectRun(t, snap, "slices")
	if !strings.Contains(out, "…") {
		t.Errorf("long names should be truncated with an ellipsis:\n%s", out)
	}
}

func TestInspectJSONKeepsExactNames(t *testing.T) {
	snap := testutil.LoadFixture(t, "testdata/fixtures/unicode-long")
	out, _ := inspectRun(t, snap, "-o", "json", "attributes")
	var env struct {
		SchemaVersion string
		Source        string
		Results       []model.Result
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatal(err)
	}
	if env.SchemaVersion != model.SchemaVersion || env.Source != "snapshot" {
		t.Fatalf("%+v", env)
	}
	var found bool
	for _, r := range env.Results {
		if len(r.Subject) > 160 && strings.Contains(r.Subject, "device-with-an-extremely-long-name-that-also-exceeds-eighty-characters-in-total") {
			found = true
		}
	}
	if !found {
		t.Fatalf("exact long subject missing:\n%s", out)
	}
}

func TestAttributesDefaultSetAndRendering(t *testing.T) {
	snap := testutil.LoadFixture(t, "testdata/fixtures/unicode-long")
	out, _ := inspectRun(t, snap, "-o", "json", "attributes", "--include-capacity")
	var env struct{ Results []model.Result }
	_ = json.Unmarshal([]byte(out), &env)
	var long, devB map[string]any
	for _, r := range env.Results {
		if strings.HasSuffix(r.Subject, "-in-total") {
			long = r.Details
		} else if strings.HasSuffix(r.Subject, "/dev-b") {
			devB = r.Details
		}
	}
	attrs := long["attributes"].(map[string]any)
	for k := range attrs {
		if strings.Contains(k, "cpuSocketID") || strings.Contains(k, "channels") {
			t.Errorf("non-default attribute %q shown by default", k)
		}
	}
	if attrs["resource.kubernetes.io/pcieRoot"] != "[pci0000:00,pci0000:80]" {
		t.Errorf("list attr: %v", attrs)
	}
	if attrs["resource.kubernetes.io/numaNode"] != "1" {
		t.Errorf("scalar attr: %v", attrs)
	}
	caps := long["capacity"].(map[string]any)
	if caps["memory"] != "64Gi" || len(caps) != 2 || devB["capacity"].(map[string]any)["memory"] != "500Mi" {
		t.Errorf("capacity: %v / %v", caps, devB["capacity"])
	}

	out, _ = inspectRun(t, snap, "-o", "json", "attributes", "--all-attributes")
	if !strings.Contains(out, "cpuSocketID") || !strings.Contains(out, "[4,2]") && !strings.Contains(out, "[2,4]") {
		t.Errorf("--all-attributes missing cpuSocketID or int list:\n%s", out)
	}
	if strings.Contains(out, `"capacity"`) {
		t.Error("capacity shown without --include-capacity")
	}
}

func TestInspectUnavailable(t *testing.T) {
	snap := testutil.LoadFixture(t, "testdata/fixtures/xe9680")
	snap.Collection["resourceslices"] = model.CollectionStatus{State: model.StateForbidden, Reason: "forbidden: cannot list resourceslices"}
	for _, args := range [][]string{{"slices"}, {"drivers"}, {"attributes"}} {
		out, code := inspectRun(t, snap, args...)
		want := "UNAVAILABLE: inspect." + args[0] + ": forbidden: cannot list resourceslices\n"
		if out != want || code != 0 {
			t.Errorf("%v: code=%d out=%q", args, code, out)
		}
		if _, code := inspectRun(t, snap, append([]string{"--strict"}, args...)...); code != 3 {
			t.Errorf("%v strict: code=%d", args, code)
		}
		jout, _ := inspectRun(t, snap, append([]string{"-o", "json"}, args...)...)
		if strings.Count(jout, `"status": "unavailable"`) != 1 {
			t.Errorf("%v: want exactly one unavailable result:\n%s", args, jout)
		}
	}
}

func TestLiveSourceRejectsFrom(t *testing.T) {
	var stdout, stderr bytes.Buffer
	deps := DefaultDeps()
	if code := run([]string{"--from", "x.json", "inspect", "slices"}, &stdout, &stderr, "v", deps); code != 2 ||
		!strings.Contains(stderr.String(), "not available yet") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

// Middle truncation must keep the distinguishing tail of device identifiers.
func TestAttributesSubjectsDistinctAfterTruncation(t *testing.T) {
	snap := testutil.LoadFixture(t, "testdata/fixtures/xe9680")
	out, _ := inspectRun(t, snap, "attributes")
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")[1:]
	seen := map[string]string{}
	for _, l := range lines {
		// DEVICE is the first column; cells are separated by two spaces.
		cell, _, _ := strings.Cut(l, "  ")
		if prev, dup := seen[cell]; dup {
			t.Errorf("subject %q truncated ambiguously:\n%s\n%s", cell, prev, l)
		}
		seen[cell] = l
	}
	if len(seen) < 2 {
		t.Fatalf("too few rows:\n%s", out)
	}
}

var draViewCases = []struct {
	name     string
	args     []string
	fixtures []string
}{
	{"topology", []string{"topology"}, []string{"xe9785l", "xe9680", "unicode-long", "numa-split", "pcie-split", "missing-attr", "classes-claims"}},
	{"topology-compact", []string{"topology", "--compact"}, []string{"xe9785l", "xe9680"}},
	{"deviceclasses", []string{"deviceclasses"}, []string{"xe9785l", "xe9680", "classes-claims"}},
	{"deviceclasses-selector", []string{"deviceclasses", "--selector", "tier in (gold,silver),vendor=example"}, []string{"classes-claims"}},
	{"claims", []string{"claims"}, []string{"xe9785l", "numa-split", "missing-attr", "classes-claims"}},
}

func TestDRAViewsGolden(t *testing.T) {
	for _, c := range draViewCases {
		for _, fx := range c.fixtures {
			snap := testutil.LoadFixture(t, "testdata/fixtures/"+fx)
			for _, format := range []string{"table", "json"} {
				t.Run(c.name+"/"+fx+"/"+format, func(t *testing.T) {
					out, code := inspectRun(t, snap, append([]string{"-o", format}, c.args...)...)
					if code != 0 {
						t.Fatalf("exit %d\n%s", code, out)
					}
					if format == "table" {
						for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
							if w := runewidth.StringWidth(l); w > testWidth {
								t.Errorf("width %d: %q", w, l)
							}
						}
					}
					ext := map[string]string{"table": "txt", "json": "json"}[format]
					testutil.Golden(t, "inspect-"+c.name+"-"+fx+"."+ext, []byte(out))
				})
			}
		}
	}
}

func TestDeviceClassesInvalidSelector(t *testing.T) {
	snap := testutil.LoadFixture(t, "testdata/fixtures/classes-claims")
	deps := Deps{NewSource: func(*Options) (model.Source, error) { return fakeSource{snap}, nil }, Now: time.Now}
	var stdout, stderr bytes.Buffer
	code := run([]string{"inspect", "deviceclasses", "--selector", "a in ("}, &stdout, &stderr, "v", deps)
	if code != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "selector") {
		t.Fatalf("code=%d out=%q err=%q", code, stdout.String(), stderr.String())
	}
}

func TestDeviceClassesSelectorFilters(t *testing.T) {
	snap := testutil.LoadFixture(t, "testdata/fixtures/classes-claims")
	out, _ := inspectRun(t, snap, "deviceclasses", "--selector", "tier=gold")
	if !strings.Contains(out, "gpu.example.com") || strings.Contains(out, "nic.example.com") || strings.Contains(out, "plain.example.com") {
		t.Errorf("selector not applied:\n%s", out)
	}
}

// Opaque configuration and CEL text must never reach user output.
func TestNoSelectorOrConfigLeak(t *testing.T) {
	snap := testutil.LoadFixture(t, "testdata/fixtures/classes-claims")
	for _, args := range [][]string{{"deviceclasses"}, {"claims"}, {"topology"}} {
		for _, format := range []string{"table", "json"} {
			out, _ := inspectRun(t, snap, append([]string{"-o", format}, args...)...)
			for _, bad := range []string{"leakmarker", "opaque-parameter-marker", "device.driver", "secretKnob", "claimKnob", "isGreaterThan"} {
				if strings.Contains(out, bad) {
					t.Errorf("%v %s leaks %q:\n%s", args, format, bad, out)
				}
			}
		}
	}
}

func TestDRAViewsUnavailable(t *testing.T) {
	snap := testutil.LoadFixture(t, "testdata/fixtures/classes-claims")
	for k, args := range map[string][]string{"resourceslices": {"topology"}, "deviceclasses": {"deviceclasses"}, "resourceclaims": {"claims"}} {
		s := snap
		s.Collection = map[string]model.CollectionStatus{}
		for kk, v := range snap.Collection {
			s.Collection[kk] = v
		}
		s.Collection[k] = model.CollectionStatus{State: model.StateForbidden, Reason: "forbidden: cannot list " + k}
		out, code := inspectRun(t, s, args...)
		if want := "UNAVAILABLE: inspect." + args[0] + ": forbidden: cannot list " + k + "\n"; out != want || code != 0 {
			t.Errorf("%v: code=%d out=%q", args, code, out)
		}
	}
}

func TestClaimsVMIUnavailable(t *testing.T) {
	snap := testutil.LoadFixture(t, "testdata/fixtures/classes-claims")
	snap.VMIs = nil
	snap.Collection["virtualmachineinstances"] = model.CollectionStatus{State: model.StateNotFound, Reason: "kubevirt.io/v1 not served"}
	out, code := inspectRun(t, snap, "-o", "json", "claims")
	if code != 0 || !strings.Contains(out, `"vmi": "unavailable: kubevirt.io/v1 not served"`) {
		t.Errorf("code=%d\n%s", code, out)
	}
}

func TestDRAViewsLayout(t *testing.T) {
	snap := testutil.LoadFixture(t, "testdata/fixtures/classes-claims")
	for _, c := range []struct {
		args []string
		want []string // exact lines, header first
	}{
		{[]string{"topology"}, []string{
			"NODE                NUMA  PCIE ROOT   DRIVERS            DEVICES     NOTE",
			"node-a.example.com  0     pci0000:00  gpu.example.com=1  gpu-0       -"}},
		{[]string{"topology", "--compact"}, []string{
			"NODE                NUMA  PCIE ROOT   DRIVERS            NOTE",
			"node-a.example.com  1     pci0000:80  gpu.example.com=1  -"}},
		{[]string{"deviceclasses"}, []string{
			"NAME               LABELS                      SELECTORS  CONFIG",
			"plain.example.com                              1          -"}},
		{[]string{"claims"}, []string{
			"CLAIM                 ALLOCATED  DEVICES  NODE                USED BY",
			"demo/web-0-gpu-xyz12  yes        1        node-a.example.com  web-0"}},
	} {
		out, _ := inspectRun(t, snap, c.args...)
		lines := strings.Split(out, "\n")
		if lines[0] != c.want[0] || !slices.Contains(lines, c.want[1]) {
			t.Errorf("%v layout:\n%s", c.args, out)
		}
	}
}
