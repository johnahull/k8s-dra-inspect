package view

import (
	"strconv"
	"strings"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/render"
)

// CPUPinning prints a pod's cpuset, the NUMA nodes those CPUs are on and the
// NUMA nodes of its devices, then a one-line verdict.
func CPUPinning(p *render.Printer, _ model.Snapshot, results []model.Result) {
	eachResult(p, "CPU Pinning Check", results, "no pod to check", func(r model.Result, d *decoder) {
		p.Line(0, "%s", p.Bold(subjectOf(r, d)))
		if cpuset := detStr(r, "cpuset"); cpuset != "" {
			p.Line(2, "cpuset = %s", cpuset)
			p.Line(2, "CPUs on NUMA %s", joinInts(detail[[]int](d, r, "numaOfCpus")))
			devs := "(none published)"
			if dn := detail[[]int](d, r, "deviceNuma"); len(dn) > 0 {
				devs = joinInts(dn)
			}
			p.Line(2, "devices on NUMA %s", devs)
		}
		_, hasPinned := r.Details["pinned"]
		switch {
		case hasPinned && !detail[bool](d, r, "pinned"):
			p.Note(2, r.Status, "pod is not CPU pinned")
		case r.Status == model.StatusWarn && detStr(r, "cpuset") != "":
			p.Note(2, r.Status, "CPUs span NUMA nodes not used by the devices")
		case notes(p, 2, r, nil) > 0:
		default:
			p.Note(2, model.StatusPass, "CPUs pinned within the devices' NUMA nodes")
		}
	})
}

func joinInts(v []int) string {
	s := make([]string, len(v))
	for i, n := range v {
		s[i] = strconv.Itoa(n)
	}
	return strings.Join(s, ",")
}
