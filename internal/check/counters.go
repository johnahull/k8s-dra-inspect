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

type counterPool struct {
	sets    map[string]map[string]resource.Quantity // counterSet -> counter -> capacity
	devices map[string]resourcev1.Device
	used    map[string]map[string]*resource.Quantity // counterSet -> counter -> consumed
	by      map[string]map[string]bool               // counterSet -> claims consuming it
	setFind map[string][]string                      // counterSet -> publication/overcommit problems
	pool    []finding                                // problems affecting every set of the pool
	refs    map[string]bool                          // counter sets devices consume
}

// Counters checks shared-counter capacity against allocations. It ports the
// legacy dra-counters.py arithmetic with exact resource.Quantity math and
// the scheduler's semantics: every allocated claim consumes the counters of
// its devices, whether or not it is reserved. The legacy PCI-sibling
// correlation heuristic is deliberately not ported.
func Counters() Check {
	return func(_ context.Context, snap model.Snapshot) []model.Result {
		if r, ok := need(snap, NameCounters, "resourceslices", "resourceclaims"); ok {
			return r
		}
		cur, sel := model.CurrentSlices(snap)
		pools := map[model.PoolKey]*counterPool{}
		get := func(k model.PoolKey) *counterPool {
			if pools[k] == nil {
				pools[k] = &counterPool{sets: map[string]map[string]resource.Quantity{}, devices: map[string]resourcev1.Device{},
					used: map[string]map[string]*resource.Quantity{}, by: map[string]map[string]bool{}, setFind: map[string][]string{}, refs: map[string]bool{}}
			}
			return pools[k]
		}
		for _, s := range cur {
			k := model.PoolKey{s.Spec.Driver, s.Spec.Pool.Name}
			for _, cs := range s.Spec.SharedCounters {
				p := get(k)
				p.sets[cs.Name] = map[string]resource.Quantity{}
				for n, c := range cs.Counters {
					p.sets[cs.Name][n] = c.Value
				}
			}
			for _, d := range s.Spec.Devices {
				if len(d.ConsumesCounters) == 0 {
					continue
				}
				p := get(k)
				p.devices[d.Name] = d
				for _, cc := range d.ConsumesCounters {
					p.refs[cc.CounterSet] = true
				}
			}
		}
		for k, p := range pools {
			if sel[k].Incomplete {
				p.pool = append(p.pool, finding{model.StatusWarn, incompleteReason(k[1])})
			}
			// Publication integrity: every consumed set/counter must be defined by the pool.
			for _, name := range slices.Sorted(maps.Keys(p.devices)) {
				for _, cc := range p.devices[name].ConsumesCounters {
					defined, ok := p.sets[cc.CounterSet]
					if !ok {
						p.setFind[cc.CounterSet] = append(p.setFind[cc.CounterSet], fmt.Sprintf("device %s consumes counter set %s, which the pool does not define", name, cc.CounterSet))
						continue
					}
					for _, cn := range slices.Sorted(maps.Keys(cc.Counters)) {
						if _, ok := defined[cn]; !ok {
							p.setFind[cc.CounterSet] = append(p.setFind[cc.CounterSet], fmt.Sprintf("device %s consumes counter %s, which counter set %s does not define", name, cn, cc.CounterSet))
						}
					}
				}
			}
		}

		idx, _ := newDeviceIndex(snap)
		seen := map[model.PoolKey]map[string]bool{}
		for _, c := range allocated(snap) {
			for _, r := range c.Status.Allocation.Devices.Results {
				k := model.PoolKey{r.Driver, r.Pool}
				p := pools[k]
				if p == nil {
					continue
				}
				d, ok := p.devices[r.Device]
				if !ok {
					switch idx.find(r.Driver, r.Pool, r.Device).state {
					case deviceStale:
						p.pool = append(p.pool, finding{model.StatusWarn, fmt.Sprintf("claim %s/%s allocates %s from a stale generation; its counters are not counted", c.Namespace, c.Name, devName(r))})
					case deviceMissing:
						p.pool = append(p.pool, finding{model.StatusFail, fmt.Sprintf("claim %s/%s allocates %s, which no ResourceSlice publishes", c.Namespace, c.Name, devName(r))})
					}
					continue
				}
				// A device that cannot be shared is one allocation however many results name it.
				if d.AllowMultipleAllocations == nil || !*d.AllowMultipleAllocations {
					if seen[k] == nil {
						seen[k] = map[string]bool{}
					}
					if seen[k][r.Device] {
						continue
					}
					seen[k][r.Device] = true
				}
				for _, cc := range d.ConsumesCounters {
					if p.used[cc.CounterSet] == nil {
						p.used[cc.CounterSet] = map[string]*resource.Quantity{}
					}
					if p.by[cc.CounterSet] == nil {
						p.by[cc.CounterSet] = map[string]bool{}
					}
					p.by[cc.CounterSet][c.Namespace+"/"+c.Name] = true
					for cn, v := range cc.Counters {
						if p.used[cc.CounterSet][cn] == nil {
							p.used[cc.CounterSet][cn] = &resource.Quantity{Format: v.Value.Format}
						}
						p.used[cc.CounterSet][cn].Add(v.Value)
					}
				}
			}
		}

		var out []model.Result
		for _, k := range slices.SortedFunc(maps.Keys(pools), func(a, b model.PoolKey) int { return strings.Compare(a[0]+"/"+a[1], b[0]+"/"+b[1]) }) {
			p := pools[k]
			names := map[string]bool{}
			for n := range p.sets {
				names[n] = true
			}
			for n := range p.refs {
				names[n] = true
			}
			for _, name := range slices.Sorted(maps.Keys(names)) {
				out = append(out, p.result(k, name))
			}
		}
		if len(out) == 0 {
			return single(NameCounters, model.StatusPass, "", "no shared counters published")
		}
		return out
	}
}

