package view

import (
	"maps"
	"slices"
	"strings"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/render"
)

// Drivers prints one line per driver, and with --attributes its attribute keys.
func Drivers(p *render.Printer, snap model.Snapshot, results []model.Result) {
	p.Banner("DRA Drivers")
	names := map[string]map[string]bool{}  // driver -> node names
	labels := map[string]map[string]bool{} // driver -> (all nodes)/(selector)
	cur, _ := model.CurrentSlices(snap)
	for _, s := range cur {
		for _, d := range s.Spec.Devices {
			if names[s.Spec.Driver] == nil {
				names[s.Spec.Driver], labels[s.Spec.Driver] = map[string]bool{}, map[string]bool{}
			}
			if r := model.DeviceNode(s, d); r.Name != "" {
				names[s.Spec.Driver][r.Name] = true
			} else if r.Label != "" {
				labels[s.Spec.Driver][r.Label] = true
			}
		}
	}
	p.Blank()
	n := 0
	for _, r := range results {
		if r.Status == model.StatusUnavailable {
			continue
		}
		n++
		if _, hasAttrs := r.Details["attributes"]; hasAttrs && n > 1 { // --attributes blocks are separated
			p.Blank()
		}
		where := append(slices.Sorted(maps.Keys(names[r.Subject])), slices.Sorted(maps.Keys(labels[r.Subject]))...)
		var d decoder
		line := model.Plural(detail[int](&d, r, "devices"), "device", "devices")
		if len(where) > 0 {
			line += " on " + strings.Join(where, ", ")
		}
		p.Line(0, "%s: %s  (%s)", p.Bold(r.Subject), line, model.Plural(detail[int](&d, r, "pools"), "pool", "pools"))
		// The inspector leaves "attributes" out of Details unless --attributes was given.
		if _, listed := r.Details["attributes"]; listed {
			keys := detail[[]string](&d, r, "attributes")
			p.Line(2, "attributes:")
			for _, k := range keys {
				p.Line(4, "%s", k)
			}
		}
		notes(p, 2, r, nil)
		d.flush(p, 2)
	}
	if n == 0 {
		p.Line(0, "no drivers")
	}
}
