package testutil

import "testing"

func TestLoadFixtureReadsYAMLDocuments(t *testing.T) {
	snap := LoadFixture(t, "testdata/fixtures/analyze/allocations")
	if got := len(snap.ResourceClaims); got != 7 {
		t.Fatalf("loaded %d ResourceClaims, want 7", got)
	}
}

func TestLoadFixtureReadsDRADeviceTaintRules(t *testing.T) {
	snap := LoadFixture(t, "testdata/fixtures/analyze/devices")
	if got := len(snap.DeviceTaintRules); got != 2 {
		t.Fatalf("loaded %d DeviceTaintRules, want 2", got)
	}
}

func TestLoadFixtureReadsEvents(t *testing.T) {
	snap := LoadFixture(t, "testdata/fixtures/analyze/why")
	if got := len(snap.Events); got != 1 {
		t.Fatalf("loaded %d Events, want 1", got)
	}
}
