package check

import (
	"fmt"
	"testing"

	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/utils/ptr"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
)

func cset(name string, counters map[string]string) resourcev1.CounterSet {
	c := resourcev1.CounterSet{Name: name, Counters: map[string]resourcev1.Counter{}}
	for k, v := range counters {
		c.Counters[k] = resourcev1.Counter{Value: q(v)}
	}
	return c
}

func consume(set string, counters map[string]string) resourcev1.DeviceCounterConsumption {
	c := resourcev1.DeviceCounterConsumption{CounterSet: set, Counters: map[string]resourcev1.Counter{}}
	for k, v := range counters {
		c.Counters[k] = resourcev1.Counter{Value: q(v)}
	}
	return c
}

func cdev(name string, cc ...resourcev1.DeviceCounterConsumption) resourcev1.Device {
	return resourcev1.Device{Name: name, ConsumesCounters: cc}
}

func counterSlice(name, pool string, gen, count int64, sets ...resourcev1.CounterSet) resourcev1.ResourceSlice {
	s := slice(name, pool, gen, count)
	s.Spec.SharedCounters = sets
	return s
}

func vf(name string) resourcev1.Device {
	return cdev(name, consume("pf-0", map[string]string{"vf-slots": "1"}))
}

// pf0 is the python fixture: counter set pf-0 with 2 vf-slots.
func pf0(devs ...resourcev1.Device) []resourcev1.ResourceSlice {
	return []resourcev1.ResourceSlice{counterSlice("c", "p", 1, 2, cset("pf-0", map[string]string{"vf-slots": "2"})), slice("d", "p", 1, 2, devs...)}
}

func counters(t *testing.T, s model.Snapshot) model.Result {
	t.Helper()
	return one(t, Counters(), s)
}

func counterVals(t *testing.T, r model.Result, name string) map[string]string {
	t.Helper()
	m, ok := r.Details["counters"].(map[string]map[string]string)
	if !ok || m[name] == nil {
		t.Fatalf("no counters[%s] in %+v", name, r.Details)
	}
	return m[name]
}

func TestCountersNoSharedCountersIsOnePass(t *testing.T) {
	r := counters(t, snapOf([]resourcev1.ResourceSlice{slice("s", "p", 1, 1, dev("a", nil))}))
	wantStatus(t, r, model.StatusPass, "no shared counters")
}

// test_direct_allocation_is_subtracted
func TestCountersAllocationIsSubtracted(t *testing.T) {
	r := counters(t, snapOf(pf0(vf("vf-0")), claim("ns", "c", nil, res("r", "vf-0"))))
	if r.Status != model.StatusPass || r.Subject != "gpu.example.com/p/pf-0" {
		t.Fatalf("%+v", r)
	}
	got := fmt.Sprint(counterVals(t, r, "vf-slots"))
	if got != "map[capacity:2 remaining:1 used:1]" {
		t.Errorf("counter = %s", got)
	}
}

func TestCountersExactlyFullPassesOvercommitFails(t *testing.T) {
	full := snapOf(pf0(vf("vf-0"), vf("vf-1")), claim("ns", "c", nil, res("r", "vf-0"), res("r", "vf-1")))
	r := counters(t, full)
	if r.Status != model.StatusPass || counterVals(t, r, "vf-slots")["remaining"] != "0" {
		t.Fatalf("exactly full must pass: %+v", r)
	}
	over := snapOf(pf0(vf("vf-0"), vf("vf-1"), vf("vf-2")), claim("ns", "c", nil, res("r", "vf-0"), res("r", "vf-1"), res("r", "vf-2")))
	r = counters(t, over)
	wantStatus(t, r, model.StatusFail, "vf-slots")
	if got := counterVals(t, r, "vf-slots"); got["used"] != "3" || got["remaining"] != "-1" {
		t.Errorf("counter = %v", got)
	}
}

