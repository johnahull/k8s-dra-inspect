package view_test

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/labels"

	"github.com/johnahull/k8s-dra-inspect/internal/inspect"
	"github.com/johnahull/k8s-dra-inspect/internal/view"
)

func contains(s, sub string) bool { return strings.Contains(s, sub) }

func TestDeviceClassesBlocksNeverShowSelectorsOrConfig(t *testing.T) {
	got := show(t, fx(t, "classes-claims"), inspect.DeviceClasses(labels.Everything()), view.DeviceClasses)
	want := joinLines(
		"═══ DeviceClasses ═══",
		"",
		"gpu.example.com",
		"  labels:",
		"    tier=gold",
		"    vendor=example",
		"  selectors: 2 (expressions not shown)",
		"  config: present",
		"",
		"nic.example.com",
		"  labels:",
		"    tier=silver",
		"    vendor=example",
		"  selectors: 0 (expressions not shown)",
		"  config: present",
		"",
		"plain.example.com",
		"  labels: none",
		"  selectors: 1 (expressions not shown)",
		"  config: none",
	)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	for _, bad := range []string{"leakmarker", "opaque-parameter-marker", "device.driver", "secretKnob"} {
		if contains(got, bad) {
			t.Errorf("leaks %q", bad)
		}
	}
}

func TestDeviceClassesSelectorAndEmpty(t *testing.T) {
	sel, _ := labels.Parse("tier=gold")
	got := show(t, fx(t, "classes-claims"), inspect.DeviceClasses(sel), view.DeviceClasses)
	if !contains(got, "gpu.example.com") || contains(got, "nic.example.com") {
		t.Errorf("selector:\n%s", got)
	}
	none, _ := labels.Parse("tier=bronze")
	if got := show(t, fx(t, "classes-claims"), inspect.DeviceClasses(none), view.DeviceClasses); !contains(got, "no DeviceClasses") {
		t.Errorf("empty:\n%s", got)
	}
}
