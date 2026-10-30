package view

import (
	"maps"
	"slices"
	"strings"

	"github.com/johnahull/k8s-dra-inspect/internal/check"
	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/render"
)

// eachResult prints the banner, then f for every result that is not
// unavailable (those are printed as UNAVAILABLE lines before the view), each
// after a blank line. A pass result without a subject carries only a reason
// ("no shared counters published"): it prints as a neutral note, never a
// check mark, and so does the empty case (none). Details type mismatches that
// f finds are printed as error notes after its output.
func eachResult(p *render.Printer, title string, results []model.Result, none string, f func(r model.Result, d *decoder)) {
	p.Banner(title)
	n := 0
	for _, r := range results {
		if r.Status == model.StatusUnavailable {
			continue
		}
		n++
		p.Blank()
		if r.Subject == "" && r.Status == model.StatusPass {
			p.Note(0, render.Info, reasonOf(r))
			continue
		}
		var d decoder
		f(r, &d)
		d.flush(p, 2)
	}
	if n == 0 {
		p.Blank()
		p.Note(0, render.Info, none)
	}
}

// reasonOf is the result's reason, else its summary.
func reasonOf(r model.Result) string {
	if s := detStr(r, "reason"); s != "" {
		return s
	}
	return detStr(r, "summary")
}

// group is one line of a finding: a key such as "NUMA 0,1" and the devices under it.
type group struct {
	label string
	items []string
}

// findingsOf is r's Findings, or for a non-pass result without any a single
// finding made of its reason.
func findingsOf(r model.Result) []model.Finding {
	if len(r.Findings) == 0 && r.Status != model.StatusPass && reasonOf(r) != "" {
		return []model.Finding{{Status: r.Status, Kind: model.KindOther, Text: reasonOf(r)}}
	}
	return r.Findings
}

// printFinding prints one sentence under a status marker. A finding that
// lists devices (Head and two or more Items) prints its stem, then the
// devices on indented lines; a view that knows better groups (devices by NUMA
// set, say) passes them instead and they replace the plain list. A pass
// Finding is informational (a note on a passing result, such as "cannot
// compare"), so it gets the neutral marker, not a check mark.
func printFinding(p *render.Printer, indent int, f model.Finding, subject string, groups []group) {
	text := f.Text
	list := f.Head != "" && len(f.Items) >= 2
	if list {
		text = f.Head
	}
	if subject != "" {
		text = subject + ": " + text
	}
	st := f.Status
	if st == model.StatusPass {
		st = render.Info
	}
	p.Note(indent, st, text)
	switch {
	case len(groups) > 0:
		for _, g := range groups {
			p.List(indent+4, g.label, g.items)
		}
	case list:
		p.List(indent+4, "", f.Items)
	}
}

// notes prints one sentence per Finding of r, except those of the skipped
// kinds (the view says them another way), and returns how many findings r
// has. groupsFor may be nil.
func notes(p *render.Printer, indent int, r model.Result, groupsFor func(model.Finding) []group, skip ...model.Kind) int {
	fs := findingsOf(r)
	for _, f := range fs {
		if slices.Contains(skip, f.Kind) {
			continue
		}
		var gs []group
		if groupsFor != nil {
			gs = groupsFor(f)
		}
		printFinding(p, indent, f, "", gs)
	}
	return len(fs)
}

// bySet groups devices that share a value set, ordered numerically by
// set: label+"0,1" holds the devices on 0,1. Device names are the last path
// segment of the keys.
func bySet(m map[string][]string, label string) []group {
	byKey := map[string][]string{}
	for d, v := range m {
		k := numSet(v)
		byKey[k] = append(byKey[k], short(d))
	}
	var out []group
	for _, k := range slices.SortedFunc(maps.Keys(byKey), cmpSets) {
		slices.Sort(byKey[k])
		out = append(out, group{label + k, byKey[k]})
	}
	return out
}

// subjectOf names a result the way its check command does, so the report and
// the command agree.
func subjectOf(r model.Result, d *decoder) string {
	switch r.Check {
	case check.NameVFIO:
		return vfioTitle(r)
	case check.NameCPUPinning:
		if n := detStr(r, "nodeName"); n != "" {
			return r.Subject + " (node: " + n + ")"
		}
	case "inspect.topology":
		return topologyPlace(r, d)
	}
	return r.Subject
}

// topologyPlace words an inspect.topology result's group: its node, NUMA set
// and, when known, PCIe roots.
func topologyPlace(r model.Result, d *decoder) string {
	s := detStr(r, "node") + ", NUMA " + orUnknown(numSet(detail[[]string](d, r, "numa")))
	if roots := detail[[]string](d, r, "pcieRoot"); len(roots) > 0 {
		s += ", pcieRoot " + strings.Join(roots, ",")
	}
	return s
}
