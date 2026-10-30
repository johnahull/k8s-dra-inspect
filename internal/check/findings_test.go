package check

import (
	"testing"

	resourcev1 "k8s.io/api/resource/v1"

	"github.com/johnahull/k8s-dra-inspect/internal/adapters/host"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
)

// Every check fills Findings next to its joined reason: each non-pass result
// has findings whose worst status is the roll-up and whose texts join to the
// reason.
func TestFindingsMatchStatusAndReason(t *testing.T) {
	ev := vfioHost()
	ev.PCI[bdf0] = host.PCIDevice{Driver: "nvidia", IOMMUGroup: "7"}
	kv := kvSnap(t)
	for name, tc := range map[string]struct {
		r    model.Result
		want int // minimum findings
	}{
		"alignment": {one(t, Alignment("", ""), snapOf(numaPool([]int64{0}, []int64{1}), claim("ns", "c", nil, res("r", "a"), res("r", "b")))), 1},
		"alignment two findings": {one(t, Alignment("", ""), snapOf(numaPool([]int64{0}, []int64{1}),
			claim("ns", "c", []resourcev1.DeviceConstraint{match("resource.kubernetes.io/pciBusID")}, res("r", "a"), res("r", "b")))), 2},
		"counters":   {bySubject(t, run(Counters(), snapOf(pf0(cdev("vf-0", consume("missing", map[string]string{"vf-slots": "1"}))))), "gpu.example.com/p/missing"), 1},
		"capacity":   {capOne(t, snapOf(memPool(nil), claim("ns", "c", nil, consumed("mem", map[string]string{"size": "101Gi"})))), 1},
		"topology":   {one(t, Topology(), snapOf([]resourcev1.ResourceSlice{slice("s", "p", 1, 1, full("a"), dev("b", attrs{pcie: str("x")}), dev("c", attrs{numa: ints(1)}))})), 2},
		"kubevirt":   {kvOne(t, kv, "demo/vm-wrong-node"), 1},
		"vfio":       {one(t, VFIO(), vfioSnap(ev)), 1},
		"cpupinning": {one(t, CPUPinning("app", "demo"), pinSnap(hostEv(), "8-9")), 1},
	} {
		t.Run(name, func(t *testing.T) {
			if tc.r.Status == model.StatusPass {
				t.Fatalf("scenario is a pass: %+v", tc.r)
			}
			if len(tc.r.Findings) < tc.want {
				t.Fatalf("got %d findings, want >= %d: %+v", len(tc.r.Findings), tc.want, tc.r)
			}
			assertFindings(t, tc.r)
		})
	}
}

func TestFindingsAbsentOnPass(t *testing.T) {
	r := one(t, Topology(), snapOf([]resourcev1.ResourceSlice{slice("s", "p", 1, 1, full("a"))}))
	if r.Status != model.StatusPass || len(r.Findings) != 0 {
		t.Errorf("%+v", r)
	}
}

func bySubject(t *testing.T, rs []model.Result, subject string) model.Result {
	t.Helper()
	for _, r := range rs {
		if r.Subject == subject {
			return r
		}
	}
	t.Fatalf("no result for %s: %+v", subject, rs)
	return model.Result{}
}
