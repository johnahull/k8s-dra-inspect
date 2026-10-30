package view_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/johnahull/k8s-dra-inspect/internal/check"
	"github.com/johnahull/k8s-dra-inspect/internal/inspect"
	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/testutil"
	"github.com/johnahull/k8s-dra-inspect/internal/view"
)

// --- Findings drive the layout, not their wording -------------------------

func alignResults(t *testing.T, edit func(*model.Result)) []model.Result {
	snap := fx(t, "numa-split")
	rs := check.Alignment("", "")(t.Context(), snap)
	for i := range rs {
		edit(&rs[i])
	}
	return rs
}

func TestAlignmentGroupsFollowFindingKindNotText(t *testing.T) {
	snap := fx(t, "numa-split")
	got := showResults(t, snap, alignResults(t, func(r *model.Result) {
		for i := range r.Findings {
			r.Findings[i].Text = "something else entirely"
		}
	}), view.Alignment)
	for _, w := range []string{"  ! something else entirely", "      NUMA 0: gpu-0", "      NUMA 1: gpu-1"} {
		if !hasLine(got, w) {
			t.Errorf("a reworded finding must keep its groups; missing %q in:\n%s", w, got)
		}
	}
	got = showResults(t, snap, alignResults(t, func(r *model.Result) {
		for i := range r.Findings {
			r.Findings[i].Kind = model.KindOther
		}
	}), view.Alignment)
	if strings.Contains(got, "      NUMA 0: gpu-0") || !hasLine(got, "  ! devices span NUMA nodes (no common NUMA node)") {
		t.Errorf("wording alone must not trigger the groups:\n%s", got)
	}
}

// --- typed decoding fails loudly -------------------------------------------

func TestUnreadableDetailsShowAsErrorNotes(t *testing.T) {
	snap := fx(t, "numa-split")
	got := showResults(t, snap, alignResults(t, func(r *model.Result) {
		r.Details["constraints"] = "oops"
		r.Details["numa"] = []int{1}
	}), view.Alignment)
	for _, w := range []string{"  E cannot read constraints: want ", "  E cannot read numa: want "} {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in:\n%s", w, got)
		}
	}
	vf := []model.Result{{Check: check.NameVFIO, Status: model.StatusPass, Subject: "d/p/x", Details: map[string]any{"nonVfioPeers": 7, "iommuGroup": "1"}}}
	if out := showResults(t, vfioSnap(t), vf, view.VFIO); !strings.Contains(out, "E cannot read nonVfioPeers: want []string, got int") {
		t.Errorf("vfio:\n%s", out)
	}
}

// Real results never trip a decode error: every view over every fixture.
func TestNoDecodeErrorsOnTheFixtureMatrix(t *testing.T) {
	root := "testdata/fixtures"
	dirs := []string{"alignment/aligned", "alignment/invalid-attr", "alignment/list-global-fail", "alignment/list-global-ok", "alignment/mixed",
		"capacity/overcommit", "capacity/pass", "capacity/unpublished", "classes-claims", "counters/exactly-full", "counters/fits", "counters/none",
		"counters/overcommit", "counters/stale-generation", "counters/unknown-counter", "counters/unknown-set", "kubevirt", "missing-attr",
		"multinode", "numa-split", "pcie-split", "unicode-long", "xe9680", "xe9785l"}
	type ins struct {
		name string
		in   inspect.Inspector
		v    viewFunc
	}
	inspectors := []ins{{"slices", inspect.Slices(), view.Slices}, {"drivers", inspect.Drivers(true), view.Drivers},
		{"attributes", inspect.Attributes(true, true), view.Attributes}, {"topology", inspect.Topology(false), view.Topology},
		{"topology-compact", inspect.Topology(true), view.Topology}, {"claims", inspect.Claims(), view.Claims},
		{"deviceclasses", inspect.DeviceClasses(labels.Everything()), view.DeviceClasses}}
	checks := []struct {
		name string
		c    check.Check
		v    viewFunc
	}{{"alignment", check.Alignment("", ""), view.Alignment}, {"counters", check.Counters(), view.Counters}, {"capacity", check.Capacity(), view.Capacity},
		{"topology", check.Topology(), view.CheckTopology}, {"kubevirt", check.KubeVirt("", ""), view.KubeVirt}, {"cpupinning", check.CPUPinning("", ""), view.CPUPinning},
		{"vfio", check.VFIO(), view.VFIO}}
	n := 0
	for _, d := range dirs {
		snap := testutil.LoadFixture(t, root+"/"+d)
		for _, i := range inspectors {
			n++
			if out := show(t, snap, i.in, i.v); strings.Contains(out, view.CannotRead) {
				t.Errorf("%s/%s:\n%s", d, i.name, out)
			}
		}
		for _, c := range checks {
			n++
			if out := showCheck(t, snap, c.c, c.v); strings.Contains(out, view.CannotRead) {
				t.Errorf("%s/%s:\n%s", d, c.name, out)
			}
		}
		var all []model.Result
		for _, e := range check.Registry("") {
			all = append(all, e.Run(t.Context(), snap)...)
		}
		if out := showResults(t, snap, all, view.Report("r", nil)); strings.Contains(out, view.CannotRead) {
			t.Errorf("%s/report:\n%s", d, out)
		}
	}
	if n < 100 {
		t.Fatalf("matrix too small: %d", n)
	}
}

