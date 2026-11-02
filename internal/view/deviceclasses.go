package view

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/render"
)

// DeviceClasses prints one block per class. Selector expressions and opaque
// driver names are printed; opaque parameters are never printed.
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
		var d decoder
		lbl := detail[map[string]string](&d, r, "labels")
		if len(lbl) == 0 {
			p.Line(2, "labels: none")
		} else {
			p.Line(2, "labels:")
			for _, k := range slices.Sorted(maps.Keys(lbl)) {
				p.Line(4, "%s=%s", k, lbl[k])
			}
		}
		selectorCount := detail[int](&d, r, "selectorCount")
		expressions := detail[[]string](&d, r, "selectorExpressions")
		if len(expressions) == selectorCount {
			p.Line(2, "selectors: %d", selectorCount)
		} else {
			p.Line(2, "selectors: %d (%d expressions shown)", selectorCount, len(expressions))
		}
		for i, expression := range expressions {
			p.ProseAt(4, 3, render.DefaultListWidth, fmt.Sprintf("%d. %s", i+1, expression))
		}
		cfg := "none"
		if detail[bool](&d, r, "configPresent") {
			cfg = "present"
			if drivers := detail[[]string](&d, r, "configDrivers"); len(drivers) > 0 {
				cfg = strings.Join(drivers, ", ")
			}
		}
		p.Line(2, "config: %s", cfg)
		d.flush(p, 2)
	}
	if n == 0 {
		p.Blank()
		p.Line(0, "no DeviceClasses")
	}
}
