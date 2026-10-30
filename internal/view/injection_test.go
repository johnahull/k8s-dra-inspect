package view_test

import (
	"strings"
	"testing"

	"github.com/johnahull/k8s-dra-inspect/internal/check"
	"github.com/johnahull/k8s-dra-inspect/internal/inspect"
	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/view"
	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/labels"
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
	for i := range snap.DeviceClasses {
		snap.DeviceClasses[i].Name += evil
		snap.DeviceClasses[i].Labels = map[string]string{"tier" + evil: "gold" + evil}
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
	{"topology", inspect.Topology(false), view.Topology},
	{"topology-compact", inspect.Topology(true), view.Topology},
	{"drivers", inspect.Drivers(true), view.Drivers},
	{"attributes", inspect.Attributes(true, true), view.Attributes},
	{"deviceclasses", inspect.DeviceClasses(labels.Everything()), view.DeviceClasses},
}

func TestViewsNeutraliseHostileNames(t *testing.T) {
	for _, c := range views {
		out := show(t, evilSnap(t), c.in, c.v)
		if strings.ContainsAny(out, "\x1b\u202e\u2028") {
			t.Errorf("%s: control or bidi characters survived:\n%q", c.name, out)
		}
		if c.name != "topology-compact" && !strings.Contains(out, "\ufffd") {
			t.Errorf("%s: hostile text missing entirely (cleaned text expected):\n%s", c.name, out)
		}
	}
	// Reasons that embed a hostile pool name: incomplete pools in topology, drivers and attributes.
	for _, c := range views[2:5] {
		snap := evilSnap(t)
		snap.ResourceSlices[0].Spec.Pool.Name = "pool" + evil
		snap.ResourceSlices[0].Spec.Pool.ResourceSliceCount = 2
		out := show(t, snap, c.in, c.v)
		if strings.ContainsAny(out, "\x1b\u202e\u2028") || !strings.Contains(out, "fewer ResourceSlices") {
			t.Errorf("%s: reason not neutralised:\n%q", c.name, out)
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

// evilResults has hostile text in every field a check view prints: subjects,
// Details values, Findings, evidence fields such as the IOMMU group.
func evilResults() map[string][]model.Result {
	f := []model.Finding{{Status: model.StatusFail, Text: "f" + evil}, {Status: model.StatusPass, Text: "i" + evil}}
	vals := map[string][]string{"d" + evil + "/p" + evil + "/dev" + evil: {"v" + evil}}
	return map[string][]model.Result{
		"alignment": {{Check: "check.alignment", Status: model.StatusFail, Subject: "ns" + evil + "/c" + evil, Findings: f, Details: map[string]any{
			"matched":     []string{"claim", "pod"},
			"constraints": []map[string]any{{"attribute": "a" + evil + "/numaNode", "status": model.StatusFail, "valuesByDevice": vals, "intersection": []string{}}},
			"numa":        map[string]any{"aligned": false, "constrained": false, "key": "k" + evil, "valuesByDevice": vals, "intersection": []string{}},
			"pcieRoot":    map[string]any{"note": "n" + evil, "valuesByDevice": vals},
		}}},
		"counters": {{Check: "check.counters", Status: model.StatusFail, Subject: "x", Findings: f, Details: map[string]any{
			"driver": "d" + evil, "pool": "p" + evil, "counterSet": "s" + evil, "allocatedBy": []string{"c" + evil},
			"counters": map[string]map[string]string{"n" + evil: {"used": "u" + evil, "capacity": "c" + evil}}}}},
		"capacity": {{Check: "check.capacity", Status: model.StatusFail, Subject: "s" + evil, Findings: f, Details: map[string]any{
			"allocatedBy": []string{"c" + evil}, "capacities": map[string]map[string]string{"n" + evil: {"consumed": "c" + evil, "published": "p" + evil, "remaining": "r" + evil}}}}},
		"topology": {{Check: "check.topology", Status: model.StatusWarn, Subject: "s" + evil, Findings: f[:1], Details: map[string]any{"summary": "sum" + evil}}},
		"kubevirt": {{Check: "check.kubevirt", Status: model.StatusWarn, Subject: "ns" + evil + "/vm" + evil, Findings: f, Details: map[string]any{
			"devices": []map[string]any{{"hostDevice": "h" + evil, "request": "r" + evil, "driver": "d" + evil, "device": "dev" + evil, "pciBusID": "b" + evil, "node": "n" + evil}},
			"numa":    []string{"0" + evil}, "warnings": []string{"w" + evil}}}},
		"cpupinning": {{Check: "check.cpupinning", Status: model.StatusWarn, Subject: "ns" + evil + "/pod" + evil, Findings: f, Details: map[string]any{
			"nodeName": "node" + evil, "cpuset": "c" + evil, "numaOfCpus": []int{1}, "deviceNuma": []int{0}, "pinned": true}}},
		"vfio": {{Check: "check.vfio", Status: model.StatusFail, Subject: "d" + evil + "/p" + evil + "/dev" + evil, Findings: f, Details: map[string]any{
			"bdf": "b" + evil, "driver": "drv" + evil, "iommuGroup": "g" + evil, "cdiPresent": true, "nonVfioPeers": []string{"peer" + evil}}}},
	}
}

func TestCheckViewsNeutraliseHostileText(t *testing.T) {
	for name, v := range map[string]viewFunc{"alignment": view.Alignment, "counters": view.Counters, "capacity": view.Capacity,
		"topology": view.CheckTopology, "kubevirt": view.KubeVirt, "cpupinning": view.CPUPinning, "vfio": view.VFIO} {
		out := showResults(t, evilSnap(t), evilResults()[name], v)
		if strings.ContainsAny(out, "\x1b\u202e\u2028") {
			t.Errorf("%s: control or bidi characters survived:\n%q", name, out)
		}
		if !strings.Contains(out, "�") {
			t.Errorf("%s: hostile text missing entirely:\n%s", name, out)
		}
	}
	// Real checks over a snapshot whose names are hostile.
	snap := evilSnap(t)
	for name, c := range map[string]check.Check{"alignment": check.Alignment("", ""), "capacity": check.Capacity(), "topology": check.Topology(), "counters": check.Counters()} {
		out := showCheck(t, snap, c, map[string]viewFunc{"alignment": view.Alignment, "capacity": view.Capacity, "topology": view.CheckTopology, "counters": view.Counters}[name])
		if strings.ContainsAny(out, "\x1b\u202e\u2028") {
			t.Errorf("real %s: control or bidi characters survived:\n%q", name, out)
		}
	}
}
