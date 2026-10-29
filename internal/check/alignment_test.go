package check

import (
	"fmt"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/utils/ptr"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
)

// gpuPool builds one pool "p" with devices a, b, c carrying the given NUMA values.
func numaPool(vals ...[]int64) []resourcev1.ResourceSlice {
	var devs []resourcev1.Device
	for i, v := range vals {
		devs = append(devs, dev(string(rune('a'+i)), attrs{numa: ints(v...)}))
	}
	return []resourcev1.ResourceSlice{slice("s", "p", 1, 1, devs...)}
}

func allocABC(cons ...resourcev1.DeviceConstraint) resourcev1.ResourceClaim {
	return claim("ns", "c", cons, res("r", "a"), res("r", "b"), res("r", "c"))
}

func align(t *testing.T, s model.Snapshot) model.Result {
	t.Helper()
	return one(t, Alignment("", ""), s)
}

func TestAlignmentConstraintHolds(t *testing.T) {
	s := snapOf(numaPool([]int64{0}, []int64{0}), claim("ns", "c", []resourcev1.DeviceConstraint{match(numa)}, res("r", "a"), res("r", "b")))
	r := align(t, s)
	if r.Status != model.StatusPass || r.Subject != "ns/c" {
		t.Fatalf("%+v", r)
	}
}

func TestAlignmentConstraintViolatedFails(t *testing.T) {
	s := snapOf(numaPool([]int64{0}, []int64{1}), claim("ns", "c", []resourcev1.DeviceConstraint{match(numa)}, res("r", "a"), res("r", "b")))
	r := align(t, s)
	wantStatus(t, r, model.StatusFail, numa)
}

// {0,1} {1,2} {0,2} intersect pairwise but share no common value overall.
func TestAlignmentGlobalIntersectionNotPairwise(t *testing.T) {
	bad := snapOf(numaPool([]int64{0, 1}, []int64{1, 2}, []int64{0, 2}), allocABC(match(numa)))
	if r := align(t, bad); r.Status != model.StatusFail {
		t.Fatalf("pairwise-only overlap must fail: %+v", r)
	}
	good := snapOf(numaPool([]int64{0, 1}, []int64{1, 2}, []int64{1}), allocABC(match(numa)))
	r := align(t, good)
	if r.Status != model.StatusPass {
		t.Fatalf("common value 1 must pass: %+v", r)
	}
	cons := r.Details["constraints"].([]map[string]any)
	if fmt.Sprint(cons[0]["intersection"]) != "[1]" {
		t.Errorf("intersection = %v", cons[0]["intersection"])
	}
}

func TestAlignmentExactLookupNeverSuffixMatches(t *testing.T) {
	d := resourcev1.Device{Name: "a", Attributes: map[resourcev1.QualifiedName]resourcev1.DeviceAttribute{"other.example.com/numaNode": ints(0)}}
	s := snapOf([]resourcev1.ResourceSlice{slice("s", "p", 1, 1, d)}, claim("ns", "c", []resourcev1.DeviceConstraint{match(numa)}, res("r", "a")))
	wantStatus(t, align(t, s), model.StatusUnavailable, "a")
}

func TestAlignmentBareKeyBelongsToDriverDomain(t *testing.T) {
	d := dev("a", attrs{"model": str("x")})
	cons := []resourcev1.DeviceConstraint{match("gpu.example.com/model")}
	ok := snapOf([]resourcev1.ResourceSlice{slice("s", "p", 1, 1, d)}, claim("ns", "c", cons, res("r", "a")))
	if r := align(t, ok); r.Status != model.StatusPass {
		t.Fatalf("driver-domain bare key: %+v", r)
	}
	other := []resourcev1.DeviceConstraint{match("other.example.com/model")}
	bad := snapOf([]resourcev1.ResourceSlice{slice("s", "p", 1, 1, d)}, claim("ns", "c", other, res("r", "a")))
	if r := align(t, bad); r.Status != model.StatusUnavailable {
		t.Fatalf("foreign domain must not match bare key: %+v", r)
	}
}

func TestAlignmentMissingAttributeUnavailableNamesDevice(t *testing.T) {
	s := snapOf([]resourcev1.ResourceSlice{slice("s", "p", 1, 1, dev("a", attrs{numa: ints(0)}), dev("b", nil))},
		claim("ns", "c", []resourcev1.DeviceConstraint{match(numa)}, res("r", "a"), res("r", "b")))
	r := align(t, s)
	wantStatus(t, r, model.StatusUnavailable, "gpu.example.com/p/b")
}

