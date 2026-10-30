package inspect

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/render"
)

type topoKey struct{ node, numa, pcie string }

type topoGroup struct {
	numa, pcie       []string
	numaKeys, pcieKs map[string]bool
	drivers          map[string]int
	devices          []string
	reasons          map[string]model.Finding
}

func join(v []string) string {
	if len(v) == 0 {
		return "-"
	}
	return strings.Join(v, ",")
}

func keysOrDash(m map[string]bool) string { return join(slices.Sorted(maps.Keys(m))) }

// Topology groups current-generation devices by node, NUMA set and PCIe-root
// set. With compact, device names are left out.
func Topology(compact bool) Inspector {
	cols := []render.Column{{Header: "NODE", Min: 8}, {Header: "NUMA", Min: 4}, {Header: "PCIE ROOT", Min: 9}, {Header: "DRIVERS", Min: 10}}
	if !compact {
		cols = append(cols, render.Column{Header: "DEVICES", Min: 10})
	}
	cols = append(cols, render.Column{Header: "NOTE", Min: 4, Truncate: render.TruncateTail})
	return Inspector{
		Spec: Spec{
			Columns: cols,
			Row: func(r model.Result) []string {
				row := []string{str(r, "node"), join(r.Details["numa"].([]string)), join(r.Details["pcieRoot"].([]string)), kvInt(r.Details["drivers"].(map[string]int))}
				if !compact {
					names, _ := r.Details["devices"].([]string)
					row = append(row, strings.Join(names, ","))
				}
				return append(row, dash(str(r, "reason")))
			},
		},
		Run: func(snap model.Snapshot) []model.Result {
			const check = "inspect.topology"
			if res, ok := unavailable(snap, check); ok {
				return res
			}
			groups := map[topoKey]*topoGroup{}
			cur, sel := current(snap)
			pcieExpected := model.PoolsWithPCIeRoot(cur)
			for _, s := range cur {
				pool := model.PoolKey{s.Spec.Driver, s.Spec.Pool.Name}
				for _, d := range s.Spec.Devices {
					top, err := model.TopologyOf(d, s.Spec.Driver)
					reasons := map[string]model.Finding{}
					add := func(k model.Kind, text string, items ...string) {
						reasons[text] = model.Finding{Status: model.StatusWarn, Kind: k, Text: text, Items: items}
					}
					if err != nil {
						add(model.KindInvalid, "invalid attribute: "+err.Error())
					}
					for _, x := range []struct {
						name string
						r    model.Resolved
					}{{"numa", top.NUMA}, {"pcieRoot", top.PCIeRoot}} {
						if len(x.r.Values) == 0 && err == nil && (x.name != "pcieRoot" || pcieExpected[pool]) {
							add(model.KindAttrMissing, "missing attribute: "+x.name, x.name)
						}
						if x.r.Conflict {
							add(model.KindTopologyConflict, fmt.Sprintf("conflicting %s attributes: %s differs from another key at the same tier", x.name, x.r.Key))
						}
					}
					if sel[poolKey{s.Spec.Driver, s.Spec.Pool.Name}].Incomplete {
						add(model.KindIncomplete, incompleteReason(s.Spec.Pool.Name), s.Spec.Pool.Name)
					}
					k := topoKey{model.DeviceNode(s, d).String(), join(top.NUMA.Values), join(top.PCIeRoot.Values)}
					g := groups[k]
					if g == nil {
						g = &topoGroup{numa: top.NUMA.Values, pcie: top.PCIeRoot.Values, numaKeys: map[string]bool{}, pcieKs: map[string]bool{}, drivers: map[string]int{}, reasons: map[string]model.Finding{}}
						groups[k] = g
					}
					if top.NUMA.Key != "" {
						g.numaKeys[top.NUMA.Key] = true
					}
					if top.PCIeRoot.Key != "" {
						g.pcieKs[top.PCIeRoot.Key] = true
					}
					g.drivers[s.Spec.Driver]++
					g.devices = append(g.devices, d.Name)
					maps.Copy(g.reasons, reasons)
				}
			}
			keys := slices.SortedFunc(maps.Keys(groups), func(a, b topoKey) int {
				return cmp.Or(cmp.Compare(a.node, b.node), cmp.Compare(a.numa, b.numa), cmp.Compare(a.pcie, b.pcie))
			})
			out := make([]model.Result, 0, len(keys))
			for _, k := range keys {
				g := groups[k]
				slices.Sort(g.devices)
				det := map[string]any{
					"node": k.node, "numa": orEmpty(g.numa), "pcieRoot": orEmpty(g.pcie),
					"numaKey": keysOrDash(g.numaKeys), "pcieKey": keysOrDash(g.pcieKs),
					"drivers": g.drivers, "deviceCount": len(g.devices),
				}
				if !compact {
					det["devices"] = g.devices
				}
				status := model.StatusPass
				var findings []model.Finding
				if len(g.reasons) > 0 {
					status = model.StatusWarn
					texts := slices.Sorted(maps.Keys(g.reasons))
					det["reason"] = strings.Join(texts, ", ")
					for _, text := range texts {
						findings = append(findings, g.reasons[text])
					}
				}
				out = append(out, model.Result{Check: check, Status: status,
					Subject: fmt.Sprintf("%s/numa=%s/pcieRoot=%s", k.node, k.numa, k.pcie), Details: det, Findings: findings})
			}
			return out
		},
	}
}

func orEmpty(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

// kvInt renders counts as "k=n,k=n" in key order.
func kvInt(m map[string]int) string {
	parts := make([]string, 0, len(m))
	for _, k := range slices.Sorted(maps.Keys(m)) {
		parts = append(parts, fmt.Sprintf("%s=%d", k, m[k]))
	}
	return strings.Join(parts, ",")
}
