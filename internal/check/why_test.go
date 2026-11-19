package check

import (
	"context"
	"strings"
	"testing"

	"github.com/johnahull/k8s-dra-inspect/internal/analyze"
	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/testutil"
)

func TestWhyReportsUnknownTargetAsError(t *testing.T) {
	ok := model.CollectionStatus{State: model.StateOK}
	s := model.Snapshot{Collection: map[string]model.CollectionStatus{
		"resourceslices": ok, "deviceclasses": ok, "resourceclaims": ok, "nodes": ok, "pods": ok, "devicetaintrules": ok,
	}}
	r := Why(context.Background(), s, "missing")
	if len(r) != 1 || r[0].Status != model.StatusError || !strings.Contains(r[0].Details["reason"].(string), "not found") {
		t.Fatalf("unknown why target = %+v", r)
	}
}

func TestWhyNamespaceRequiresClusterwideAllocations(t *testing.T) {
	ok := model.CollectionStatus{State: model.StateOK}
	s := model.Snapshot{Namespace: "team-a", Collection: map[string]model.CollectionStatus{
		"resourceslices": ok, "deviceclasses": ok, "resourceclaims": ok, "nodes": ok, "pods": ok, "devicetaintrules": ok,
	}}
	r := Why(context.Background(), s, "missing")
	if len(r) != 1 || r[0].Status != model.StatusUnavailable || !strings.Contains(r[0].Details["reason"].(string), "globalallocations") {
		t.Fatalf("why without clusterwide allocation evidence = %+v", r)
	}
}

func TestWhyExplainsAllocatedClaimAndPod(t *testing.T) {
	snap := testutil.LoadFixture(t, "testdata/fixtures/classes-claims")
	snap.Collection["devicetaintrules"] = model.CollectionStatus{State: model.StateOK}

	for _, target := range []string{"shared-gpu", "pod/virt-launcher-vm1-abcde"} {
		t.Run(target, func(t *testing.T) {
			results := Why(context.Background(), snap, target)
			if len(results) != 1 {
				t.Fatalf("got %d results, want 1: %+v", len(results), results)
			}
			result := results[0]
			if result.Status != model.StatusPass || result.Details["verdict"] != analyze.VerdictAllocated {
				t.Fatalf("why result = %+v, want pass/ALLOCATED", result)
			}
			explanation, ok := result.Details["explanation"].(*analyze.Explanation)
			if !ok || explanation.Claim != "demo/shared-gpu" || len(explanation.Allocation) != 1 || explanation.Allocation[0].Device != "gpu-0" {
				t.Fatalf("allocation explanation = %+v, want demo/shared-gpu on gpu-0", result.Details["explanation"])
			}
		})
	}
}

func TestWhyCanMatchEventsAfterSnapshotRedaction(t *testing.T) {
	snap := model.Redact(testutil.LoadFixture(t, "testdata/fixtures/analyze/why"))
	snap.Collection["devicetaintrules"] = model.CollectionStatus{State: model.StateOK}
	results := Why(context.Background(), snap, "bound")
	if len(results) != 1 {
		t.Fatalf("why results = %+v, want one result", results)
	}
	explanation, ok := results[0].Details["explanation"].(*analyze.Explanation)
	if !ok || len(explanation.Events) != 1 || explanation.Events[0] != "FailedScheduling" {
		t.Fatalf("redacted snapshot explanation = %+v, want safe FailedScheduling reason", results[0].Details["explanation"])
	}
}
