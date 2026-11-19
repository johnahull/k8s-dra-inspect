package model

import resourcev1 "k8s.io/api/resource/v1"

// PoolKey identifies a pool by driver and pool name.
type PoolKey [2]string

// PoolSel is the generation consumers should use for a pool.
type PoolSel struct {
	Gen        int64
	Incomplete bool // no generation has all of its slices observed
	Invalid    bool // slices disagree with each other or the observed count is overfull
}

// SelectGenerations picks the newest generation for every pool. Older
// generations are never used to fill holes in the newest one: the scheduler
// ignores them. A selected generation is incomplete when its observed count
// differs from the declaration, and invalid when slices disagree on the count
// or too many are observed.
func SelectGenerations(snap Snapshot) map[PoolKey]PoolSel {
	type gk struct {
		pool PoolKey
		gen  int64
	}
	seen, want := map[gk]int64{}, map[gk]int64{}
	counts := map[gk]map[int64]bool{}
	for _, s := range snap.ResourceSlices {
		k := gk{PoolKey{s.Spec.Driver, s.Spec.Pool.Name}, s.Spec.Pool.Generation}
		seen[k]++
		want[k] = max(want[k], s.Spec.Pool.ResourceSliceCount)
		if counts[k] == nil {
			counts[k] = map[int64]bool{}
		}
		counts[k][s.Spec.Pool.ResourceSliceCount] = true
	}
	sel := map[PoolKey]PoolSel{}
	for k := range seen {
		complete := seen[k] == want[k]
		cur, have := sel[k.pool]
		if !have || k.gen > cur.Gen {
			sel[k.pool] = PoolSel{Gen: k.gen, Incomplete: !complete, Invalid: len(counts[k]) > 1 || seen[k] > want[k]}
		}
	}
	return sel
}

// CurrentSlices keeps only the selected generation of each pool and returns
// the selection so callers can report incomplete pools.
func CurrentSlices(snap Snapshot) ([]resourcev1.ResourceSlice, map[PoolKey]PoolSel) {
	sel := SelectGenerations(snap)
	var out []resourcev1.ResourceSlice
	for _, s := range snap.ResourceSlices {
		if s.Spec.Pool.Generation == sel[PoolKey{s.Spec.Driver, s.Spec.Pool.Name}].Gen {
			out = append(out, s)
		}
	}
	return out, sel
}
