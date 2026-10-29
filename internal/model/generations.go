package model

import resourcev1 "k8s.io/api/resource/v1"

// PoolKey identifies a pool by driver and pool name.
type PoolKey [2]string

// PoolSel is the generation consumers should use for a pool.
type PoolSel struct {
	Gen        int64
	Incomplete bool // no generation has all of its slices observed
}

// SelectGenerations picks, per pool, the newest generation whose observed
// slice count reaches Pool.ResourceSliceCount. A newer partial generation
// never displaces an older complete one; with no complete generation the
// newest is used and flagged incomplete.
func SelectGenerations(snap Snapshot) map[PoolKey]PoolSel {
	type gk struct {
		pool PoolKey
		gen  int64
	}
	seen, want := map[gk]int64{}, map[gk]int64{}
	for _, s := range snap.ResourceSlices {
		k := gk{PoolKey{s.Spec.Driver, s.Spec.Pool.Name}, s.Spec.Pool.Generation}
		seen[k]++
		want[k] = max(want[k], s.Spec.Pool.ResourceSliceCount)
	}
	sel := map[PoolKey]PoolSel{}
	for k := range seen {
		complete := seen[k] >= want[k]
		cur, have := sel[k.pool]
		// Prefer complete over incomplete, then the higher generation.
		if !have || (complete && cur.Incomplete) || (complete != cur.Incomplete && k.gen > cur.Gen) {
			sel[k.pool] = PoolSel{Gen: k.gen, Incomplete: !complete}
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
