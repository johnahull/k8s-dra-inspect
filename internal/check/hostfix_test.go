package check

import (
	"encoding/json"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	"github.com/johnahull/k8s-dra-inspect/internal/adapters/host"
	"github.com/johnahull/k8s-dra-inspect/internal/model"
)

const (
	uidA   = "00000000-0000-4000-8000-000000000001"
	pciKey = "resource.kubernetes.io/pciBusID"
)

// hostEv is a two-NUMA host: cpus 0-7 on node 0, 8-15 on node 1.
func hostEv() host.Evidence {
	return host.Evidence{
		Version: host.Version, NodeName: "node-a",
		NUMA:        map[string]string{"0": "0-7", "1": "8-15"},
		PCI:         map[string]host.PCIDevice{},
		IOMMUGroups: map[string][]string{}, VFIODevNodes: []string{}, CDI: []host.CDISpec{},
		Cgroups: map[string]host.Cgroup{},
	}
}

func withHost(s model.Snapshot, ev host.Evidence) model.Snapshot {
	b, err := json.Marshal(ev)
	if err != nil {
		panic(err)
	}
	s.Evidence = map[string]json.RawMessage{host.Name: b}
	return s
}

func podOn(ns, name, uid, node string, claims ...string) model.PodRef {
	p := model.PodRef{Namespace: ns, Name: name, UID: types.UID(uid), NodeName: node}
	for _, c := range claims {
		p.ResourceClaimStatuses = append(p.ResourceClaimStatuses, corev1.PodResourceClaimStatus{Name: c, ResourceClaimName: ptr.To(c)})
	}
	return p
}
