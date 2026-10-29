package check

import (
	"reflect"
	"runtime"
	"strings"
	"testing"

	resourcev1 "k8s.io/api/resource/v1"

	"github.com/johnahull/k8s-dra-inspect/internal/adapters/host"
	"github.com/johnahull/k8s-dra-inspect/internal/model"
)

// pinSnap: pod demo/app (uid A) on node-a holding claim c1 -> gpu-0 on NUMA 0.
func pinSnap(ev host.Evidence, cpus string, containers ...string) model.Snapshot {
	if cpus != "" {
		ev.Cgroups[uidA] = host.Cgroup{Path: "p", CPUs: cpus, Containers: containers}
	}
	s := snapOf([]resourcev1.ResourceSlice{slice("s", "p", 1, 1, dev("gpu-0", attrs{numa: ints(0)}), dev("plain", nil))},
		claim("demo", "c1", nil, res("r", "gpu-0")))
	s.Pods = []model.PodRef{podOn("demo", "app", uidA, "node-a", "c1")}
	return withHost(s, ev)
}

func TestCPUPinningMatrix(t *testing.T) {
	for _, tc := range []struct {
		name       string
		cpus       string
		want       model.Status
		reasonHas  string
		cpuNuma    []int
		pinned     bool
		modify     func(*model.Snapshot)
		deviceNuma []int
	}{
		{"pinned within device NUMA", "2-5", model.StatusPass, "", []int{0}, true, nil, []int{0}},
		{"pinned on a NUMA node without the device", "8-9", model.StatusWarn, "NUMA node(s) 1 not in", []int{1}, true, nil, []int{0}},
		{"pinned across NUMA incl. device node", "4-11", model.StatusWarn, "NUMA node(s) 1 not in", []int{0, 1}, true, nil, []int{0}},
		{"all online cpus is not pinned", "0-15", model.StatusWarn, "not CPU-pinned", []int{0, 1}, false, nil, []int{0}},
		{"no device NUMA to compare", "2-5", model.StatusPass, "cannot compare", []int{0}, true, func(s *model.Snapshot) {
			s.ResourceClaims[0].Status.Allocation.Devices.Results[0].Device = "plain"
		}, []int{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := pinSnap(hostEv(), tc.cpus)
			if tc.modify != nil {
				tc.modify(&s)
			}
			r := one(t, CPUPinning("app", "demo"), s)
			if r.Status != tc.want || r.Subject != "demo/app" {
				t.Fatalf("%+v", r)
			}
			if got, _ := r.Details["reason"].(string); !strings.Contains(got, tc.reasonHas) {
				t.Errorf("reason %q lacks %q", got, tc.reasonHas)
			}
			if !reflect.DeepEqual(r.Details["numaOfCpus"], tc.cpuNuma) || !reflect.DeepEqual(r.Details["deviceNuma"], tc.deviceNuma) ||
				r.Details["pinned"] != tc.pinned || r.Details["cpuset"] != tc.cpus || r.Details["nodeName"] != "node-a" {
				t.Errorf("details = %+v", r.Details)
			}
		})
	}
}

func TestCPUPinningUsesContainerCpusets(t *testing.T) {
	devOn1 := func(s *model.Snapshot) {
		s.ResourceSlices[0].Spec.Devices[0].Attributes[numa] = ints(1)
	}
	for _, tc := range []struct {
		name       string
		podCPUs    string
		containers []string
		version    int
		modify     func(*model.Snapshot)
		want       model.Status
		reasonHas  string
		cpuset     string
		pinned     bool
	}{
		{"container pinned on the device NUMA", "0-15", []string{"4-7"}, host.Version, nil, model.StatusPass, "", "4-7", true},
		{"container pinned on another NUMA", "0-15", []string{"4-7"}, host.Version, devOn1, model.StatusWarn, "NUMA node(s) 0 not in", "4-7", true},
		{"union of containers", "0-15", []string{"2-3", "4-5"}, host.Version, nil, model.StatusPass, "", "2-5", true},
		{"every container holds all cpus", "0-15", []string{"0-15", "0-15"}, host.Version, nil, model.StatusWarn, "not CPU-pinned", "0-15", false},
		{"one pinned and one shared container is not pinned", "0-15", []string{"4-7", "0-15"}, host.Version, nil, model.StatusWarn, "not CPU-pinned", "0-15", false},
		{"version 1 evidence falls back to the pod cpuset", "2-5", nil, 1, nil, model.StatusPass, "", "2-5", true},
		{"version 1 evidence all cpus", "0-15", nil, 1, nil, model.StatusWarn, "not CPU-pinned", "0-15", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ev := hostEv()
			ev.Version = tc.version
			s := pinSnap(ev, tc.podCPUs, tc.containers...)
			if tc.modify != nil {
				tc.modify(&s)
			}
			r := one(t, CPUPinning("app", "demo"), s)
			if got, _ := r.Details["reason"].(string); r.Status != tc.want || !strings.Contains(got, tc.reasonHas) ||
				r.Details["cpuset"] != tc.cpuset || r.Details["pinned"] != tc.pinned {
				t.Fatalf("%+v", r)
			}
		})
	}
}

