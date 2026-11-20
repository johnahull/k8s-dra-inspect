package model

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	resourcev1 "k8s.io/api/resource/v1"
)

// PoolKey identifies a pool by driver and pool name.
type PoolKey [2]string

// PoolSel is the generation consumers should use for a pool.
type PoolSel struct {
	Gen        int64
	Observed   int   // slices seen at Gen
	Declared   int64 // resourceSliceCount at Gen; 0 when the slices disagree
	Incomplete bool  // observed slice count differs from the declared count
	Invalid    bool  // the scheduler refuses the pool; Problems says why
	// Problems explains Invalid: slices disagree on resourceSliceCount, more
	// slices than declared are observed, or a device name is published twice.
	Problems []string
}

// SelectGenerations picks the newest generation for every pool. Older
// generations are never used to fill holes in the newest one: the scheduler
// ignores them.
func SelectGenerations(snap Snapshot) map[PoolKey]PoolSel {
	ptrs := make([]*resourcev1.ResourceSlice, len(snap.ResourceSlices))
	for i := range snap.ResourceSlices {
		ptrs[i] = &snap.ResourceSlices[i]
	}
	return SelectGenerationsOf(ptrs)
}

// SelectGenerationsOf is SelectGenerations over slice pointers. A selected
// generation is incomplete when its observed count differs from the
// declaration, and invalid when slices disagree on the count, too many are
// observed, or two slices publish the same device name (the upstream
// allocator's validateDeviceNames).
func SelectGenerationsOf(list []*resourcev1.ResourceSlice) map[PoolKey]PoolSel {
	newest := map[PoolKey][]*resourcev1.ResourceSlice{}
	for _, s := range list {
		k := PoolKey{s.Spec.Driver, s.Spec.Pool.Name}
		cur := newest[k]
		switch {
		case len(cur) == 0 || s.Spec.Pool.Generation > cur[0].Spec.Pool.Generation:
			newest[k] = []*resourcev1.ResourceSlice{s}
		case s.Spec.Pool.Generation == cur[0].Spec.Pool.Generation:
			newest[k] = append(cur, s)
		}
	}
	sel := make(map[PoolKey]PoolSel, len(newest))
	for k, gen := range newest {
		sel[k] = selectPool(gen)
	}
	return sel
}

// selectPool judges the slices of one pool's newest generation.
func selectPool(gen []*resourcev1.ResourceSlice) PoolSel {
	gen = slices.Clone(gen)
	slices.SortStableFunc(gen, func(a, b *resourcev1.ResourceSlice) int { return cmp.Compare(a.Name, b.Name) })
	p := PoolSel{Gen: gen[0].Spec.Pool.Generation, Observed: len(gen)}
	var counts []int64
	for _, s := range gen {
		if !slices.Contains(counts, s.Spec.Pool.ResourceSliceCount) {
			counts = append(counts, s.Spec.Pool.ResourceSliceCount)
		}
	}
	slices.Sort(counts)
	want := counts[len(counts)-1]
	if len(counts) == 1 {
		p.Declared = want
	}
	p.Incomplete = int64(p.Observed) != want
	// The scheduler takes the count from whichever slice it sees first, so
	// disagreement makes its view of the pool depend on ordering.
	if len(counts) > 1 {
		p.Problems = append(p.Problems, fmt.Sprintf(
			"slices at generation %d disagree on resourceSliceCount (%s), so the scheduler's view depends on slice order",
			p.Gen, joinInts(counts)))
	} else if int64(p.Observed) > want {
		p.Problems = append(p.Problems, fmt.Sprintf(
			"%d slices observed at generation %d but resourceSliceCount is %d, which makes the pool invalid",
			p.Observed, p.Gen, want))
	}
	publishedBy := map[string]string{}
	reported := map[string]bool{}
	for _, s := range gen {
		for _, d := range s.Spec.Devices {
			prev, dup := publishedBy[d.Name]
			if !dup {
				publishedBy[d.Name] = s.Name
				continue
			}
			if !reported[d.Name] {
				reported[d.Name] = true
				p.Problems = append(p.Problems, fmt.Sprintf(
					"device name %q is published by both %s and %s, which makes the pool invalid", d.Name, prev, s.Name))
			}
		}
	}
	p.Invalid = len(p.Problems) > 0
	return p
}

func joinInts(values []int64) string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = fmt.Sprint(v)
	}
	return strings.Join(parts, ", ")
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
