package view_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/johnahull/k8s-dra-inspect/internal/adapters/host"
	"github.com/johnahull/k8s-dra-inspect/internal/check"
	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/view"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
)

func TestAlignmentSplitNamesTheDevicesAndNUMASets(t *testing.T) {
	got := showCheck(t, fx(t, "numa-split"), check.Alignment("", ""), view.Alignment)
	want := lines(
		"═══ Alignment Check ═══",
		"",
		"demo/two-gpus",
		"  gpu",
		"    gpu-0  gpu.example.com",
		"    NUMA 0  pcieRoot pci0000:00",
		"    gpu-1  gpu.example.com",
		"    NUMA 1  pcieRoot pci0000:80",
		"  ! devices span NUMA nodes (no common NUMA node)",
		"      NUMA 0: gpu-0",
		"      NUMA 1: gpu-1",
		"  · devices span PCIe roots (informational)",
		"      pcieRoot pci0000:00: gpu-0",
		"      pcieRoot pci0000:80: gpu-1",
	)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestAlignmentAlignedPrintsCheckmarks(t *testing.T) {
	got := showCheck(t, fx(t, "alignment/aligned"), check.Alignment("", ""), view.Alignment)
	for _, want := range []string{
		"demo/aligned-gpus",
		"  ✓ matchAttribute resource.kubernetes.io/numaNode satisfied: common value 0",
	} {
		if !hasLine(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "! ") || strings.Contains(got, "✗") {
		t.Errorf("aligned claim shows a problem:\n%s", got)
	}
}

func TestAlignmentUnconstrainedAlignedSaysAllOnNUMA(t *testing.T) {
	snap := fx(t, "numa-split")
	setAttr(&snap, "gpu-1", "resource.kubernetes.io/numaNode", "0")
	setAttr(&snap, "gpu-1", "resource.kubernetes.io/pcieRoot", "pci0000:00")
	got := showCheck(t, snap, check.Alignment("", ""), view.Alignment)
	for _, want := range []string{"  ✓ numaNode aligned: all on NUMA 0", "  ✓ pcieRoot aligned: all on pci0000:00"} {
		if !hasLine(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestAlignmentViolatedConstraintListsValuesPerDevice(t *testing.T) {
	got := showCheck(t, fx(t, "alignment/list-global-fail"), check.Alignment("", ""), view.Alignment)
	for _, want := range []string{
		"  ✗ matchAttribute resource.kubernetes.io/numaNode violated: no value common to all devices in scope",
		"      numaNode 0,1: gpu-0",
		"      numaNode 0,2: gpu-2",
		"      numaNode 1,2: gpu-1",
	} {
		if !hasLine(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestAlignmentHeadingSaysAlsoMatchedPod(t *testing.T) {
	snap := fx(t, "numa-split")
	snap.Pods = []model.PodRef{{Name: "two-gpus", Namespace: "demo",
		ResourceClaimStatuses: []corev1.PodResourceClaimStatus{{Name: "g", ResourceClaimName: ptr.To("two-gpus")}}}}
	got := showCheck(t, snap, check.Alignment("two-gpus", ""), view.Alignment)
	if !hasLine(got, "demo/two-gpus (also matched pod)") {
		t.Errorf("heading:\n%s", got)
	}
}

func TestAlignmentNeutralWhenNoClaims(t *testing.T) {
	snap := fx(t, "xe9680")
	got := showCheck(t, snap, check.Alignment("", ""), view.Alignment)
	if got != lines("═══ Alignment Check ═══", "", "· no allocated claims to check") {
		t.Errorf("got:\n%s", got)
	}
}

func TestCountersSaysInUseNotARatio(t *testing.T) {
	got := showCheck(t, fx(t, "counters/overcommit"), check.Counters(), view.Counters)
	want := lines(
		"═══ Shared Counters ═══",
		"",
		"gpu.example.com (pool: node-a.example.com)",
		"  counter set pf-0",
		"    vf-slots: 3 of 2 in use",
		"    used by demo/vfs",
		"    ✗ counter vf-slots overcommitted: 3 used of 2",
	)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestCountersFitsAndNoneAndUnknownSet(t *testing.T) {
	got := showCheck(t, fx(t, "counters/fits"), check.Counters(), view.Counters)
	if !strings.Contains(got, "    ✓ within capacity\n") || !strings.Contains(got, " of ") {
		t.Errorf("fits:\n%s", got)
	}
	if got := showCheck(t, fx(t, "counters/none"), check.Counters(), view.Counters); got != lines("═══ Shared Counters ═══", "", "· no shared counters published") {
		t.Errorf("none:\n%s", got)
	}
	got = showCheck(t, fx(t, "counters/unknown-set"), check.Counters(), view.Counters)
	if !hasLine(got, "    ✗ device vf-0 consumes counter set pf-9, which the pool does not define") || strings.Count(got, "gpu.example.com (pool:") != 1 {
		t.Errorf("unknown set (one pool heading expected):\n%s", got)
	}
	got = showCheck(t, fx(t, "counters/stale-generation"), check.Counters(), view.Counters)
	if !strings.Contains(got, "    ! claim demo/vfs allocates gpu.example.com/node-a.example.com/vf-old from a stale generation") {
		t.Errorf("stale:\n%s", got)
	}
}

func TestCapacityLinesShowConsumedPublishedRemaining(t *testing.T) {
	got := showCheck(t, fx(t, "capacity/overcommit"), check.Capacity(), view.Capacity)
	want := lines(
		"═══ Consumable Capacity ═══",
		"",
		"mem.example.com/node-a.example.com/mem-0",
		"  size: 120Gi of 100Gi consumed (remaining -20Gi)",
		"  used by demo/first",
		"  used by demo/second",
		"  ✗ total consumed 120Gi of size exceeds the published 100Gi",
	)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	pass := showCheck(t, fx(t, "capacity/pass"), check.Capacity(), view.Capacity)
	if !strings.Contains(pass, "  ✓ within published capacity\n") {
		t.Errorf("pass:\n%s", pass)
	}
	if got := showCheck(t, fx(t, "xe9680"), check.Capacity(), view.Capacity); got != lines("═══ Consumable Capacity ═══", "", "· no consumable capacity allocated") {
		t.Errorf("none:\n%s", got)
	}
}

func TestTopologyCheckNamesDevicesMissingAttributes(t *testing.T) {
	got := showCheck(t, fx(t, "missing-attr"), check.Topology(), view.CheckTopology)
	want := lines(
		"═══ Topology Check ═══",
		"",
		"gpu.example.com/node-a.example.com",
		"  2 devices, 1 missing NUMA, 2 missing PCIe root",
		"  ! NUMA node missing on gpu-1",
		"  ! PCIe root missing on 2 devices",
		"      gpu-0, gpu-1",
	)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	ok := showCheck(t, fx(t, "xe9680"), check.Topology(), view.CheckTopology)
	if !strings.Contains(ok, "  ✓ every device publishes a NUMA node and a PCIe root\n") {
		t.Errorf("pass:\n%s", ok)
	}
}

func TestKubeVirtDevicesNUMAAndFlagsInPlainWords(t *testing.T) {
	snap := fx(t, "kubevirt")
	got := showCheck(t, snap, check.KubeVirt("demo/vm-ok", ""), view.KubeVirt)
	want := lines(
		"═══ KubeVirt Check ═══",
		"",
		"demo/vm-ok",
		"  gpu0",
		"    gpu-0 (0000:3b:00.0)  gpu.example.com",
		"    hostDevice gpu0  node node-a.example.com",
		"  nic0",
		"    nic-0 (0000:3c:00.0)  gpu.example.com",
		"    hostDevice nic0  node node-a.example.com",
		"  gpu2/sub",
		"    gpu-2 (0000:af:00.0)  gpu.example.com",
		"    hostDevice gpu2  node node-a.example.com",
		"  devices on NUMA 0,1",
		"  guest NUMA passthrough: yes",
		"  dedicated CPU placement: yes",
		"  ✓ 3 devices on node node-a.example.com",
	)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	all := showCheck(t, snap, check.KubeVirt("", ""), view.KubeVirt)
	for _, w := range []string{
		"  ✗ claim demo/no-such-claim not found",
		"  ! devices span NUMA nodes 0,1 without guestMappingPassthrough; the guest cannot see the topology",
		"  guest NUMA passthrough: no",
		"  · no DRA host devices",
		"  ! evidence: 9 warnings",
		"      status.nodeName is not a string",
	} {
		if !hasLine(all, w) {
			t.Errorf("missing %q in:\n%s", w, all)
		}
	}
	if strings.Contains(all, "vm-pending") {
		t.Errorf("unavailable VMI belongs to the UNAVAILABLE lines:\n%s", all)
	}
	none := snap
	none.VMIs = nil
	if got := showCheck(t, none, check.KubeVirt("", ""), view.KubeVirt); got != lines("═══ KubeVirt Check ═══", "", "· no VirtualMachineInstances") {
		t.Errorf("none:\n%s", got)
	}
}

func cpuResult(det map[string]any, st model.Status) []model.Result {
	return []model.Result{{Check: check.NameCPUPinning, Status: st, Subject: "demo/app", Details: det}}
}

func TestCPUPinningVerdicts(t *testing.T) {
	base := func() map[string]any {
		return map[string]any{"nodeName": "node-a", "cpuset": "8-11", "numaOfCpus": []int{1}, "deviceNuma": []int{0}, "pinned": true}
	}
	header := []string{"═══ CPU Pinning Check ═══", "", "demo/app (node: node-a)", "  cpuset = 8-11", "  CPUs on NUMA 1", "  devices on NUMA 0"}
	got := showResults(t, model.Snapshot{}, cpuResult(base(), model.StatusWarn), view.CPUPinning)
	if want := lines(append(header, "  ! CPUs span NUMA nodes not used by the devices")...); got != want {
		t.Errorf("bad:\n%s", got)
	}
	d := base()
	d["numaOfCpus"] = []int{0}
	got = showResults(t, model.Snapshot{}, cpuResult(d, model.StatusPass), view.CPUPinning)
	if !hasLine(got, "  ✓ CPUs pinned within the devices' NUMA nodes") {
		t.Errorf("good:\n%s", got)
	}
	d = base()
	d["pinned"] = false
	got = showResults(t, model.Snapshot{}, cpuResult(d, model.StatusWarn), view.CPUPinning)
	if !hasLine(got, "  ! pod is not CPU pinned") {
		t.Errorf("unpinned:\n%s", got)
	}
	d = base()
	d["deviceNuma"] = []int{}
	rs := cpuResult(d, model.StatusPass)
	rs[0].Findings = []model.Finding{{Status: model.StatusPass, Text: "no allocated device publishes a NUMA node; cannot compare"}}
	got = showResults(t, model.Snapshot{}, rs, view.CPUPinning)
	if !hasLine(got, "  · no allocated device publishes a NUMA node; cannot compare") || strings.Contains(got, "✓") {
		t.Errorf("informational pass must be neutral:\n%s", got)
	}
}

func vfioSnap(t *testing.T, nodes ...string) model.Snapshot {
	ev, _ := json.Marshal(host.Evidence{Version: host.Version, NodeName: "node-a", VFIODevNodes: nodes})
	return model.Snapshot{Evidence: map[string]json.RawMessage{host.Name: ev}}
}

func TestVFIOPerDeviceDetails(t *testing.T) {
	rs := []model.Result{{Check: check.NameVFIO, Status: model.StatusFail, Subject: "gpu.example.com/node-a/gpu-0",
		Details: map[string]any{"bdf": "0000:3b:00.0", "cdiPresent": false, "driver": "nvidia", "iommuGroup": "7",
			"nonVfioPeers": []string{"0000:3b:00.1 (driver snd_hda_intel)", "0000:3b:00.2 (no driver)"}},
		Findings: []model.Finding{{Status: model.StatusFail, Kind: model.KindVFIODriver, Text: "bound to driver nvidia, want vfio-pci"},
			{Status: model.StatusWarn, Kind: model.KindVFIOPeers, Text: "IOMMU group 7 also holds 0000:3b:00.1 (driver snd_hda_intel), 0000:3b:00.2 (no driver)",
				Head: "IOMMU group 7 also holds 2 devices", Items: []string{"0000:3b:00.1 (driver snd_hda_intel)", "0000:3b:00.2 (no driver)"}}}}}
	got := showResults(t, vfioSnap(t, "7"), rs, view.VFIO)
	want := lines(
		"═══ VFIO Check ═══",
		"",
		"gpu.example.com/gpu-0 (0000:3b:00.0)",
		"  ✗ bound driver: nvidia, want vfio-pci",
		"  IOMMU group: 7",
		"  ! non-vfio peer: 0000:3b:00.1 (driver snd_hda_intel)",
		"  ! non-vfio peer: 0000:3b:00.2 (no driver)",
		"  /dev/vfio/7: present",
		"  CDI spec lists device: no",
	)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if got := showResults(t, vfioSnap(t), rs, view.VFIO); !hasLine(got, "  ! /dev/vfio/7: missing") {
		t.Errorf("missing node:\n%s", got)
	}
	good := []model.Result{{Check: check.NameVFIO, Status: model.StatusPass, Subject: "gpu.example.com/node-a/gpu-0",
		Details: map[string]any{"bdf": "0000:3b:00.0", "cdiPresent": true, "driver": "vfio-pci", "iommuGroup": "7", "summary": "0000:3b:00.0 bound to vfio-pci in IOMMU group 7"}}}
	got = showResults(t, vfioSnap(t, "7"), good, view.VFIO)
	if !hasLine(got, "  CDI spec lists device: yes") || !hasLine(got, "  ✓ 0000:3b:00.0 bound to vfio-pci in IOMMU group 7") || strings.Contains(got, "non-vfio peer") {
		t.Errorf("good:\n%s", got)
	}
	none := []model.Result{{Check: check.NameVFIO, Status: model.StatusPass, Details: map[string]any{"reason": "no allocated device with a pciBusID on node node-a"}}}
	if got := showResults(t, vfioSnap(t), none, view.VFIO); got != lines("═══ VFIO Check ═══", "", "· no allocated device with a pciBusID on node node-a") {
		t.Errorf("none:\n%s", got)
	}
}
