package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/testutil"
)

// snapDeps serves live from snap and fails the test if replay builds a
// live client.
func snapDeps(t *testing.T, snap model.Snapshot) Deps {
	t.Helper()
	return Deps{
		NewSource: func(o *Options) (model.Source, error) {
			return newSource(o, func(*Options) (model.Source, error) {
				if o.From != "" {
					t.Fatal("live client constructed during replay")
				}
				return fakeSource{snap}, nil
			})
		},
		Now:   func() time.Time { return time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC) },
		Width: testWidth,
	}
}

func runSnap(t *testing.T, deps Deps, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var o, e bytes.Buffer
	code = run(args, &o, &e, "v", deps)
	return o.String(), e.String(), code
}

func claimsFixture(t *testing.T) model.Snapshot {
	t.Helper()
	return testutil.LoadFixture(t, "testdata/fixtures/classes-claims")
}

func tmpFile(t *testing.T) string { return filepath.Join(t.TempDir(), "snap.json") }

func TestCaptureWritesPrivateFile(t *testing.T) {
	deps, p := snapDeps(t, claimsFixture(t)), tmpFile(t)
	out, errOut, code := runSnap(t, deps, "snapshot", "capture", "--file", p)
	if code != 0 || out != "" {
		t.Fatalf("code=%d out=%q err=%q", code, out, errOut)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode().Perm())
	}
	if ents, _ := os.ReadDir(filepath.Dir(p)); len(ents) != 1 {
		t.Errorf("temp files left behind: %v", ents)
	}
	var s model.Snapshot
	b, _ := os.ReadFile(p)
	if err := json.Unmarshal(b, &s); err != nil || s.SchemaVersion != model.SchemaVersion || len(s.ResourceClaims) == 0 {
		t.Fatalf("bad file: %v %.80s", err, b)
	}
}

func TestCaptureDefaultsToStdout(t *testing.T) {
	out, _, code := runSnap(t, snapDeps(t, claimsFixture(t)), "snapshot", "capture")
	if code != 0 || !strings.Contains(out, `"schemaVersion"`) {
		t.Fatalf("code=%d out=%.80s", code, out)
	}
}

func TestCaptureRefusesOverwriteWithoutForce(t *testing.T) {
	deps, p := snapDeps(t, claimsFixture(t)), tmpFile(t)
	if err := os.WriteFile(p, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, errOut, code := runSnap(t, deps, "snapshot", "capture", "-f", p)
	if b, _ := os.ReadFile(p); code != 2 || string(b) != "keep" || !strings.Contains(errOut, "already exists") || !strings.Contains(errOut, "--force") {
		t.Fatalf("code=%d err=%q content=%q", code, errOut, b)
	}
	if _, errOut, code := runSnap(t, deps, "snapshot", "capture", "-f", p, "--force"); code != 0 {
		t.Fatalf("force: code=%d err=%q", code, errOut)
	}
	if b, _ := os.ReadFile(p); !strings.Contains(string(b), `"schemaVersion"`) {
		t.Fatalf("not replaced: %.40s", b)
	}
}

func TestCaptureWriteErrorExits2(t *testing.T) {
	p := filepath.Join(t.TempDir(), "missing-dir", "snap.json")
	if _, _, code := runSnap(t, snapDeps(t, claimsFixture(t)), "snapshot", "capture", "-f", p); code != 2 {
		t.Fatalf("code=%d", code)
	}
}

func TestCaptureFailedRenameLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "snap.json")
	if err := os.Mkdir(target, 0o700); err != nil { // rename over a directory fails
		t.Fatal(err)
	}
	if _, _, code := runSnap(t, snapDeps(t, claimsFixture(t)), "snapshot", "capture", "-f", target, "--force"); code != 2 {
		t.Fatalf("code=%d", code)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 1 {
		t.Fatalf("temp file left behind: %v", ents)
	}
}

func TestCaptureRedaction(t *testing.T) {
	snap := claimsFixture(t)
	snap.ResourceClaims[0].Annotations = map[string]string{"kubectl.kubernetes.io/last-applied-configuration": "last-applied-marker"}
	snap.VMIs[0].Object["spec"] = map[string]any{"volumes": []any{map[string]any{
		"name": "ci", "cloudInitNoCloud": map[string]any{"userData": "cloud-init-marker"}}}}
	markers := []string{"opaque-parameter-marker", "last-applied-marker", "cloud-init-marker"}
	deps := snapDeps(t, snap)

	out, errOut, code := runSnap(t, deps, "snapshot", "capture")
	if code != 0 {
		t.Fatalf("code=%d %s", code, errOut)
	}
	for _, m := range markers {
		if strings.Contains(out, m) {
			t.Errorf("redacted capture contains %q", m)
		}
	}
	if !strings.Contains(out, `"redacted": true`) || strings.Contains(errOut, "sensitive") {
		t.Errorf("redacted=%v stderr=%q", strings.Contains(out, `"redacted": true`), errOut)
	}

	out, errOut, code = runSnap(t, deps, "snapshot", "capture", "--no-redact")
	if code != 0 || !strings.Contains(errOut, "may contain sensitive data") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	for _, m := range markers {
		if !strings.Contains(out, m) {
			t.Errorf("--no-redact capture lost %q", m)
		}
	}
}

