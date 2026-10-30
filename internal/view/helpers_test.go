package view_test

import (
	"bytes"
	"context"
	"github.com/johnahull/k8s-dra-inspect/internal/check"
	"strings"
	"testing"

	resourcev1 "k8s.io/api/resource/v1"

	"github.com/johnahull/k8s-dra-inspect/internal/inspect"
	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/render"
	"github.com/johnahull/k8s-dra-inspect/internal/testutil"
)

type viewFunc = func(*render.Printer, model.Snapshot, []model.Result)

func fx(t *testing.T, name string) model.Snapshot {
	t.Helper()
	return testutil.LoadFixture(t, "testdata/fixtures/"+name)
}

// show runs the inspector on snap and prints it through v with colour off.
func show(t *testing.T, snap model.Snapshot, in inspect.Inspector, v viewFunc) string {
	t.Helper()
	var b bytes.Buffer
	p := render.NewPrinter(&b, render.Style{})
	v(p, snap, in.Run(snap))
	if err := p.Err(); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func strp(s string) *string { return &s }

// setAttr sets a string attribute on the named device of every slice.
func setAttr(snap *model.Snapshot, device, key, val string) {
	for i := range snap.ResourceSlices {
		for j := range snap.ResourceSlices[i].Spec.Devices {
			d := &snap.ResourceSlices[i].Spec.Devices[j]
			if d.Name == device {
				d.Attributes[resourcev1.QualifiedName(key)] = resourcev1.DeviceAttribute{StringValue: &val}
			}
		}
	}
}

func hasLine(out, line string) bool {
	return slicesContains(strings.Split(out, "\n"), line)
}

func slicesContains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// showCheck runs the check on snap and prints it through v with colour off.
func showCheck(t *testing.T, snap model.Snapshot, c check.Check, v viewFunc) string {
	t.Helper()
	return showResults(t, snap, c(context.Background(), snap), v)
}

// showResults prints hand-built results through v with colour off.
func showResults(t *testing.T, snap model.Snapshot, rs []model.Result, v viewFunc) string {
	t.Helper()
	var b bytes.Buffer
	p := render.NewPrinter(&b, render.Style{})
	v(p, snap, rs)
	if err := p.Err(); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func lines(s ...string) string { return strings.Join(s, "\n") + "\n" }
