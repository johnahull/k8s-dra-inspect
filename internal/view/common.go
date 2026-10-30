// Package view prints inspector results as readable text. Views only write
// through a render.Printer, which neutralises every untrusted string.
//
// Views read what a check or inspector produced: Result.Status, the typed
// Details values and the Findings. A view picks its layout from Finding.Kind
// and the Details types, never from the wording of a sentence; Details values
// are read through the typed helpers in decode.go, which report a mismatch as
// an error note instead of guessing.
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

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// run is the leading part of s made of digits (when digits) or of anything else.
func run(s string, digits bool) string {
	i := 0
	for i < len(s) && isDigit(s[i]) == digits {
		i++
	}
	return s[:i]
}

// cmpNatural orders strings with digit runs by number: pci0000:20 before
// pci0000:100, and 2 before 10.
func cmpNatural(a, b string) int {
	for a != "" && b != "" {
		da, db := isDigit(a[0]), isDigit(b[0])
		ra, rb := run(a, da), run(b, db)
		if da && db {
			x, y := strings.TrimLeft(ra, "0"), strings.TrimLeft(rb, "0")
			if c := cmp.Or(cmp.Compare(len(x), len(y)), cmp.Compare(x, y)); c != 0 {
				return c
			}
		} else if c := cmp.Compare(ra, rb); c != 0 {
			return c
		}
		a, b = a[len(ra):], b[len(rb):]
	}
	return cmp.Compare(len(a), len(b))
}

// cmpNum orders numeric strings numerically (2 before 10) and anything else
// naturally, so mixed or non-numeric values still sort deterministically.
func cmpNum(a, b string) int {
	x, e1 := strconv.Atoi(a)
	y, e2 := strconv.Atoi(b)
	if e1 == nil && e2 == nil {
		return cmp.Compare(x, y)
	}
	return cmpNatural(a, b)
}

// cmpList orders value lists element by element; a prefix sorts first.
func cmpList(a, b []string) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if c := cmpNum(a[i], b[i]); c != 0 {
			return c
		}
	}
	return cmp.Compare(len(a), len(b))
}

// numSet joins values numerically sorted; "" for an empty set.
func numSet(v []string) string {
	s := slices.Clone(v)
	slices.SortFunc(s, cmpNum)
	return strings.Join(s, ",")
}

// cmpSets orders joined value sets numerically, element by element ("0,1",
// "0,9", "0,10", "2,3", "9", "10,11"); an empty set (unknown) sorts last.
func cmpSets(a, b string) int {
	if (a == "") != (b == "") {
		if a == "" {
			return 1
		}
		return -1
	}
	return cmpList(strings.Split(a, ","), strings.Split(b, ","))
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

// topo returns the exact numeric-sorted NUMA set and the PCIe-root set, "" when unknown.
func topo(dev resourcev1.Device, driver string) (numa, root string) {
	t, err := model.TopologyOf(dev, driver)
	if err != nil {
		return "", ""
	}
	return numSet(t.NUMA.Values), strings.Join(t.PCIeRoot.Values, ",")
}

// detStr reads a Details value as text, "" when absent. It is for values the
// producers store as strings or numbers; structured values go through get.
func detStr(r model.Result, key string) string {
	if v, ok := r.Details[key]; ok && v != nil {
		return fmt.Sprint(v)
	}
	return ""
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// short is a device's last path segment: driver/pool/device -> device.
func short(full string) string { return full[strings.LastIndex(full, "/")+1:] }
