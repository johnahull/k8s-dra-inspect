// Package inspect turns a Snapshot into Results; it never calls the API.
package inspect

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/render"
)

// Spec tells the table renderer how to print an inspector's Results.
type Spec struct {
	Columns []render.Column
	Row     func(model.Result) []string
}

// Inspector pairs a Result producer with its table layout.
type Inspector struct {
	Spec
	Run func(model.Snapshot) []model.Result
}

// unavailable returns the single result for a snapshot whose ResourceSlices
// were not collected, so a failed collection never looks like an empty pass.
func unavailable(snap model.Snapshot, check string) ([]model.Result, bool) {
	return unavailableOf(snap, check, "resourceslices")
}

// unavailableOf is unavailable for any collection key.
func unavailableOf(snap model.Snapshot, check, key string) ([]model.Result, bool) {
	st := snap.Collection[key]
	if st.State == model.StateOK {
		return nil, false
	}
	reason := st.Reason
	if reason == "" {
		reason = key + " collection state: " + cmpState(st.State)
	}
	return []model.Result{{Check: check, Status: model.StatusUnavailable, Details: map[string]any{"reason": reason}}}, true
}

func cmpState(s string) string {
	if s == "" {
		return "not collected"
	}
	return s
}

const (
	allNodesLabel = "(all nodes)"
	selectorLabel = "(selector)"
)

// nodeRef is a real node name or, when name is empty, a placeholder label.
type nodeRef struct{ name, label string }

func (n nodeRef) String() string { return n.name + n.label }

func refOf(name *string, selector *corev1.NodeSelector, all *bool) (nodeRef, bool) {
	switch {
	case name != nil:
		return nodeRef{name: *name}, true
	case all != nil && *all:
		return nodeRef{label: allNodesLabel}, true
	case selector != nil:
		return nodeRef{label: selectorLabel}, true
	}
	return nodeRef{}, false
}

// deviceNode resolves where one device is available, honouring
// PerDeviceNodeSelection.
func deviceNode(s resourcev1.ResourceSlice, d resourcev1.Device) nodeRef {
	var r nodeRef
	if s.Spec.PerDeviceNodeSelection != nil && *s.Spec.PerDeviceNodeSelection {
		r, _ = refOf(d.NodeName, d.NodeSelector, d.AllNodes)
	} else {
		r, _ = refOf(s.Spec.NodeName, s.Spec.NodeSelector, s.Spec.AllNodes)
	}
	return r
}

// sliceNodes lists the distinct node refs of a slice: names first (sorted),
// then placeholders.
func sliceNodes(s resourcev1.ResourceSlice) []nodeRef {
	var refs []nodeRef
	if s.Spec.PerDeviceNodeSelection != nil && *s.Spec.PerDeviceNodeSelection {
		for _, d := range s.Spec.Devices {
			if r := deviceNode(s, d); r != (nodeRef{}) {
				refs = append(refs, r)
			}
		}
	} else if r, ok := refOf(s.Spec.NodeName, s.Spec.NodeSelector, s.Spec.AllNodes); ok {
		refs = append(refs, r)
	}
	slices.SortFunc(refs, func(a, b nodeRef) int {
		return cmp.Or(cmp.Compare(a.label, b.label), cmp.Compare(a.name, b.name))
	})
	return slices.Compact(refs)
}

// sliceNode names the node(s) a slice is bound to, or how they are selected.
func sliceNode(s resourcev1.ResourceSlice) string {
	refs := sliceNodes(s)
	parts := make([]string, len(refs))
	for i, r := range refs {
		parts[i] = r.String()
	}
	return strings.Join(parts, ", ")
}

type poolKey [2]string

// poolSel is the generation consumers should use for a pool.
type poolSel struct {
	gen        int64
	incomplete bool // no generation has all of its slices observed
}

// selectGenerations picks, per pool, the newest generation whose observed
// slice count reaches Pool.ResourceSliceCount. A newer partial generation
// never displaces an older complete one; with no complete generation the
// newest is used and flagged incomplete.
func selectGenerations(snap model.Snapshot) map[poolKey]poolSel {
	type gk struct {
		pool poolKey
		gen  int64
	}
	seen, want := map[gk]int64{}, map[gk]int64{}
	for _, s := range snap.ResourceSlices {
		k := gk{poolKey{s.Spec.Driver, s.Spec.Pool.Name}, s.Spec.Pool.Generation}
		seen[k]++
		want[k] = max(want[k], s.Spec.Pool.ResourceSliceCount)
	}
	sel := map[poolKey]poolSel{}
	for k := range seen {
		complete := seen[k] >= want[k]
		cur, have := sel[k.pool]
		// Prefer complete over incomplete, then the higher generation.
		if !have || (complete && cur.incomplete) || (complete != cur.incomplete && k.gen > cur.gen) {
			sel[k.pool] = poolSel{gen: k.gen, incomplete: !complete}
		}
	}
	return sel
}

// current keeps only the selected generation of each pool and returns the
// selection so callers can report incomplete pools.
func current(snap model.Snapshot) ([]resourcev1.ResourceSlice, map[poolKey]poolSel) {
	sel := selectGenerations(snap)
	var out []resourcev1.ResourceSlice
	for _, s := range snap.ResourceSlices {
		if s.Spec.Pool.Generation == sel[poolKey{s.Spec.Driver, s.Spec.Pool.Name}].gen {
			out = append(out, s)
		}
	}
	return out, sel
}

// incompleteReason explains a warn status for a partially observed pool.
func incompleteReason(pools ...string) string {
	return "pool has fewer ResourceSlices than expected for its newest generation: " + strings.Join(pools, ", ")
}

// str reads a Details field for table rows.
func str(r model.Result, key string) string {
	v, ok := r.Details[key]
	if !ok || v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

// kv renders a map as "k=v, k=v" in key order.
func kv(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + m[k]
	}
	return strings.Join(parts, ", ")
}
