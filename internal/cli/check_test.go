package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mattn/go-runewidth"
	"k8s.io/utils/ptr"

	corev1 "k8s.io/api/core/v1"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/testutil"
)

// cmdRun runs the full command line (no implicit group) against snap.
func cmdRun(t *testing.T, snap model.Snapshot, args ...string) (string, int) {
	t.Helper()
	deps := Deps{
		NewSource: func(*Options) (model.Source, error) { return fakeSource{snap}, nil },
		Now:       func() time.Time { return time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC) },
		Width:     testWidth,
	}
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr, "v", deps)
	if stderr.Len() > 0 {
		t.Fatalf("stderr: %s", stderr.String())
	}
	return stdout.String(), code
}

var checkCases = []struct {
	check    string
	fixture  string
	wantExit int
}{
	{"alignment", "numa-split", 0},   // warn
	{"alignment", "pcie-split", 1},   // fail
	{"alignment", "missing-attr", 0}, // unavailable, not strict
	{"alignment", "xe9785l", 0},
	{"alignment", "alignment/aligned", 0},
	{"alignment", "alignment/list-global-fail", 1},
	{"alignment", "alignment/list-global-ok", 0},
	{"alignment", "alignment/mixed", 0},
	{"alignment", "alignment/invalid-attr", 2}, // error
	{"counters", "counters/fits", 0},
	{"counters", "counters/exactly-full", 0},
	{"counters", "counters/overcommit", 1},
	{"counters", "counters/unknown-set", 1},
	{"counters", "counters/unknown-counter", 1},
	{"counters", "counters/stale-generation", 0}, // warn
	{"counters", "counters/none", 0},
	{"counters", "xe9785l", 0},
	{"capacity", "capacity/pass", 0},
	{"capacity", "capacity/overcommit", 1},
	{"capacity", "capacity/unpublished", 1},
	{"capacity", "xe9785l", 0},
	{"topology", "xe9785l", 0},
	{"topology", "xe9680", 0},
	{"topology", "missing-attr", 0},
	{"topology", "numa-split", 0},
}

func TestCheckGolden(t *testing.T) {
	for _, c := range checkCases {
		snap := testutil.LoadFixture(t, "testdata/fixtures/"+c.fixture)
		for _, format := range []string{"table", "json"} {
			name := c.check + "/" + c.fixture + "/" + format
			t.Run(name, func(t *testing.T) {
				out, code := cmdRun(t, snap, "check", c.check, "-o", format)
				if code != c.wantExit {
					t.Fatalf("exit %d, want %d\n%s", code, c.wantExit, out)
				}
				if format == "table" {
					for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
						if w := runewidth.StringWidth(l); w > testWidth {
							t.Errorf("width %d: %q", w, l)
						}
					}
				}
				ext := map[string]string{"table": "txt", "json": "json"}[format]
				testutil.Golden(t, "check-"+c.check+"-"+strings.ReplaceAll(c.fixture, "/", "-")+"."+ext, []byte(out))
			})
		}
	}
}

type envelope struct {
	SchemaVersion string
	Source        string
	Results       []model.Result
}

func decode(t *testing.T, out string) envelope {
	t.Helper()
	var e envelope
	if err := json.Unmarshal([]byte(out), &e); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	return e
}

func TestCheckExitCodes(t *testing.T) {
	forbidden := testutil.LoadFixture(t, "testdata/fixtures/numa-split")
	forbidden.Collection["resourceclaims"] = model.CollectionStatus{State: model.StateForbidden, Reason: "forbidden: cannot list resourceclaims"}
	cases := []struct {
		name    string
		fixture string
		snap    *model.Snapshot
		args    []string
		want    int
	}{
		{"pass", "alignment/aligned", nil, []string{"check", "alignment"}, 0},
		{"warn", "numa-split", nil, []string{"check", "alignment"}, 0},
		{"fail", "pcie-split", nil, []string{"check", "alignment"}, 1},
		{"unavailable", "", &forbidden, []string{"check", "alignment"}, 0},
		{"unavailable strict", "", &forbidden, []string{"check", "alignment", "--strict"}, 3},
		{"error", "alignment/invalid-attr", nil, []string{"check", "alignment"}, 2},
		{"error beats fail and strict unavailable", "alignment/invalid-attr", nil, []string{"report", "cluster", "--strict"}, 2},
		{"fail beats strict unavailable", "", &forbidden, []string{"check", "capacity", "--strict"}, 3},
		{"warn only", "counters/stale-generation", nil, []string{"check", "counters"}, 0},
		{"counters fail", "counters/overcommit", nil, []string{"check", "counters"}, 1},
		{"topology unavailable strict", "", &forbidden, []string{"check", "topology", "--strict"}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var snap model.Snapshot
			if tc.snap != nil {
				snap = *tc.snap
			} else {
				snap = testutil.LoadFixture(t, "testdata/fixtures/"+tc.fixture)
			}
			out, code := cmdRun(t, snap, tc.args...)
			if code != tc.want {
				t.Fatalf("exit %d, want %d\n%s", code, tc.want, out)
			}
		})
	}
	// An unavailable table prints a UNAVAILABLE line, JSON carries the status.
	out, _ := cmdRun(t, forbidden, "check", "alignment")
	if !strings.Contains(out, "UNAVAILABLE: check.alignment: forbidden: cannot list resourceclaims") {
		t.Errorf("table:\n%s", out)
	}
	out, _ = cmdRun(t, forbidden, "check", "alignment", "-o", "json")
	if r := decode(t, out).Results; len(r) != 1 || r[0].Status != model.StatusUnavailable {
		t.Errorf("json: %+v", r)
	}
}