func TestAlignmentRequestScope(t *testing.T) {
	devs := []resourcev1.Device{dev("a", attrs{numa: ints(0)}), dev("b", attrs{numa: ints(0)}), dev("c", attrs{numa: ints(1)}), dev("d", attrs{numa: ints(2)})}
	sl := []resourcev1.ResourceSlice{slice("s", "p", 1, 1, devs...)}
	// gpu and gpu/sub are in scope; gpu2 (prefix without slash) and nic are not.
	c := claim("ns", "c", []resourcev1.DeviceConstraint{match(numa, "gpu")},
		res("gpu", "a"), res("gpu/sub", "b"), res("gpu2", "c"), res("nic", "d"))
	r := align(t, snapOf(sl, c))
	cons := r.Details["constraints"].([]map[string]any)
	if cons[0]["status"] != model.StatusPass {
		t.Fatalf("scoped devices a,b agree: %+v", cons)
	}
	if fmt.Sprint(cons[0]["valuesByDevice"]) != "map[gpu.example.com/p/a:[0] gpu.example.com/p/b:[0]]" {
		t.Errorf("scope = %v", cons[0]["valuesByDevice"])
	}
}

func TestAlignmentEmptyRequestsCoversAllResults(t *testing.T) {
	s := snapOf(numaPool([]int64{0}, []int64{1}), claim("ns", "c", []resourcev1.DeviceConstraint{match(numa)}, res("x", "a"), res("y", "b")))
	if r := align(t, s); r.Status != model.StatusFail {
		t.Fatalf("%+v", r)
	}
}

func TestAlignmentUnconstrainedNUMASplitWarnsNeverFails(t *testing.T) {
	r := align(t, snapOf(numaPool([]int64{0}, []int64{1}), claim("ns", "c", nil, res("r", "a"), res("r", "b"))))
	wantStatus(t, r, model.StatusWarn, "NUMA")
	ok := align(t, snapOf(numaPool([]int64{0, 1}, []int64{1}), claim("ns", "c", nil, res("r", "a"), res("r", "b"))))
	if ok.Status != model.StatusPass {
		t.Fatalf("shared NUMA 1: %+v", ok)
	}
}

func TestAlignmentNUMAConstraintReplacesHeuristic(t *testing.T) {
	r := align(t, snapOf(numaPool([]int64{0}, []int64{1}), claim("ns", "c", []resourcev1.DeviceConstraint{match(numa)}, res("r", "a"), res("r", "b"))))
	if r.Status != model.StatusFail || strings.Contains(fmt.Sprint(r.Details["reason"]), "span NUMA") {
		t.Fatalf("constraint result applies without a second warning: %+v", r)
	}
}

func TestAlignmentPCIeSplitIsInformational(t *testing.T) {
	sl := []resourcev1.ResourceSlice{slice("s", "p", 1, 1,
		dev("a", attrs{numa: ints(0), pcie: str("pci0000:00")}), dev("b", attrs{numa: ints(0), pcie: str("pci0000:80")}))}
	r := align(t, snapOf(sl, claim("ns", "c", nil, res("r", "a"), res("r", "b"))))
	if r.Status != model.StatusPass {
		t.Fatalf("pcie split alone must pass: %+v", r)
	}
	p := r.Details["pcieRoot"].(map[string]any)
	if p["aligned"] != false || !strings.Contains(fmt.Sprint(p["note"]), "informational") || p["key"] != pcie {
		t.Errorf("pcieRoot = %v", p)
	}
	if r.Details["numa"].(map[string]any)["key"] != numa {
		t.Errorf("numa summary lacks key: %v", r.Details["numa"])
	}
	c := align(t, snapOf(sl, claim("ns", "c", []resourcev1.DeviceConstraint{match(pcie)}, res("r", "a"), res("r", "b"))))
	if c.Status != model.StatusFail {
		t.Fatalf("pcieRoot constraint violated: %+v", c)
	}
}

func TestAlignmentUnconstrainedMissingNUMAUnavailable(t *testing.T) {
	sl := []resourcev1.ResourceSlice{slice("s", "p", 1, 1, dev("a", attrs{numa: ints(0)}), dev("b", nil))}
	wantStatus(t, align(t, snapOf(sl, claim("ns", "c", nil, res("r", "a"), res("r", "b")))), model.StatusUnavailable, "NUMA")
}

func TestAlignmentMissingDeviceFailsStaleWarns(t *testing.T) {
	sl := []resourcev1.ResourceSlice{
		slice("old", "p", 1, 1, dev("gone", attrs{numa: ints(0)}), dev("a", attrs{numa: ints(0)})),
		slice("new", "p", 2, 1, dev("a", attrs{numa: ints(0)})),
	}
	wantStatus(t, align(t, snapOf(sl, claim("ns", "c", nil, res("r", "a"), res("r", "nowhere")))), model.StatusFail, "nowhere")
	wantStatus(t, align(t, snapOf(sl, claim("ns", "c", nil, res("r", "a"), res("r", "gone")))), model.StatusWarn, "stale generation")
}

func TestAlignmentInvalidMatchAttributeIsError(t *testing.T) {
	for _, bad := range []string{"numaNode", "a/b/c", "Bad Domain/x", "dom.example.com/"} {
		s := snapOf(numaPool([]int64{0}), claim("ns", "c", []resourcev1.DeviceConstraint{match(bad)}, res("r", "a")))
		wantStatus(t, align(t, s), model.StatusError, "matchAttribute")
	}
}

