// Package inspect turns a Snapshot into Results; it never calls the API.
package inspect

import (
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
	st := snap.Collection["resourceslices"]
	if st.State == model.StateOK {
		return nil, false
	}
	reason := st.Reason
	if reason == "" {
		reason = "resourceslices collection state: " + cmpState(st.State)
	}
	return []model.Result{{Check: check, Status: model.StatusUnavailable, Details: map[string]any{"reason": reason}}}, true
}

func cmpState(s string) string {
	if s == "" {
		return "not collected"
	}
	return s
}

// sliceNode names the node a slice is bound to, or how it is selected.
func sliceNode(s resourcev1.ResourceSlice) string {
	switch {
	case s.Spec.NodeName != nil:
		return *s.Spec.NodeName
	case s.Spec.AllNodes != nil && *s.Spec.AllNodes:
		return "(all nodes)"
	case s.Spec.NodeSelector != nil:
		return "(selector)"
	}
	return ""
}

// current keeps only the newest generation of each pool.
func current(snap model.Snapshot) []resourcev1.ResourceSlice {
	newest := map[[2]string]int64{}
	for _, s := range snap.ResourceSlices {
		k := [2]string{s.Spec.Driver, s.Spec.Pool.Name}
		newest[k] = max(newest[k], s.Spec.Pool.Generation)
	}
	var out []resourcev1.ResourceSlice
	for _, s := range snap.ResourceSlices {
		if s.Spec.Pool.Generation == newest[[2]string{s.Spec.Driver, s.Spec.Pool.Name}] {
			out = append(out, s)
		}
	}
	return out
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
