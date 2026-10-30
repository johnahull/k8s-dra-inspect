package check

import (
	"context"
	"fmt"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
)

// Adapter availability results for the support report.
const (
	NameAdapterHost     = "adapter.host"
	NameAdapterKubeVirt = "adapter.kubevirt"
)

// AdapterHost reports whether usable host evidence is in the snapshot.
func AdapterHost() Check {
	return func(_ context.Context, snap model.Snapshot) []model.Result {
		ev, un, ok := loadHost(snap, NameAdapterHost)
		if !ok {
			return un
		}
		return []model.Result{{Check: NameAdapterHost, Status: model.StatusPass, Details: map[string]any{"summary": "host evidence for node " + ev.NodeName}}}
	}
}

// AdapterKubeVirt reports whether KubeVirt evidence is in, or can be derived
// from, the snapshot.
func AdapterKubeVirt() Check {
	return func(ctx context.Context, snap model.Snapshot) []model.Result {
		ev, un, ok := loadKubeVirt(ctx, snap, NameAdapterKubeVirt)
		if !ok {
			return un
		}
		return []model.Result{{Check: NameAdapterKubeVirt, Status: model.StatusPass, Details: map[string]any{"summary": fmt.Sprintf("%d VMIs", len(ev.VMIs))}}}
	}
}