func TestAlignmentRollupPrecedence(t *testing.T) {
	// constraint 1 fails, constraint 2 unavailable (no such attr), NUMA heuristic would warn.
	sl := []resourcev1.ResourceSlice{slice("s", "p", 1, 1, dev("a", attrs{"x.example.com/k": ints(0)}), dev("b", attrs{"x.example.com/k": ints(1)}))}
	c := claim("ns", "c", []resourcev1.DeviceConstraint{match("x.example.com/k"), match("y.example.com/missing")}, res("r", "a"), res("r", "b"))
	if r := align(t, snapOf(sl, c)); r.Status != model.StatusFail {
		t.Fatalf("fail must beat unavailable: %+v", r)
	}
	c = claim("ns", "c", []resourcev1.DeviceConstraint{match("y.example.com/missing")}, res("r", "a"), res("r", "b"))
	if r := align(t, snapOf(sl, c)); r.Status != model.StatusUnavailable {
		t.Fatalf("unavailable beats warn/pass: %+v", r)
	}
}

func TestAlignmentSkipsUnallocatedWithoutTarget(t *testing.T) {
	s := snapOf(numaPool([]int64{0}), claim("ns", "idle", nil), claim("ns", "used", nil, res("r", "a")))
	r := align(t, s)
	if r.Subject != "ns/used" {
		t.Fatalf("%+v", r)
	}
	if got := run(Alignment("", ""), snapOf(numaPool([]int64{0}), claim("ns", "idle", nil))); len(got) != 0 {
		t.Fatalf("no allocated claims, no results: %+v", got)
	}
}

func targetSnap() model.Snapshot {
	s := snapOf(numaPool([]int64{0}, []int64{0}),
		claim("ns1", "c1", nil, res("r", "a")),
		claim("ns2", "c1", nil, res("r", "b")),
		claim("ns1", "idle", nil),
		claim("ns1", "podclaim", nil, res("r", "a")),
		claim("ns1", "other", nil, res("r", "a")))
	s.Pods = []model.PodRef{{Name: "pod-1", Namespace: "ns1",
		ResourceClaims:        []corev1.PodResourceClaim{{Name: "x", ResourceClaimName: ptr.To("c1")}, {Name: "t", ResourceClaimTemplateName: ptr.To("tmpl")}},
		ResourceClaimStatuses: []corev1.PodResourceClaimStatus{{Name: "t", ResourceClaimName: ptr.To("podclaim")}}}}
	return s
}

func subjects(rs []model.Result) string {
	var s []string
	for _, r := range rs {
		s = append(s, r.Subject+"="+string(r.Status))
	}
	return strings.Join(s, ",")
}

func TestAlignmentTargetResolution(t *testing.T) {
	cases := []struct{ name, arg, ns, want string }{
		{"claim name in two namespaces", "c1", "", "ns1/c1=pass,ns2/c1=pass"},
		{"claim name scoped by -n", "c1", "ns2", "ns2/c1=pass"},
		{"ns/name", "ns1/c1", "", "ns1/c1=pass"},
		{"pod via spec and status", "pod-1", "", "ns1/c1=pass,ns1/podclaim=pass"},
		{"pod scoped by namespace miss", "pod-1", "ns2", "=unavailable"},
		{"unknown", "nope", "", "=unavailable"},
		{"named unallocated", "idle", "", "ns1/idle=unavailable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rs := run(Alignment(tc.arg, tc.ns), targetSnap())
			if got := subjects(rs); got != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
		})
	}
	r := run(Alignment("nope", ""), targetSnap())[0]
	wantStatus(t, r, model.StatusUnavailable, "no claim or pod named nope")
	r = run(Alignment("idle", ""), targetSnap())[0]
	wantStatus(t, r, model.StatusUnavailable, "not allocated")
}

func TestAlignmentNamespaceScopesUntargetedRun(t *testing.T) {
	if got := subjects(run(Alignment("", "ns2"), targetSnap())); got != "ns2/c1=pass" {
		t.Fatalf("got %s", got)
	}
}

func TestAlignmentPCIeAlignedOrConstrainedHasNoNote(t *testing.T) {
	same := []resourcev1.ResourceSlice{slice("s", "p", 1, 1,
		dev("a", attrs{numa: ints(0), pcie: str("r0")}), dev("b", attrs{numa: ints(0), pcie: str("r0")}))}
	if p := align(t, snapOf(same, claim("ns", "c", nil, res("r", "a"), res("r", "b")))).Details["pcieRoot"].(map[string]any); p["note"] != nil {
		t.Errorf("aligned roots carry a note: %v", p)
	}
	split := []resourcev1.ResourceSlice{slice("s", "p", 1, 1,
		dev("a", attrs{numa: ints(0), pcie: str("r0")}), dev("b", attrs{numa: ints(0), pcie: str("r1")}))}
	c := claim("ns", "c", []resourcev1.DeviceConstraint{match(pcie)}, res("r", "a"), res("r", "b"))
	if p := align(t, snapOf(split, c)).Details["pcieRoot"].(map[string]any); p["note"] != nil {
		t.Errorf("a constrained split is reported by the constraint, not as a note: %v", p)
	}
}
