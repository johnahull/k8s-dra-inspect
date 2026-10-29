package check

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/johnahull/k8s-dra-inspect/internal/adapters/host"
	"github.com/johnahull/k8s-dra-inspect/internal/model"
)

// NameCPUPinning is the Result.Check of CPUPinning.
const NameCPUPinning = "check.cpupinning"

// CPUPinning compares the CPUs a pod is confined to (from host evidence) with
// the NUMA nodes of its allocated DRA devices. target is "name" or
// "ns/name"; namespace (-n) scopes a bare name.
func CPUPinning(target, namespace string) Check {
	return func(_ context.Context, snap model.Snapshot) []model.Result {
		if r, ok := need(snap, NameCPUPinning, "pods", "resourceslices", "resourceclaims"); ok {
			return r
		}
		ns, name := namespace, target
		if a, b, ok := strings.Cut(target, "/"); ok {
			ns, name = a, b
		}
		var found []model.PodRef
		for _, p := range snap.Pods {
			if p.Name == name && (ns == "" || p.Namespace == ns) {
				found = append(found, p)
			}
		}
		switch {
		case target == "":
			return single(NameCPUPinning, model.StatusUnavailable, "", "a pod name is required")
		case len(found) == 0:
			return single(NameCPUPinning, model.StatusUnavailable, "", "no pod named "+target)
		case len(found) > 1:
			var c []string
			for _, p := range found {
				c = append(c, p.Namespace+"/"+p.Name)
			}
			return single(NameCPUPinning, model.StatusUnavailable, "", fmt.Sprintf("pod %s is ambiguous: %s; use ns/name or -n", target, strings.Join(c, ", ")))
		}
		return []model.Result{pinPod(snap, found[0])}
	}
}

func pinPod(snap model.Snapshot, p model.PodRef) model.Result {
	subject := p.Namespace + "/" + p.Name
	fail := func(st model.Status, reason string) model.Result {
		return single(NameCPUPinning, st, subject, reason)[0]
	}
	if p.NodeName == "" {
		return fail(model.StatusUnavailable, "pod is not scheduled to a node")
	}
	if p.UID == "" {
		return fail(model.StatusUnavailable, "pod has no UID in the snapshot")
	}
	ev, un, ok := loadHost(snap, NameCPUPinning)
	if !ok {
		un[0].Subject = subject
		return un[0]
	}
	if ev.NodeName != p.NodeName {
		return fail(model.StatusUnavailable, offNodeReason(ev.NodeName, "pod", p.NodeName))
	}
	cg, ok := ev.Cgroups[string(p.UID)]
	switch {
	case !ok:
		return fail(model.StatusUnavailable, "no cgroup for pod "+string(p.UID)+" in host evidence (pod not running on this node?)")
	case cg.Error != "":
		return fail(model.StatusUnavailable, cg.Error)
	case len(ev.NUMA) == 0:
		return fail(model.StatusUnavailable, "no NUMA topology in host evidence")
	}
	cpus, err := host.ParseCPUList(cg.CPUs)
	if err != nil {
		return fail(model.StatusError, err.Error())
	}
	var all, cpuNuma []int
	for id, list := range ev.NUMA {
		n, err := strconv.Atoi(id)
		if err != nil {
			return fail(model.StatusError, "invalid NUMA node id "+id)
		}
		l, err := host.ParseCPUList(list)
		if err != nil {
			return fail(model.StatusError, err.Error())
		}
		all = append(all, l...)
		if slices.ContainsFunc(cpus, func(c int) bool { return slices.Contains(l, c) }) {
			cpuNuma = append(cpuNuma, n)
		}
	}
	slices.Sort(all)
	slices.Sort(cpuNuma)
	all = slices.Compact(all)
	devNuma, err := deviceNuma(snap, p)
	if err != nil {
		return fail(model.StatusError, err.Error())
	}
	pinned := !slices.Equal(cpus, all)
	det := map[string]any{"nodeName": ev.NodeName, "cpuset": cg.CPUs, "numaOfCpus": cpuNuma, "deviceNuma": devNuma, "pinned": pinned}
	st, reason := model.StatusPass, ""
	var extra []int
	for _, n := range cpuNuma {
		if !slices.Contains(devNuma, n) {
			extra = append(extra, n)
		}
	}
	switch {
	case !pinned:
		st, reason = model.StatusWarn, "cpuset is all online CPUs; pod is not CPU-pinned"
	case len(devNuma) == 0:
		reason = "no allocated device publishes a NUMA node; cannot compare"
	case len(extra) > 0:
		st, reason = model.StatusWarn, fmt.Sprintf("pinned CPUs are on NUMA node(s) %s not in the devices' NUMA node(s) %s", joinInts(extra), joinInts(devNuma))
	}
	if reason != "" {
		det["reason"] = reason
	} else {
		det["summary"] = fmt.Sprintf("CPUs %s on NUMA node(s) %s match the devices", cg.CPUs, joinInts(cpuNuma))
	}
	return model.Result{Check: NameCPUPinning, Status: st, Subject: subject, Details: det}
}

func joinInts(v []int) string {
	s := make([]string, len(v))
	for i, n := range v {
		s[i] = strconv.Itoa(n)
	}
	return strings.Join(s, ",")
}

// deviceNuma unions the NUMA nodes of the pod's allocated devices.
func deviceNuma(snap model.Snapshot, p model.PodRef) ([]int, error) {
	idx := newDeviceIndex(snap)
	names := podClaims(p)
	out := []int{}
	for _, c := range allocated(snap) {
		if c.Namespace != p.Namespace || !slices.Contains(names, c.Name) {
			continue
		}
		for _, r := range c.Status.Allocation.Devices.Results {
			ref := idx.find(r.Driver, r.Pool, r.Device)
			if ref.state == deviceMissing {
				continue
			}
			top, err := model.TopologyOf(ref.dev, r.Driver)
			if err != nil {
				return nil, err
			}
			for _, v := range top.NUMA.Values {
				if n, err := strconv.Atoi(v); err == nil {
					out = append(out, n)
				}
			}
		}
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}
