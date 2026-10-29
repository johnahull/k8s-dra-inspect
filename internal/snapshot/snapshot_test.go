package snapshot_test

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	resourcev1 "k8s.io/api/resource/v1"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/snapshot"
	"github.com/johnahull/k8s-dra-inspect/internal/testutil"
)

func fixture(t *testing.T) model.Snapshot {
	t.Helper()
	return testutil.LoadFixture(t, "testdata/fixtures/classes-claims")
}

func capture(t *testing.T, s model.Snapshot) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := snapshot.Capture(&b, s); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func write(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "s.json")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRoundTrip(t *testing.T) {
	orig := fixture(t)
	orig.Namespace = "team-a"
	orig.Collection["pods"] = model.CollectionStatus{State: model.StateForbidden, Reason: "no"}
	got, err := snapshot.Read(bytes.NewReader(capture(t, orig)))
	if err != nil {
		t.Fatal(err)
	}
	orig.Source = model.SourceSnapshot
	// RawExtension keeps the indentation JSON gave it, so classes and claims
	// compare by canonical bytes; everything else must be deeply equal.
	if !bytes.Equal(capture(t, orig), capture(t, got)) {
		t.Fatalf("round trip differs:\n%s\n---\n%s", capture(t, orig), capture(t, got))
	}
	orig.DeviceClasses, orig.ResourceClaims, got.DeviceClasses, got.ResourceClaims = nil, nil, nil, nil
	if !reflect.DeepEqual(orig, got) {
		t.Fatalf("round trip not deeply equal:\n%+v\n---\n%+v", orig, got)
	}
}

func TestCaptureShape(t *testing.T) {
	s := fixture(t)
	s.SchemaVersion = ""
	out := capture(t, s)
	if !strings.Contains(string(out), `"schemaVersion": "`+model.SchemaVersion+`"`) || !bytes.HasSuffix(out, []byte("}\n")) {
		t.Fatalf("want indented JSON with schemaVersion and trailing newline, got:\n%.200s", out)
	}
}

func TestCaptureDeterministic(t *testing.T) {
	a, b := fixture(t), fixture(t)
	r := rand.New(rand.NewPCG(1, 2))
	r.Shuffle(len(b.ResourceSlices), func(i, j int) { b.ResourceSlices[i], b.ResourceSlices[j] = b.ResourceSlices[j], b.ResourceSlices[i] })
	r.Shuffle(len(b.DeviceClasses), func(i, j int) { b.DeviceClasses[i], b.DeviceClasses[j] = b.DeviceClasses[j], b.DeviceClasses[i] })
	r.Shuffle(len(b.ResourceClaims), func(i, j int) { b.ResourceClaims[i], b.ResourceClaims[j] = b.ResourceClaims[j], b.ResourceClaims[i] })
	r.Shuffle(len(b.Pods), func(i, j int) { b.Pods[i], b.Pods[j] = b.Pods[j], b.Pods[i] })
	r.Shuffle(len(b.VMIs), func(i, j int) { b.VMIs[i], b.VMIs[j] = b.VMIs[j], b.VMIs[i] })
	slices.Reverse(b.DeviceClasses)
	slices.Reverse(b.ResourceClaims)
	if len(a.DeviceClasses) < 2 || len(a.ResourceClaims) < 2 {
		t.Fatal("fixture too small to prove ordering")
	}
	if out := capture(t, a); !bytes.Contains(out, []byte(a.ResourceClaims[0].Name)) {
		t.Fatalf("capture produced no claims:\n%.100s", out)
	}
	if !bytes.Equal(capture(t, a), capture(t, b)) {
		t.Fatal("capture output depends on input order")
	}
}

func TestReadValidation(t *testing.T) {
	cases := []struct{ name, content, want string }{
		{"missing", `{"resourceSlices":[]}`, "not a dra-inspect snapshot (no schemaVersion)"},
		{"wrong", `{"schemaVersion":"dra-inspect/v0"}`, `snapshot schemaVersion "dra-inspect/v0" is not supported (this dra-inspect supports "` + model.SchemaVersion + `"); re-capture with this dra-inspect version`},
		{"malformed", `{"schemaVersion":`, "S.json: "},
	}
	for _, c := range cases {
		p := write(t, c.content)
		_, err := snapshot.Load(p)
		want := strings.ReplaceAll(c.want, "S.json", p)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err=%v want contains %q", c.name, err, want)
		}
	}
	missing := filepath.Join(t.TempDir(), "nope.json")
	if _, err := snapshot.Load(missing); err == nil || !strings.Contains(err.Error(), missing) {
		t.Errorf("unreadable: err=%v", err)
	}
}

