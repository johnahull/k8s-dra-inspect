package cli

import (
	"os"
	"strings"
	"testing"
)

func TestCaptureHelpSaysCELKept(t *testing.T) {
	out, _, _ := runSnap(t, Deps{}, "snapshot", "capture", "--help")
	if !strings.Contains(out, "CEL selector text is kept") {
		t.Errorf("capture help must say CEL selector text is kept:\n%s", out)
	}
}

// The code keeps CEL selector text in snapshots; docs must not claim otherwise.
func TestDocsDoNotClaimCELRemoved(t *testing.T) {
	for _, f := range []string{"README.md", "docs/migration-from-dra-verify.md", "SECURITY.md"} {
		b, err := os.ReadFile("../../" + f)
		if err != nil {
			t.Fatal(err)
		}
		flat := strings.Join(strings.Fields(string(b)), " ")
		for _, bad := range []string{"driver parameters, CEL selectors, cloud-init", "CEL selectors, last-applied annotations"} {
			if strings.Contains(flat, bad) {
				t.Errorf("%s still lists CEL selectors among redacted data: %q", f, bad)
			}
		}
		if !strings.Contains(flat, "CEL selector text is kept") {
			t.Errorf("%s must state that CEL selector text is kept", f)
		}
	}
}
