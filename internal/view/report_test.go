package view_test

import (
	"strings"
	"testing"

	"github.com/johnahull/k8s-dra-inspect/internal/check"
	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/view"
)

func res(check string, st model.Status, subject, reason string) model.Result {
	r := model.Result{Check: check, Status: st, Subject: subject, Details: map[string]any{}}
	if reason != "" {
		r.Details["reason"] = reason
	}
	return r
}

// topo is an inspect.topology result for node with the given NUMA and PCIe-root sets.
func topo(node string, numa, roots []string) model.Result {
	r := model.Result{Check: "inspect.topology", Status: model.StatusWarn, Subject: node + "/numa=0/pcieRoot=-", Details: map[string]any{"node": node, "numa": numa, "pcieRoot": roots}}
	r.Findings = []model.Finding{{Status: model.StatusWarn, Kind: model.KindAttrMissing, Text: "missing attribute: pcieRoot", Items: []string{"pcieRoot"}}}
	return r
}

func report(t *testing.T, members []string, rs []model.Result) string {
	return showResults(t, model.Snapshot{}, rs, view.Report("Cluster Report", members))
}

const hint = "Details: run the individual check or inspect commands, or use -o json."

func TestReportSummaryCountsTheWorstStatusPerMember(t *testing.T) {
	members := []string{"a.warn", "b.ok", "c.failwarn", "d.unavail", "e.err", "f.empty"}
	rs := []model.Result{
		res("a.warn", model.StatusWarn, "x1", "w1"), res("a.warn", model.StatusWarn, "x2", "w2"), res("a.warn", model.StatusWarn, "x3", "w3"),
		res("b.ok", model.StatusPass, "y", ""),
		res("c.failwarn", model.StatusWarn, "z1", "w"), res("c.failwarn", model.StatusFail, "z2", "f"),
		res("d.unavail", model.StatusUnavailable, "", "no data"),
		res("e.err", model.StatusError, "q", "boom"),
		res("g.extra", model.StatusPass, "", ""),
	}
	got := report(t, members, rs)
	want := "Summary: 7 checks: 3 ok, 1 warning, 1 failure, 1 unavailable, 1 error"
	if !hasLine(got, want) {
		t.Errorf("missing %q in:\n%s", want, got)
	}
	if !strings.HasPrefix(got, "═══ Cluster Report ═══\n\n"+want+"\n") || !strings.HasSuffix(got, "\n"+hint+"\n") {
		t.Errorf("banner, summary or closing hint wrong:\n%s", got)
	}
}