func (p *counterPool) result(k model.PoolKey, set string) model.Result {
	fs := slices.Clone(p.pool)
	for _, m := range p.setFind[set] {
		fs = append(fs, finding{model.StatusFail, m})
	}
	counters := map[string]map[string]string{}
	var over []string
	for cn, capacity := range p.sets[set] {
		used := resource.Quantity{Format: capacity.Format}
		if u := p.used[set][cn]; u != nil {
			used = *u
		}
		remaining := capacity.DeepCopy()
		remaining.Sub(used)
		counters[cn] = map[string]string{"capacity": capacity.String(), "used": used.String(), "remaining": remaining.String()}
		if used.Cmp(capacity) > 0 {
			over = append(over, cn)
		}
	}
	slices.Sort(over)
	for _, cn := range over {
		fs = append(fs, finding{model.StatusFail, fmt.Sprintf("counter %s overcommitted: %s used of %s", cn, counters[cn]["used"], counters[cn]["capacity"])})
	}
	st := worst(fs)
	var sum []string
	for _, cn := range slices.Sorted(maps.Keys(counters)) {
		sum = append(sum, fmt.Sprintf("%s %s/%s", cn, counters[cn]["used"], counters[cn]["capacity"]))
	}
	det := map[string]any{"summary": strings.Join(sum, ", "), "counters": counters, "allocatedBy": nonNil(slices.Sorted(maps.Keys(p.by[set]))), "driver": k[0], "pool": k[1], "counterSet": set}
	if _, defined := p.sets[set]; !defined {
		delete(det, "counters")
		delete(det, "summary")
	}
	if st != model.StatusPass {
		det["reason"] = reasons(fs)
	}
	return model.Result{Check: NameCounters, Status: st, Subject: k[0] + "/" + k[1] + "/" + set, Details: det}
}

func incompleteReason(pool string) string {
	return "pool " + pool + " has fewer ResourceSlices than expected for its newest generation"
}
