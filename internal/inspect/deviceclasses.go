package inspect

import (
	"strings"

	"k8s.io/apimachinery/pkg/labels"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/render"
)

// DeviceClasses lists DeviceClasses matching sel. CEL selector text and opaque
// driver names are reported; opaque parameters never leave this function.
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
					if drivers, ok := r.Details["configDrivers"].([]string); ok && len(drivers) > 0 {
						cfg = strings.Join(drivers, ",")
					}
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
				expressions := make([]string, 0, len(c.Spec.Selectors))
				for _, selector := range c.Spec.Selectors {
					if selector.CEL != nil && selector.CEL.Expression != "" {
						expressions = append(expressions, selector.CEL.Expression)
					}
				}
				drivers := make([]string, 0, len(c.Spec.Config))
				for _, config := range c.Spec.Config {
					if config.Opaque != nil && config.Opaque.Driver != "" {
						drivers = append(drivers, config.Opaque.Driver)
					}
				}
				out = append(out, model.Result{Check: check, Status: model.StatusPass, Subject: c.Name, Details: map[string]any{
					"configDrivers": drivers, "configPresent": len(c.Spec.Config) > 0, "labels": lbl,
					"selectorCount": len(c.Spec.Selectors), "selectorExpressions": expressions,
				}})
			}
			return out
		},
	}
}
