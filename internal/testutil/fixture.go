// Package testutil holds helpers shared by tests: fixture loading and goldens.
package testutil

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	"sigs.k8s.io/yaml"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
)

var update = flag.Bool("update", false, "rewrite golden files")

// repoRoot finds the directory holding go.mod, so tests in any package can
// use paths like "testdata/fixtures/x".
func repoRoot(t testing.TB) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

// LoadFixture reads every *.yaml in dir (repo-relative unless absolute). Each
// file is a `kind: List` or a single object.
func LoadFixture(t testing.TB, dir string) model.Snapshot {
	t.Helper()
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(repoRoot(t), dir)
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no fixtures in %s (err=%v)", dir, err)
	}
	snap := model.Snapshot{
		SchemaVersion: model.SchemaVersion,
		GeneratedAt:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Source:        model.SourceSnapshot,
		Collection:    map[string]model.CollectionStatus{},
	}
	for _, k := range []string{"resourceslices", "deviceclasses", "resourceclaims", "pods", "nodes", "virtualmachineinstances"} {
		snap.Collection[k] = model.CollectionStatus{State: model.StateOK}
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var list struct {
			Kind  string            `json:"kind"`
			Items []json.RawMessage `json:"items"`
		}
		if err := yaml.Unmarshal(data, &list); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		items := list.Items
		if list.Kind != "List" {
			j, err := yaml.YAMLToJSON(data)
			if err != nil {
				t.Fatalf("%s: %v", f, err)
			}
			items = []json.RawMessage{j}
		}
		for _, raw := range items {
			add(t, &snap, raw, f)
		}
	}
	snap.Normalize()
	return snap
}

func add(t testing.TB, snap *model.Snapshot, raw json.RawMessage, file string) {
	t.Helper()
	var tm struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(raw, &tm); err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	var err error
	switch tm.Kind {
	case "ResourceSlice":
		var o resourcev1.ResourceSlice
		err = json.Unmarshal(raw, &o)
		snap.ResourceSlices = append(snap.ResourceSlices, o)
	case "DeviceClass":
		var o resourcev1.DeviceClass
		err = json.Unmarshal(raw, &o)
		snap.DeviceClasses = append(snap.DeviceClasses, o)
	case "ResourceClaim":
		var o resourcev1.ResourceClaim
		err = json.Unmarshal(raw, &o)
		snap.ResourceClaims = append(snap.ResourceClaims, o)
	case "Pod":
		var o corev1.Pod
		err = json.Unmarshal(raw, &o)
		snap.Pods = append(snap.Pods, model.NewPodRef(o))
	case "Node":
		var o corev1.Node
		err = json.Unmarshal(raw, &o)
		snap.Nodes = append(snap.Nodes, o)
	default:
		t.Fatalf("%s: unsupported kind %q", file, tm.Kind)
	}
	if err != nil {
		t.Fatalf("%s: %s: %v", file, tm.Kind, err)
	}
}

// Golden compares got with testdata/golden/<name>; with -update it rewrites it.
func Golden(t testing.TB, name string, got []byte) {
	t.Helper()
	path := filepath.Join(repoRoot(t), "testdata", "golden", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update)", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("golden %s mismatch (run with -update)\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}
