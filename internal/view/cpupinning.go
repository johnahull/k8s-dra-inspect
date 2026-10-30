package view

import (
	"strings"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/render"
)

// CPUPinning prints a pod's cpuset, the NUMA nodes those CPUs are on and the
// NUMA nodes of its devices, then a one-line verdict.
func CPUPinning(p *render.Printer, _ model.Snapshot, results []model.Result) {
	eachResult(p, "CPU Pinning Check", results, "no pod to check", func(r model.Result) {
		title := r.Subject
		if n := detStr(r, "nodeName"); n != "" {
			title += " (node: " + n + ")"
		}
		p.Line(0, "%s", p.Bold(title))
		if cpuset := detStr(r, "cpuset"); cpuset != "" {
			p.Line(2, "cpuset = %s", cpuset)
			p.Line(2, "CPUs on NUMA %s", strings.Join(ints(r.Details["numaOfCpus"]), ","))
			devs := "(none published)"
			if d := ints(r.Details["deviceNuma"]); len(d) > 0 {
				devs = strings.Join(d, ",")
			}
			p.Line(2, "devices on NUMA %s", devs)
		}
		switch {
		case r.Details["pinned"] == false:
			p.Note(2, r.Status, "pod is not CPU pinned")
		case r.Status == model.StatusWarn && detStr(r, "cpuset") != "":
			p.Note(2, r.Status, "CPUs span NUMA nodes not used by the devices")
		case notes(p, 2, r, nil) > 0:
		default:
			p.Note(2, model.StatusPass, "CPUs pinned within the devices' NUMA nodes")
		}
	})
}
