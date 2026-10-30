package cli

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
)

// The sample outputs in README.md are real: each "<!-- sample: ARGS -->" block
// must equal the first lines of what the command prints for the xe9785l
// snapshot. When the text layout changes, copy the new output into the README.
func TestReadmeSamplesAreRealOutput(t *testing.T) {
	b, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile("(?s)<!-- sample: ([a-z ]+) -->\n```text\n\\$ [^\n]*\n(.*?)```")
	samples := re.FindAllStringSubmatch(string(b), -1)
	if len(samples) < 3 {
		t.Fatalf("README has %d samples, want at least 3", len(samples))
	}
	for _, m := range samples {
		args := append(strings.Fields(m[1]), "--from", "../../testdata/snapshots/xe9785l.json")
		deps := snapDeps(t, model.Snapshot{})
		deps.NewHost = noHost(t)
		out, errOut, code := runSnap(t, deps, args...)
		if errOut != "" || code != 0 {
			t.Fatalf("%s: stderr %q code %d", m[1], errOut, code)
		}
		want := m[2]
		if !strings.HasPrefix(out, want) {
			t.Errorf("README sample %q is stale.\n--- README ---\n%s--- output starts with ---\n%s", m[1], want, out[:min(len(out), len(want)+200)])
		}
	}
}