// Allocated claims count even with empty reservedFor (scheduler behaviour); the
// legacy script skipped them.
func TestCountersAllocatedWithoutReservedForCounts(t *testing.T) {
	c := claim("ns", "c", nil, res("r", "vf-0"))
	if len(c.Status.ReservedFor) != 0 {
		t.Fatal("setup")
	}
	r := counters(t, snapOf(pf0(vf("vf-0")), c))
	if counterVals(t, r, "vf-slots")["used"] != "1" {
		t.Fatalf("%+v", r)
	}
}

func TestCountersUsesQuantityArithmetic(t *testing.T) {
	sl := []resourcev1.ResourceSlice{
		counterSlice("c", "p", 1, 2, cset("mem", map[string]string{"size": "1Ki"})),
		slice("d", "p", 1, 2, cdev("a", consume("mem", map[string]string{"size": "512"})), cdev("b", consume("mem", map[string]string{"size": "0.5Ki"})), cdev("c", consume("mem", map[string]string{"size": "512"}))),
	}
	r := counters(t, snapOf(sl, claim("ns", "c", nil, res("r", "a"), res("r", "b"))))
	if r.Status != model.StatusPass || counterVals(t, r, "size")["remaining"] != "0" {
		t.Fatalf("%+v", r)
	}
	over := snapOf(sl, claim("ns", "c", nil, res("r", "a"), res("r", "b")), claim("ns", "d", nil, res("r", "c")))
	if r := counters(t, over); r.Status != model.StatusFail {
		t.Fatalf("1.5Ki of 1Ki: %+v", r)
	}
}

// test_allocation_pool_is_part_of_device_identity
func TestCountersPoolIsPartOfDeviceIdentity(t *testing.T) {
	sl := []resourcev1.ResourceSlice{
		counterSlice("ca", "node-a", 1, 2, cset("pf-0", map[string]string{"vf-slots": "1"})), slice("da", "node-a", 1, 2, vf("vf-0")),
		counterSlice("cb", "node-b", 1, 2, cset("pf-0", map[string]string{"vf-slots": "1"})), slice("db", "node-b", 1, 2, vf("vf-0")),
	}
	c := claim("ns", "c", nil, res("r", "vf-0"))
	c.Status.Allocation.Devices.Results[0].Pool = "node-b"
	rs := run(Counters(), snapOf(sl, c))
	if len(rs) != 2 {
		t.Fatalf("one result per pool+set: %+v", rs)
	}
	used := map[string]string{}
	for _, r := range rs {
		used[r.Subject] = counterVals(t, r, "vf-slots")["used"]
	}
	if used["gpu.example.com/node-a/pf-0"] != "0" || used["gpu.example.com/node-b/pf-0"] != "1" {
		t.Errorf("used = %v", used)
	}
}

// Python's PCI-sibling correlation is dropped: a device that consumes no
// counters (the PF) consumes nothing, whatever it shares a PCI address with.
func TestCountersNoSiblingCorrelation(t *testing.T) {
	r := counters(t, snapOf(pf0(dev("gpu-0", attrs{"resource.kubernetes.io/pciBusID": str("0000:01:00.0")}), vf("gpu-vfio-0")),
		claim("ns", "c", nil, res("r", "gpu-0"))))
	if r.Status != model.StatusPass || counterVals(t, r, "vf-slots")["used"] != "0" {
		t.Fatalf("%+v", r)
	}
}

// test_invalid_counter_reference_is_diagnostic
func TestCountersUnknownSetFails(t *testing.T) {
	rs := run(Counters(), snapOf(pf0(cdev("vf-0", consume("missing", map[string]string{"vf-slots": "1"})))))
	var bad *model.Result
	for i := range rs {
		if rs[i].Subject == "gpu.example.com/p/missing" {
			bad = &rs[i]
		}
	}
	if bad == nil {
		t.Fatalf("no result for undefined set: %+v", rs)
	}
	wantStatus(t, *bad, model.StatusFail, "counter set missing")
}

func TestCountersUnknownCounterFails(t *testing.T) {
	rs := run(Counters(), snapOf(pf0(cdev("vf-0", consume("pf-0", map[string]string{"nope": "1"})))))
	if len(rs) != 1 {
		t.Fatalf("%+v", rs)
	}
	wantStatus(t, rs[0], model.StatusFail, "counter nope")
}

