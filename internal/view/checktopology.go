package view

import (
	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/render"
)

// CheckTopology prints each pool's published-topology summary and one
// sentence per missing or conflicting attribute, naming the devices.
func CheckTopology(p *render.Printer, _ model.Snapshot, results []model.Result) {
	eachResult(p, "Topology Check", results, "no ResourceSlices to check", func(r model.Result, _ *decoder) {
		p.Line(0, "%s", p.Bold(r.Subject))
		if s := detStr(r, "summary"); s != "" {
			p.Line(2, "%s", s)
		}
		if notes(p, 2, r, nil) == 0 {
			p.Note(2, model.StatusPass, "every device publishes a NUMA node; PCIe roots are present where expected")
		}
	})
}
