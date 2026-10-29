package model

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	resourcev1 "k8s.io/api/resource/v1"
)

// AttrSet normalizes an attribute value to a sorted set of strings. A scalar
// is a one-element set; an attribute with no value yields nil.
func AttrSet(a resourcev1.DeviceAttribute) []string {
	var v []string
	add := func(s string) { v = append(v, s) }
	if a.IntValue != nil {
		add(strconv.FormatInt(*a.IntValue, 10))
	}
	if a.BoolValue != nil {
		add(strconv.FormatBool(*a.BoolValue))
	}
	if a.StringValue != nil {
		add(*a.StringValue)
	}
	if a.VersionValue != nil {
		add(*a.VersionValue)
	}
	for _, i := range a.IntValues {
		add(strconv.FormatInt(i, 10))
	}
	for _, b := range a.BoolValues {
		add(strconv.FormatBool(b))
	}
	v = append(v, a.StringValues...)
	v = append(v, a.VersionValues...)
	sort.Strings(v)
	return slices.Compact(v)
}

// AttrTypedSet is AttrSet with each value prefixed by its type ("int:1",
// "string:1"), because the API matches only equal values of the same type.
func AttrTypedSet(a resourcev1.DeviceAttribute) []string {
	var v []string
	if a.IntValue != nil {
		v = append(v, "int:"+strconv.FormatInt(*a.IntValue, 10))
	}
	if a.BoolValue != nil {
		v = append(v, "bool:"+strconv.FormatBool(*a.BoolValue))
	}
	if a.StringValue != nil {
		v = append(v, "string:"+*a.StringValue)
	}
	if a.VersionValue != nil {
		v = append(v, "version:"+*a.VersionValue)
	}
	for _, i := range a.IntValues {
		v = append(v, "int:"+strconv.FormatInt(i, 10))
	}
	for _, b := range a.BoolValues {
		v = append(v, "bool:"+strconv.FormatBool(b))
	}
	for _, s := range a.StringValues {
		v = append(v, "string:"+s)
	}
	for _, s := range a.VersionValues {
		v = append(v, "version:"+s)
	}
	sort.Strings(v)
	return slices.Compact(v)
}

// Resolved is the outcome of resolving one topology attribute on a device.
type Resolved struct {
	Values   []string // sorted set; empty when the attribute is absent
	Key      string   // attribute key the values came from
	Conflict bool     // another key at the same tier had different values
}

// Topology is a device's resolved NUMA node and PCIe root.
type Topology struct {
	NUMA     Resolved
	PCIeRoot Resolved
}

// TopologyOf resolves NUMA and PCIe-root attributes for a device published by
// driver. cpuSocketID is deliberately not interpreted. Under the last tier
// (any vendor), keys are ordered by fallback name first (numaNode, numa,
// numaNodeID), then lexically within one name; the first key is reported.
func TopologyOf(dev resourcev1.Device, driver string) (Topology, error) {
	numa, err := resolve(dev, driver, "resource.kubernetes.io/numaNode", []string{"numaNode", "numa", "numaNodeID"})
	if err != nil {
		return Topology{}, err
	}
	pcie, err := resolve(dev, driver, "resource.kubernetes.io/pcieRoot", []string{"pcieRoot"})
	if err != nil {
		return Topology{}, err
	}
	return Topology{NUMA: numa, PCIeRoot: pcie}, nil
}

// resolve tries the standard key, then <driver>/<name> or <name>, then any
// <vendor>/<name> in lexical order. The first tier with any key wins.
func resolve(dev resourcev1.Device, driver, standard string, names []string) (Resolved, error) {
	has := func(k string) bool { _, ok := dev.Attributes[resourcev1.QualifiedName(k)]; return ok }

	tier2 := []string{}
	for _, n := range names {
		tier2 = append(tier2, driver+"/"+n, n)
	}
	var tier3 []string
	for _, n := range names {
		var ks []string
		for k := range dev.Attributes {
			if strings.HasSuffix(string(k), "/"+n) {
				ks = append(ks, string(k))
			}
		}
		sort.Strings(ks)
		tier3 = append(tier3, ks...)
	}

	for _, tier := range [][]string{{standard}, tier2, tier3} {
		var keys []string
		for _, k := range tier {
			if has(k) && !slices.Contains(keys, k) {
				keys = append(keys, k)
			}
		}
		if len(keys) == 0 {
			continue
		}
		r := Resolved{Key: keys[0]}
		for i, k := range keys {
			vals := AttrSet(dev.Attributes[resourcev1.QualifiedName(k)])
			if len(vals) == 0 {
				return Resolved{}, fmt.Errorf("device %q attribute %q has no value", dev.Name, k)
			}
			if i == 0 {
				r.Values = vals
			} else if !slices.Equal(vals, r.Values) {
				r.Conflict = true
			}
		}
		return r, nil
	}
	return Resolved{}, nil
}

// FindDevice locates a device by driver, pool name and device name. When a
// pool has several generations it returns the newest.
func FindDevice(list []resourcev1.ResourceSlice, driver, pool, device string) (resourcev1.Device, *resourcev1.ResourceSlice, bool) {
	var (
		best  *resourcev1.ResourceSlice
		found resourcev1.Device
	)
	for i := range list {
		s := &list[i]
		if s.Spec.Driver != driver || s.Spec.Pool.Name != pool {
			continue
		}
		if best != nil && s.Spec.Pool.Generation <= best.Spec.Pool.Generation {
			continue
		}
		for _, d := range s.Spec.Devices {
			if d.Name == device {
				best, found = s, d
			}
		}
	}
	return found, best, best != nil
}
