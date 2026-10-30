package view

import (
	"maps"
	"slices"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/render"
)

// Capacity prints each device whose capacity is consumed: consumed of
// published with what remains, the claims using it, then the findings.
func Capacity(p *render.Printer, _ model.Snapshot, results []model.Result) {
	eachResult(p, "Consumable Capacity", results, "no consumable capacity to check", func(r model.Result, d *decoder) {
		p.Line(0, "%s", p.Bold(r.Subject))
		caps := detail[map[string]map[string]string](d, r, "capacities")
		for _, name := range slices.Sorted(maps.Keys(caps)) {
			c := caps[name]
			p.Line(2, "%s: %s of %s consumed (remaining %s)", name, c["consumed"], c["published"], c["remaining"])
		}
		for _, by := range detail[[]string](d, r, "allocatedBy") {
			p.Line(2, "used by %s", by)
		}
		if notes(p, 2, r, nil) == 0 {
			p.Note(2, model.StatusPass, "within published capacity")
		}
	})
}
