package view

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/render"
)

type deviceRow struct {
	numa, text string
	users      model.Usage
	used       bool
}

// observed counts the slices seen for a pool generation and the count the
// pool says it has.
func observed(snap model.Snapshot, driver, pool string, gen int64) (seen, want int64) {
	for _, s := range snap.ResourceSlices {
		if s.Spec.Driver == driver && s.Spec.Pool.Name == pool && s.Spec.Pool.Generation == gen {
			seen++
			want = max(want, s.Spec.Pool.ResourceSliceCount)
		}
	}
	return seen, want
}

// sliceNote words one stale or incomplete slice result.
func sliceNote(d *decoder, snap model.Snapshot, sel map[model.PoolKey]model.PoolSel, r model.Result) string {
	driver, pool := detStr(r, "driver"), detStr(r, "pool")
	gen := detail[int64](d, r, "generation")
	cur := sel[model.PoolKey{driver, pool}].Gen
	seen, want := observed(snap, driver, pool, gen)
	switch r.Details["state"] {
	case "stale":
		if gen > cur {
			return fmt.Sprintf("slice %s (generation %d) is ignored: newer than current generation %d but incomplete (%d of %d slices observed)", r.Subject, gen, cur, seen, want)
		}
		return fmt.Sprintf("slice %s (generation %d) is stale: pool %s is at generation %d", r.Subject, gen, pool, cur)
	case "incomplete":
		return fmt.Sprintf("slice %s (generation %d): pool %s has %d of %d slices for its newest generation", r.Subject, gen, pool, seen, want)
	}
	return ""
}

// Slices prints, per node and driver, every current device with its NUMA
// node, hardware label and users.
func Slices(p *render.Printer, snap model.Snapshot, results []model.Result) {
	p.Banner("ResourceSlice Hardware Summary")
	usage, sc := model.DeviceUsage(snap)
	cur, sel := model.CurrentSlices(snap)

	rows := map[string]map[string][]deviceRow{} // node -> driver -> devices
	for _, s := range cur {
		for _, d := range s.Spec.Devices {
			node := model.DeviceNode(s, d).String()
			if rows[node] == nil {
				rows[node] = map[string][]deviceRow{}
			}
			numa, _ := topo(d, s.Spec.Driver)
			u, used := usage[model.DeviceKey{Driver: s.Spec.Driver, Pool: s.Spec.Pool.Name, Device: d.Name}]
			rows[node][s.Spec.Driver] = append(rows[node][s.Spec.Driver],
				deviceRow{numa: numa, text: d.Name + hardware(d, s.Spec.Driver), users: u, used: used})
		}
	}
	notes := map[string][]string{} // node -> sentences
	var d decoder
	for _, r := range results {
		if n := sliceNote(&d, snap, sel, r); n != "" {
			node := detStr(r, "node")
			notes[node] = append(notes[node], n)
			if rows[node] == nil {
				rows[node] = map[string][]deviceRow{}
			}
		}
	}

	d.flush(p, 0)
	p.Blank()
	if !sc.Available {
		p.Note(0, model.StatusUnavailable, "usage unavailable: "+sc.Reason)
		p.Blank()
	} else if !sc.PodsAvailable {
		p.Note(0, model.StatusUnavailable, "pods not collected: arrows show claims, not pods")
		p.Blank()
	}
	if len(rows) == 0 {
		p.Line(0, "no ResourceSlices")
		return
	}
	for i, node := range sortedNodes(rows) {
		if i > 0 {
			p.Blank()
		}
		p.Line(0, "%s", nodeTitle(p, node))
		for _, driver := range slices.Sorted(maps.Keys(rows[node])) {
			devs := rows[node][driver]
			slices.SortStableFunc(devs, func(a, b deviceRow) int {
				if c := cmpSets(a.numa, b.numa); c != 0 {
					return c
				}
				return strings.Compare(a.text, b.text)
			})
			p.Line(2, "%s (%s)", p.Bold(driver), counts(devs, sc))
			w := 0
			for _, d := range devs {
				w = max(w, len(d.numa))
			}
			for _, d := range devs {
				p.Line(4, "%s", deviceLine(d, w, sc))
			}
		}
		for _, n := range notes[node] {
			p.Note(2, model.StatusWarn, n)
		}
	}
}

func counts(devs []deviceRow, sc model.UsageScope) string {
	s := model.Plural(len(devs), "device", "devices")
	if !sc.Available {
		return s
	}
	in := 0
	for _, d := range devs {
		if d.used {
			in++
		}
	}
	s += fmt.Sprintf(", %d in use", in)
	if sc.Namespace != "" {
		return s + " (namespace " + sc.Namespace + " only)"
	}
	return fmt.Sprintf("%s, %d free", s, len(devs)-in)
}

func deviceLine(d deviceRow, numaW int, sc model.UsageScope) string {
	var b strings.Builder
	if numaW > 0 {
		if d.numa == "" {
			b.WriteString(strings.Repeat(" ", len("NUMA ")+numaW+2))
		} else {
			b.WriteString("NUMA " + d.numa + strings.Repeat(" ", numaW-len(d.numa)) + "  ")
		}
	}
	b.WriteString(d.text)
	if sc.Available && d.used {
		if sc.PodsAvailable && len(d.users.Pods) > 0 { // without the pod list every arrow is a claim
			b.WriteString("  →" + strings.Join(d.users.Pods, ", "))
		} else {
			b.WriteString("  →claim " + strings.Join(d.users.Claims, ", "))
		}
	}
	return b.String()
}