func TestCaptureReportsCollectionStates(t *testing.T) {
	snap := claimsFixture(t)
	snap.Namespace = "demo"
	snap.Collection["resourceslices"] = model.CollectionStatus{State: model.StateForbidden, Reason: "no list"}
	deps, p := snapDeps(t, snap), tmpFile(t)
	_, errOut, code := runSnap(t, deps, "snapshot", "capture", "-f", p)
	if code != 0 || !strings.Contains(errOut, "resourceslices: forbidden (no list)") || strings.Contains(errOut, "pods:") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	var got model.Snapshot
	b, _ := os.ReadFile(p)
	if err := json.Unmarshal(b, &got); err != nil || got.Namespace != "demo" || got.Collection["resourceslices"].State != model.StateForbidden {
		t.Fatalf("not preserved: %v ns=%q coll=%v", err, got.Namespace, got.Collection)
	}
	if _, _, code := runSnap(t, deps, "snapshot", "capture", "--strict", "-f", tmpFile(t)); code != 3 {
		t.Fatalf("strict: code=%d", code)
	}
}

func TestReplayPropagatesUnavailable(t *testing.T) {
	snap := claimsFixture(t)
	snap.Collection["resourceslices"] = model.CollectionStatus{State: model.StateForbidden, Reason: "no list"}
	deps, p := snapDeps(t, snap), tmpFile(t)
	runSnap(t, deps, "snapshot", "capture", "-f", p)
	out, errOut, code := runSnap(t, deps, "--from", p, "-o", "json", "inspect", "slices")
	if code != 0 || strings.Count(out, `"status": "unavailable"`) != 1 || !strings.Contains(out, `"source": "snapshot"`) {
		t.Fatalf("code=%d err=%q out=%s", code, errOut, out)
	}
	if _, _, code := runSnap(t, deps, "--from", p, "--strict", "inspect", "slices"); code != 3 {
		t.Fatalf("strict code=%d", code)
	}
}

func TestFromRejectsClusterFlags(t *testing.T) {
	deps := snapDeps(t, claimsFixture(t))
	for _, f := range []string{"--kubeconfig", "--context"} {
		_, errOut, code := runSnap(t, deps, "--from", "x.json", f, "v", "inspect", "slices")
		if code != 2 || !strings.Contains(errOut, "--from cannot be combined with --kubeconfig or --context") {
			t.Errorf("%s: code=%d err=%q", f, code, errOut)
		}
	}
}

func TestDefaultDepsFromBuildsNoClients(t *testing.T) {
	p := tmpFile(t)
	runSnap(t, snapDeps(t, claimsFixture(t)), "snapshot", "capture", "-f", p)
	t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "absent"))
	src, err := DefaultDeps().NewSource(&Options{From: p})
	if err != nil {
		t.Fatal(err)
	}
	if s, err := src.Snapshot(context.Background()); err != nil || s.Source != model.SourceSnapshot {
		t.Fatalf("err=%v source=%q", err, s.Source)
	}
}

