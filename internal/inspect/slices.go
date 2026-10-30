package inspect

import (
	"cmp"
	"slices"

	resourcev1 "k8s.io/api/resource/v1"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/render"
)

// Slices lists every ResourceSlice.
func Slices() Inspector {
	return Inspector{
		Spec: Spec{
			Columns: []render.Column{{Header: "NODE", Min: 8}, {Header: "DRIVER", Min: 10}, {Header: "POOL", Min: 8}, {Header: "SLICE", Min: 12, Truncate: render.TruncateMiddle}, {Header: "GEN", Min: 3}, {Header: "DEVICES", Min: 7}, {Header: "STATE", Min: 5}},
			Row: func(r model.Result) []string {
				return []string{str(r, "node"), str(r, "driver"), str(r, "pool"), r.Subject, str(r, "generation"), str(r, "deviceCount"), str(r, "state")}
			},
		},
		Run: func(snap model.Snapshot) []model.Result {
			const check = "inspect.slices"
			if res, ok := unavailable(snap, check); ok {
				return res
			}
			_, sel := current(snap)
			list := slices.Clone(snap.ResourceSlices)
			slices.SortFunc(list, func(a, b resourcev1.ResourceSlice) int {
				return cmp.Or(cmp.Compare(sliceNode(a), sliceNode(b)), cmp.Compare(a.Spec.Driver, b.Spec.Driver),
					cmp.Compare(a.Spec.Pool.Name, b.Spec.Pool.Name), cmp.Compare(a.Name, b.Name))
			})
			out := make([]model.Result, 0, len(list))
			for _, s := range list {
				ps := sel[poolKey{s.Spec.Driver, s.Spec.Pool.Name}]
				state, status := "current", model.StatusPass
				switch {
				case s.Spec.Pool.Generation != ps.Gen:
					state = "stale"
				case ps.Incomplete:
					state, status = "incomplete", model.StatusWarn
				}
				det := map[string]any{
					"state":             state,
					"stale":             state == "stale",
					"node":              sliceNode(s),
					"driver":            s.Spec.Driver,
					"pool":              s.Spec.Pool.Name,
					"generation":        s.Spec.Pool.Generation,
					"deviceCount":       len(s.Spec.Devices),
					"sharedCounterSets": len(s.Spec.SharedCounters),
				}
				if state == "incomplete" {
					det["reason"] = incompleteReason(s.Spec.Pool.Name)
				}
				res := model.Result{Check: check, Status: status, Subject: s.Name, Details: det}
				if state == "incomplete" {
					res.Findings = incompleteFinding(s.Spec.Pool.Name)
				}
				out = append(out, res)
			}
			return out
		},
	}
}
