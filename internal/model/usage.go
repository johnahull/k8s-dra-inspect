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
}

func sortedUnique(v []string) []string {
	slices.Sort(v)
	return slices.Compact(v)
}

// podIndex maps "ns/claimName" to the pods whose spec or status resolves to it.
func podIndex(snap Snapshot) map[string][]string {
	idx := map[string][]string{}
	for _, p := range snap.Pods {
		for _, rc := range p.ResourceClaims {
			if rc.ResourceClaimName != nil {
				idx[p.Namespace+"/"+*rc.ResourceClaimName] = append(idx[p.Namespace+"/"+*rc.ResourceClaimName], p.Name)
			}
		}
		for _, st := range p.ResourceClaimStatuses {
			if st.ResourceClaimName != nil {
				idx[p.Namespace+"/"+*st.ResourceClaimName] = append(idx[p.Namespace+"/"+*st.ResourceClaimName], p.Name)
			}
		}
	}
	return idx
}

func podsOf(idx map[string][]string, c resourcev1.ResourceClaim) []string {
	names := slices.Clone(idx[c.Namespace+"/"+c.Name])
	for _, r := range c.Status.ReservedFor {
		if r.Resource == "pods" {
			names = append(names, r.Name)
		}
	}
	return sortedUnique(names)
}

// PodsUsing finds pods whose spec or status resolves to claim c, plus pods in
// reservedFor. Names are bare (the claim's namespace).
func PodsUsing(snap Snapshot, c resourcev1.ResourceClaim) []string {
	return append([]string{}, podsOf(podIndex(snap), c)...)
}

// DeviceUsage inverts every allocated claim's results: an allocated device is
// in use even when nothing reserves it. A shareable device may list several
// claims.
func DeviceUsage(snap Snapshot) (map[DeviceKey]Usage, UsageScope) {
	sc := UsageScope{Namespace: snap.Namespace, PodsAvailable: snap.Collection["pods"].State == StateOK}
	st := snap.Collection["resourceclaims"]
	if st.State != StateOK {
		sc.Reason = st.Reason
		if sc.Reason == "" {
			state := st.State
			if state == "" {
				state = "not collected"
			}
			sc.Reason = "resourceclaims collection state: " + state
		}
		return map[DeviceKey]Usage{}, sc
	}
	sc.Available = true
	idx := podIndex(snap)
	out := map[DeviceKey]Usage{}
	for _, c := range snap.ResourceClaims {
		if c.Status.Allocation == nil {
			continue
		}
		name := c.Namespace + "/" + c.Name
		var pods []string
		for _, p := range podsOf(idx, c) {
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
