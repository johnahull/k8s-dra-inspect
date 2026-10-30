package view

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	resourcev1 "k8s.io/api/resource/v1"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/render"
)

// alloc is one allocated device of a claim, including any share capacity.
type alloc struct {
	request, driver, pool, device string
	consumed                      map[string]string
}

func publishedCapacity(dev resourcev1.Device, driver, name string) (resourcev1.DeviceCapacity, bool) {
	if capacity, ok := dev.Capacity[resourcev1.QualifiedName(name)]; ok {
		return capacity, true
	}
	domain, bare, qualified := strings.Cut(name, "/")
	if qualified && domain == driver {
		capacity, ok := dev.Capacity[resourcev1.QualifiedName(bare)]
		return capacity, ok
	}
	return resourcev1.DeviceCapacity{}, false
}

func capacityLines(dev resourcev1.Device, driver string, consumed map[string]string) []string {
	if len(consumed) == 0 {
		return nil
	}
	// ResourceSlices and ResourceClaims are collected independently, so this
	// compares the values observed in one snapshot on a best-effort basis.
	var out []string
	for _, name := range slices.Sorted(maps.Keys(consumed)) {
		value := consumed[name]
		if published, ok := publishedCapacity(dev, driver, name); ok {
			out = append(out, fmt.Sprintf("capacity %s: %s of %s consumed", name, value, published.Value.String()))
			continue
		}
		out = append(out, fmt.Sprintf("capacity %s: %s consumed (not published)", name, value))
	}
	return out
}

// printAllocs prints each request name on its own line (once for consecutive
// devices of the same request), then per device an indented line with the
// device, its bus ID, product and driver, capacity usage, and NUMA/PCIe root.
// The node line is added when withNode. Consumed capacity is still shown when
// the device is missing or slices were not collected.
func printAllocs(p *render.Printer, indent int, allocs []alloc, devs model.DeviceIndex, withNode bool, missing func(a alloc)) {
	prev, first := "", true
	for _, a := range allocs {
		if first || a.request != prev {
			p.Line(indent, "%s", a.request)
			prev, first = a.request, false
		}
		var dev resourcev1.Device
		var s *resourcev1.ResourceSlice
		ok := false
		if devs != nil {
			dev, s, ok = devs.Find(a.driver, a.pool, a.device)
		}
		printCapacity := func(d resourcev1.Device) {
			for _, line := range capacityLines(d, a.driver, a.consumed) {
				p.Line(indent+2, "%s", line)
			}
		}
		switch {
		case devs == nil:
			p.Line(indent+2, "%s  %s", a.device, a.driver)
			printCapacity(resourcev1.Device{})
		case !ok:
			missing(a)
			printCapacity(resourcev1.Device{})
		default:
			p.Line(indent+2, "%s%s  %s", a.device, hardware(dev, a.driver), a.driver)
			printCapacity(dev)
			numa, root := topo(dev, a.driver)
			p.Line(indent+2, "NUMA %s  pcieRoot %s", orUnknown(numa), orUnknown(root))
			if withNode {
				p.Line(indent+2, "node %s", model.DeviceNode(*s, dev))
			}
		}
	}
}

// Alignment prints each allocated claim with its devices and placement, then
// one sentence per finding; a sentence that names devices by NUMA set or by
// attribute value puts one group on each indented line.
func Alignment(p *render.Printer, snap model.Snapshot, results []model.Result) {
	claims := map[string]*resourcev1.ResourceClaim{}
	for i := range snap.ResourceClaims {
		c := &snap.ResourceClaims[i]
		if _, dup := claims[c.Namespace+"/"+c.Name]; !dup {
			claims[c.Namespace+"/"+c.Name] = c
		}
	}
	devs := model.NewDeviceIndex(snap.ResourceSlices)
	eachResult(p, "Alignment Check", results, "no allocated claims to check", func(r model.Result, d *decoder) {
		title := r.Subject
		if m := detail[[]string](d, r, "matched"); slices.Contains(m, "claim") && slices.Contains(m, "pod") {
			title += " (also matched pod)"
		}
		p.Line(0, "%s", p.Bold(title))
		if c := claims[r.Subject]; c != nil && c.Status.Allocation != nil {
			var as []alloc
			for _, a := range c.Status.Allocation.Devices.Results {
				as = append(as, alloc{request: a.Request, driver: a.Driver, pool: a.Pool, device: a.Device})
			}
			printAllocs(p, 2, as, devs, false, func(a alloc) { p.Line(4, "%s  %s", a.device, a.driver) })
		}
		printed := alignedLines(p, r, d)
		printed += notes(p, 2, r, func(f model.Finding) []group { return alignGroups(d, r, f) })
		if pc := detail[map[string]any](d, r, "pcieRoot"); pc != nil {
			if note := field[string](d, "pcieRoot", pc, "note"); note != "" {
				p.Note(2, render.Info, note)
				for _, g := range bySet(field[map[string][]string](d, "pcieRoot", pc, "valuesByDevice"), "pcieRoot ") {
					p.List(6, g.label, g.items)
				}
				printed++
			}
		}
		if printed == 0 && r.Status == model.StatusPass {
			p.Note(2, model.StatusPass, "no placement problems found")
		}
	})
}

// alignedLines prints a check mark for each satisfied constraint and for
// NUMA / PCIe-root placement that no constraint covers but that lines up.
func alignedLines(p *render.Printer, r model.Result, d *decoder) int {
	n := 0
	for _, c := range detail[[]map[string]any](d, r, "constraints") {
		if field[model.Status](d, "constraints[]", c, "status") == model.StatusPass {
			attr := field[string](d, "constraints[]", c, "attribute")
			p.Note(2, model.StatusPass, "matchAttribute "+attr+" satisfied: common value "+numSet(field[[]string](d, "constraints[]", c, "intersection")))
			n++
		}
	}
	for _, k := range []struct{ det, label, prefix string }{{"numa", "numaNode", "NUMA "}, {"pcieRoot", "pcieRoot", ""}} {
		m := detail[map[string]any](d, r, k.det)
		if m == nil {
			continue
		}
		inter := field[[]string](d, k.det, m, "intersection")
		if field[bool](d, k.det, m, "aligned") && !field[bool](d, k.det, m, "constrained") && field[string](d, k.det, m, "key") != "" && len(inter) > 0 {
			p.Note(2, model.StatusPass, k.label+" aligned: all on "+k.prefix+strings.Join(inter, ","))
			n++
		}
	}
	return n
}

// alignGroups lists the devices behind a finding by the value they have:
// NUMA nodes for a NUMA split, the attribute's values for a violated
// constraint. Other findings have no groups.
func alignGroups(d *decoder, r model.Result, f model.Finding) []group {
	switch f.Kind {
	case model.KindNUMASplit:
		numa := detail[map[string]any](d, r, "numa")
		return bySet(field[map[string][]string](d, "numa", numa, "valuesByDevice"), "NUMA ")
	case model.KindConstraintViolated:
		if len(f.Items) != 1 {
			return nil
		}
		for _, c := range detail[[]map[string]any](d, r, "constraints") {
			if field[string](d, "constraints[]", c, "attribute") == f.Items[0] && field[model.Status](d, "constraints[]", c, "status") == model.StatusFail {
				name := f.Items[0][strings.LastIndex(f.Items[0], "/")+1:]
				return bySet(field[map[string][]string](d, "constraints[]", c, "valuesByDevice"), name+" ")
			}
		}
	}
	return nil
}
