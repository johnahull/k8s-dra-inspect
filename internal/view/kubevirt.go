package view

import (
	"strings"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/render"
)

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

// evidenceWarnings prints the host-evidence warnings of a VMI: one line when
// there is one, else a count and the distinct texts with how often each came.
func evidenceWarnings(p *render.Printer, ws []string) {
	switch len(ws) {
	case 0:
		return
	case 1:
		p.Note(2, model.StatusWarn, "evidence: "+ws[0])
		return
	}
	var order []string
	n := map[string]int{}
	for _, w := range ws {
		if n[w] == 0 {
			order = append(order, w)
		}
		n[w]++
	}
	p.Note(2, model.StatusWarn, "evidence: "+model.Plural(len(ws), "warning", "warnings"))
	for _, w := range order {
		if n[w] > 1 {
			w += " (" + model.Plural(n[w], "time", "times") + ")"
		}
		p.Prose(6, 2, w)
	}
}

// KubeVirt prints each VMI with its host devices (request name, then the
// device and the host device on indented lines), their NUMA nodes and the
// guest settings that decide whether the guest can see them.
func KubeVirt(p *render.Printer, _ model.Snapshot, results []model.Result) {
	eachResult(p, "KubeVirt Check", results, "no VirtualMachineInstances to check", func(r model.Result, d *decoder) {
		p.Line(0, "%s", p.Bold(r.Subject))
		devs := detail[[]map[string]any](d, r, "devices")
		prev, node := "", ""
		for i, dv := range devs {
			req := field[string](d, "devices[]", dv, "request")
			if i == 0 || req != prev {
				p.Line(2, "%s", req)
				prev = req
			}
			line := field[string](d, "devices[]", dv, "device")
			if bdf := field[string](d, "devices[]", dv, "pciBusID"); bdf != "" {
				line += " (" + bdf + ")"
			}
			p.Line(4, "%s  %s", line, field[string](d, "devices[]", dv, "driver"))
			host := "hostDevice " + field[string](d, "devices[]", dv, "hostDevice")
			if n := field[string](d, "devices[]", dv, "node"); n != "" {
				host += "  node " + n
				node = n
			}
			p.Line(4, "%s", host)
		}
		if len(devs) > 0 {
			numa := "(none published)"
			if n := detail[[]string](d, r, "numa"); len(n) > 0 {
				numa = strings.Join(n, ",")
			}
			p.Line(2, "devices on NUMA %s", numa)
			p.Line(2, "guest NUMA passthrough: %s", yesNo(detail[bool](d, r, "guestNUMAPassthrough")))
			p.Line(2, "dedicated CPU placement: %s", yesNo(detail[bool](d, r, "dedicatedCPUPlacement")))
		}
		evidenceWarnings(p, detail[[]string](d, r, "warnings"))
		switch {
		case notes(p, 2, r, nil) > 0:
		case len(devs) == 0:
			p.Note(2, render.Info, reasonOf(r))
		case node != "":
			p.Note(2, model.StatusPass, model.Plural(len(devs), "device", "devices")+" on node "+node)
		default:
			p.Note(2, model.StatusPass, reasonOf(r))
		}
	})
}
