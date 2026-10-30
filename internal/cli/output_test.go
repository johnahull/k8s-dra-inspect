package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/render"
	"github.com/johnahull/k8s-dra-inspect/internal/testutil"
)

// B2: when every result is unavailable, text output is byte-identical to
// render.Unavailable: no banner, never wrapped, whatever the terminal width.
func TestTextAllUnavailableIsByteIdenticalAndNeverWrapped(t *testing.T) {
	snap := testutil.LoadFixture(t, "testdata/fixtures/xe9680")
	snap.Collection["resourceslices"] = model.CollectionStatus{State: model.StateError, Reason: "timed out after 3s: " + strings.Repeat("slow ", 20)}
	deps := Deps{
		NewSource: func(*Options) (model.Source, error) { return fakeSource{snap}, nil },
		Now:       func() time.Time { return time.Time{} },
		Width:     20,
	}
	var out, errOut bytes.Buffer
	if code := run([]string{"-o", "text", "inspect", "slices"}, &out, &errOut, "v", deps); code != 0 || errOut.Len() != 0 {
		t.Fatalf("exit %d stderr %q", code, errOut.String())
	}
	want := "UNAVAILABLE: inspect.slices: timed out after 3s: " + strings.Repeat("slow ", 20) + "\n"
	if out.String() != want {
		t.Errorf("got %q want %q", out.String(), want)
	}
}

func TestRenderTextUnavailableOnlySkipsTheView(t *testing.T) {
	rs := []model.Result{
		{Check: "check.a", Status: model.StatusUnavailable, Details: map[string]any{"reason": "no data"}},
		{Check: "check.b", Subject: "ns/x", Status: model.StatusUnavailable, Details: map[string]any{"reason": "later"}},
	}
	var want, got bytes.Buffer
	if err := render.Unavailable(&want, rs); err != nil {
		t.Fatal(err)
	}
	called := false
	view := func(*render.Printer, model.Snapshot, []model.Result) { called = true }
	if err := renderText(&got, render.Style{Width: 10}, model.Snapshot{}, rs, view); err != nil {
		t.Fatal(err)
	}
	if got.String() != want.String() || got.Len() == 0 || called {
		t.Errorf("got %q (view called: %v), want %q", got.String(), called, want.String())
	}
}

func TestRenderTextMixPrintsUnavailableFirstThenView(t *testing.T) {
	rs := []model.Result{
		{Check: "check.ok", Status: model.StatusPass},
		{Check: "check.a", Status: model.StatusUnavailable, Details: map[string]any{"reason": "no data"}},
	}
	snap := model.Snapshot{Namespace: "seen"}
	var got bytes.Buffer
	view := func(p *render.Printer, s model.Snapshot, rs []model.Result) {
		p.Banner("View " + s.Namespace)
		p.Line(0, "%d results", len(rs))
	}
	if err := renderText(&got, render.Style{}, snap, rs, view); err != nil {
		t.Fatal(err)
	}
	if want := "UNAVAILABLE: check.a: no data\n═══ View seen ═══\n2 results\n"; got.String() != want {
		t.Errorf("got %q want %q", got.String(), want)
	}
}

func TestOutputFlagAcceptsTextTableJSON(t *testing.T) {
	for _, o := range []string{"text", "table", "json"} {
		if err := (&Options{Output: o}).Validate(); err != nil {
			t.Errorf("-o %s rejected: %v", o, err)
		}
	}
	err := (&Options{Output: "xml"}).Validate()
	if err == nil || err.Error() != `invalid output format "xml": must be text, table or json` {
		t.Errorf("got %v", err)
	}
}

func TestOutputDefaultsToTextAndHelpSaysSo(t *testing.T) {
	cmd := NewRootCommand(&Options{}, "v", Deps{})
	f := cmd.PersistentFlags().Lookup("output")
	if f.DefValue != "text" || !strings.Contains(f.Usage, "text") || !strings.Contains(f.Usage, "table") || !strings.Contains(f.Usage, "json") {
		t.Errorf("default %q usage %q", f.DefValue, f.Usage)
	}
}

func TestReadmeAndSpecDescribeOutputModes(t *testing.T) {
	for file, want := range map[string]string{
		"README.md": "-o/--output text|table|json",
		"docs/superpowers/specs/2026-10-29-dra-inspect-design.md": "Default output: readable text",
	} {
		b, err := os.ReadFile("../../" + file)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), want) {
			t.Errorf("%s lacks %q", file, want)
		}
	}
}

// The snapshot handed to text views must be the one the results were computed
// from, including adapter evidence attached by a live support run.
func TestSupportResultsReturnEvidenceSnapshot(t *testing.T) {
	in := kvFixture(t)
	opts := &Options{}
	deps := Deps{NewHost: (&recorder{}).New}
	snap, rs := supportResults(opts, deps, kvHost(t).Root, kvNode)(t.Context(), in)
	if len(rs) == 0 || snap.Evidence["host"] == nil || snap.Evidence["kubevirt"] == nil {
		t.Errorf("evidence not attached: %d results, evidence keys %v", len(rs), len(snap.Evidence))
	}
	if in.Evidence["host"] != nil {
		t.Error("input snapshot was mutated")
	}
	if same, _ := clusterResults(opts)(t.Context(), in); same.Namespace != in.Namespace || len(same.ResourceSlices) != len(in.ResourceSlices) {
		t.Error("cluster results must return the snapshot unchanged")
	}
}
