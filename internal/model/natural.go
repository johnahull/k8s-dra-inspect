package model

import (
	"cmp"
	"strings"
)

// CompareNatural orders strings with embedded numbers by value, so gpu-2
// comes before gpu-10 and pci0000:20 before pci0000:100. The strings are split
// into alternating digit and non-digit runs: digit runs compare as numbers,
// with equal values tie-broken on the shorter run (nic-1 before nic-01) so
// the order is total; other runs compare as whole strings (numa1 before
// numa-1).
func CompareNatural(a, b string) int {
	for a != "" && b != "" {
		da, db := isDigit(a[0]), isDigit(b[0])
		ra, rb := leadingRun(a, da), leadingRun(b, db)
		if da && db {
			x, y := strings.TrimLeft(ra, "0"), strings.TrimLeft(rb, "0")
			if c := cmp.Or(cmp.Compare(len(x), len(y)), cmp.Compare(x, y), cmp.Compare(len(ra), len(rb))); c != 0 {
				return c
			}
		} else if c := cmp.Compare(ra, rb); c != 0 {
			return c
		}
		a, b = a[len(ra):], b[len(rb):]
	}
	return cmp.Compare(len(a), len(b))
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// leadingRun is the leading part of s made of digits (when digits) or of
// anything else.
func leadingRun(s string, digits bool) string {
	i := 0
	for i < len(s) && isDigit(s[i]) == digits {
		i++
	}
	return s[:i]
}
