package check

import (
	"testing"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
)

func TestCollectionNotOKIsOneUnavailable(t *testing.T) {
	cases := []struct {
		name string
		c    Check
		key  string
	}{
		{"alignment/slices", Alignment("", ""), "resourceslices"},
		{"alignment/claims", Alignment("", ""), "resourceclaims"},
		{"counters/slices", Counters(), "resourceslices"},
		{"counters/claims", Counters(), "resourceclaims"},
		{"capacity/slices", Capacity(), "resourceslices"},
		{"capacity/claims", Capacity(), "resourceclaims"},
		{"topology/slices", Topology(), "resourceslices"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := snapOf(nil)
			s.Collection[tc.key] = model.CollectionStatus{State: model.StateForbidden, Reason: "forbidden: cannot list " + tc.key}
			r := one(t, tc.c, s)
			wantStatus(t, r, model.StatusUnavailable, "cannot list "+tc.key)
		})
	}
}

func TestChecksHaveNames(t *testing.T) {
	s := snapOf(nil)
	s.Collection["resourceslices"] = model.CollectionStatus{State: model.StateError}
	for want, c := range map[string]Check{"check.alignment": Alignment("", ""), "check.counters": Counters(), "check.capacity": Capacity(), "check.topology": Topology()} {
		if got := one(t, c, s).Check; got != want {
			t.Errorf("check name = %q, want %q", got, want)
		}
	}
}
