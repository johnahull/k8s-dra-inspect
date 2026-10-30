package view_test

import (
	"testing"

	"github.com/johnahull/k8s-dra-inspect/internal/inspect"
	"github.com/johnahull/k8s-dra-inspect/internal/view"
)

func TestDriversLayout(t *testing.T) {
	got := show(t, fx(t, "multinode"), inspect.Drivers(false), view.Drivers)
	want := joinLines(
		"═══ DRA Drivers ═══",
		"",
		"gpu.example.com: 4 devices on node-a.example.com, node-b.example.com  (2 pools)",
	)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestDriversAttributesOneKeyPerLine(t *testing.T) {
	got := show(t, fx(t, "classes-claims"), inspect.Drivers(true), view.Drivers)
	want := joinLines(
		"═══ DRA Drivers ═══",
		"",
		"gpu.example.com: 2 devices on node-a.example.com  (1 pool)",
		"  attributes:",
		"    resource.kubernetes.io/numaNode",
		"    resource.kubernetes.io/pcieRoot",
	)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestDriversPlaceholderNodesAndIncompletePools(t *testing.T) {
	snap := fx(t, "classes-claims")
	all := true
	snap.ResourceSlices[0].Spec.NodeName = nil
	snap.ResourceSlices[0].Spec.AllNodes = &all
	snap.ResourceSlices[0].Spec.Pool.ResourceSliceCount = 2
	got := show(t, snap, inspect.Drivers(false), view.Drivers)
	for _, want := range []string{
		"gpu.example.com: 2 devices on (all nodes)  (1 pool)",
		"  ! pool has fewer ResourceSlices than expected for its newest generation: node-a.example.com",
	} {
		if !hasLine(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestDriversAttributeBlocksAreSeparated(t *testing.T) {
	got := show(t, fx(t, "xe9680"), inspect.Drivers(true), view.Drivers)
	if !contains(got, "    dra.net/numaNode\n\ndra.memory: 8 devices") {
		t.Errorf("blank line between attribute blocks missing:\n%s", got)
	}
	if plain := show(t, fx(t, "xe9680"), inspect.Drivers(false), view.Drivers); contains(plain, "\n\ndra.memory") {
		t.Errorf("no blank lines between one-line driver entries:\n%s", plain)
	}
}