// --- claims -----------------------------------------------------------------

func TestClaimsMarkerFollowsTheResultStatus(t *testing.T) {
	snap := fx(t, "classes-claims")
	rs := inspect.Claims().Run(snap)
	got := showResults(t, snap, rs, view.Claims)
	want := "    ! gpu.example.com/gpu-9 not found in ResourceSlices (pool node-a.example.com)"
	if !hasLine(got, want) {
		t.Errorf("JSON says warn; the text must too. missing %q in:\n%s", want, got)
	}
	for i := range rs {
		if rs[i].Details["missingDevices"] != nil {
			rs[i].Status = model.StatusFail
		}
	}
	if got := showResults(t, snap, rs, view.Claims); !hasLine(got, strings.Replace(want, "!", "✗", 1)) {
		t.Errorf("the marker must come from the status, not a constant:\n%s", got)
	}
}

// --- topology ---------------------------------------------------------------

func devWith(name string, numa []int64, roots ...string) resourcev1.Device {
	d := resourcev1.Device{Name: name, Attributes: map[resourcev1.QualifiedName]resourcev1.DeviceAttribute{}}
	if numa != nil {
		d.Attributes["resource.kubernetes.io/numaNode"] = resourcev1.DeviceAttribute{IntValues: numa}
	}
	switch len(roots) {
	case 0:
	case 1:
		d.Attributes["resource.kubernetes.io/pcieRoot"] = resourcev1.DeviceAttribute{StringValue: &roots[0]}
	default:
		d.Attributes["resource.kubernetes.io/pcieRoot"] = resourcev1.DeviceAttribute{StringValues: roots}
	}
	return d
}

func oneSliceSnap(devs ...resourcev1.Device) model.Snapshot {
	node := "n"
	s := resourcev1.ResourceSlice{ObjectMeta: metav1.ObjectMeta{Name: "s"}}
	s.Spec.Driver, s.Spec.NodeName = "d.example.com", &node
	s.Spec.Pool = resourcev1.ResourcePool{Name: "n", Generation: 1, ResourceSliceCount: 1}
	s.Spec.Devices = devs
	snap := model.Snapshot{Collection: map[string]model.CollectionStatus{}, ResourceSlices: []resourcev1.ResourceSlice{s}}
	for _, k := range []string{"resourceslices", "resourceclaims", "pods"} {
		snap.Collection[k] = model.CollectionStatus{State: model.StateOK}
	}
	return snap
}

func treeKeys(out string) []string {
	var keys []string
	for _, l := range strings.Split(out, "\n") {
		if i := strings.Index(l, "NUMA "); i >= 0 {
			keys = append(keys, strings.TrimSpace(l[i:]))
		} else if i := strings.Index(l, "pcieRoot "); i >= 0 {
			keys = append(keys, strings.TrimSpace(l[i:]))
		}
	}
	return keys
}

