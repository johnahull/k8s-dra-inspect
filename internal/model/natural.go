package model

import (
	"cmp"
	"strings"
)

// CompareNatural orders strings with embedded numbers by value, so gpu-2
// comes before gpu-10 and pci0000:20 before pci0000:100. Digit runs compare as
// numbers; equal values tie-break on the shorter run (nic-1 before nic-01) so
// the order is total. Everything else compares byte by byte.
func CompareNatural(a, b string) int {
	for a != "" && b != "" {
		if isDigit(a[0]) && isDigit(b[0]) {
			na, nb := digitRun(a), digitRun(b)
			va, vb := strings.TrimLeft(a[:na], "0"), strings.TrimLeft(b[:nb], "0")
			if c := cmp.Or(cmp.Compare(len(va), len(vb)), cmp.Compare(va, vb), cmp.Compare(na, nb)); c != 0 {
				return c
			}
			a, b = a[na:], b[nb:]
			continue
		}
		if c := cmp.Compare(a[0], b[0]); c != 0 {
			return c
		}
		a, b = a[1:], b[1:]
	}
	return cmp.Compare(len(a), len(b))
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func digitRun(s string) int {
	n := 0
	for n < len(s) && isDigit(s[n]) {
		n++
	}
	return n
}