func TestCPUPinningHostileCPULists(t *testing.T) {
	hostile := strings.Repeat("0-65536,", 1500) + "1"
	for name, s := range map[string]model.Snapshot{
		"pod cpuset":       pinSnap(hostEv(), hostile),
		"container cpuset": pinSnap(hostEv(), "0-15", hostile),
		"numa cpulist":     pinSnap(func() host.Evidence { e := hostEv(); e.NUMA["0"] = hostile; return e }(), "2-5"),
	} {
		t.Run(name, func(t *testing.T) {
			var before, after runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&before)
			r := one(t, CPUPinning("app", ""), s)
			runtime.ReadMemStats(&after)
			wantStatus(t, r, model.StatusError, "invalid cpulist")
			if d := after.TotalAlloc - before.TotalAlloc; d > 64<<20 {
				t.Fatalf("allocated %d MiB", d>>20)
			}
		})
	}
}

func TestCPUPinningUnavailable(t *testing.T) {
	noEv := pinSnap(hostEv(), "2-5")
	noEv.Evidence = nil
	offNode := hostEv()
	offNode.NodeName = "node-x"
	noName := hostEv()
	noName.NodeName = ""
	failed := hostEv()
	failed.Error = "no sysfs under /x; run on the node (or pass --host-root)"
	cgErr := hostEv()
	cgErr.Cgroups[uidA] = host.Cgroup{Path: "p", Error: "cpuset.cpus.effective not readable"}
	noNuma := hostEv()
	noNuma.NUMA = map[string]string{}
	oldVer := hostEv()
	oldVer.Version = 9
	badJSON := pinSnap(hostEv(), "2-5")
	badJSON.Evidence["host"] = []byte(`{not json`)
	unsched := pinSnap(hostEv(), "2-5")
	unsched.Pods[0].NodeName = ""
	noUID := pinSnap(hostEv(), "2-5")
	noUID.Pods[0].UID = ""
	two := pinSnap(hostEv(), "2-5")
	two.Pods = append(two.Pods, podOn("other", "app", uidA, "node-a"))
	for _, tc := range []struct {
		name   string
		snap   model.Snapshot
		target string
		want   string
	}{
		{"no evidence", noEv, "app", "no host evidence in snapshot; run on the node"},
		{"other node", pinSnap(offNode, "2-5"), "app", "evidence is from node node-x; this pod is on node node-a: run on node node-a"},
		{"evidence without node name", pinSnap(noName, "2-5"), "app", "evidence has no node name"},
		{"collection failed", pinSnap(failed, ""), "app", "no sysfs under /x"},
		{"no cgroup for pod", pinSnap(hostEv(), ""), "app", "no cgroup for pod " + uidA},
		{"unreadable cpuset", pinSnap(cgErr, ""), "app", "cpuset.cpus.effective not readable"},
		{"no numa", pinSnap(noNuma, "2-5"), "app", "no NUMA topology"},
		{"unsupported version", pinSnap(oldVer, "2-5"), "app", "unsupported host evidence version 9"},
		{"bad evidence json", badJSON, "app", "invalid host evidence"},
		{"unknown pod", pinSnap(hostEv(), "2-5"), "nope", "no pod named nope"},
		{"unscheduled pod", unsched, "app", "not scheduled"},
		{"pod without uid", noUID, "app", "no UID"},
		{"ambiguous", two, "app", "ambiguous: demo/app, other/app"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ns := ""
			wantStatus(t, one(t, CPUPinning(tc.target, ns), tc.snap), model.StatusUnavailable, tc.want)
		})
	}
}

func TestCPUPinningTargetForms(t *testing.T) {
	s := pinSnap(hostEv(), "2-5")
	s.Pods = append(s.Pods, podOn("other", "app", "00000000-0000-4000-8000-000000000009", "node-a"))
	for _, tc := range []struct{ target, ns, subject string }{{"demo/app", "", "demo/app"}, {"app", "demo", "demo/app"}} {
		if r := one(t, CPUPinning(tc.target, tc.ns), s); r.Subject != tc.subject || r.Status != model.StatusPass {
			t.Errorf("%s/%s: %+v", tc.target, tc.ns, r)
		}
	}
	// The other pod has no cgroup in the evidence.
	wantStatus(t, one(t, CPUPinning("other/app", ""), s), model.StatusUnavailable, "no cgroup for pod")
	// An unrelated -n does not hide the pod name.
	wantStatus(t, one(t, CPUPinning("app", "nope"), s), model.StatusUnavailable, "no pod named app")
}

func TestCPUPinningBadCPUListIsError(t *testing.T) {
	wantStatus(t, one(t, CPUPinning("app", ""), pinSnap(hostEv(), "x-y")), model.StatusError, "invalid cpulist")
	bad := hostEv()
	bad.NUMA["0"] = "zzz"
	wantStatus(t, one(t, CPUPinning("app", ""), pinSnap(bad, "2-5")), model.StatusError, "invalid cpulist")
}

func TestCPUPinningNeedsPods(t *testing.T) {
	s := pinSnap(hostEv(), "2-5")
	s.Collection["pods"] = model.CollectionStatus{State: model.StateForbidden, Reason: "forbidden: cannot list pods"}
	wantStatus(t, one(t, CPUPinning("app", ""), s), model.StatusUnavailable, "forbidden: cannot list pods")
}
