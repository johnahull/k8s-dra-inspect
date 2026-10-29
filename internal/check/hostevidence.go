package check

import (
	"encoding/json"
	"fmt"

	"github.com/johnahull/k8s-dra-inspect/internal/adapters/host"
	"github.com/johnahull/k8s-dra-inspect/internal/model"
)

// loadHost decodes Evidence["host"]. A missing, failed or unsupported
// evidence block yields the single unavailable result to return.
func loadHost(snap model.Snapshot, check string) (host.Evidence, []model.Result, bool) {
	raw, ok := snap.Evidence[host.Name]
	if !ok {
		return host.Evidence{}, single(check, model.StatusUnavailable, "", "no host evidence in snapshot; run on the node"), false
	}
	var ev host.Evidence
	if err := json.Unmarshal(raw, &ev); err != nil {
		return ev, single(check, model.StatusUnavailable, "", "invalid host evidence in snapshot"), false
	}
	switch {
	case ev.Version < host.MinVersion || ev.Version > host.Version:
		return ev, single(check, model.StatusUnavailable, "", fmt.Sprintf("unsupported host evidence version %d", ev.Version)), false
	case ev.Error != "":
		return ev, single(check, model.StatusUnavailable, "", ev.Error), false
	case ev.NodeName == "":
		return ev, single(check, model.StatusUnavailable, "", "evidence has no node name; collect it with --node-name"), false
	}
	return ev, nil, true
}

func offNodeReason(evNode, what, node string) string {
	return fmt.Sprintf("evidence is from node %s; this %s is on node %s: run on node %s", evNode, what, node, node)
}
