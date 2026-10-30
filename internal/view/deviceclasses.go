package view

import (
	"maps"
	"slices"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/render"
)

// DeviceClasses prints one block per class. Selector expressions and opaque
// config are never printed, only their presence.
func DeviceClasses(p *render.Printer, snap model.Snapshot, results []model.Result) {
	p.Banner("DeviceClasses")
	n := 0
	for _, r := range results {
		if r.Status == model.StatusUnavailable {
			continue
		}
		n++
		p.Blank()
		p.Line(0, "%s", p.Bold(r.Subject))
		lbl, _ := r.Details["labels"].(map[string]string)
		if len(lbl) == 0 {
			p.Line(2, "labels: none")
		} else {
			p.Line(2, "labels:")
			for _, k := range slices.Sorted(maps.Keys(lbl)) {
				p.Line(4, "%s=%s", k, lbl[k])
			}
		}
		p.Line(2, "selectors: %d (expressions not shown)", intOf(r.Details["selectorCount"]))
		cfg := "none"
		if r.Details["configPresent"] == true {
			cfg = "present"
		}
		p.Line(2, "config: %s", cfg)
	}
	if n == 0 {
		p.Blank()
		p.Line(0, "no DeviceClasses")
	}
}
