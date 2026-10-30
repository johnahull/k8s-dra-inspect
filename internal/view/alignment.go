package view

import (
	"slices"
	"strings"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/render"
)

// Alignment prints each allocated claim with its devices and placement, then
// one sentence per finding that names the devices and NUMA sets involved.
func Alignment(p *render.Printer, snap model.Snapshot, results []model.Result) {
	eachResult(p, "Alignment Check", results, "no allocated claims to check", func(r model.Result) {
		title := r.Subject
		if m := strs(r.Details["matched"]); slices.Contains(m, "claim") && slices.Contains(m, "pod") {
			title += " (also matched pod)"
		}
		p.Line(0, "%s", p.Bold(title))
		for _, c := range snap.ResourceClaims {
			if c.Namespace+"/"+c.Name != r.Subject || c.Status.Allocation == nil {
				continue
			}
			for _, a := range c.Status.Allocation.Devices.Results {
				id := a.Driver + "/" + a.Device
				dev, _, ok := model.FindDevice(snap.ResourceSlices, a.Driver, a.Pool, a.Device)
				if !ok {
					p.Line(2, "request %s: %s", a.Request, id)
					continue
				}
				numa, root := topo(dev, a.Driver)
				p.Line(2, "request %s: %s%s  NUMA %s  pcieRoot %s", a.Request, id, hardware(dev, a.Driver), orUnknown(numa), orUnknown(root))
			}
		}
		printed := alignedLines(p, r)
		printed += notes(p, 2, r, func(f model.Finding) string { return spellOut(r, f) })
		if note, _ := asMap(r.Details["pcieRoot"])["note"].(string); note != "" {
			vals, _ := asMap(r.Details["pcieRoot"])["valuesByDevice"].(map[string][]string)
			p.Note(2, render.Info, note+": "+bySet(vals, "pcieRoot "))
			printed++
		}
		if printed == 0 && r.Status == model.StatusPass {
			p.Note(2, model.StatusPass, "no placement problems found")
		}
	})
}

// alignedLines prints a check mark for each satisfied constraint and for
// NUMA / PCIe-root placement that no constraint covers but that lines up.
func alignedLines(p *render.Printer, r model.Result) int {
	n := 0
	cons, _ := r.Details["constraints"].([]map[string]any)
	for _, c := range cons {
		if c["status"] == model.StatusPass {
			attr, _ := c["attribute"].(string)
			p.Note(2, model.StatusPass, "matchAttribute "+attr+" satisfied: common value "+numSet(strs(c["intersection"])))
			n++
		}
	}
	for _, k := range []struct{ det, label, prefix string }{{"numa", "numaNode", "NUMA "}, {"pcieRoot", "pcieRoot", ""}} {
		d := asMap(r.Details[k.det])
		if d["aligned"] == true && d["constrained"] == false && d["key"] != "" && len(strs(d["intersection"])) > 0 {
			p.Note(2, model.StatusPass, k.label+" aligned: all on "+k.prefix+strings.Join(strs(d["intersection"]), ","))
			n++
		}
	}
	return n
}

// spellOut adds the devices and values behind a finding whose text lacks them.
func spellOut(r model.Result, f model.Finding) string {
	if strings.HasPrefix(f.Text, "devices span NUMA nodes (no common") {
		vals, _ := asMap(r.Details["numa"])["valuesByDevice"].(map[string][]string)
		return f.Text + ": " + bySet(vals, "NUMA ")
	}
	cons, _ := r.Details["constraints"].([]map[string]any)
	for _, c := range cons {
		if attr, _ := c["attribute"].(string); c["status"] == model.StatusFail && strings.HasPrefix(f.Text, "matchAttribute "+attr+" violated: no value") {
			vals, _ := c["valuesByDevice"].(map[string][]string)
			return f.Text + " (" + strings.Join(byDevice(vals, func(d, v string) string { return d + " = " + v }), "; ") + ")"
		}
	}
	return f.Text
}
