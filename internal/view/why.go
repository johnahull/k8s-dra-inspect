package view

import (
	"fmt"

	"github.com/johnahull/k8s-dra-inspect/internal/analyze"
	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/render"
)

// Why prints the allocator verdict and the request funnel for each target.
func Why(p *render.Printer, _ model.Snapshot, results []model.Result) {
	p.Banner("Allocation Explanation")
	for i, r := range results {
		if i > 0 {
			p.Blank()
		}
		p.Line(0, "%s  %s", p.Bold(r.Subject), r.Status)
		if reason := whyDetail(r, "reason"); reason != "" {
			p.Note(2, r.Status, reason)
			continue
		}
		e, ok := r.Details["explanation"].(*analyze.Explanation)
		if !ok {
			p.Line(2, "%s", whyDetail(r, "summary"))
			continue
		}
		p.Line(2, "%s", e.Summary)
		if e.Pod != "" {
			p.Line(2, "Pod: %s", e.Pod)
		}
		if len(e.Allocation) > 0 {
			p.Line(2, "Allocated devices")
			for _, device := range e.Allocation {
				p.Line(4, "%s  %s/%s/%s", device.Request, device.Driver, device.Pool, device.Device)
			}
		}
		if e.PodScheduled != "" || len(e.Events) > 0 {
			p.Line(2, "Scheduler")
			if e.PodScheduled != "" {
				p.Line(4, "Pod scheduling: %s", e.PodScheduled)
			}
			for _, event := range e.Events {
				p.Line(4, "Warning event: %s", event)
			}
		}
		if len(e.CandidateNodes) > 0 {
			p.List(2, "Candidate nodes", e.CandidateNodes)
		}
		if len(e.ExcludedNodes) > 0 {
			p.Line(2, "Excluded nodes")
			for _, excluded := range e.ExcludedNodes {
				detail := fmt.Sprintf("%d: %s", excluded.Count, excluded.Reason)
				if len(excluded.Examples) > 0 {
					detail += " (" + fmt.Sprint(excluded.Examples) + ")"
				}
				p.Note(4, model.StatusFail, detail)
			}
		}
		for _, req := range e.Requests {
			p.Line(2, "Request %s", p.Bold(req.Name))
			for _, alt := range req.Alternatives {
				detail := alt.Problem
				if detail == "" {
					detail = alt.Error
				}
				if detail == "" {
					detail = fmt.Sprintf("%d of %d needed; best node %s", alt.BestCount, alt.Need, alt.BestNode)
				}
				p.Line(4, "%s: %s", alt.Request, detail)
				for _, stage := range alt.Stages {
					p.Line(6, "%s: %d remain", stage.Name, stage.Remaining)
					for _, drop := range stage.Dropped {
						p.Line(8, "%s: %d", drop.Reason, drop.Count)
					}
				}
			}
		}
		for _, warning := range e.PoolWarnings {
			p.Note(2, model.StatusWarn, warning)
		}
		if a := e.Allocator; a != nil {
			if len(a.Nodes) > 0 {
				p.Note(2, model.StatusPass, fmt.Sprintf("allocator found devices on %d of %d candidate nodes: %s", len(a.Nodes), a.NodesTried, fmt.Sprint(a.Nodes)))
			} else if a.Error != "" {
				p.Note(2, model.StatusFail, "allocator rejects the claim: "+a.Error)
			} else {
				p.Note(2, model.StatusFail, fmt.Sprintf("allocator found no allocation on %d candidate nodes", a.NodesTried))
			}
			for _, err := range a.NodeErrors {
				p.Line(4, "%s", err)
			}
		}
		for _, hint := range e.Hints {
			p.Note(2, model.StatusWarn, hint)
		}
	}
}

func whyDetail(r model.Result, key string) string {
	if v, ok := r.Details[key].(string); ok {
		return v
	}
	return ""
}
