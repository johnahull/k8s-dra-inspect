// Package inspect turns a Snapshot into Results; it never calls the API.
package inspect

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

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

// sliceNodes lists the distinct node refs of a slice: names first (sorted),
// then placeholders.
func sliceNodes(s resourcev1.ResourceSlice) []model.NodeRef {
	var refs []model.NodeRef
	if s.Spec.PerDeviceNodeSelection != nil && *s.Spec.PerDeviceNodeSelection {
		for _, d := range s.Spec.Devices {
			if r := model.DeviceNode(s, d); r != (model.NodeRef{}) {
				refs = append(refs, r)
			}
		}
	} else if r, ok := model.NodeRefOf(s.Spec.NodeName, s.Spec.NodeSelector, s.Spec.AllNodes); ok {
		refs = append(refs, r)
	}
	slices.SortFunc(refs, func(a, b model.NodeRef) int {
		return cmp.Or(cmp.Compare(a.Label, b.Label), cmp.Compare(a.Name, b.Name))
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

type poolKey = model.PoolKey

// current keeps only the selected generation of each pool.
func current(snap model.Snapshot) ([]resourcev1.ResourceSlice, map[poolKey]model.PoolSel) {
	return model.CurrentSlices(snap)
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
