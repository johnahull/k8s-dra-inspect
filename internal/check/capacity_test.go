package check

import (
	"fmt"
	"testing"

	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
)

func capDev(name string, caps map[string]string, policy map[string]*resourcev1.CapacityRequestPolicy) resourcev1.Device {
	d := resourcev1.Device{Name: name, Capacity: map[resourcev1.QualifiedName]resourcev1.DeviceCapacity{}}
	for k, v := range caps {
		d.Capacity[resourcev1.QualifiedName(k)] = resourcev1.DeviceCapacity{Value: q(v), RequestPolicy: policy[k]}
	}
	return d
}

func consumed(device string, caps map[string]string) resourcev1.DeviceRequestAllocationResult {
	r := res("r", device)
	r.ConsumedCapacity = map[resourcev1.QualifiedName]resource.Quantity{}
	for k, v := range caps {
		r.ConsumedCapacity[resourcev1.QualifiedName(k)] = q(v)
	}
	return r
}

func capOne(t *testing.T, s model.Snapshot) model.Result { t.Helper(); return one(t, Capacity(), s) }

func memPool(policy map[string]*resourcev1.CapacityRequestPolicy) []resourcev1.ResourceSlice {
	return []resourcev1.ResourceSlice{slice("s", "p", 1, 1, capDev("mem", map[string]string{"size": "100Gi", "cpu": "32"}, policy))}
}

func TestCapacityNoConsumersIsOnePass(t *testing.T) {
	wantStatus(t, capOne(t, snapOf(memPool(nil), claim("ns", "c", nil, res("r", "mem")))), model.StatusPass, "no consumable capacity")
}

func TestCapacityWithinPublishedPasses(t *testing.T) {
	r := capOne(t, snapOf(memPool(nil), claim("ns", "c", nil, consumed("mem", map[string]string{"size": "40Gi"}))))
	if r.Status != model.StatusPass || r.Subject != "gpu.example.com/p/mem" {
		t.Fatalf("%+v", r)
	}
	got := fmt.Sprint(r.Details["capacities"].(map[string]map[string]string)["size"])
	if got != "map[consumed:40Gi published:100Gi remaining:60Gi]" {
		t.Errorf("size = %s", got)
	}
}

func TestCapacitySingleConsumptionOverPublishedFails(t *testing.T) {
	r := capOne(t, snapOf(memPool(nil), claim("ns", "c", nil, consumed("mem", map[string]string{"size": "101Gi"}))))
	wantStatus(t, r, model.StatusFail, "size")
}

func TestCapacitySumAcrossClaimsOverPublishedFails(t *testing.T) {
	s := snapOf(memPool(nil),
		claim("ns", "a", nil, consumed("mem", map[string]string{"size": "60Gi"})),
		claim("ns", "b", nil, consumed("mem", map[string]string{"size": "60Gi"})))
	r := capOne(t, s)
	wantStatus(t, r, model.StatusFail, "120Gi")
	exact := snapOf(memPool(nil),
		claim("ns", "a", nil, consumed("mem", map[string]string{"size": "60Gi"})),
		claim("ns", "b", nil, consumed("mem", map[string]string{"size": "40Gi"})))
	if r := capOne(t, exact); r.Status != model.StatusPass {
		t.Fatalf("exactly full: %+v", r)
	}
}

func TestCapacityUnpublishedNameFails(t *testing.T) {
	r := capOne(t, snapOf(memPool(nil), claim("ns", "c", nil, consumed("mem", map[string]string{"gpu-memory": "1"}))))
	wantStatus(t, r, model.StatusFail, "gpu-memory is not published")
}

func TestCapacityBareNameBelongsToDriverDomain(t *testing.T) {
	s := snapOf(memPool(nil), claim("ns", "c", nil, consumed("mem", map[string]string{"gpu.example.com/size": "1Gi"})))
	if r := capOne(t, s); r.Status != model.StatusPass {
		t.Fatalf("%+v", r)
	}
	s = snapOf(memPool(nil), claim("ns", "c", nil, consumed("mem", map[string]string{"other.example.com/size": "1Gi"})))
	if r := capOne(t, s); r.Status != model.StatusFail {
		t.Fatalf("foreign domain must not match bare key: %+v", r)
	}
}

func TestCapacityRequestPolicyRange(t *testing.T) {
	min, max := q("10Gi"), q("50Gi")
	pol := map[string]*resourcev1.CapacityRequestPolicy{"size": {ValidRange: &resourcev1.CapacityRequestPolicyRange{Min: &min, Max: &max}}}
	for size, want := range map[string]model.Status{"5Gi": model.StatusFail, "10Gi": model.StatusPass, "50Gi": model.StatusPass, "51Gi": model.StatusFail} {
		r := capOne(t, snapOf(memPool(pol), claim("ns", "c", nil, consumed("mem", map[string]string{"size": size}))))
		if r.Status != want {
			t.Errorf("size %s: %s want %s (%v)", size, r.Status, want, r.Details["reason"])
		}
	}
	r := capOne(t, snapOf(memPool(pol), claim("ns", "c", nil, consumed("mem", map[string]string{"size": "5Gi"}))))
	wantStatus(t, r, model.StatusFail, "below the policy minimum")
}

func TestCapacityStaleWarnsMissingFails(t *testing.T) {
	sl := []resourcev1.ResourceSlice{
		slice("old", "p", 1, 1, capDev("gone", map[string]string{"size": "1Gi"}, nil)),
		slice("new", "p", 2, 1, capDev("mem", map[string]string{"size": "1Gi"}, nil)),
	}
	r := capOne(t, snapOf(sl, claim("ns", "c", nil, consumed("gone", map[string]string{"size": "1Gi"}))))
	wantStatus(t, r, model.StatusWarn, "stale generation")
	r = capOne(t, snapOf(sl, claim("ns", "c", nil, consumed("nowhere", map[string]string{"size": "1Gi"}))))
	wantStatus(t, r, model.StatusFail, "nowhere")
}

func TestCapacitySummaryShowsConsumedOverPublished(t *testing.T) {
	r := capOne(t, snapOf(memPool(nil), claim("ns", "c", nil, consumed("mem", map[string]string{"size": "40Gi"}))))
	if r.Details["summary"] != "size 40Gi/100Gi" {
		t.Errorf("summary = %v", r.Details["summary"])
	}
}

func TestCapacityConsumingExactlyAllPasses(t *testing.T) {
	r := capOne(t, snapOf(memPool(nil), claim("ns", "c", nil, consumed("mem", map[string]string{"size": "100Gi"}))))
	if r.Status != model.StatusPass {
		t.Fatalf("%+v", r)
	}
}
