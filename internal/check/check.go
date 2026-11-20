// Package check turns a Snapshot into validation Results; it never calls the API.
package check

import (
	"context"
	"fmt"
	"strings"

	resourcev1 "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
)

// Check inspects a snapshot and returns findings; it never calls the API.
type Check func(ctx context.Context, snap model.Snapshot) []model.Result

// Entry names a check for the report.
type Entry struct {
	Name string
	Run  Check
}

// Registry lists every check in report order; alignment covers all allocated
// claims in namespace (all namespaces when empty).
func Registry(namespace string) []Entry {
	return []Entry{
		{NameAlignment, Alignment("", namespace)},
		{NameCounters, Counters()},
		{NameCapacity, Capacity()},
		{NameTopology, Topology()},
	}
}

// Check names, used as Result.Check.
const (
	NameAlignment = "check.alignment"
	NameCounters  = "check.counters"
	NameCapacity  = "check.capacity"
	NameTopology  = "check.topology"
	NameWhy       = "check.why"
)

// Need returns the single unavailable result for the first required
// collection that was not collected, so a failed collection never reads as pass.
func Need(snap model.Snapshot, check string, keys ...string) ([]model.Result, bool) {
	for _, k := range keys {
		st := snap.Collection[k]
		if st.State == model.StateOK {
			continue
		}
		reason := st.Reason
		if reason == "" {
			state := st.State
			if state == "" {
				state = "not collected"
			}
			reason = fmt.Sprintf("%s collection state: %s", k, state)
		}
		return []model.Result{{Check: check, Status: model.StatusUnavailable, Details: map[string]any{"reason": reason},
			Findings: []model.Finding{{Status: model.StatusUnavailable, Kind: model.KindUnavailable, Text: reason}}}}, true
	}
	return nil, false
}

// AllocationKeys lists the collections that together hold every allocation:
// claims, plus the masked outside-namespace allocations when namespaced.
func AllocationKeys(snap model.Snapshot) []string {
	if snap.Namespace != "" {
		return []string{"resourceclaims", "globalallocations"}
	}
	return []string{"resourceclaims"}
}

func needAllocations(snap model.Snapshot, check string) ([]model.Result, bool) {
	return Need(snap, check, AllocationKeys(snap)...)
}

func single(check string, st model.Status, subject, reason string) []model.Result {
	r := model.Result{Check: check, Status: st, Subject: subject, Details: map[string]any{"reason": reason}}
	if st != model.StatusPass {
		k := model.KindOther
		switch st {
		case model.StatusUnavailable:
			k = model.KindUnavailable
		case model.StatusError:
			k = model.KindError
		}
		r.Findings = []model.Finding{{Status: st, Kind: k, Text: reason}}
	}
	return []model.Result{r}
}

func devName(r resourcev1.DeviceRequestAllocationResult) string {
	return r.Driver + "/" + r.Pool + "/" + r.Device
}

// allocated lists claims that have an allocation.
func allocated(snap model.Snapshot) []resourcev1.ResourceClaim {
	var out []resourcev1.ResourceClaim
	for _, c := range snap.ResourceClaims {
		if c.Status.Allocation != nil {
			out = append(out, c)
		}
	}
	for i := range snap.GlobalAllocations {
		out = append(out, resourcev1.ResourceClaim{
			ObjectMeta: metav1.ObjectMeta{Namespace: "outside namespace", Name: "allocation"},
			Status:     resourcev1.ResourceClaimStatus{Allocation: snap.GlobalAllocations[i].DeepCopy()},
		})
	}
	return out
}

// resolveKey finds qualified (a "domain/name" qualified name) in m: an exact
// key, or the bare name when the domain is the publishing driver's, because
// unqualified keys belong to the driver's domain. Never a suffix match.
func resolveKey[V any](m map[resourcev1.QualifiedName]V, driver, qualified string) (resourcev1.QualifiedName, bool) {
	if _, ok := m[resourcev1.QualifiedName(qualified)]; ok {
		return resourcev1.QualifiedName(qualified), true
	}
	if dom, name, ok := strings.Cut(qualified, "/"); ok && dom == driver {
		if _, ok := m[resourcev1.QualifiedName(name)]; ok {
			return resourcev1.QualifiedName(name), true
		}
	}
	return "", false
}

func lookupKey[V any](m map[resourcev1.QualifiedName]V, driver, qualified string) (V, bool) {
	k, ok := resolveKey(m, driver, qualified)
	return m[k], ok
}
