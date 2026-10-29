package inspect

import (
	"strings"

	"k8s.io/apimachinery/pkg/labels"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/render"
)

// DeviceClasses lists DeviceClasses matching sel. Only the selector count and
// whether config exists are reported; CEL text and opaque parameters never
// leave this function.
func DeviceClasses(sel labels.Selector) Inspector {
	return Inspector{
		Spec: Spec{
			Columns: []render.Column{
				{Header: "NAME", Min: 12, Truncate: render.TruncateMiddle},
				{Header: "LABELS", Min: 10},
				{Header: "SELECTORS", Min: 9},
				{Header: "CONFIG", Min: 6},
			},
			Row: func(r model.Result) []string {
				lbl, _ := r.Details["labels"].(map[string]string)
				cfg := "-"
				if r.Details["configPresent"] == true {
					cfg = "present"
				}
				return []string{r.Subject, strings.ReplaceAll(kv(lbl), ", ", ","), str(r, "selectorCount"), cfg}
			},
		},
		Run: func(snap model.Snapshot) []model.Result {
			const check = "inspect.deviceclasses"
			if res, ok := unavailableOf(snap, check, "deviceclasses"); ok {
				return res
			}
			out := []model.Result{}
			for _, c := range snap.DeviceClasses {
				if !sel.Matches(labels.Set(c.Labels)) {
					continue
				}
				lbl := map[string]string{}
				for k, v := range c.Labels {
					lbl[k] = v
				}
				out = append(out, model.Result{Check: check, Status: model.StatusPass, Subject: c.Name, Details: map[string]any{
					"labels": lbl, "selectorCount": len(c.Spec.Selectors), "configPresent": len(c.Spec.Config) > 0,
				}})
			}
			return out
		},
	}
}