func TestTopologyGroupsAndRootsSortNumerically(t *testing.T) {
	snap := oneSliceSnap(
		devWith("a", []int64{10, 11}, "pci0000:100"),
		devWith("b", []int64{9}, "pci0000:100"),
		devWith("c", []int64{0, 1}, "pci0000:100"),
		devWith("d", []int64{2, 3}, "pci0000:20"),
		devWith("e", []int64{9}, "pci0000:20"),
		devWith("h", []int64{9}, "pci0000:5", "pci0000:7"),
		devWith("g", nil, "pci0000:20"),
	)
	got := treeKeys(topoOut(t, snap, false))
	want := []string{
		"NUMA 0,1", "pcieRoot pci0000:100",
		"NUMA 2,3", "pcieRoot pci0000:20",
		"NUMA 9", "pcieRoot pci0000:20", "pcieRoot pci0000:100", "pcieRoot pci0000:5,pci0000:7",
		"NUMA 10,11", "pcieRoot pci0000:100",
		"NUMA unknown", "pcieRoot pci0000:20",
	}
	if !slices.Equal(got, want) {
		t.Errorf("tree order\n got: %q\nwant: %q", got, want)
	}
}

func TestTopologyNumericOrderInsideAPrefix(t *testing.T) {
	// 0,9 sorts before 0,10: element-wise numeric, not text.
	got := treeKeys(topoOut(t, oneSliceSnap(devWith("a", []int64{0, 10}, "r"), devWith("b", []int64{0, 9}, "r")), false))
	if want := []string{"NUMA 0,9", "pcieRoot r", "NUMA 0,10", "pcieRoot r"}; !slices.Equal(got, want) {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestTopologyHasNoFooterForWhatTheTreeShows(t *testing.T) {
	got := topoOut(t, fx(t, "missing-attr"), false)
	if strings.Contains(got, "missing attribute") || strings.Contains(got, "! ") {
		t.Errorf("rootless topology should not produce a missing-attribute footer:\n%s", got)
	}
	snap := fx(t, "classes-claims")
	snap.ResourceSlices[0].Spec.Pool.ResourceSliceCount = 2
	if got := topoOut(t, snap, false); !strings.Contains(got, "! NUMA 0, pcieRoot pci0000:00: pool has fewer") {
		t.Errorf("other reasons keep their note:\n%s", got)
	}
}

// The inspector marks --compact by leaving "devices" out of Details; the view
// follows that, one test per direction.
func TestTopologyViewFollowsTheDevicesDetailKey(t *testing.T) {
	snap := fx(t, "xe9785l")
	if full := topoOut(t, snap, false); !strings.Contains(full, "gpu-vfio-0") {
		t.Errorf("devices present: names expected")
	}
	if compact := topoOut(t, snap, true); strings.Contains(compact, "gpu-vfio-0") || !strings.Contains(compact, "gpu=1") {
		t.Errorf("devices absent: counts expected:\n%s", compact)
	}
}

// Likewise "attributes" present in a driver result means --attributes.
func TestDriversViewFollowsTheAttributesDetailKey(t *testing.T) {
	snap := fx(t, "classes-claims")
	if out := show(t, snap, inspect.Drivers(true), view.Drivers); !strings.Contains(out, "  attributes:\n") {
		t.Errorf("attributes present:\n%s", out)
	}
	if out := show(t, snap, inspect.Drivers(false), view.Drivers); strings.Contains(out, "attributes:") {
		t.Errorf("attributes absent:\n%s", out)
	}
}

// --- attributes -------------------------------------------------------------

func TestAttributesScalarStringLookingLikeAListIsNotRespaced(t *testing.T) {
	snap := fx(t, "classes-claims")
	setAttr(&snap, "gpu-0", "tag", "[x,y]")
	for i := range snap.ResourceSlices {
		for j := range snap.ResourceSlices[i].Spec.Devices {
			if snap.ResourceSlices[i].Spec.Devices[j].Name == "gpu-1" {
				snap.ResourceSlices[i].Spec.Devices[j].Attributes["tag"] = resourcev1.DeviceAttribute{StringValues: []string{"x", "y"}}
			}
		}
	}
	got := show(t, snap, inspect.Attributes(true, false), view.Attributes)
	squashed := strings.Join(strings.Fields(got), " ")
	if !strings.Contains(squashed, "tag = [x,y] ") && !strings.HasSuffix(strings.TrimSpace(squashed), "tag = [x,y]") {
		// gpu-0 is a scalar: it keeps its text exactly.
		t.Errorf("scalar [x,y] was respaced:\n%s", got)
	}
	if !strings.Contains(squashed, "tag = [x, y]") {
		t.Errorf("a real list must be spaced:\n%s", got)
	}
}

// --- slices -----------------------------------------------------------------

func TestSlicesArrowsAreAllClaimsWhenPodsAreNotCollected(t *testing.T) {
	snap := fx(t, "classes-claims")
	snap.Collection["pods"] = model.CollectionStatus{State: model.StateForbidden, Reason: "forbidden: cannot list pods"}
	snap.Pods = nil // reservedFor still names a pod on shared-gpu
	got := slicesOut(t, snap)
	if strings.Contains(got, "→demo/virt-launcher") || strings.Contains(got, "→demo/web-0\n") {
		t.Errorf("no arrow may name a pod when pods were not collected:\n%s", got)
	}
	for _, w := range []string{"    NUMA 0  gpu-0  →claim demo/shared-gpu", "    NUMA 1  gpu-1  →claim demo/web-0-gpu-xyz12"} {
		if !hasLine(got, w) {
			t.Errorf("missing %q in:\n%s", w, got)
		}
	}
}

// --- checks: rhythm and wording ----------------------------------------------

func TestCountersBlankLineBetweenSets(t *testing.T) {
	mk := func(set string) model.Result {
		return model.Result{Check: check.NameCounters, Status: model.StatusPass, Subject: "d/p/" + set, Details: map[string]any{
			"driver": "d", "pool": "p", "counterSet": set, "allocatedBy": []string{},
			"counters": map[string]map[string]string{"slots": {"used": "0", "capacity": "2"}}}}
	}
	got := showResults(t, model.Snapshot{}, []model.Result{mk("pf-0"), mk("pf-1")}, view.Counters)
	want := lines("═══ Shared Counters ═══", "", "d (pool: p)",
		"  counter set pf-0", "    slots: 0 of 2 in use", "    ✓ within capacity",
		"",
		"  counter set pf-1", "    slots: 0 of 2 in use", "    ✓ within capacity")
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestKubeVirtEvidenceWarningsAreCollapsedWithACount(t *testing.T) {
	mk := func(w ...string) []model.Result {
		return []model.Result{{Check: check.NameKubeVirt, Status: model.StatusPass, Subject: "demo/vm", Details: map[string]any{
			"devices": []map[string]any{}, "numa": []string{}, "warnings": w, "summary": "no DRA host devices"}}}
	}
	got := showResults(t, model.Snapshot{}, mk("a is not a string", "b is not an object", "a is not a string"), view.KubeVirt)
	want := lines("═══ KubeVirt Check ═══", "", "demo/vm",
		"  ! evidence: 3 warnings",
		"      a is not a string (2 times)",
		"      b is not an object",
		"  · no DRA host devices")
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if got := showResults(t, model.Snapshot{}, mk("only one"), view.KubeVirt); !hasLine(got, "  ! evidence: only one") {
		t.Errorf("a single warning stays on one line:\n%s", got)
	}
}

func TestKubeVirtVerdictCountsDevicesInWords(t *testing.T) {
	r := []model.Result{{Check: check.NameKubeVirt, Status: model.StatusPass, Subject: "demo/vm", Details: map[string]any{
		"devices": []map[string]any{{"hostDevice": "h", "request": "r", "driver": "d", "device": "x", "node": "n1"}},
		"numa":    []string{"0"}, "guestNUMAPassthrough": false, "dedicatedCPUPlacement": false, "summary": "1 device(s) on node n1"}}}
	got := showResults(t, model.Snapshot{}, r, view.KubeVirt)
	if !hasLine(got, "  ✓ 1 device on node n1") || strings.Contains(got, "(s)") {
		t.Errorf("got:\n%s", got)
	}
}

func TestVFIOSaysEachFactOnce(t *testing.T) {
	snap := vfioSnap(t)
	r := []model.Result{{Check: check.NameVFIO, Status: model.StatusFail, Subject: "d/p/x", Details: map[string]any{
		"bdf": "0000:3b:00.0", "cdiPresent": true, "driver": "", "iommuGroup": "7", "nonVfioPeers": []string{"0000:3b:00.1 (no driver)"}},
		Findings: []model.Finding{
			{Status: model.StatusFail, Kind: model.KindVFIODriver, Text: "no driver bound, want vfio-pci"},
			{Status: model.StatusWarn, Kind: model.KindVFIOPeers, Text: "IOMMU group 7 also holds 0000:3b:00.1 (no driver)", Head: "IOMMU group 7 also holds 1 device", Items: []string{"0000:3b:00.1 (no driver)"}},
			{Status: model.StatusWarn, Kind: model.KindDevNode, Text: "/dev/vfio/7 is missing"},
		}}}
	got := showResults(t, snap, r, view.VFIO)
	want := lines("═══ VFIO Check ═══", "", "d/x (0000:3b:00.0)",
		"  ✗ bound driver: none, want vfio-pci",
		"  IOMMU group: 7",
		"  ! non-vfio peer: 0000:3b:00.1 (no driver)",
		"  ! /dev/vfio/7: missing",
		"  CDI spec lists device: yes")
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// --- scaling ----------------------------------------------------------------

func bigSnap(n int) model.Snapshot {
	devs := make([]resourcev1.Device, n)
	claims := make([]resourcev1.ResourceClaim, n)
	for i := range n {
		devs[i] = devWith(fmt.Sprintf("dev-%05d", i), []int64{int64(i % 8)}, "pci0000:00")
		c := resourcev1.ResourceClaim{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: fmt.Sprintf("claim-%05d", i)}}
		c.Status.Allocation = &resourcev1.AllocationResult{}
		c.Status.Allocation.Devices.Results = []resourcev1.DeviceRequestAllocationResult{{Request: "r", Driver: "d.example.com", Pool: "n", Device: devs[i].Name}}
		claims[i] = c
	}
	snap := oneSliceSnap(devs...)
	snap.ResourceClaims = claims
	snap.Pods = []model.PodRef{{Name: "p", Namespace: "ns", ResourceClaimStatuses: []corev1.PodResourceClaimStatus{}}}
	return snap
}

// bestOf times f and keeps the fastest of three runs, so a busy machine
// cannot fake a slowdown.
func bestOf(f func()) time.Duration {
	best := time.Hour
	for range 3 {
		start := time.Now()
		f()
		best = min(best, time.Since(start))
	}
	return best
}

// Four times the claims must cost about four times, not sixteen: the views
// index claims and devices once instead of scanning them per result.
func TestAlignmentAndClaimsViewsScaleLinearly(t *testing.T) {
	for name, v := range map[string]viewFunc{"alignment": view.Alignment, "claims": view.Claims} {
		cost := func(n int) time.Duration {
			snap := bigSnap(n)
			var rs []model.Result
			if name == "alignment" {
				rs = check.Alignment("", "")(t.Context(), snap)
			} else {
				rs = inspect.Claims().Run(snap)
			}
			return bestOf(func() { showResults(t, snap, rs, v) })
		}
		small, big := cost(1500), cost(6000)
		if big > 9*small {
			t.Errorf("%s view is superlinear: %v for 1500 claims, %v for 6000 (ratio %.1f, linear is 4)", name, small, big, float64(big)/float64(small))
		}
	}
}
