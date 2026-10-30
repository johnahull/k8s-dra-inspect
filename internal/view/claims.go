package view

import (
	"strings"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/render"
)

// consumers words who uses an allocated claim.
func consumers(r model.Result) string {
	used, _ := r.Details["usedBy"].([]string)
	var s string
	switch {
	case detStr(r, "usedByState") != "":
		return "pods unavailable"
	case len(used) == 0:
		return "no pods"
	case len(used) == 1:
		s = "pod " + used[0]
	default:
		s = "pods " + strings.Join(used, ", ")
	}
	if v := detStr(r, "vmi"); v != "" && !strings.HasPrefix(v, "unavailable") {
		s += " (VM " + v + ")"
	}
	return s
}

// Claims prints each ResourceClaim: who uses it, then one line per allocated
// device with its hardware, NUMA node, PCIe root and node.
func Claims(p *render.Printer, snap model.Snapshot, results []model.Result) {
	p.Banner("ResourceClaims")
	slicesOK := snap.Collection["resourceslices"].State == model.StateOK
	n := 0
	for _, r := range results {
		if r.Status == model.StatusUnavailable {
			continue
		}
		p.Blank()
		n++
		if r.Details["allocated"] != true {
			p.Line(0, "%s → pending (not allocated)", p.Bold(r.Subject))
			continue
		}
		p.Line(0, "%s → %s", p.Bold(r.Subject), consumers(r))
		devs, _ := r.Details["results"].([]map[string]string)
		for _, d := range devs {
			id := d["driver"] + "/" + d["device"]
			dev, s, ok := model.FindDevice(snap.ResourceSlices, d["driver"], d["pool"], d["device"])
			switch {
			case !slicesOK:
				p.Line(2, "request %s: %s", d["request"], id)
			case !ok:
				p.Line(2, "%s request %s: %s not found in ResourceSlices (pool %s)", p.Marker(model.StatusFail), d["request"], id, d["pool"])
			default:
				numa, root := topo(dev, d["driver"])
				p.Line(2, "request %s: %s%s  NUMA %s  pcieRoot %s  node %s", d["request"], id, hardware(dev, d["driver"]),
					orUnknown(numa), orUnknown(root), model.DeviceNode(*s, dev))
			}
		}
		if v := detStr(r, "vmi"); strings.HasPrefix(v, "unavailable") {
			p.Line(2, "%s vmi %s", p.Marker(model.StatusUnavailable), v)
		}
		if v := detStr(r, "usedByState"); strings.HasPrefix(v, "unavailable") {
			p.Line(2, "%s pods %s", p.Marker(model.StatusUnavailable), v)
		}
	}
	if n == 0 {
		p.Blank()
		p.Line(0, "no ResourceClaims")
	}
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}
