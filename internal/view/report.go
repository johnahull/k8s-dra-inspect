package view

import (
	"slices"
	"strings"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/render"
)

// rank orders statuses for a member: error, fail, unavailable, warn, pass.
var rank = map[model.Status]int{
	model.StatusError: 4, model.StatusFail: 3, model.StatusUnavailable: 2, model.StatusWarn: 1, model.StatusPass: 0,
}

// Report prints a titled report: one summary line counting each member check
// once by its worst status, then the non-pass findings per member and the
// adapter rows. Members (in table order) with no results count as ok.
func Report(title string, members []string) func(*render.Printer, model.Snapshot, []model.Result) {
	return func(p *render.Printer, _ model.Snapshot, results []model.Result) {
		order := slices.Clone(members)
		worst := map[string]model.Status{}
		for _, m := range members {
			worst[m] = model.StatusPass
		}
		byMember := map[string][]model.Result{}
		for _, r := range results {
			if _, ok := worst[r.Check]; !ok {
				order = append(order, r.Check)
				worst[r.Check] = model.StatusPass
			}
			if rank[r.Status] > rank[worst[r.Check]] {
				worst[r.Check] = r.Status
			}
			byMember[r.Check] = append(byMember[r.Check], r)
		}
		n := map[model.Status]int{}
		for _, m := range order {
			n[worst[m]]++
		}
		p.Banner(title)
		p.Blank()
		p.Line(0, "Summary: %s: %d ok, %s, %s, %d unavailable, %s", model.Plural(len(order), "check", "checks"),
			n[model.StatusPass], model.Plural(n[model.StatusWarn], "warning", "warnings"), model.Plural(n[model.StatusFail], "failure", "failures"),
			n[model.StatusUnavailable], model.Plural(n[model.StatusError], "error", "errors"))
		for _, m := range order {
			if strings.HasPrefix(m, "adapter.") {
				continue
			}
			// UNAVAILABLE results are already printed above the view.
			var rest []model.Result
			for _, r := range byMember[m] {
				if r.Status != model.StatusPass && r.Status != model.StatusUnavailable {
					rest = append(rest, r)
				}
			}
			if len(rest) == 0 {
				continue
			}
			p.Blank()
			p.Line(0, "%s", p.Bold(m))
			for _, r := range rest {
				var d decoder
				subject := subjectOf(r, &d)
				for _, f := range findingsOf(r) {
					printFinding(p, 2, reportFinding(f), subject, nil)
				}
				d.flush(p, 2)
			}
		}
		var adapters []model.Result
		for _, m := range order {
			if strings.HasPrefix(m, "adapter.") {
				adapters = append(adapters, byMember[m]...)
			}
		}
		if len(adapters) > 0 {
			p.Blank()
			p.Line(0, "%s", p.Bold("adapters"))
			for _, r := range adapters {
				p.Note(2, r.Status, r.Check+": "+reasonOf(r))
			}
		}
		p.Blank()
		p.Line(0, "Details: run the individual check or inspect commands, or use -o json.")
	}
}

// reportFinding is f as the report words it. An inspector's "missing
// attribute" is said as what it means for the group the subject names.
func reportFinding(f model.Finding) model.Finding {
	if f.Kind == model.KindAttrMissing && len(f.Items) == 1 {
		f.Text = "no " + attrLabel(f.Items[0]) + " published"
	}
	return f
}

// attrLabel is the name a topology attribute goes by in text.
func attrLabel(attr string) string {
	if attr == "numa" {
		return "NUMA node"
	}
	return attr
}