func TestReadNormalizesAndMarksReplay(t *testing.T) {
	s := fixture(t)
	s.Source = model.SourceLive
	slices.Reverse(s.ResourceClaims)
	b, err := json.Marshal(s) // deliberately unsorted, as a hand-edited file might be
	if err != nil {
		t.Fatal(err)
	}
	got, err := snapshot.Read(bytes.NewReader(b))
	if err != nil || got.Source != model.SourceSnapshot || !slices.IsSortedFunc(got.ResourceClaims, func(a, b resourcev1.ResourceClaim) int {
		return cmp.Compare(a.Name, b.Name)
	}) {
		t.Fatalf("err=%v source=%q claims=%v", err, got.Source, got.ResourceClaims[0].Name)
	}
}

func TestFileSource(t *testing.T) {
	p := write(t, string(capture(t, fixture(t))))
	s, err := snapshot.NewFileSource(p).Snapshot(context.Background())
	if err != nil || s.Source != model.SourceSnapshot || len(s.ResourceClaims) == 0 {
		t.Fatalf("err=%v source=%q claims=%d", err, s.Source, len(s.ResourceClaims))
	}
}

func TestScope(t *testing.T) {
	s := fixture(t)
	s.Collection["resourceclaims"] = model.CollectionStatus{State: model.StateOK}
	// Move the first of each kind to another namespace so a filter has work to do.
	s.ResourceClaims[0].Namespace = "other"
	s.Pods[0].Namespace = "other"
	s.VMIs[0].SetNamespace("other")
	ns := s.ResourceClaims[1].Namespace
	got, err := snapshot.Scope(s, ns)
	if err != nil {
		t.Fatal(err)
	}
	if got.Namespace != ns || len(got.ResourceClaims) == 0 || len(got.ResourceClaims) >= len(s.ResourceClaims) {
		t.Fatalf("namespace=%q claims=%d/%d", got.Namespace, len(got.ResourceClaims), len(s.ResourceClaims))
	}
	for _, c := range got.ResourceClaims {
		if c.Namespace != ns {
			t.Errorf("claim from %s leaked", c.Namespace)
		}
	}
	for _, p := range got.Pods {
		if p.Namespace != ns {
			t.Errorf("pod from %s leaked", p.Namespace)
		}
	}
	for _, v := range got.VMIs {
		if v.GetNamespace() != ns {
			t.Errorf("vmi from %s leaked", v.GetNamespace())
		}
	}
	if st := got.Collection["resourceclaims"]; st.State != model.StateOK || st.Reason != "namespace "+ns+" only" {
		t.Errorf("collection note: %+v", st)
	}
	if len(s.ResourceClaims) == len(got.ResourceClaims) {
		t.Error("input mutated or not filtered")
	}

	// Cannot widen a namespace-scoped snapshot.
	scoped := got
	if _, err := snapshot.Scope(scoped, "other"); err == nil ||
		!strings.Contains(err.Error(), `captured for namespace "`+ns+`"; cannot widen to "other"`) {
		t.Fatalf("err=%v", err)
	}
	if same, err := snapshot.Scope(scoped, ns); err != nil || same.Namespace != ns {
		t.Fatalf("same ns: %v", err)
	}
	if empty, err := snapshot.Scope(scoped, ""); err != nil || empty.Namespace != ns {
		t.Fatalf("empty ns must keep file scope: %v %q", err, empty.Namespace)
	}
}

// The golden file doubles as a sample snapshot and proves old files stay loadable.
func TestGoldenSnapshot(t *testing.T) {
	s := model.Redact(testutil.LoadFixture(t, "testdata/fixtures/xe9785l"))
	out := capture(t, s)
	testutil.Golden(t, "../snapshots/xe9785l.json", out)
	loaded, err := snapshot.Read(bytes.NewReader(out))
	if err != nil || len(loaded.ResourceSlices) == 0 || !loaded.Redacted {
		t.Fatalf("err=%v slices=%d", err, len(loaded.ResourceSlices))
	}
}
