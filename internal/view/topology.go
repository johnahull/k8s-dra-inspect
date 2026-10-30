package view

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/render"
)

// driverLabels shortens well-known driver names; any other driver is shown by
// its full name.
var driverLabels = []struct{ prefix, label string }{
	{"gpu.", "gpu"}, {"dra.cpu", "cpu"}, {"dra.memory", "memory"},
	{"dra.net", "nic"}, {"dra.nvme", "nvme"}, {"sriovnetwork.", "nic"},
}

func driverLabel(driver string) string {
	for _, l := range driverLabels {
		if strings.HasPrefix(driver, l.prefix) {
			return l.label
		}
	}
	return driver
}

// groupKey is the key the topology inspector groups by.
type groupKey struct{ node, numa, root string }

type entry struct{ driver, name, hw string }

// leaf is one PCIe-root set: its devices (or, compact, per-label counts).
type leaf struct {
	root    string   // exact set, "" when unknown
	roots   []string // the same set, one per root
	notes   []model.Finding
	devices []entry
	counts  string // compact: "gpu=1 cpu=1"
}

type numaGroup struct {
	numa   string // exact set, "" when unknown
	leaves []*leaf
}

// cmpRoots orders PCIe-root sets: unknown last, then by how many roots, then
// by their values in natural order (pci0000:20 before pci0000:100).
func cmpRoots(a, b *leaf) int {
	if (len(a.roots) == 0) != (len(b.roots) == 0) {
		return cmp.Compare(boolInt(len(a.roots) == 0), boolInt(len(b.roots) == 0))
	}
	return cmp.Or(cmp.Compare(len(a.roots), len(b.roots)), cmpList(a.roots, b.roots))
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// countsOf renders per-label device counts, labels in driver-name order.
func countsOf(byDriver map[string]int) string {
	var order []string
	n := map[string]int{}
	for _, d := range slices.Sorted(maps.Keys(byDriver)) {
		l := driverLabel(d)
		if n[l] == 0 {
			order = append(order, l)
		}
		n[l] += byDriver[d]
	}
	parts := make([]string, len(order))
	for i, l := range order {
		parts[i] = fmt.Sprintf("%s=%d", l, n[l])
	}
	return strings.Join(parts, " ")
}

// Topology prints node, NUMA set, PCIe-root set and one device per line, from
// the topology results plus a snapshot lookup for each device's driver. A
// result without a devices list (--compact) prints per-group counts only.
// NUMA sets are ordered numerically (0,1 then 2,3 then 9 then 10,11), PCIe
// roots by count and then naturally, and unknown branches go last. A device
// without a pcieRoot or NUMA node shows as "unknown" in the tree, so that
// finding is not repeated below it; other findings are listed after the tree.
func Topology(p *render.Printer, snap model.Snapshot, results []model.Result) {
	p.Banner("DRA Topology")

	idx := map[groupKey][]entry{}
	owners := map[string]map[string]map[string]bool{} // node -> device name -> drivers
	cur, _ := model.CurrentSlices(snap)
	for _, s := range cur {
		for _, d := range s.Spec.Devices {
			node := model.DeviceNode(s, d).String()
			top, _ := model.TopologyOf(d, s.Spec.Driver)
			k := groupKey{node, strings.Join(top.NUMA.Values, ","), strings.Join(top.PCIeRoot.Values, ",")}
			idx[k] = append(idx[k], entry{s.Spec.Driver, d.Name, hardware(d, s.Spec.Driver)})
			if owners[node] == nil {
				owners[node] = map[string]map[string]bool{}
			}
			if owners[node][d.Name] == nil {
				owners[node][d.Name] = map[string]bool{}
			}
			owners[node][d.Name][s.Spec.Driver] = true
		}
	}

	var nodes []string
	var decodeErrs []string
	groups := map[string][]*numaGroup{}
	for _, r := range results {
		if r.Status == model.StatusUnavailable {
			continue
		}
		node := detStr(r, "node")
		var d decoder
		numa, root := detail[[]string](&d, r, "numa"), detail[[]string](&d, r, "pcieRoot")
		if groups[node] == nil {
			nodes = append(nodes, node)
		}
		ng := &numaGroup{numa: numSet(numa)}
		if i := slices.IndexFunc(groups[node], func(g *numaGroup) bool { return g.numa == ng.numa }); i >= 0 {
			ng = groups[node][i]
		} else {
			groups[node] = append(groups[node], ng)
		}
		l := &leaf{root: strings.Join(root, ","), roots: root}
		for _, f := range findingsOf(r) {
			if f.Kind != model.KindAttrMissing {
				l.notes = append(l.notes, f)
			}
		}
		// The inspector leaves "devices" out of Details with --compact.
		if _, listed := r.Details["devices"]; listed {
			avail := slices.Clone(idx[groupKey{node, strings.Join(numa, ","), l.root}])
			for _, name := range detail[[]string](&d, r, "devices") {
				e := entry{name: name}
				if i := slices.IndexFunc(avail, func(a entry) bool { return a.name == name }); i >= 0 {
					e = avail[i]
					avail = slices.Delete(avail, i, i+1)
				}
				if len(owners[node][name]) > 1 && e.driver != "" {
					e.name = e.driver + "/" + name
				}
				l.devices = append(l.devices, e)
			}
			slices.SortStableFunc(l.devices, func(a, b entry) int {
				return cmp.Or(cmp.Compare(a.driver, b.driver), cmp.Compare(a.name, b.name))
			})
		} else {
			l.counts = countsOf(detail[map[string]int](&d, r, "drivers"))
		}
		ng.leaves = append(ng.leaves, l)
		decodeErrs = append(decodeErrs, d.errs...)
	}

	p.Blank()
	if len(nodes) == 0 {
		p.Line(0, "no devices")
		return
	}
	for i, node := range nodes {
		if i > 0 {
			p.Blank()
		}
		p.Line(0, "%s %s", p.Bold("Node:"), node)
		slices.SortStableFunc(groups[node], func(a, b *numaGroup) int { return cmpSets(a.numa, b.numa) })
		var notes []string
		for gi, ng := range groups[node] {
			lastN := gi == len(groups[node])-1
			p.Line(0, "%s%s", p.Branch(lastN), p.Bold("NUMA "+orUnknown(ng.numa)))
			slices.SortStableFunc(ng.leaves, cmpRoots)
			for li, l := range ng.leaves {
				lastP := li == len(ng.leaves)-1
				for _, f := range l.notes {
					notes = append(notes, "NUMA "+orUnknown(ng.numa)+", pcieRoot "+orUnknown(l.root)+": "+f.Text)
				}
				head := "pcieRoot " + orUnknown(l.root)
				if l.counts != "" {
					head += "  " + l.counts
				}
				p.Line(0, "%s%s%s", p.Trunk(lastN), p.Branch(lastP), head)
				w := 0
				for _, e := range l.devices {
					w = max(w, utf8.RuneCountInString(driverLabel(e.driver)))
				}
				for di, e := range l.devices {
					lab := driverLabel(e.driver)
					p.Line(0, "%s%s%s%s  %s%s", p.Trunk(lastN), p.Trunk(lastP), p.Branch(di == len(l.devices)-1), lab+strings.Repeat(" ", w-utf8.RuneCountInString(lab)), e.name, e.hw)
				}
			}
		}
		if len(notes) > 0 {
			p.Blank()
		}
		for _, n := range notes {
			p.Note(0, model.StatusWarn, n)
		}
	}
	(&decoder{errs: decodeErrs}).flush(p, 0)
}
