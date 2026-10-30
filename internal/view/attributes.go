package view

import (
	"maps"
	"slices"
	"strings"

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

// listValue spaces the commas of a list value "[a,b]" to "[a, b]".
// shortcut: a scalar string that itself looks like "[x,y]" is spaced too; upgrade
// by having the inspector expose the values instead of a joined display string.
func listValue(v string) string {
	if strings.HasPrefix(v, "[") && strings.HasSuffix(v, "]") {
		return strings.ReplaceAll(v, ",", ", ")
	}
	return v
}

// Attributes prints, per driver and device, one "key = value" line per
// attribute (and "capacity key = value" with --include-capacity).
func Attributes(p *render.Printer, snap model.Snapshot, results []model.Result) {
	p.Banner("DRA Device Attributes")
	byDriver := map[string][]model.Result{}
	for _, r := range results {
		if r.Status != model.StatusUnavailable {
			d, _, _ := splitSubject(r.Subject)
			byDriver[d] = append(byDriver[d], r)
		}
	}
	p.Blank()
	if len(byDriver) == 0 {
		p.Line(0, "no devices")
		return
	}
	for i, driver := range slices.Sorted(maps.Keys(byDriver)) {
		if i > 0 {
			p.Blank()
		}
		rs := byDriver[driver]
		p.Line(0, "%s (%s)", p.Bold(driver), plural(len(rs), "device", "devices"))
		for _, r := range rs {
			_, pool, dev := splitSubject(r.Subject)
			p.Line(2, "%s  (pool %s, node %s)", p.Bold(dev), pool, detStr(r, "node"))
			attrs, _ := r.Details["attributes"].(map[string]string)
			caps, _ := r.Details["capacity"].(map[string]string)
			w := 0
			for k := range attrs {
				w = max(w, len(k))
			}
			for k := range caps {
				w = max(w, len("capacity "+k))
			}
			line := func(k, v string) { p.Line(4, "%s = %s", k+strings.Repeat(" ", w-len(k)), listValue(v)) }
			for _, k := range slices.Sorted(maps.Keys(attrs)) {
				line(k, attrs[k])
			}
			for _, k := range slices.Sorted(maps.Keys(caps)) {
				line("capacity "+k, caps[k])
			}
			if reason := detStr(r, "reason"); reason != "" {
				p.Note(4, r.Status, reason)
			}
		}
	}
}
