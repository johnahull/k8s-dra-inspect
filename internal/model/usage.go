package model

import (
	"slices"

	resourcev1 "k8s.io/api/resource/v1"
)

// DeviceKey identifies a published device.
type DeviceKey struct{ Driver, Pool, Device string }

// Usage lists the claims ("ns/name") that allocate a device and the pods
// ("ns/name") that use those claims.
type Usage struct{ Claims, Pods []string }

// UsageScope says how far a usage answer can be trusted. Claims and pods are
// namespace-filtered when Namespace is set while ResourceSlices are not, so a
// device missing from the map is only "free" within that scope.
type UsageScope struct {
	Available     bool   // false: resourceclaims were not collected; the map is empty
	Reason        string // why, when not Available
	Namespace     string // claims and pods were limited to this namespace
	PodsAvailable bool   // false: only reservedFor pods are known
	ForeignClaims bool   // true: outside namespace allocations are known but names masked
}

func sortedUnique(v []string) []string {
	slices.Sort(v)
	return slices.Compact(v)
}

// DeviceUsage inverts every allocated claim's results: an allocated device is
// in use even when nothing reserves it. A shareable device may list several
// claims. users maps a claim ("ns/name") to the bare names of the pods using
// it, as analyze.ClaimPodNames computes them.
func DeviceUsage(snap Snapshot, users map[string][]string) (map[DeviceKey]Usage, UsageScope) {
	sc := UsageScope{Namespace: snap.Namespace, PodsAvailable: snap.Collection["pods"].State == StateOK}
	collection := "resourceclaims"
	st := snap.Collection["resourceclaims"]
	if snap.Namespace != "" {
		if st.State == StateOK {
			st = snap.Collection["globalallocations"]
			collection = "globalallocations"
		}
		sc.ForeignClaims = snap.Collection["globalallocations"].State == StateOK
	}
	if st.State != StateOK {
		sc.Reason = st.Reason
		if sc.Reason == "" {
			state := st.State
			if state == "" {
				state = "not collected"
			}
			sc.Reason = collection + " collection state: " + state
		}
		return map[DeviceKey]Usage{}, sc
	}
	sc.Available = true
	out := map[DeviceKey]Usage{}
	for _, c := range snap.ResourceClaims {
		if c.Status.Allocation == nil {
			continue
		}
		name := c.Namespace + "/" + c.Name
		var pods []string
		for _, p := range users[name] {
			pods = append(pods, c.Namespace+"/"+p)
		}
		for _, r := range c.Status.Allocation.Devices.Results {
			k := DeviceKey{r.Driver, r.Pool, r.Device}
			u := out[k]
			if slices.Contains(u.Claims, name) {
				continue
			}
			u.Claims = append(u.Claims, name)
			u.Pods = append(u.Pods, pods...)
			out[k] = u
		}
	}
	for k, u := range out {
		slices.Sort(u.Claims)
		u.Pods = sortedUnique(u.Pods)
		out[k] = u
	}
	if snap.Namespace != "" {
		for _, allocation := range snap.GlobalAllocations {
			for _, result := range allocation.Devices.Results {
				k := DeviceKey{result.Driver, result.Pool, result.Device}
				u := out[k]
				if !slices.Contains(u.Claims, "allocated outside namespace") {
					u.Claims = append(u.Claims, "allocated outside namespace")
				}
				slices.Sort(u.Claims)
				u.Pods = sortedUnique(u.Pods)
				out[k] = u
			}
		}
	}
	return out, sc
}

// AttrString reads a string attribute: the standard resource.kubernetes.io/
// key, then <driver>/<name>, then the bare name. The first key present wins;
// it is not ok when that attribute is not a string.
func AttrString(dev resourcev1.Device, driver, name string) (string, bool) {
	for _, k := range []string{"resource.kubernetes.io/" + name, driver + "/" + name, name} {
		if a, ok := dev.Attributes[resourcev1.QualifiedName(k)]; ok {
			if a.StringValue == nil {
				return "", false
			}
			return *a.StringValue, true
		}
	}
	return "", false
}
