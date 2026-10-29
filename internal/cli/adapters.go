package cli

import (
	"context"
	"os"

	"github.com/johnahull/k8s-dra-inspect/internal/adapters/host"
	"github.com/johnahull/k8s-dra-inspect/internal/adapters/kubevirt"
	"github.com/johnahull/k8s-dra-inspect/internal/model"
)

func kubevirtAdapter(snap model.Snapshot) *kubevirt.Adapter {
	return kubevirt.New(kubevirt.Config{VMIs: snap.VMIs, State: snap.Collection["virtualmachineinstances"]})
}

// attachAdapters runs the host adapter (recording why when it is not
// available) and the KubeVirt adapter (only when VMIs were collected), and
// returns snap with their evidence. It reads the node only below cfg.Root.
func attachAdapters(ctx context.Context, d Deps, cfg host.Config, snap model.Snapshot) model.Snapshot {
	snap = attachHost(ctx, d.NewHost, cfg, snap)
	if ev, _, ok := runAdapter(ctx, kubevirt.Name, kubevirtAdapter(snap)); ok {
		snap = withEvidence(snap, kubevirt.Name, ev)
	}
	return snap
}

func hostConfig(root, nodeName string) host.Config {
	return host.Config{Root: root, NodeName: resolveNodeName(nodeName, os.Getenv, os.Hostname)}
}
