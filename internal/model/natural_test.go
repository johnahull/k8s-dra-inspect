package model

import (
	"slices"
	"testing"
)

func TestCompareNatural(t *testing.T) {
	// Equal values tie-break on the shorter digit run: nic-1 before nic-01.
	sorted := []string{"gpu-0", "gpu-1", "gpu-2", "gpu-9", "gpu-10", "gpu-11", "gpu-100", "mig-1g.10gb-0", "nic", "nic-0", "nic-1", "nic-01", "pci0000:20", "pci0000:100"}
	shuffled := []string{"gpu-10", "pci0000:100", "nic-1", "gpu-2", "mig-1g.10gb-0", "gpu-100", "gpu-0", "nic", "gpu-11", "nic-01", "pci0000:20", "gpu-9", "nic-0", "gpu-1"}
	slices.SortFunc(shuffled, CompareNatural)
	if !slices.Equal(shuffled, sorted) {
		t.Errorf("got  %v\nwant %v", shuffled, sorted)
	}
	if CompareNatural("a", "a") != 0 || CompareNatural("01", "1") <= 0 {
		t.Error("equal strings must compare 0 and 01 must sort after 1")
	}
}
