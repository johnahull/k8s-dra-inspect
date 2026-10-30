// Package check turns a Snapshot into validation Results; it never calls the API.
package check

import (
	"context"
	"fmt"
	"strings"

	resourcev1 "k8s.io/api/resource/v1"

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
)

// need returns the single unavailable result for the first required
// collection that was not collected, so a failed collection never reads as pass.
func need(snap model.Snapshot, check string, keys ...string) ([]model.Result, bool) {
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
		return []model.Result{{Check: check, Status: model.StatusUnavailable, Details: map[string]any{"reason": reason}}}, true
	}
	return nil, false
}

func single(check string, st model.Status, subject, reason string) []model.Result {
	return []model.Result{{Check: check, Status: st, Subject: subject, Details: map[string]any{"reason": reason}}}
}

type deviceState int

const (
	deviceCurrent deviceState = iota // in the pool's selected generation
	deviceStale                      // only in another generation of the pool
	deviceMissing                    // in no ResourceSlice
)

type deviceRef struct {
	dev   resourcev1.Device
	state deviceState
}

type deviceKey [3]string

// deviceIndex finds allocated devices in the current generation of their
// pool, falling back to the newest other generation so stale allocations
// can be flagged rather than silently dropped.
type deviceIndex struct{ cur, other map[deviceKey]deviceRef }

func newDeviceIndex(snap model.Snapshot) deviceIndex {
	cur, _ := model.CurrentSlices(snap)
	x := deviceIndex{cur: map[deviceKey]deviceRef{}, other: map[deviceKey]deviceRef{}}
	for _, s := range cur {
		for _, d := range s.Spec.Devices {
			x.cur[deviceKey{s.Spec.Driver, s.Spec.Pool.Name, d.Name}] = deviceRef{d, deviceCurrent}
		}
	}
	gen := map[deviceKey]int64{}
	for _, s := range snap.ResourceSlices {
		for _, d := range s.Spec.Devices {
			k := deviceKey{s.Spec.Driver, s.Spec.Pool.Name, d.Name}
			if g, ok := gen[k]; !ok || s.Spec.Pool.Generation > g {
				gen[k] = s.Spec.Pool.Generation
				x.other[k] = deviceRef{d, deviceStale}
			}
		}
	}
	return x
}

func (x deviceIndex) find(driver, pool, device string) deviceRef {
	k := deviceKey{driver, pool, device}
	if r, ok := x.cur[k]; ok {
		return r
	}
	if r, ok := x.other[k]; ok {
		return r
	}
	return deviceRef{state: deviceMissing}
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
