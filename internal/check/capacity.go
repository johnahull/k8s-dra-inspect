package check

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
)

type capDevice struct {
	ref      deviceRef
	consumed map[resourcev1.QualifiedName]*resource.Quantity
	by       map[string]bool
	fs       []finding
}

// Capacity verifies consumable-capacity allocations: each consumed value
// must be published by the device and fit within it, as must the per-device
// sum over every allocated claim. Of the device's requestPolicy only
// validRange min and max are checked; step, validValues and default are
// NOT validated, nor are claim capacity requests (only what the allocation
// recorded as consumed).
func Capacity() Check {
	return func(_ context.Context, snap model.Snapshot) []model.Result {
		if r, ok := need(snap, NameCapacity, "resourceslices", "resourceclaims"); ok {
			return r
		}
		idx, _ := newDeviceIndex(snap)
		devs := map[string]*capDevice{}
		for _, c := range allocated(snap) {
			for _, r := range c.Status.Allocation.Devices.Results {
				if len(r.ConsumedCapacity) == 0 {
					continue
				}
				key := devName(r)
				d := devs[key]
				if d == nil {
					d = &capDevice{ref: idx.find(r.Driver, r.Pool, r.Device), consumed: map[resourcev1.QualifiedName]*resource.Quantity{}, by: map[string]bool{}}
					devs[key] = d
					switch d.ref.state {
					case deviceMissing:
						d.fs = append(d.fs, finding{model.StatusFail, fmt.Sprintf("allocated device %s is not in any ResourceSlice", key)})
					case deviceStale:
						d.fs = append(d.fs, finding{model.StatusWarn, fmt.Sprintf("allocated device %s is only in a stale generation of its pool", key)})
					}
				}
				d.by[c.Namespace+"/"+c.Name] = true
				if d.ref.state == deviceMissing {
					continue
				}
				for _, name := range slices.Sorted(maps.Keys(r.ConsumedCapacity)) {
					v := r.ConsumedCapacity[name]
					k, ok := resolveKey(d.ref.dev.Capacity, r.Driver, string(name))
					if !ok {
						d.fs = append(d.fs, finding{model.StatusFail, fmt.Sprintf("consumed capacity %s is not published by %s", name, key)})
						continue
					}
					pub := d.ref.dev.Capacity[k]
					if v.Cmp(pub.Value) > 0 {
						d.fs = append(d.fs, finding{model.StatusFail, fmt.Sprintf("claim %s/%s consumes %s of %s, more than the published %s", c.Namespace, c.Name, v.String(), k, pub.Value.String())})
					}
					if p := pub.RequestPolicy; p != nil && p.ValidRange != nil {
						if m := p.ValidRange.Min; m != nil && v.Cmp(*m) < 0 {
							d.fs = append(d.fs, finding{model.StatusFail, fmt.Sprintf("claim %s/%s consumes %s of %s, below the policy minimum %s", c.Namespace, c.Name, v.String(), k, m.String())})
						}
						if m := p.ValidRange.Max; m != nil && v.Cmp(*m) > 0 {
							d.fs = append(d.fs, finding{model.StatusFail, fmt.Sprintf("claim %s/%s consumes %s of %s, above the policy maximum %s", c.Namespace, c.Name, v.String(), k, m.String())})
						}
					}
					if d.consumed[k] == nil {
						d.consumed[k] = &resource.Quantity{Format: v.Format}
					}
					d.consumed[k].Add(v)
				}
			}
		}
		if len(devs) == 0 {
			return single(NameCapacity, model.StatusPass, "", "no consumable capacity allocated")
		}
		out := make([]model.Result, 0, len(devs))
		for _, key := range slices.Sorted(maps.Keys(devs)) {
			d := devs[key]
			caps := map[string]map[string]string{}
			for k, used := range d.consumed {
				pub := d.ref.dev.Capacity[k].Value
				rem := pub.DeepCopy()
				rem.Sub(*used)
				caps[string(k)] = map[string]string{"published": pub.String(), "consumed": used.String(), "remaining": rem.String()}
				if used.Cmp(pub) > 0 {
					d.fs = append(d.fs, finding{model.StatusFail, fmt.Sprintf("total consumed %s of %s exceeds the published %s", used.String(), k, pub.String())})
				}
			}
			var sum []string
			for _, n := range slices.Sorted(maps.Keys(caps)) {
				sum = append(sum, fmt.Sprintf("%s %s/%s", n, caps[n]["consumed"], caps[n]["published"]))
			}
			det := map[string]any{"summary": strings.Join(sum, ", "), "capacities": caps, "allocatedBy": nonNil(slices.Sorted(maps.Keys(d.by)))}
			st := worst(d.fs)
			if st != model.StatusPass {
				det["reason"] = reasons(d.fs)
			}
			out = append(out, model.Result{Check: NameCapacity, Status: st, Subject: key, Details: det})
		}
		return out
	}
}
