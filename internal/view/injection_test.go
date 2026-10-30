package view_test

import (
	"strings"
	"testing"

	"github.com/johnahull/k8s-dra-inspect/internal/inspect"
	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/view"
	resourcev1 "k8s.io/api/resource/v1"
)

const evil = "x\x1b[31mRED\u202eRTL\u2028LS"

// evilSnap puts hostile text into every name a driver or user controls: the
// device, productName, driver, pod and claim names, and collection reasons.
func evilSnap(t *testing.T) model.Snapshot {
	snap := fx(t, "classes-claims")
	driver := "gpu.example.com" + evil
	for i := range snap.ResourceSlices {
		s := &snap.ResourceSlices[i]
		s.Spec.Driver = driver
		s.Spec.Devices[0].Name = "gpu-0" + evil
		s.Spec.Devices[0].Attributes["productName"] = resourcev1.DeviceAttribute{StringValue: strp("MI" + evil)}
		s.Spec.Devices[0].Attributes["resource.kubernetes.io/pciBusID"] = resourcev1.DeviceAttribute{StringValue: strp("0000:" + evil)}
	}
	for i := range snap.ResourceClaims {
		c := &snap.ResourceClaims[i]
		if c.Name == "shared-gpu" {
			c.Name += evil
		}
		if c.Status.Allocation == nil {
			continue
		}
		for j := range c.Status.Allocation.Devices.Results {
			r := &c.Status.Allocation.Devices.Results[j]
			r.Driver = driver
			if r.Device == "gpu-0" {
				r.Device += evil
			}
		}
	}
	for i := range snap.Pods {
		snap.Pods[i].Name += evil
		for j := range snap.Pods[i].ResourceClaims {
			if snap.Pods[i].ResourceClaims[j].ResourceClaimName != nil {
				snap.Pods[i].ResourceClaims[j].ResourceClaimName = strp("shared-gpu" + evil)
			}
		}
	}
	snap.Collection["virtualmachineinstances"] = model.CollectionStatus{State: model.StateError, Reason: "vmi" + evil}
	return snap
}

// views lists every text view with the inspector that feeds it.
var views = []struct {
	name string
	in   inspect.Inspector
	v    viewFunc
}{
	{"slices", inspect.Slices(), view.Slices},
	{"claims", inspect.Claims(), view.Claims},
}

func TestViewsNeutraliseHostileNames(t *testing.T) {
	for _, c := range views {
		out := show(t, evilSnap(t), c.in, c.v)
		if strings.ContainsAny(out, "\x1b\u202e\u2028") {
			t.Errorf("%s: control or bidi characters survived:\n%q", c.name, out)
		}
		if !strings.Contains(out, "gpu-0x") {
			t.Errorf("%s: hostile device name missing entirely (cleaned text expected):\n%s", c.name, out)
		}
	}
	// The reason strings come out too: usage and VMI unavailability.
	snap := evilSnap(t)
	snap.Collection["resourceclaims"] = model.CollectionStatus{State: model.StateError, Reason: "claims" + evil}
	if out := show(t, snap, inspect.Slices(), view.Slices); strings.ContainsAny(out, "\x1b\u202e\u2028") || !strings.Contains(out, "usage unavailable: claimsx") {
		t.Errorf("slices reason:\n%q", out)
	}
	if out := show(t, evilSnap(t), inspect.Claims(), view.Claims); !strings.Contains(out, "vmi unavailable: vmix") {
		t.Errorf("claims reason:\n%q", out)
	}
}
