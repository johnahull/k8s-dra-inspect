// Package view prints inspector results as readable text. Views only write
// through a render.Printer, which neutralises every untrusted string.
package view

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"

	resourcev1 "k8s.io/api/resource/v1"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
)

// cmpNum orders numeric strings numerically (2 before 10) and falls back to
// string order, so mixed or non-numeric values still sort deterministically.
func cmpNum(a, b string) int {
	x, e1 := strconv.Atoi(a)
	y, e2 := strconv.Atoi(b)
	if e1 == nil && e2 == nil {
		return cmp.Compare(x, y)
	}
	return cmp.Compare(a, b)
}

// numSet joins values numerically sorted; "" for an empty set.
func numSet(v []string) string {
	s := slices.Clone(v)
	slices.SortFunc(s, cmpNum)
	return strings.Join(s, ",")
}

// cmpSets orders NUMA sets by their first value, then the full text; an empty
// set (unknown) sorts last.
func cmpSets(a, b string) int {
	if (a == "") != (b == "") {
		if a == "" {
			return 1
		}
		return -1
	}
	af, _, _ := strings.Cut(a, ",")
	bf, _, _ := strings.Cut(b, ",")
	return cmp.Or(cmpNum(af, bf), cmp.Compare(a, b))
}

// hardware is the "(pciBusID) [product]" suffix of a device line.
func hardware(dev resourcev1.Device, driver string) string {
	var s string
	if bdf, ok := model.AttrString(dev, driver, "pciBusID"); ok && bdf != "" {
		s += " (" + bdf + ")"
	}
	label, ok := model.AttrString(dev, driver, "productName")
	if !ok || label == "" {
		label, ok = model.AttrString(dev, driver, "model")
	}
	if ok && label != "" {
		s += " [" + label + "]"
	}
	return s
}

// numaOf and rootOf are the exact numeric-sorted sets, "" when unknown.
func topo(dev resourcev1.Device, driver string) (numa, root string) {
	t, err := model.TopologyOf(dev, driver)
	if err != nil {
		return "", ""
	}
	return numSet(t.NUMA.Values), strings.Join(t.PCIeRoot.Values, ",")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// detStr reads a Details value as text, "" when absent.
func detStr(r model.Result, key string) string {
	if v, ok := r.Details[key]; ok && v != nil {
		return fmt.Sprint(v)
	}
	return ""
}
