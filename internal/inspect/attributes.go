package inspect

import (
	"cmp"
	"slices"
	"strings"

	resourcev1 "k8s.io/api/resource/v1"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/render"
)

// defaultAttrs are the attribute names shown without --all-attributes, in
// any domain. cpuSocketID is deliberately absent.
var defaultAttrs = map[string]bool{
	"numaNode": true, "numa": true, "numaNodeID": true, "pcieRoot": true,
	"pciBusID": true, "productName": true, "pciDevice": true,
}

func attrName(key string) string { return key[strings.LastIndex(key, "/")+1:] }

// attrDisplay renders a list-valued attribute as [a,b] and a scalar plainly.
func attrDisplay(a resourcev1.DeviceAttribute) string {
	v := strings.Join(model.AttrSet(a), ",")
	if a.IntValues != nil || a.BoolValues != nil || a.StringValues != nil || a.VersionValues != nil {
		return "[" + v + "]"
	}
	return v
}

// Attributes lists attributes (and optionally capacity) of every device.
func Attributes(all, capacity bool) Inspector {
	cols := []render.Column{{Header: "DEVICE", Min: 12, Truncate: render.TruncateMiddle}, {Header: "NODE", Min: 8}, {Header: "ATTRIBUTES", Min: 12}}
	if capacity {
		cols = append(cols, render.Column{Header: "CAPACITY", Min: 8})
	}
	return Inspector{
		Spec: Spec{
			Columns: cols,
			Row: func(r model.Result) []string {
				at, _ := r.Details["attributes"].(map[string]string)
				row := []string{r.Subject, str(r, "node"), kv(at)}
				if capacity {
					c, _ := r.Details["capacity"].(map[string]string)
					row = append(row, kv(c))
				}
				return row
			},
		},
		Run: func(snap model.Snapshot) []model.Result {
			const check = "inspect.attributes"
			if res, ok := unavailable(snap, check); ok {
				return res
			}
			var out []model.Result
			cur, sel := current(snap)
			for _, s := range cur {
				incomplete := sel[poolKey{s.Spec.Driver, s.Spec.Pool.Name}].Incomplete
				for _, d := range s.Spec.Devices {
					attrs := map[string]string{}
					for k, v := range d.Attributes {
						if all || defaultAttrs[attrName(string(k))] {
							attrs[string(k)] = attrDisplay(v)
						}
					}
					det := map[string]any{"node": model.DeviceNode(s, d).String(), "attributes": attrs}
					status := model.StatusPass
					if incomplete {
						status = model.StatusWarn
						det["incomplete"] = true
						det["reason"] = incompleteReason(s.Spec.Pool.Name)
					}
					if capacity {
						caps := map[string]string{}
						for k, v := range d.Capacity {
							caps[string(k)] = v.Value.String()
						}
						det["capacity"] = caps
					}
					out = append(out, model.Result{Check: check, Status: status,
						Subject: s.Spec.Driver + "/" + s.Spec.Pool.Name + "/" + d.Name, Details: det})
				}
			}
			slices.SortFunc(out, func(a, b model.Result) int {
				return cmp.Or(cmp.Compare(str(a, "node"), str(b, "node")), cmp.Compare(a.Subject, b.Subject))
			})
			return out
		},
	}
}