func TestReplayNamespaceFilterAndNoWidening(t *testing.T) {
	snap := claimsFixture(t)
	snap.ResourceClaims[0].Namespace = "other"
	deps, p := snapDeps(t, snap), tmpFile(t)
	runSnap(t, deps, "snapshot", "capture", "-f", p)

	out, errOut, code := runSnap(t, deps, "--from", p, "-n", "demo", "-o", "json", "inspect", "claims")
	if code != 0 || strings.Contains(out, snap.ResourceClaims[0].Name) || !strings.Contains(out, snap.ResourceClaims[1].Name) {
		t.Fatalf("code=%d err=%q out=%s", code, errOut, out)
	}

	// A namespace-scoped capture cannot be widened to another namespace.
	scoped := claimsFixture(t)
	scoped.Namespace = "demo"
	p2 := tmpFile(t)
	runSnap(t, snapDeps(t, scoped), "snapshot", "capture", "-f", p2)
	_, errOut, code = runSnap(t, deps, "--from", p2, "-n", "other", "inspect", "claims")
	if code != 2 || !strings.Contains(errOut, "cannot widen") {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
}

// Every cluster command must give identical results from the fixture and
// from a captured-then-replayed copy of it.
func TestReplayEquivalence(t *testing.T) {
	cmds := [][]string{
		{"inspect", "slices"}, {"inspect", "drivers"}, {"inspect", "drivers", "--attributes"},
		{"inspect", "attributes"}, {"inspect", "topology"}, {"inspect", "claims"}, {"inspect", "deviceclasses"},
		{"check", "alignment"}, {"check", "counters"}, {"check", "capacity"}, {"check", "topology"},
		{"report", "cluster"},
	}
	results := func(out string) string {
		var env struct{ Results json.RawMessage }
		if err := json.Unmarshal([]byte(out), &env); err != nil || len(env.Results) < 2 {
			t.Fatalf("bad envelope: %v\n%.200s", err, out)
		}
		return string(env.Results)
	}
	nonEmpty := map[string]bool{}
	defer func() {
		for _, c := range cmds {
			if !nonEmpty[strings.Join(c, " ")] {
				t.Errorf("%v yields empty results[] for every fixture: equivalence is vacuous", c)
			}
		}
	}()
	for _, fx := range []string{"xe9785l", "xe9680", "classes-claims"} {
		deps := snapDeps(t, testutil.LoadFixture(t, "testdata/fixtures/"+fx))
		p := tmpFile(t)
		if _, errOut, code := runSnap(t, deps, "snapshot", "capture", "-f", p); code != 0 {
			t.Fatalf("%s capture: %d %s", fx, code, errOut)
		}
		for _, c := range cmds {
			live, _, liveCode := runSnap(t, deps, append([]string{"-o", "json"}, c...)...)
			replay, errOut, replayCode := runSnap(t, deps, append([]string{"--from", p, "-o", "json"}, c...)...)
			if liveCode != replayCode || errOut != "" || results(live) != results(replay) {
				t.Errorf("%s %v: codes %d/%d err=%q\nlive:   %.300s\nreplay: %.300s", fx, c, liveCode, replayCode, errOut, results(live), results(replay))
			}
			if r := results(live); r != "[]" && r != "null" {
				nonEmpty[strings.Join(c, " ")] = true
			}
		}
	}
}

func TestCaptureSyncFailureRemovesTempFile(t *testing.T) {
	old := syncFile
	defer func() { syncFile = old }()
	syncFile = func(*os.File) error { return errors.New("sync boom") }
	p := tmpFile(t)
	_, errOut, code := runSnap(t, snapDeps(t, claimsFixture(t)), "snapshot", "capture", "-f", p)
	if code != 2 || !strings.Contains(errOut, "sync boom") {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
	if ents, _ := os.ReadDir(filepath.Dir(p)); len(ents) != 0 {
		t.Fatalf("file or temp left behind: %v", ents)
	}
}

func TestReplayUnavailableForEachNonOKState(t *testing.T) {
	for _, st := range []string{model.StateForbidden, model.StateNotFound, model.StateError} {
		snap := claimsFixture(t)
		snap.Collection["resourceslices"] = model.CollectionStatus{State: st, Reason: "why-" + st}
		deps, p := snapDeps(t, snap), tmpFile(t)
		runSnap(t, deps, "snapshot", "capture", "-f", p)
		out, errOut, code := runSnap(t, deps, "--from", p, "-o", "json", "inspect", "slices")
		if code != 0 || strings.Count(out, `"status": "unavailable"`) != 1 || !strings.Contains(out, "why-"+st) {
			t.Errorf("%s: code=%d err=%q out=%s", st, code, errOut, out)
		}
	}
}

// A file that appears after the pre-check must not be overwritten without --force.
func TestWriteSnapshotNeverOverwritesWithoutForce(t *testing.T) {
	p := tmpFile(t)
	if err := os.WriteFile(p, []byte("precious"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := writeSnapshot(nil, p, false, model.Snapshot{})
	if err == nil || !strings.Contains(err.Error(), "already exists; use --force") {
		t.Fatalf("err=%v", err)
	}
	if b, _ := os.ReadFile(p); string(b) != "precious" {
		t.Errorf("file overwritten: %q", b)
	}
	if m, _ := filepath.Glob(filepath.Join(filepath.Dir(p), ".dra-inspect-snapshot-*")); len(m) != 0 {
		t.Errorf("temp file left: %v", m)
	}
	if err := writeSnapshot(nil, p, true, model.Snapshot{}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) == "precious" {
		t.Error("--force did not overwrite")
	}
	q := tmpFile(t)
	if err := writeSnapshot(nil, q, false, model.Snapshot{}); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(q); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("new file: %v %v", fi, err)
	}
}
