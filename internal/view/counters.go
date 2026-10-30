package view

import (
	"maps"
	"slices"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/render"
)

// Counters prints each pool's counter sets: how many of each counter are in
// use, who uses them, then one sentence per finding.
func Counters(p *render.Printer, _ model.Snapshot, results []model.Result) {
	p.Banner("Shared Counters")
	n, last := 0, ""
	for _, r := range results {
		if r.Status == model.StatusUnavailable {
			continue
		}
		n++
		if r.Subject == "" && r.Status == model.StatusPass {
			p.Blank()
			p.Note(0, render.Info, reasonOf(r))
			continue
		}
		drv, pool := detStr(r, "driver"), detStr(r, "pool")
		if key := drv + "\x00" + pool; key != last {
			p.Blank()
			p.Line(0, "%s (pool: %s)", p.Bold(drv), pool)
			last = key
		} else {
			p.Blank() // sets of one pool are separated like devices of one check
		}
		p.Line(2, "counter set %s", detStr(r, "counterSet"))
		var d decoder
		counters := detail[map[string]map[string]string](&d, r, "counters")
		for _, name := range slices.Sorted(maps.Keys(counters)) {
			p.Line(4, "%s: %s of %s in use", name, counters[name]["used"], counters[name]["capacity"])
		}
		for _, by := range detail[[]string](&d, r, "allocatedBy") {
			p.Line(4, "used by %s", by)
		}
		if notes(p, 4, r, nil) == 0 {
			p.Note(4, model.StatusPass, "within capacity")
		}
		d.flush(p, 4)
	}
	if n == 0 {
		p.Blank()
		p.Note(0, render.Info, "no shared counters to check")
	}
}