// test_missing_allocated_device_is_not_reported_free
func TestCountersMissingAllocatedDeviceFailsNotFree(t *testing.T) {
	r := counters(t, snapOf(pf0(vf("vf-0")), claim("ns", "c", nil, res("r", "vf-removed"))))
	wantStatus(t, r, model.StatusFail, "vf-removed")
}

func TestCountersStaleGenerationAllocationWarns(t *testing.T) {
	sl := []resourcev1.ResourceSlice{
		counterSlice("c1", "p", 1, 2, cset("pf-0", map[string]string{"vf-slots": "2"})), slice("d1", "p", 1, 2, vf("vf-old")),
		counterSlice("c2", "p", 2, 2, cset("pf-0", map[string]string{"vf-slots": "2"})), slice("d2", "p", 2, 2, vf("vf-new")),
	}
	r := counters(t, snapOf(sl, claim("ns", "c", nil, res("r", "vf-old"))))
	wantStatus(t, r, model.StatusWarn, "stale generation")
	if counterVals(t, r, "vf-slots")["used"] != "0" {
		t.Errorf("stale device must not be counted: %+v", r)
	}
}

func TestCountersUsesNewestCompleteGenerationAcrossSlices(t *testing.T) {
	// gen 2 has counters in one slice and devices in another; gen 3 is partial.
	sl := []resourcev1.ResourceSlice{
		counterSlice("c1", "p", 1, 2, cset("pf-0", map[string]string{"vf-slots": "9"})),
		counterSlice("c2", "p", 2, 2, cset("pf-0", map[string]string{"vf-slots": "2"})), slice("d2", "p", 2, 2, vf("vf-0")),
		counterSlice("c3", "p", 3, 2, cset("pf-0", map[string]string{"vf-slots": "100"})),
	}
	r := counters(t, snapOf(sl, claim("ns", "c", nil, res("r", "vf-0"))))
	if got := counterVals(t, r, "vf-slots"); got["capacity"] != "2" || got["used"] != "1" {
		t.Fatalf("%v", got)
	}
}

func TestCountersMultiAllocationCountsPerResult(t *testing.T) {
	shared := vf("vf-0")
	shared.AllowMultipleAllocations = ptr.To(true)
	two := snapOf(pf0(shared), claim("ns", "a", nil, res("r", "vf-0")), claim("ns", "b", nil, res("r", "vf-0")))
	if got := counterVals(t, counters(t, two), "vf-slots")["used"]; got != "2" {
		t.Errorf("multi-allocatable device counted %s times, want 2", got)
	}
	single := snapOf(pf0(vf("vf-0")), claim("ns", "a", nil, res("r", "vf-0")), claim("ns", "b", nil, res("r", "vf-0")))
	if got := counterVals(t, counters(t, single), "vf-slots")["used"]; got != "1" {
		t.Errorf("single-allocation device counted %s times, want 1", got)
	}
}

func TestCountersIncompletePoolWarns(t *testing.T) {
	sl := []resourcev1.ResourceSlice{counterSlice("c", "p", 1, 3, cset("pf-0", map[string]string{"vf-slots": "2"}))}
	wantStatus(t, counters(t, snapOf(sl)), model.StatusWarn, "fewer ResourceSlices")
}

func TestCountersSummaryShowsUsedOverCapacity(t *testing.T) {
	r := counters(t, snapOf(pf0(vf("vf-0")), claim("ns", "c", nil, res("r", "vf-0"))))
	if r.Details["summary"] != "vf-slots 1/2" {
		t.Errorf("summary = %v", r.Details["summary"])
	}
}

func TestCountersAllocatedByIsEmptyListNotNull(t *testing.T) {
	r := counters(t, snapOf(pf0(vf("vf-0"))))
	if by, ok := r.Details["allocatedBy"].([]string); !ok || by == nil {
		t.Errorf("allocatedBy = %#v, want empty non-nil list", r.Details["allocatedBy"])
	}
}
