package view_test

import (
	"regexp"
	"testing"

	"github.com/johnahull/k8s-dra-inspect/internal/inspect"
	"github.com/johnahull/k8s-dra-inspect/internal/view"
)

func TestAttributesLayoutAlignsEquals(t *testing.T) {
	got := show(t, fx(t, "classes-claims"), inspect.Attributes(false, false), view.Attributes)
	want := joinLines(
		"═══ DRA Device Attributes ═══",
		"",
		"gpu.example.com (2 devices)",
		"  gpu-0  (pool node-a.example.com, node node-a.example.com)",
		"    resource.kubernetes.io/numaNode = 0",
		"    resource.kubernetes.io/pcieRoot = pci0000:00",
		"  gpu-1  (pool node-a.example.com, node node-a.example.com)",
		"    resource.kubernetes.io/numaNode = 1",
		"    resource.kubernetes.io/pcieRoot = pci0000:80",
	)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestAttributesListValuesAndCapacity(t *testing.T) {
	got := show(t, fx(t, "unicode-long"), inspect.Attributes(true, true), view.Attributes)
	// Keys are padded to align the "="; squash the padding to compare content.
	squashed := regexp.MustCompile(` {2,}= `).ReplaceAllString(got, " = ")
	for _, want := range []string{
		"    resource.kubernetes.io/pcieRoot = [pci0000:00, pci0000:80]",
		"    resource.kubernetes.io/numaNode = 1",
		"    capacity memory = 64Gi",
	} {
		if !hasLine(squashed, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if !contains(got, "/channels") || !contains(got, "= [2, 4]") {
		t.Errorf("--all-attributes list value missing:\n%s", got)
	}
	if off := show(t, fx(t, "unicode-long"), inspect.Attributes(false, false), view.Attributes); contains(off, "capacity") || contains(off, "channels") {
		t.Errorf("flags off must hide capacity and non-topology attributes:\n%s", off)
	}
}

func TestAttributesIncompletePoolIsFlagged(t *testing.T) {
	snap := fx(t, "classes-claims")
	snap.ResourceSlices[0].Spec.Pool.ResourceSliceCount = 2
	got := show(t, snap, inspect.Attributes(false, false), view.Attributes)
	if want := "    ! pool has fewer ResourceSlices than expected for its newest generation: node-a.example.com"; !hasLine(got, want) {
		t.Errorf("missing %q in:\n%s", want, got)
	}
}
