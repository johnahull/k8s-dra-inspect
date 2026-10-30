package check

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
)

const (
	drv  = "gpu.example.com"
	numa = "resource.kubernetes.io/numaNode"
	pcie = "resource.kubernetes.io/pcieRoot"
)

type attrs map[string]resourcev1.DeviceAttribute

func ints(v ...int64) resourcev1.DeviceAttribute {
	if len(v) == 1 {
		return resourcev1.DeviceAttribute{IntValue: &v[0]}
	}
	return resourcev1.DeviceAttribute{IntValues: v}
}

func str(s string) resourcev1.DeviceAttribute { return resourcev1.DeviceAttribute{StringValue: &s} }

func dev(name string, a attrs) resourcev1.Device {
	d := resourcev1.Device{Name: name, Attributes: map[resourcev1.QualifiedName]resourcev1.DeviceAttribute{}}
	for k, v := range a {
		d.Attributes[resourcev1.QualifiedName(k)] = v
	}
	return d
}

func slice(name, pool string, gen, count int64, devs ...resourcev1.Device) resourcev1.ResourceSlice {
	return slicePool(name, drv, pool, gen, count, devs...)
}

func slicePool(name, driver, pool string, gen, count int64, devs ...resourcev1.Device) resourcev1.ResourceSlice {
	s := resourcev1.ResourceSlice{ObjectMeta: metav1.ObjectMeta{Name: name}}
	s.Spec.Driver = driver
	s.Spec.NodeName = ptr.To("node-a")
	s.Spec.Pool = resourcev1.ResourcePool{Name: pool, Generation: gen, ResourceSliceCount: count}
	s.Spec.Devices = devs
	return s
}

func res(req, device string) resourcev1.DeviceRequestAllocationResult {
	return resourcev1.DeviceRequestAllocationResult{Request: req, Driver: drv, Pool: "p", Device: device}
}

func claim(ns, name string, cons []resourcev1.DeviceConstraint, results ...resourcev1.DeviceRequestAllocationResult) resourcev1.ResourceClaim {
	c := resourcev1.ResourceClaim{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name}}
	c.Spec.Devices.Constraints = cons
	if len(results) > 0 {
		c.Status.Allocation = &resourcev1.AllocationResult{}
		c.Status.Allocation.Devices.Results = results
	}
	return c
}

func match(attr string, reqs ...string) resourcev1.DeviceConstraint {
	a := resourcev1.FullyQualifiedName(attr)
	return resourcev1.DeviceConstraint{Requests: reqs, MatchAttribute: &a}
}

func snapOf(slices []resourcev1.ResourceSlice, claims ...resourcev1.ResourceClaim) model.Snapshot {
	s := model.Snapshot{Collection: map[string]model.CollectionStatus{}, ResourceSlices: slices, ResourceClaims: claims}
	for _, k := range []string{"resourceslices", "resourceclaims", "pods", "deviceclasses", "nodes", "virtualmachineinstances"} {
		s.Collection[k] = model.CollectionStatus{State: model.StateOK}
	}
	return s
}

func q(s string) resource.Quantity { return resource.MustParse(s) }

func run(c Check, s model.Snapshot) []model.Result { return c(context.Background(), s) }

// one runs c and requires exactly one result.
func one(t *testing.T, c Check, s model.Snapshot) model.Result {
	t.Helper()
	r := run(c, s)
	if len(r) != 1 {
		t.Fatalf("want 1 result, got %d: %+v", len(r), r)
	}
	return r[0]
}

func wantStatus(t *testing.T, r model.Result, st model.Status, reasonHas string) {
	t.Helper()
	if r.Status != st {
		t.Fatalf("status = %s, want %s: %+v", r.Status, st, r)
	}
	if got, _ := r.Details["reason"].(string); !strings.Contains(got, reasonHas) {
		t.Fatalf("reason %q lacks %q", got, reasonHas)
	}
	assertFindings(t, r)
}

// assertFindings checks the Findings invariant: the reason is the "; "-join of
// the finding texts and the worst finding is the result's status.
func assertFindings(t *testing.T, r model.Result) {
	t.Helper()
	if r.Status == model.StatusPass && len(r.Findings) == 0 {
		return // informational reasons on a pass carry no finding
	}
	var texts []string
	st := model.StatusPass
	for _, f := range r.Findings {
		texts = append(texts, f.Text)
		if rollupRank[f.Status] > rollupRank[st] {
			st = f.Status
		}
	}
	if reason, _ := r.Details["reason"].(string); strings.Join(texts, "; ") != reason {
		t.Errorf("%s %s: findings %q do not join to reason %q", r.Check, r.Subject, texts, reason)
	}
	if st != r.Status {
		t.Errorf("%s %s: worst finding %s != status %s", r.Check, r.Subject, st, r.Status)
	}
}

var _ = corev1.Pod{}
