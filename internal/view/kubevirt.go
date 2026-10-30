package view

import (
	"strings"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/render"
)

func yesNo(v any) string {
	if v == true {
		return "yes"
	}
	return "no"
}

// KubeVirt prints each VMI with its host devices, their NUMA nodes and the
// guest settings that decide whether the guest can see them.
func KubeVirt(p *render.Printer, _ model.Snapshot, results []model.Result) {
	eachResult(p, "KubeVirt Check", results, "no VirtualMachineInstances to check", func(r model.Result) {
		p.Line(0, "%s", p.Bold(r.Subject))
		devs, _ := r.Details["devices"].([]map[string]any)
		for _, d := range devs {
			line := "hostDevice " + detAny(d["hostDevice"]) + "  request " + detAny(d["request"]) + ": " + detAny(d["driver"]) + "/" + detAny(d["device"])
			if bdf := detAny(d["pciBusID"]); bdf != "" {
				line += " (" + bdf + ")"
			}
			if n := detAny(d["node"]); n != "" {
				line += "  node " + n
			}
			p.Line(2, "%s", line)
		}
		if len(devs) > 0 {
			numa := "(none published)"
			if n := strs(r.Details["numa"]); len(n) > 0 {
				numa = strings.Join(n, ",")
			}
			p.Line(2, "devices on NUMA %s", numa)
			p.Line(2, "guest NUMA passthrough: %s", yesNo(r.Details["guestNUMAPassthrough"]))
			p.Line(2, "dedicated CPU placement: %s", yesNo(r.Details["dedicatedCPUPlacement"]))
		}
		for _, w := range strs(r.Details["warnings"]) {
			p.Note(2, model.StatusWarn, "evidence: "+w)
		}
		switch {
		case notes(p, 2, r, nil) > 0:
		case len(devs) == 0:
			p.Note(2, render.Info, reasonOf(r))
		default:
			p.Note(2, model.StatusPass, reasonOf(r))
		}
	})
}