func TestCheckErrorStatusInJSON(t *testing.T) {
	snap := testutil.LoadFixture(t, "testdata/fixtures/alignment/invalid-attr")
	out, code := cmdRun(t, snap, "check", "alignment", "-o", "json")
	r := decode(t, out).Results
	if code != 2 || len(r) != 1 || r[0].Status != model.StatusError {
		t.Fatalf("exit %d: %+v", code, r)
	}
}

func TestCheckAlignmentArguments(t *testing.T) {
	snap := testutil.LoadFixture(t, "testdata/fixtures/numa-split")
	other := snap.ResourceClaims[0].DeepCopy()
	other.Namespace, other.Name = "other", "two-gpus"
	snap.ResourceClaims = append(snap.ResourceClaims, *other)
	snap.Pods = []model.PodRef{{Name: "app", Namespace: "demo",
		ResourceClaimStatuses: []corev1.PodResourceClaimStatus{{Name: "gpus", ResourceClaimName: ptr.To("two-gpus")}}}}
	subjects := func(args ...string) string {
		out, _ := cmdRun(t, snap, append([]string{"check", "alignment", "-o", "json"}, args...)...)
		var s []string
		for _, r := range decode(t, out).Results {
			s = append(s, r.Subject+"="+string(r.Status))
		}
		return strings.Join(s, ",")
	}
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"claim in every namespace", []string{"two-gpus"}, "demo/two-gpus=warn,other/two-gpus=warn"},
		{"ns/name", []string{"other/two-gpus"}, "other/two-gpus=warn"},
		{"-n scopes", []string{"two-gpus", "-n", "demo"}, "demo/two-gpus=warn"},
		{"pod", []string{"app"}, "demo/two-gpus=warn"},
		{"unknown", []string{"nope"}, "=unavailable"},
		{"all", nil, "demo/two-gpus=warn,other/two-gpus=warn"},
	} {
		if got := subjects(tc.args...); got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.name, got, tc.want)
		}
	}
	out, _ := cmdRun(t, snap, "check", "alignment", "nope")
	if !strings.Contains(out, "UNAVAILABLE: check.alignment: no claim or pod named nope") {
		t.Errorf("unknown name:\n%s", out)
	}
	var stdout, stderr bytes.Buffer
	deps := Deps{NewSource: func(*Options) (model.Source, error) { return fakeSource{snap}, nil }, Now: time.Now}
	if code := run([]string{"check", "alignment", "a", "b"}, &stdout, &stderr, "v", deps); code != 2 || !strings.Contains(stderr.String(), "at most 1 arg") {
		t.Errorf("two args: exit %d stderr %q", code, stderr.String())
	}
}

func TestReportClusterGolden(t *testing.T) {
	for _, fx := range []string{"xe9785l", "xe9680"} {
		snap := testutil.LoadFixture(t, "testdata/fixtures/"+fx)
		for _, format := range []string{"table", "json"} {
			t.Run(fx+"/"+format, func(t *testing.T) {
				out, code := cmdRun(t, snap, "report", "cluster", "-o", format)
				if code != 0 {
					t.Fatalf("exit %d\n%s", code, out)
				}
				seen := map[string]bool{}
				if format == "json" {
					for _, r := range decode(t, out).Results {
						seen[r.Check] = true
					}
					for _, want := range []string{"inspect.slices", "inspect.drivers", "inspect.topology", "check.counters", "check.capacity", "check.topology"} {
						if !seen[want] {
							t.Errorf("report lacks %s", want)
						}
					}
				} else {
					// The summary lists every member, even one with no results (xe9680 has no claims).
					for _, want := range reportMembers {
						if !strings.Contains(out, want) {
							t.Errorf("summary lacks %s", want)
						}
					}
				}
				ext := map[string]string{"table": "txt", "json": "json"}[format]
				testutil.Golden(t, "report-cluster-"+fx+"."+ext, []byte(out))
			})
		}
	}
}

func TestReportClusterTableSummaryThenNonPass(t *testing.T) {
	snap := testutil.LoadFixture(t, "testdata/fixtures/pcie-split")
	out, code := cmdRun(t, snap, "report", "cluster")
	if code != 1 {
		t.Fatalf("exit %d", code)
	}
	lines := strings.Split(out, "\n")
	if !strings.HasPrefix(lines[0], "CHECK") || !strings.Contains(lines[0], "PASS") || !strings.Contains(lines[0], "FAIL") {
		t.Fatalf("summary header: %q", lines[0])
	}
	if !strings.Contains(out, "check.alignment") || !strings.Contains(out, "demo/aligned-nics") {
		t.Errorf("failing result missing:\n%s", out)
	}
	if strings.Contains(out, "inspect.slices  ") && strings.Count(out, "inspect.slices") > 1 {
		t.Errorf("pass results must not be listed:\n%s", out)
	}
}

func TestReportClusterUnavailableCollection(t *testing.T) {
	snap := testutil.LoadFixture(t, "testdata/fixtures/xe9680")
	snap.Collection["resourceslices"] = model.CollectionStatus{State: model.StateForbidden, Reason: "forbidden: cannot list resourceslices"}
	out, code := cmdRun(t, snap, "report", "cluster")
	if code != 0 || !strings.Contains(out, "UNAVAILABLE: check.topology: forbidden: cannot list resourceslices") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if _, code := cmdRun(t, snap, "report", "cluster", "--strict"); code != 3 {
		t.Errorf("strict exit %d", code)
	}
}
