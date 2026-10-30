package inspect

import (
	"io/fs"
	"path/filepath"
	"testing"

	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/utils/ptr"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/testutil"
)

func fixtureDirs(t *testing.T) []string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "testdata", "fixtures"))
	if err != nil {
		t.Fatal(err)
	}
	var dirs []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, _ error) error {
		if m, _ := filepath.Glob(filepath.Join(p, "*.yaml")); d.IsDir() && len(m) > 0 {
			dirs = append(dirs, p)
		}
		return nil
	})
	return dirs
}

// Every non-pass inspector result explains itself with kinded Findings, over
// every fixture, so views never have to guess from Details["reason"].
func TestEveryNonPassInspectResultHasKindedFindings(t *testing.T) {
	ins := map[string]Inspector{
		"slices": Slices(), "drivers": Drivers(true), "attributes": Attributes(true, true), "topology": Topology(false),
		"topology-compact": Topology(true), "claims": Claims(), "deviceclasses": DeviceClasses(labels.Everything()),
	}
	nonPass := 0
	for _, d := range fixtureDirs(t) {
		snap := testutil.LoadFixture(t, d)
		for name, in := range ins {
			for _, r := range in.Run(snap) {
				if r.Status == model.StatusPass || r.Status == model.StatusUnavailable {
					continue
				}
				nonPass++
				if len(r.Findings) == 0 {
					t.Errorf("%s %s %q: %s result without Findings (reason %v)", filepath.Base(d), name, r.Subject, r.Status, r.Details["reason"])
				}
				for _, f := range r.Findings {
					if f.Kind == "" {
						t.Errorf("%s %s %q: finding without Kind: %q", filepath.Base(d), name, r.Subject, f.Text)
					}
				}
			}
		}
	}
	if nonPass < 3 {
		t.Errorf("only %d non-pass results examined", nonPass)
	}
}

func TestTopologyMissingAttributeFindingNamesTheAttribute(t *testing.T) {
	root := numaDev("rooted", map[string]int64{"resource.kubernetes.io/numaNode": 0})
	root.Attributes["resource.kubernetes.io/pcieRoot"] = resourcev1.DeviceAttribute{StringValue: ptr.To("pci0000:88")}
	snap := snapOf(mkSlice("s", "p", 1, 1, root, numaDev("rootless", map[string]int64{"resource.kubernetes.io/numaNode": 0})))
	var got []model.Finding
	for _, r := range Topology(false).Run(snap) {
		got = append(got, r.Findings...)
	}
	if len(got) == 0 || got[0].Kind != model.KindAttrMissing || len(got[0].Items) != 1 || got[0].Items[0] != "pcieRoot" || got[0].Text != "missing attribute: pcieRoot" {
		t.Errorf("%+v", got)
	}
}

func TestClaimsMissingDeviceFinding(t *testing.T) {
	snap := testutil.LoadFixture(t, "testdata/fixtures/classes-claims")
	for _, r := range Claims().Run(snap) {
		if r.Details["missingDevices"] == nil {
			continue
		}
		f := r.Findings[0]
		if f.Kind != model.KindMissingDevice || f.Status != model.StatusWarn || len(f.Items) != 1 {
			t.Errorf("%+v", f)
		}
		return
	}
	t.Error("no claim with a missing device in the fixture")
}
