// Package kubevirt derives evidence from the VirtualMachineInstances already in
// a snapshot. It makes no API calls and stores only the fields the kubevirt
// check needs.
package kubevirt

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
)

// Name is the key under Snapshot.Evidence.
const Name = "kubevirt"

// Version is the evidence schema version.
const Version = 1

// HostDevice is one VMI host device that names a DRA claim.
type HostDevice struct {
	Name        string `json:"name"`
	ClaimAlias  string `json:"claimAlias"`
	ClaimName   string `json:"claimName,omitempty"` // resolved through spec.resourceClaims; empty when unresolved
	RequestName string `json:"requestName"`
}

// VMI is the producer-redacted view of one VirtualMachineInstance.
type VMI struct {
	Namespace             string            `json:"namespace"`
	Name                  string            `json:"name"`
	NodeName              string            `json:"nodeName,omitempty"`
	Phase                 string            `json:"phase,omitempty"`
	Claims                map[string]string `json:"claims"`
	HostDevices           []HostDevice      `json:"hostDevices"`
	GuestNUMAPassthrough  bool              `json:"guestNUMAPassthrough"`
	DedicatedCPUPlacement bool              `json:"dedicatedCPUPlacement"`
	Sockets               int64             `json:"sockets,omitempty"`
	Cores                 int64             `json:"cores,omitempty"`
	Threads               int64             `json:"threads,omitempty"`
	Warnings              []string          `json:"warnings,omitempty"`
}

// Evidence is the versioned kubevirt evidence.
type Evidence struct {
	Version int   `json:"version"`
	VMIs    []VMI `json:"vmis"`
}

// Config is the snapshot data the adapter works from.
type Config struct {
	VMIs  []unstructured.Unstructured
	State model.CollectionStatus
}

// Adapter is the KubeVirt adapter.
type Adapter struct{ cfg Config }

// New returns an adapter over cfg.
func New(cfg Config) *Adapter { return &Adapter{cfg} }

// Name implements adapters.Adapter.
func (a *Adapter) Name() string { return Name }

// Available implements adapters.Adapter: true when the VMI collection was
// read, even with zero VMIs.
func (a *Adapter) Available(context.Context) (bool, string, error) {
	st := a.cfg.State
	switch {
	case st.State == model.StateOK:
		return true, "", nil
	case st.Reason != "":
		return false, st.Reason, nil
	case st.State == model.StateNotFound:
		return false, "kubevirt.io/v1 not served", nil
	case st.State == "":
		return false, "virtualmachineinstances collection state: not collected", nil
	}
	return false, "virtualmachineinstances collection state: " + st.State, nil
}

// Collect implements adapters.Adapter. Only the fields of VMI are read; every
// other part of the object (volumes, cloud-init, credentials, interfaces,
// annotations) is never copied.
func (a *Adapter) Collect(ctx context.Context) (model.Evidence, error) {
	ev := Evidence{Version: Version, VMIs: []VMI{}}
	for i := range a.cfg.VMIs {
		ev.VMIs = append(ev.VMIs, fromObject(a.cfg.VMIs[i].Object))
	}
	slices.SortFunc(ev.VMIs, func(x, y VMI) int {
		return cmp.Or(cmp.Compare(x.Namespace, y.Namespace), cmp.Compare(x.Name, y.Name))
	})
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return json.Marshal(ev)
}

// field reads a nested value; any wrong-typed intermediate yields (nil, false).
func field(obj map[string]any, path ...string) (any, bool) {
	var cur any = obj
	for _, k := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = m[k]; !ok || cur == nil {
			return nil, false
		}
	}
	return cur, true
}

func fromObject(obj map[string]any) VMI {
	v := VMI{Claims: map[string]string{}, HostDevices: []HostDevice{}}
	var warns []string
	warn := func(w string) {
		if !slices.Contains(warns, w) {
			warns = append(warns, w)
		}
	}
	str := func(path ...string) string {
		x, ok := field(obj, path...)
		if !ok {
			return ""
		}
		s, isStr := x.(string)
		if !isStr {
			warn(strings.Join(path, ".") + " is not a string")
		}
		return s
	}
	num := func(path ...string) int64 {
		x, ok := field(obj, path...)
		if !ok {
			return 0
		}
		switch n := x.(type) {
		case int64:
			return n
		case int:
			return int64(n)
		case float64:
			if n == float64(int64(n)) {
				return int64(n)
			}
		}
		warn(strings.Join(path, ".") + " is not an integer")
		return 0
	}
	v.Namespace = str("metadata", "namespace")
	v.Name = str("metadata", "name")
	v.NodeName = str("status", "nodeName")
	v.Phase = str("status", "phase")
	v.Sockets, v.Cores, v.Threads = num("spec", "domain", "cpu", "sockets"), num("spec", "domain", "cpu", "cores"), num("spec", "domain", "cpu", "threads")
	if x, ok := field(obj, "spec", "domain", "cpu", "dedicatedCpuPlacement"); ok {
		b, isBool := x.(bool)
		if !isBool {
			warn("spec.domain.cpu.dedicatedCpuPlacement is not a boolean")
		}
		v.DedicatedCPUPlacement = b
	}
	if x, ok := field(obj, "spec", "domain", "cpu", "numa"); ok {
		m, isMap := x.(map[string]any)
		if !isMap {
			warn("spec.domain.cpu.numa is not an object")
		}
		v.GuestNUMAPassthrough = m["guestMappingPassthrough"] != nil
	}
	if x, ok := field(obj, "spec", "resourceClaims"); ok {
		list, isList := x.([]any)
		if !isList {
			warn("spec.resourceClaims is not a list")
		}
		for _, e := range list {
			m, isMap := e.(map[string]any)
			name, _ := m["name"].(string)
			if !isMap || name == "" {
				warn("spec.resourceClaims has an entry without a name")
				continue
			}
			v.Claims[name], _ = m["resourceClaimName"].(string)
		}
	}
	if x, ok := field(obj, "spec", "domain", "devices", "hostDevices"); ok {
		list, isList := x.([]any)
		if !isList {
			warn("spec.domain.devices.hostDevices is not a list")
		}
		for _, e := range list {
			m, isMap := e.(map[string]any)
			if !isMap {
				warn("hostDevices has an entry that is not an object")
				continue
			}
			alias, hasAlias := m["claimName"]
			if !hasAlias || alias == nil {
				continue // not a DRA device
			}
			d := HostDevice{}
			var ok1, ok2, ok3 bool
			d.Name, ok1 = m["name"].(string)
			d.ClaimAlias, ok2 = alias.(string)
			d.RequestName, ok3 = m["requestName"].(string)
			if !ok1 || !ok2 || !ok3 {
				warn("hostDevices has a DRA entry with a missing or non-string field")
				continue
			}
			d.ClaimName = v.Claims[d.ClaimAlias]
			v.HostDevices = append(v.HostDevices, d)
		}
	}
	v.Warnings = warns
	return v
}
