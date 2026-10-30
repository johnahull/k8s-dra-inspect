package check

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
)

type topoPool struct {
	gen                    int64
	devices, slices        int
	expected               int64
	missingNUMA, missingPC []string
	conflicts, invalid     []string
}

// Topology validates the published slices of each pool's current
// generation: devices without NUMA or PCIe-root attributes, conflicting
// vendor keys, and pools with fewer slices than Pool.ResourceSliceCount all
// warn. One result per driver/pool.
func Topology() Check {
	return func(_ context.Context, snap model.Snapshot) []model.Result {
		if r, ok := need(snap, NameTopology, "resourceslices"); ok {
			return r
		}
		cur, sel := model.CurrentSlices(snap)
		pools := map[model.PoolKey]*topoPool{}
		for _, s := range cur {
			k := model.PoolKey{s.Spec.Driver, s.Spec.Pool.Name}
			p := pools[k]
			if p == nil {
				p = &topoPool{gen: s.Spec.Pool.Generation}
				pools[k] = p
			}
			p.slices++
			p.expected = max(p.expected, s.Spec.Pool.ResourceSliceCount)
			for _, d := range s.Spec.Devices {
				p.devices++
				top, err := model.TopologyOf(d, s.Spec.Driver)
				if err != nil {
					p.invalid = append(p.invalid, err.Error())
					continue
				}
				if len(top.NUMA.Values) == 0 {
					p.missingNUMA = append(p.missingNUMA, d.Name)
				}
				if len(top.PCIeRoot.Values) == 0 {
					p.missingPC = append(p.missingPC, d.Name)
				}
				if top.NUMA.Conflict {
					p.conflicts = append(p.conflicts, "NUMA:"+d.Name)
				}
				if top.PCIeRoot.Conflict {
					p.conflicts = append(p.conflicts, "PCIe root:"+d.Name)
				}
			}
		}
		if len(pools) == 0 {
			return single(NameTopology, model.StatusPass, "", "no ResourceSlices published")
		}
		var out []model.Result
		for _, k := range slices.SortedFunc(maps.Keys(pools), func(a, b model.PoolKey) int { return strings.Compare(a[0]+"/"+a[1], b[0]+"/"+b[1]) }) {
			p := pools[k]
			var fs []finding
			warn := func(f string, a ...any) { fs = append(fs, finding{model.StatusWarn, fmt.Sprintf(f, a...)}) }
			if len(p.missingNUMA) > 0 {
				warn("NUMA node missing on %s", strings.Join(p.missingNUMA, ", "))
			}
			if len(p.missingPC) > 0 {
				warn("PCIe root missing on %s", strings.Join(p.missingPC, ", "))
			}
			for _, c := range p.conflicts {
				kind, name, _ := strings.Cut(c, ":")
				warn("conflicting %s attributes on %s", kind, name)
			}
			for _, e := range p.invalid {
				warn("%s", e)
			}
			if sel[k].Incomplete {
				warn("pool has fewer ResourceSlices than expected for its newest generation (%d of %d)", p.slices, p.expected)
			}
			det := map[string]any{
				"generation": p.gen, "devices": p.devices, "slicesObserved": p.slices, "slicesExpected": p.expected,
				"missingNUMA": nonNil(p.missingNUMA), "missingPCIeRoot": nonNil(p.missingPC), "conflicts": nonNil(p.conflicts),
				"summary": fmt.Sprintf("%d devices, %d missing NUMA, %d missing PCIe root", p.devices, len(p.missingNUMA), len(p.missingPC)),
			}
			st := worst(fs)
			res := model.Result{Check: NameTopology, Status: st, Subject: k[0] + "/" + k[1], Details: det}
			if st != model.StatusPass {
				det["reason"] = reasons(fs)
				res.Findings = findingsOf(fs)
			}
			out = append(out, res)
		}
		return out
	}
}

func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}
