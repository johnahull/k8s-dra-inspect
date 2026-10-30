package view

import (
	"cmp"
	"maps"
	"slices"
	"strings"

	resourcev1 "k8s.io/api/resource/v1"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/render"
)

// splitSubject cuts "driver/pool/device"; a pool name may itself contain "/".
func splitSubject(s string) (driver, pool, device string) {
	driver, rest, _ := strings.Cut(s, "/")
	i := strings.LastIndex(rest, "/")
	if i < 0 {
		return driver, "", rest
	}
	return driver, rest[:i], rest[i+1:]
}

// maxKeyPad is the widest attribute key the "=" column is aligned for.
const maxKeyPad = 56

// Attributes prints, per node, driver and device, one "key = value" line per
// attribute (and "capacity key = value" with --include-capacity). A device
// heading names its pool only when the pool is not called like the node.
func Attributes(p *render.Printer, snap model.Snapshot, results []model.Result) {
	p.Banner("DRA Device Attributes")
	devs := model.NewDeviceIndex(snap.ResourceSlices)
	byNode := map[string]map[string][]model.Result{} // node -> driver -> devices
	for _, r := range results {
		if r.Status == model.StatusUnavailable {
			continue
		}
		driver, _, _ := splitSubject(r.Subject)
		node := detStr(r, "node")
		if byNode[node] == nil {
			byNode[node] = map[string][]model.Result{}
		}
		byNode[node][driver] = append(byNode[node][driver], r)
	}
	p.Blank()
	if len(byNode) == 0 {
		p.Line(0, "no devices")
		return
	}
	for i, node := range slices.SortedFunc(maps.Keys(byNode), cmpNode) {
		if i > 0 {
			p.Blank()
		}
		p.Line(0, "%s", nodeTitle(p, node))
		for _, driver := range slices.Sorted(maps.Keys(byNode[node])) {
			rs := byNode[node][driver]
			slices.SortStableFunc(rs, func(a, b model.Result) int {
				_, pa, da := splitSubject(a.Subject)
				_, pb, db := splitSubject(b.Subject)
				return cmp.Or(cmp.Compare(da, db), cmp.Compare(pa, pb))
			})
			p.Line(2, "%s (%s)", p.Bold(driver), model.Plural(len(rs), "device", "devices"))
			for _, r := range rs {
				attributeBlock(p, devs, node, driver, r)
			}
		}
	}
}

func attributeBlock(p *render.Printer, devs model.DeviceIndex, node, driver string, r model.Result) {
	_, pool, dev := splitSubject(r.Subject)
	if pool != node {
		p.Line(4, "%s  (pool %s)", p.Bold(dev), pool)
	} else {
		p.Line(4, "%s", p.Bold(dev))
	}
	var d decoder
	attrs := detail[map[string]string](&d, r, "attributes")
	caps := detail[map[string]string](&d, r, "capacity")
	device, _, found := devs.Find(driver, pool, dev)
	w := 0
	// A key longer than maxKeyPad is printed whole but does not widen the others.
	for k := range attrs {
		if len(k) <= maxKeyPad {
			w = max(w, len(k))
		}
	}
	for k := range caps {
		if len("capacity "+k) <= maxKeyPad {
			w = max(w, len("capacity "+k))
		}
	}
	line := func(k, v string) { p.Line(6, "%s = %s", k+strings.Repeat(" ", max(w-len(k), 0)), v) }
	for _, k := range slices.Sorted(maps.Keys(attrs)) {
		v := attrs[k]
		// A list is spaced from the attribute itself, so a scalar string that
		// merely looks like "[x,y]" is printed as it is.
		if a, ok := device.Attributes[resourcev1.QualifiedName(k)]; found && ok && model.IsListAttr(a) {
			v = "[" + strings.Join(model.AttrSet(a), ", ") + "]"
		}
		line(k, v)
	}
	for _, k := range slices.Sorted(maps.Keys(caps)) {
		line("capacity "+k, caps[k])
	}
	notes(p, 6, r, nil)
	d.flush(p, 6)
}
