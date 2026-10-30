package view

import (
	"strings"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/render"
)

// consumers words who uses an allocated claim; podsDown and vmiDown say the
// collection failed, so the consumers are unknown rather than none.
func consumers(r model.Result, d *decoder, podsDown, vmiDown bool) string {
	used := detail[[]string](d, r, "usedBy")
	var s string
	switch {
	case podsDown:
		return "pods unavailable"
	case len(used) == 0:
		return "no pods"
	case len(used) == 1:
		s = "pod " + used[0]
	default:
		s = "pods " + strings.Join(used, ", ")
	}
	if v := detStr(r, "vmi"); v != "" && !vmiDown {
		s += " (VM " + v + ")"
	}
	return s
}

// Claims prints each ResourceClaim: who uses it, then per request its
// devices with hardware, NUMA node, PCIe root and node on indented lines. A
// failed pods or VMI collection is said once, above the claims.
func Claims(p *render.Printer, snap model.Snapshot, results []model.Result) {
	p.Banner("ResourceClaims")
	var devs model.DeviceIndex
	if snap.Collection["resourceslices"].State == model.StateOK {
		devs = model.NewDeviceIndex(snap.ResourceSlices)
	}
	podsDown := snap.Collection["pods"].State != model.StateOK
	vmiDown := snap.Collection["virtualmachineinstances"].State != model.StateOK
	var shown []model.Result
	for _, r := range results {
		if r.Status != model.StatusUnavailable {
			shown = append(shown, r)
		}
	}
	if len(shown) > 0 && (podsDown || vmiDown) {
		p.Blank()
		for _, c := range []struct {
			down      bool
			key, noun string
		}{{podsDown, "usedByState", "pods"}, {vmiDown, "vmi", "vmi"}} {
			if !c.down {
				continue
			}
			for _, r := range shown {
				if v := detStr(r, c.key); v != "" {
					p.Note(0, model.StatusUnavailable, c.noun+" "+v)
					break
				}
			}
		}
	}
	for _, r := range shown {
		p.Blank()
		var d decoder
		if r.Details["allocated"] != true {
			p.Line(0, "%s → pending (not allocated)", p.Bold(r.Subject))
			continue
		}
		p.Line(0, "%s → %s", p.Bold(r.Subject), consumers(r, &d, podsDown, vmiDown))
		var as []alloc
		for _, m := range detail[[]map[string]string](&d, r, "results") {
			as = append(as, alloc{m["request"], m["driver"], m["pool"], m["device"]})
		}
		printAllocs(p, 2, as, devs, true, func(a alloc) {
			p.Note(4, r.Status, a.driver+"/"+a.device+" not found in ResourceSlices (pool "+a.pool+")")
		})
		d.flush(p, 2)
	}
	if len(shown) == 0 {
		p.Blank()
		p.Line(0, "no ResourceClaims")
	}
}
