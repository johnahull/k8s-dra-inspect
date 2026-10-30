package cli

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/snapshot"
)

// The files under docs/examples are generated from the kubevirt fixture and a
// fake node by the real CLI (snapshot capture --with-adapters, then report
// support --from). The test regenerates them and compares, so they cannot rot.
// Refresh with: go test ./internal/cli -run DocsExamples -update
func TestDocsExamples(t *testing.T) {
	dir := filepath.Join("..", "..", "docs", "examples")
	bundle := filepath.Join(dir, "support-bundle.json")
	reportFile := filepath.Join(dir, "support-report.txt")
	_, raw := captureWithAdapters(t, kvFixture(t), kvHost(t).Root)
	if f := flag.Lookup("update"); f != nil && f.Value.String() == "true" {
		if err := os.WriteFile(bundle, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(reportFile, []byte(replayReport(t, bundle)), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got, err := os.ReadFile(bundle)
	if err != nil || !bytes.Equal(got, raw) {
		t.Fatalf("docs/examples/support-bundle.json is stale (err %v); run with -update", err)
	}
	loaded, err := snapshot.Read(bytes.NewReader(got))
	if err != nil || loaded.SchemaVersion != model.SchemaVersion || !loaded.Redacted {
		t.Fatalf("bundle does not load: schema %q redacted %v err %v", loaded.SchemaVersion, loaded.Redacted, err)
	}
	want, err := os.ReadFile(reportFile)
	if report := replayReport(t, bundle); err != nil || string(want) != report {
		t.Fatalf("docs/examples/support-report.txt is stale (err %v); run with -update\n%s", err, report)
	}
}

// replayReport runs `report support --from FILE` (table output) offline.
func replayReport(t *testing.T, bundle string) string {
	t.Helper()
	deps := snapDeps(t, model.Snapshot{})
	deps.NewHost = noHost(t)
	out, errOut, _ := runSnap(t, deps, "report", "support", "--from", bundle, "-o", "table")
	if errOut != "" {
		t.Fatalf("stderr %q", errOut)
	}
	return out
}
