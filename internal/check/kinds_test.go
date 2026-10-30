package check

import (
	"io/fs"
	"path/filepath"
	"slices"
	"testing"

	resourcev1 "k8s.io/api/resource/v1"

	"github.com/johnahull/k8s-dra-inspect/internal/adapters/host"
	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/testutil"
)

func kindsOf(r model.Result) []model.Kind {
	var out []model.Kind
	for _, f := range r.Findings {
		out = append(out, f.Kind)
	}
	return out
}

func finding1(t *testing.T, r model.Result, k model.Kind) model.Finding {
	t.Helper()
	for _, f := range r.Findings {
		if f.Kind == k {
			return f
		}
	}
	t.Fatalf("no %q finding in %v: %+v", k, kindsOf(r), r.Findings)
	return model.Finding{}
}

// Views pick their layout from Finding.Kind, so each check must set it.
func TestFindingKinds(t *testing.T) {
	ev := vfioHost()
	ev.PCI[bdf0] = host.PCIDevice{Driver: "nvidia", IOMMUGroup: "7"}
	peerEv := vfioHost()
	peerEv.IOMMUGroups["7"] = []string{bdf0, bdf1}
	peerEv.PCI[bdf1] = host.PCIDevice{Driver: "snd_hda_intel", Class: "0403"}
	missingDev := one(t, Alignment("", ""), snapOf(numaPool([]int64{0}), claim("ns", "c", nil, res("r", "a"), res("r", "gone"))))
	for name, tc := range map[string]struct {
		r    model.Result
		want []model.Kind
	}{
		"numa split": {one(t, Alignment("", ""), snapOf(numaPool([]int64{0}, []int64{1}), claim("ns", "c", nil, res("r", "a"), res("r", "b")))),
			[]model.Kind{model.KindNUMASplit}},
		"constraint not published then split": {one(t, Alignment("", ""), snapOf(numaPool([]int64{0}, []int64{1}),
			claim("ns", "c", []resourcev1.DeviceConstraint{match("resource.kubernetes.io/pciBusID")}, res("r", "a"), res("r", "b")))),
			[]model.Kind{model.KindConstraintMissing, model.KindNUMASplit}},
		"constraint violated": {one(t, Alignment("", ""), snapOf(numaPool([]int64{0}, []int64{1}),
			claim("ns", "c", []resourcev1.DeviceConstraint{match(numa)}, res("r", "a"), res("r", "b")))),
			[]model.Kind{model.KindConstraintViolated}},
		"missing device": {missingDev, []model.Kind{model.KindMissingDevice}},
		"topology": {one(t, Topology(), snapOf([]resourcev1.ResourceSlice{slice("s", "p", 1, 1, full("a"), dev("b", attrs{pcie: str("x")}), dev("c", attrs{numa: ints(1)}))})),
			[]model.Kind{model.KindNUMAMissing, model.KindPCIeRootMissing}},
		"vfio":       {one(t, VFIO(), vfioSnap(ev)), []model.Kind{model.KindVFIODriver}},
		"vfio peers": {one(t, VFIO(), vfioSnap(peerEv)), []model.Kind{model.KindVFIOPeers}},
		"capacity": {capOne(t, snapOf(memPool(nil), claim("ns", "c", nil, consumed("mem", map[string]string{"size": "101Gi"})))),
			[]model.Kind{model.KindCapacity, model.KindCapacity}},
		"cpupinning": {one(t, CPUPinning("app", "demo"), pinSnap(hostEv(), "8-9")), []model.Kind{model.KindPinning}},
		"kubevirt":   {kvOne(t, kvSnap(t), "demo/vm-wrong-node"), []model.Kind{model.KindNode}},
		"unavailable": {run(Alignment("", ""), model.Snapshot{Collection: map[string]model.CollectionStatus{}})[0],
			[]model.Kind{model.KindUnavailable}},
	} {
		t.Run(name, func(t *testing.T) {
			if got := kindsOf(tc.r); !slices.Equal(got, tc.want) {
				t.Errorf("kinds = %v, want %v", got, tc.want)
			}
		})
	}
}

// Findings that list devices carry the stem and the items, so a view can
// print one group per line; Text stays the sentence the JSON reason has.
func TestListFindingsCarryHeadAndItems(t *testing.T) {
	r := one(t, Topology(), snapOf([]resourcev1.ResourceSlice{slice("s", "p", 1, 1, full("a"), dev("b", attrs{numa: ints(1)}), dev("c", attrs{numa: ints(1)}))}))
	f := finding1(t, r, model.KindPCIeRootMissing)
	if !slices.Equal(f.Items, []string{"b", "c"}) || f.Head != "PCIe root missing on 2 devices" || f.Text != "PCIe root missing on b, c" {
		t.Errorf("%+v", f)
	}
	a := one(t, Alignment("", ""), snapOf(numaPool([]int64{0}, []int64{1}),
		claim("ns", "c", []resourcev1.DeviceConstraint{match("resource.kubernetes.io/pciBusID")}, res("r", "a"), res("r", "b"))))
	g := finding1(t, a, model.KindConstraintMissing)
	if len(g.Items) != 2 || g.Head != "resource.kubernetes.io/pciBusID not published by 2 devices" {
		t.Errorf("%+v", g)
	}
}

// A violated matchAttribute names its attribute in Items, so a view can find
// the constraint's values without reading the sentence.
func TestConstraintFindingNamesItsAttribute(t *testing.T) {
	r := one(t, Alignment("", ""), snapOf(numaPool([]int64{0}, []int64{1}),
		claim("ns", "c", []resourcev1.DeviceConstraint{match(numa)}, res("r", "a"), res("r", "b"))))
	if f := finding1(t, r, model.KindConstraintViolated); !slices.Equal(f.Items, []string{numa}) {
		t.Errorf("%+v", f)
	}
}

// Every non-pass result of every check, over every fixture, explains itself
// with at least one Finding, and every Finding has a Kind.
func TestEveryNonPassResultHasKindedFindings(t *testing.T) {
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
	if len(dirs) < 10 {
		t.Fatalf("found only %d fixture dirs", len(dirs))
	}
	checks := map[string]Check{
		"alignment": Alignment("", ""), "counters": Counters(), "capacity": Capacity(), "topology": Topology(),
		"kubevirt": KubeVirt("", ""), "vfio": VFIO(), "cpupinning": CPUPinning("", ""),
	}
	n := 0
	for _, d := range dirs {
		snap := testutil.LoadFixture(t, d)
		for name, c := range checks {
			for _, r := range run(c, snap) {
				n++
				if r.Status == model.StatusPass {
					continue
				}
				if len(r.Findings) == 0 {
					t.Errorf("%s %s %q: %s result without Findings: %+v", filepath.Base(d), name, r.Subject, r.Status, r.Details)
				}
				for _, f := range r.Findings {
					if f.Kind == "" {
						t.Errorf("%s %s %q: finding without Kind: %q", filepath.Base(d), name, r.Subject, f.Text)
					}
				}
			}
		}
	}
	if n < 20 {
		t.Errorf("only %d results examined", n)
	}
}
