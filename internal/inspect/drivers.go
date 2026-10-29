package inspect

import (
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
			}
			byDriver := map[string]*agg{}
			for _, s := range current(snap) {
				a := byDriver[s.Spec.Driver]
				if a == nil {
					a = &agg{map[string]bool{}, map[string]bool{}, 0, map[string]bool{}}
					byDriver[s.Spec.Driver] = a
				}
				a.pools[s.Spec.Pool.Name] = true
				if n := sliceNode(s); n != "" {
					a.nodes[n] = true
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
				if attributes {
					keys := make([]string, 0, len(a.attrs))
					for k := range a.attrs {
						keys = append(keys, k)
					}
					slices.Sort(keys)
					d["attributes"] = keys
				}
				out = append(out, model.Result{Check: check, Status: model.StatusPass, Subject: n, Details: d})
			}
			return out
		},
	}
}
