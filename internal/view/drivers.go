package view

import (
	"maps"
	"slices"
	"strings"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/render"
)

// driverCell is what one driver publishes on one node (or placeholder).
type driverCell struct {
	devices int
	pools   map[string]bool
	attrs   map[string]bool
}

// Drivers prints, per node and driver, the current devices and pools, and
// with --attributes the attribute keys those devices publish.
//
// Warnings stay with the node whose devices they concern: an incomplete-pool
// finding prints under every node that holds a device of one of its pools.
// What no node can own (another kind of finding, a pool with no devices, a
// Details type mismatch) prints once at the end under "Other notes".
func Drivers(p *render.Printer, snap model.Snapshot, results []model.Result) {
	p.Banner("DRA Drivers")
	cells := map[string]map[string]*driverCell{} // node -> driver -> cell
	cur, _ := model.CurrentSlices(snap)
	for _, s := range cur {
		for _, d := range s.Spec.Devices {
			node := model.DeviceNode(s, d).String()
			if cells[node] == nil {
				cells[node] = map[string]*driverCell{}
			}
			c := cells[node][s.Spec.Driver]
			if c == nil {
				c = &driverCell{pools: map[string]bool{}, attrs: map[string]bool{}}
				cells[node][s.Spec.Driver] = c
			}
			c.devices++
			c.pools[s.Spec.Pool.Name] = true
			for k := range d.Attributes {
				c.attrs[string(k)] = true
			}
		}
	}
	byDriver := map[string]model.Result{}
	for _, r := range results {
		if r.Status != model.StatusUnavailable {
			byDriver[r.Subject] = r
		}
	}
	p.Blank()
	if len(byDriver) == 0 {
		p.Line(0, "no drivers")
		return
	}

	type other struct {
		subject string
		f       model.Finding
	}
	var others []other
	// A pool note names only the pools that node holds; a pool no node holds
	// devices for is left for "Other notes".
	held := func(items []string, cs ...*driverCell) []string {
		return slices.DeleteFunc(slices.Clone(items), func(x string) bool {
			return !slices.ContainsFunc(cs, func(c *driverCell) bool { return c != nil && c.pools[x] })
		})
	}

	first := true
	for _, node := range sortedNodes(cells) {
		if !first {
			p.Blank()
		}
		first = false
		nodeTitle(p, node)
		drivers := slices.Sorted(maps.Keys(cells[node]))
		for i, name := range drivers {
			c, r := cells[node][name], byDriver[name]
			if i > 0 && len(drivers) > 1 && hasAttrs(r) { // --attributes blocks are separated
				p.Blank()
			}
			p.Line(2, "%s: %s  (%s)", p.Bold(name), model.Plural(c.devices, "device", "devices"), model.Plural(len(c.pools), "pool", "pools"))
			if hasAttrs(r) {
				p.Line(4, "attributes:")
				for _, k := range slices.Sorted(maps.Keys(c.attrs)) {
					p.Line(6, "%s", k)
				}
			}
			for _, f := range findingsOf(r) {
				if f.Kind == model.KindIncomplete {
					if items := held(f.Items, c); len(items) > 0 {
						printFinding(p, 4, poolNote(f, items), "", nil)
					}
				}
			}
		}
	}
	// Drivers without a device on any node still print.
	var empty []string
	for name := range byDriver {
		if !slices.ContainsFunc(slices.Collect(maps.Values(cells)), func(m map[string]*driverCell) bool { return m[name] != nil }) {
			empty = append(empty, name)
		}
	}
	slices.Sort(empty)
	var d decoder
	for i, name := range empty {
		if i == 0 && !first {
			p.Blank()
		}
		r := byDriver[name]
		p.Line(0, "%s: %s  (%s)", p.Bold(name), model.Plural(detail[int](&d, r, "devices"), "device", "devices"), model.Plural(detail[int](&d, r, "pools"), "pool", "pools"))
	}
	for _, name := range slices.Sorted(maps.Keys(byDriver)) {
		for _, f := range findingsOf(byDriver[name]) {
			if f.Kind != model.KindIncomplete || len(f.Items) == 0 {
				others = append(others, other{name, f})
				continue
			}
			var all []*driverCell
			for _, drv := range cells {
				all = append(all, drv[name])
			}
			if rest := slices.DeleteFunc(slices.Clone(f.Items), func(x string) bool { return len(held([]string{x}, all...)) > 0 }); len(rest) == len(f.Items) {
				others = append(others, other{name, f})
			} else if len(rest) > 0 {
				others = append(others, other{name, poolNote(f, rest)})
			}
		}
	}
	if len(others) > 0 || len(d.errs) > 0 {
		p.Blank()
		p.Line(0, "%s", p.Bold("Other notes"))
		for _, o := range others {
			printFinding(p, 2, o.f, o.subject, nil)
		}
		d.flush(p, 2)
	}
}

// poolNote is an incomplete-pool finding narrowed to the given pools, worded
// by this view so the sentence always matches the list it prints.
func poolNote(f model.Finding, pools []string) model.Finding {
	f.Items = pools
	f.Text = "pool has fewer ResourceSlices than expected for its newest generation: " + strings.Join(pools, ", ")
	return f
}

// hasAttrs reports whether --attributes was given: the inspector leaves its
// "attributes" Detail key out otherwise (the same signal as before). Per-node
// keys come from the snapshot, as the inspector reports driver-wide keys only.
func hasAttrs(r model.Result) bool {
	_, ok := r.Details["attributes"]
	return ok
}