func TestReportListsNonPassResultsPerMemberInMemberOrder(t *testing.T) {
	members := []string{"inspect.topology", "check.alignment", "check.vfio"}
	rs := []model.Result{
		res("check.alignment", model.StatusFail, "demo/c", "matchAttribute x violated"),
		topo("node-a", []string{"0"}, nil),
		res("check.vfio", model.StatusUnavailable, "node/b", "run on node b"),
		res("check.vfio", model.StatusPass, "d", ""),
	}
	want := lines(
		"═══ Cluster Report ═══",
		"",
		"Summary: 3 checks: 0 ok, 1 warning, 1 failure, 1 unavailable, 0 errors",
		"",
		"inspect.topology",
		"  ! node-a, NUMA 0: no pcieRoot published",
		"",
		"check.alignment",
		"  ✗ demo/c: matchAttribute x violated",
		"",
		hint,
	)
	if got := report(t, members, rs); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestReportUsesFindingsOneSentenceEach(t *testing.T) {
	r := res("check.topology", model.StatusWarn, "gpu/p", "joined")
	r.Findings = []model.Finding{{Status: model.StatusWarn, Text: "NUMA node missing on a"}, {Status: model.StatusFail, Text: "PCIe root missing on b"}}
	got := report(t, []string{"check.topology"}, []model.Result{r})
	for _, w := range []string{"  ! gpu/p: NUMA node missing on a", "  ✗ gpu/p: PCIe root missing on b"} {
		if !hasLine(got, w) {
			t.Errorf("missing %q in:\n%s", w, got)
		}
	}
}

func TestReportAdapterRows(t *testing.T) {
	members := []string{"check.vfio", "adapter.host", "adapter.kubevirt"}
	rs := []model.Result{
		res("check.vfio", model.StatusPass, "d", ""),
		{Check: "adapter.host", Status: model.StatusPass, Details: map[string]any{"summary": "host evidence for node node-a"}},
		res("adapter.kubevirt", model.StatusUnavailable, "", "kubevirt.io/v1 not served"),
	}
	got := report(t, members, rs)
	for _, w := range []string{"  ✓ adapter.host: host evidence for node node-a", "  ? adapter.kubevirt: kubevirt.io/v1 not served"} {
		if !hasLine(got, w) {
			t.Errorf("missing %q in:\n%s", w, got)
		}
	}
	if !hasLine(got, "Summary: 3 checks: 2 ok, 0 warnings, 0 failures, 1 unavailable, 0 errors") {
		t.Errorf("summary:\n%s", got)
	}
}

func TestReportAllPassHasNoMemberSections(t *testing.T) {
	got := report(t, []string{"a", "b"}, []model.Result{res("a", model.StatusPass, "s", ""), res("b", model.StatusPass, "", "")})
	want := lines("═══ Cluster Report ═══", "", "Summary: 2 checks: 2 ok, 0 warnings, 0 failures, 0 unavailable, 0 errors", "", hint)
	if got != want {
		t.Errorf("got:\n%s", got)
	}
}

func TestReportNeutraliseHostileText(t *testing.T) {
	rs := []model.Result{res("c"+evil, model.StatusFail, "s"+evil, "r"+evil), res("adapter.x", model.StatusUnavailable, "", "a"+evil)}
	got := report(t, []string{"c" + evil, "adapter.x"}, rs)
	if strings.ContainsAny(got, "\x1b\u202e\u2028") || !strings.Contains(got, "\ufffd") {
		t.Errorf("%q", got)
	}
}

func TestReportCountsArePluralised(t *testing.T) {
	one := report(t, []string{"a"}, []model.Result{res("a", model.StatusWarn, "s", "w")})
	if !hasLine(one, "Summary: 1 check: 0 ok, 1 warning, 0 failures, 0 unavailable, 0 errors") {
		t.Errorf("singular:\n%s", one)
	}
	two := report(t, []string{"a", "b"}, []model.Result{res("a", model.StatusFail, "s", "f"), res("b", model.StatusError, "s", "e")})
	if !hasLine(two, "Summary: 2 checks: 0 ok, 0 warnings, 1 failure, 0 unavailable, 1 error") {
		t.Errorf("plural:\n%s", two)
	}
	if strings.Contains(one+two, "(s)") {
		t.Errorf("no (s) plurals:\n%s%s", one, two)
	}
}

func TestReportListFindingsHeadThenItemsOnTheirOwnLine(t *testing.T) {
	r := res("check.topology", model.StatusWarn, "dra.memory/node-a", "joined")
	r.Findings = []model.Finding{{Status: model.StatusWarn, Kind: model.KindPCIeRootMissing, Text: "PCIe root missing on a, b, c",
		Head: "PCIe root missing on 3 devices", Items: []string{"a", "b", "c"}}}
	got := report(t, []string{"check.topology"}, []model.Result{r})
	for _, w := range []string{"  ! dra.memory/node-a: PCIe root missing on 3 devices", "      a, b, c"} {
		if !hasLine(got, w) {
			t.Errorf("missing %q in:\n%s", w, got)
		}
	}
}

// The report names a result the way its check command does.
func TestReportSubjectsMatchTheCheckCommands(t *testing.T) {
	vf := model.Result{Check: check.NameVFIO, Status: model.StatusFail, Subject: "gpu.example.com/node-a/gpu-0",
		Details:  map[string]any{"bdf": "0000:3b:00.0", "driver": "nvidia", "iommuGroup": "7"},
		Findings: []model.Finding{{Status: model.StatusFail, Kind: model.KindVFIODriver, Text: "bound to driver nvidia, want vfio-pci"}}}
	cp := model.Result{Check: check.NameCPUPinning, Status: model.StatusWarn, Subject: "demo/app", Details: map[string]any{"nodeName": "node-a", "pinned": false},
		Findings: []model.Finding{{Status: model.StatusWarn, Kind: model.KindPinning, Text: "cpuset is all online CPUs; pod is not CPU-pinned"}}}
	got := report(t, []string{check.NameVFIO, check.NameCPUPinning}, []model.Result{vf, cp})
	for _, w := range []string{
		"  ✗ gpu.example.com/gpu-0 (0000:3b:00.0): bound to driver nvidia, want vfio-pci",
		"  ! demo/app (node: node-a): cpuset is all online CPUs; pod is not CPU-pinned",
	} {
		if !hasLine(got, w) {
			t.Errorf("missing %q in:\n%s", w, got)
		}
	}
	if !hasLine(showResults(t, vfioSnap(t, "7"), []model.Result{vf}, view.VFIO), "gpu.example.com/gpu-0 (0000:3b:00.0)") {
		t.Error("check vfio heading changed")
	}
}
