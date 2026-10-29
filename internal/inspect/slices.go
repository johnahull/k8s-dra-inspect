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
			Columns: []render.Column{{Header: "NODE", Min: 8}, {Header: "DRIVER", Min: 10}, {Header: "POOL", Min: 8}, {Header: "SLICE", Min: 12}, {Header: "GEN", Min: 3}, {Header: "DEVICES", Min: 7}},
			Row: func(r model.Result) []string {
				return []string{str(r, "node"), str(r, "driver"), str(r, "pool"), r.Subject, str(r, "generation"), str(r, "deviceCount")}
			},
		},
		Run: func(snap model.Snapshot) []model.Result {
			const check = "inspect.slices"
			if res, ok := unavailable(snap, check); ok {
				return res
			}
			list := slices.Clone(snap.ResourceSlices)
			slices.SortFunc(list, func(a, b resourcev1.ResourceSlice) int {
				return cmp.Or(cmp.Compare(sliceNode(a), sliceNode(b)), cmp.Compare(a.Spec.Driver, b.Spec.Driver),
					cmp.Compare(a.Spec.Pool.Name, b.Spec.Pool.Name), cmp.Compare(a.Name, b.Name))
			})
			out := make([]model.Result, 0, len(list))
			for _, s := range list {
				out = append(out, model.Result{Check: check, Status: model.StatusPass, Subject: s.Name, Details: map[string]any{
					"node":              sliceNode(s),
					"driver":            s.Spec.Driver,
					"pool":              s.Spec.Pool.Name,
					"generation":        s.Spec.Pool.Generation,
					"deviceCount":       len(s.Spec.Devices),
					"sharedCounterSets": len(s.Spec.SharedCounters),
				}})
			}
			return out
		},
	}
}
