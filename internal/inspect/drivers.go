package inspect

import (
	"maps"
	"slices"
	"strings"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/render"
)

// Drivers summarizes pools, nodes, and devices per driver. With attributes
// it also lists the union of attribute keys.
func Drivers(attributes bool) Inspector {
	cols := []render.Column{{Header: "DRIVER", Min: 10}, {Header: "POOLS", Min: 5}, {Header: "NODES", Min: 5}, {Header: "DEVICES", Min: 7}}
	if attributes {
		cols = append(cols, render.Column{Header: "ATTRIBUTES", Min: 10})
	}
	return Inspector{
		Spec: Spec{
			Columns: cols,
			Row: func(r model.Result) []string {
				row := []string{r.Subject, str(r, "pools"), str(r, "nodes"), str(r, "devices")}
				if attributes {
					keys, _ := r.Details["attributes"].([]string)
					row = append(row, strings.Join(keys, ", "))
				}
				return row
			},
		},
		Run: func(snap model.Snapshot) []model.Result {
			const check = "inspect.drivers"
			if res, ok := unavailable(snap, check); ok {
				return res
			}
			type agg struct {
				pools, nodes map[string]bool
				devices      int
				attrs        map[string]bool
				placeholders map[string]bool // (all nodes)/(selector): never counted as nodes
				incomplete   []string
			}
			byDriver := map[string]*agg{}
			cur, sel := current(snap)
			for _, s := range cur {
				a := byDriver[s.Spec.Driver]
				if a == nil {
					a = &agg{pools: map[string]bool{}, nodes: map[string]bool{}, attrs: map[string]bool{}, placeholders: map[string]bool{}}
					byDriver[s.Spec.Driver] = a
				}
				if !a.pools[s.Spec.Pool.Name] && sel[poolKey{s.Spec.Driver, s.Spec.Pool.Name}].Incomplete {
					a.incomplete = append(a.incomplete, s.Spec.Pool.Name)
				}
				a.pools[s.Spec.Pool.Name] = true
				for _, r := range sliceNodes(s) {
					if r.Name != "" {
						a.nodes[r.Name] = true
					} else {
						a.placeholders[r.Label] = true
					}
				}
				a.devices += len(s.Spec.Devices)
				for _, d := range s.Spec.Devices {
					for k := range d.Attributes {
						a.attrs[string(k)] = true
					}
				}
			}
			names := make([]string, 0, len(byDriver))
			for n := range byDriver {
				names = append(names, n)
			}
			slices.Sort(names)
			out := make([]model.Result, 0, len(names))
			for _, n := range names {
				a := byDriver[n]
				d := map[string]any{"pools": len(a.pools), "nodes": len(a.nodes), "devices": a.devices}
				status := model.StatusPass
				if len(a.incomplete) > 0 {
					slices.Sort(a.incomplete)
					status = model.StatusWarn
					d["incompletePools"] = a.incomplete
					d["reason"] = incompleteReason(a.incomplete...)
				}
				if len(a.placeholders) > 0 {
					d["nodeSelection"] = slices.Sorted(maps.Keys(a.placeholders))
				}
				if attributes {
					keys := make([]string, 0, len(a.attrs))
					for k := range a.attrs {
						keys = append(keys, k)
					}
					slices.Sort(keys)
					d["attributes"] = keys
				}
				out = append(out, model.Result{Check: check, Status: status, Subject: n, Details: d})
			}
			return out
		},
	}
}
