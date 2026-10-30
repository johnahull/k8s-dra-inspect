package view

import (
	"maps"
	"slices"

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
	errs := map[string]*decoder{} // driver -> mismatches
	// owners[driver][i] lists the nodes that print finding i of driver.
	owners := map[string]map[int][]string{}
	for name, r := range byDriver {
		owners[name] = map[int][]string{}
		for i, f := range findingsOf(r) {
			if f.Kind != model.KindIncomplete {
				continue
			}
			for node, drv := range cells {
				if c := drv[name]; c != nil && slices.ContainsFunc(f.Items, func(pool string) bool { return c.pools[pool] }) {
					owners[name][i] = append(owners[name][i], node)
				}
			}
		}
	}

	first := true
	for _, node := range slices.SortedFunc(maps.Keys(cells), cmpNode) {
		if !first {
			p.Blank()
		}
		first = false
		p.Line(0, "%s", nodeTitle(p, node))
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
			for fi, f := range findingsOf(r) {
				if slices.Contains(owners[name][fi], node) {
					printFinding(p, 4, f, "", nil)
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
	for i, name := range empty {
		if i == 0 && !first {
			p.Blank()
		}
		r := byDriver[name]
		d := &decoder{}
		errs[name] = d
		p.Line(0, "%s: %s  (%s)", p.Bold(name), model.Plural(detail[int](d, r, "devices"), "device", "devices"), model.Plural(detail[int](d, r, "pools"), "pool", "pools"))
	}
	for _, name := range slices.Sorted(maps.Keys(byDriver)) {
		for i, f := range findingsOf(byDriver[name]) {
			if len(owners[name][i]) == 0 {
				others = append(others, other{name, f})
			}
		}
	}
	if len(others) > 0 || hasErrs(errs) {
		p.Blank()
		p.Line(0, "%s", p.Bold("Other notes"))
		for _, o := range others {
			printFinding(p, 2, o.f, o.subject, nil)
		}
		for _, name := range slices.Sorted(maps.Keys(errs)) {
			errs[name].flush(p, 2)
		}
	}
}

// hasAttrs reports whether --attributes was given: the inspector leaves
// "attributes" out of Details otherwise.
func hasAttrs(r model.Result) bool {
	_, ok := r.Details["attributes"]
	return ok
}

func hasErrs(m map[string]*decoder) bool {
	for _, d := range m {
		if len(d.errs) > 0 {
			return true
		}
	}
	return false
}
