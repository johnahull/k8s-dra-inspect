package check

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	resourcev1 "k8s.io/api/resource/v1"

	"github.com/johnahull/k8s-dra-inspect/internal/adapters/kubevirt"
	"github.com/johnahull/k8s-dra-inspect/internal/model"
)

// NameKubeVirt is the Result.Check of KubeVirt.
const NameKubeVirt = "check.kubevirt"

// KubeVirt maps each VMI host device to its ResourceClaim allocation and
// checks that the devices are on the VMI's node and that the guest can see
// their NUMA layout. target is an optional VMI name ("name" or "ns/name");
// namespace (-n) scopes it. It reads Evidence["kubevirt"] when present and
// otherwise derives it from the snapshot's VMIs. One result per VMI.
func KubeVirt(target, namespace string) Check {
	return func(ctx context.Context, snap model.Snapshot) []model.Result {
		ev, un, ok := loadKubeVirt(ctx, snap, NameKubeVirt)
		if !ok {
			return un
		}
		ns, name := namespace, target
		if a, b, cut := strings.Cut(target, "/"); cut {
			ns, name = a, b
		}
		var vmis []kubevirt.VMI
		for _, v := range ev.VMIs {
			if (ns == "" || v.Namespace == ns) && (name == "" || v.Name == name) {
				vmis = append(vmis, v)
			}
		}
		if len(vmis) == 0 {
			if target != "" {
				return single(NameKubeVirt, model.StatusUnavailable, "", "no VMI named "+target)
			}
			return []model.Result{{Check: NameKubeVirt, Status: model.StatusPass, Details: map[string]any{"summary": "no VirtualMachineInstances"}}}
		}
		out := make([]model.Result, 0, len(vmis))
		var idx deviceIndex
		built := false
		for _, v := range vmis {
			if len(v.HostDevices) > 0 && !built {
				if r, bad := need(snap, NameKubeVirt, "resourceslices", "resourceclaims"); bad {
					return r
				}
				idx, built = newDeviceIndex(snap), true
			}
			r := kubevirtVMI(snap, idx, v)
			if len(ev.Warnings) > 0 {
				ws, _ := r.Details["warnings"].([]string)
				r.Details["warnings"] = append(slices.Clone(ws), ev.Warnings...)
			}
			out = append(out, r)
		}
		return out
	}
}

// loadKubeVirt returns Evidence["kubevirt"], or derives it from snap.VMIs.
func loadKubeVirt(ctx context.Context, snap model.Snapshot, name string) (kubevirt.Evidence, []model.Result, bool) {
	raw, have := snap.Evidence[kubevirt.Name]
	if !have {
		a := kubevirt.New(kubevirt.Config{VMIs: snap.VMIs, State: snap.Collection["virtualmachineinstances"]})
		if ok, reason, _ := a.Available(ctx); !ok {
			return kubevirt.Evidence{}, single(name, model.StatusUnavailable, "", reason), false
		}
		var err error
		if raw, err = a.Collect(ctx); err != nil {
			return kubevirt.Evidence{}, single(name, model.StatusError, "", "kubevirt evidence: "+err.Error()), false
		}
	}
	var ev kubevirt.Evidence
	if err := json.Unmarshal(raw, &ev); err != nil {
		return ev, single(name, model.StatusUnavailable, "", "invalid kubevirt evidence in snapshot"), false
	}
	if ev.Version < 1 || ev.Version > kubevirt.Version {
		return ev, single(name, model.StatusUnavailable, "", fmt.Sprintf("unsupported kubevirt evidence version %d", ev.Version)), false
	}
	ev.Bound() // evidence from --from is only checked for version and shape
	return ev, nil, true
}

func kubevirtVMI(snap model.Snapshot, idx deviceIndex, v kubevirt.VMI) model.Result {
	var (
		fs      []finding
		devices = []map[string]any{}
		numaSet = map[string]bool{}
		unknown bool
	)
	for _, hd := range v.HostDevices {
		switch cl, found := kubevirtClaim(snap, v, hd); {
		case hd.ClaimName == "" && !claimDefined(v, hd.ClaimAlias):
			fs = append(fs, finding{status: model.StatusFail, kind: model.KindClaimRef, reason: fmt.Sprintf("claim alias %s is not defined in spec.resourceClaims", hd.ClaimAlias)})
		case hd.ClaimName == "":
			fs = append(fs, finding{status: model.StatusUnavailable, kind: model.KindClaimRef, reason: fmt.Sprintf("claim %s comes from a template; its generated name is not in the VMI", hd.ClaimAlias)})
		case !found:
			fs = append(fs, finding{status: model.StatusFail, kind: model.KindClaimRef, reason: fmt.Sprintf("claim %s/%s not found", v.Namespace, hd.ClaimName)})
		default:
			var results []resourcev1.DeviceRequestAllocationResult
			if cl.Status.Allocation != nil {
				for _, r := range cl.Status.Allocation.Devices.Results {
					if r.Request == hd.RequestName || strings.HasPrefix(r.Request, hd.RequestName+"/") {
						results = append(results, r)
					}
				}
			}
			if len(results) == 0 {
				fs = append(fs, finding{status: model.StatusFail, kind: model.KindClaimRef, reason: fmt.Sprintf("request %s has no allocation result in claim %s/%s", hd.RequestName, v.Namespace, hd.ClaimName)})
			}
			for _, r := range results {
				d := map[string]any{"hostDevice": hd.Name, "request": r.Request, "driver": r.Driver, "pool": r.Pool, "device": r.Device}
				devices = append(devices, d)
				ref := idx.find(r.Driver, r.Pool, r.Device)
				node, tied := deviceNode(snap, r)
				if ref.state != deviceMissing {
					if bdf, _ := model.AttrString(ref.dev, r.Driver, "pciBusID"); bdf != "" {
						d["pciBusID"] = strings.ToLower(bdf)
					}
					top, err := model.TopologyOf(ref.dev, r.Driver)
					if err != nil {
						fs = append(fs, finding{status: model.StatusUnavailable, kind: model.KindInvalid, reason: "device " + devName(r) + " has invalid topology attributes"})
					}
					for _, n := range top.NUMA.Values {
						numaSet[n] = true
					}
				}
				switch {
				case !tied:
					unknown = true
					fs = append(fs, finding{status: model.StatusUnavailable, kind: model.KindNode, reason: "device " + devName(r) + " has no known node"})
				case v.NodeName == "":
				case node != v.NodeName:
					d["node"] = node
					fs = append(fs, finding{status: model.StatusFail, kind: model.KindNode, reason: fmt.Sprintf("device %s is on node %s, but the VMI runs on %s", devName(r), node, v.NodeName)})
				default:
					d["node"] = node
				}
			}
		}
	}
	if v.NodeName == "" && len(v.HostDevices) > 0 && !unknown {
		fs = append(fs, finding{status: model.StatusUnavailable, kind: model.KindNode, reason: "VMI is not scheduled to a node"})
	}
	numa := slices.SortedFunc(func(yield func(string) bool) {
		for n := range numaSet {
			if !yield(n) {
				return
			}
		}
	}, func(a, b string) int {
		x, ea := strconv.Atoi(a)
		y, eb := strconv.Atoi(b)
		if ea != nil || eb != nil {
			return strings.Compare(a, b)
		}
		return cmp.Compare(x, y)
	})
	if numa == nil {
		numa = []string{}
	}
	if len(numa) >= 2 && !v.GuestNUMAPassthrough {
		fs = append(fs, finding{status: model.StatusWarn, kind: model.KindGuestNUMA, reason: fmt.Sprintf("devices span NUMA nodes %s without guestMappingPassthrough; the guest cannot see the topology", strings.Join(numa, ","))})
	}
	if v.GuestNUMAPassthrough && !v.DedicatedCPUPlacement {
		fs = append(fs, finding{status: model.StatusWarn, kind: model.KindGuestNUMA, reason: "NUMA passthrough requires dedicated CPU placement; KubeVirt will not schedule such a VMI"})
	}
	st := worst(fs)
	det := map[string]any{
		"devices": devices, "numa": numa,
		"guestNUMAPassthrough": v.GuestNUMAPassthrough, "dedicatedCPUPlacement": v.DedicatedCPUPlacement,
	}
	if len(v.Warnings) > 0 {
		det["warnings"] = v.Warnings
	}
	res := model.Result{Check: NameKubeVirt, Status: st, Subject: v.Namespace + "/" + v.Name, Details: det}
	switch {
	case st != model.StatusPass:
		det["reason"] = reasons(fs)
		res.Findings = findingsOf(fs)
	case len(v.HostDevices) == 0:
		det["summary"] = "no DRA host devices"
	default:
		det["summary"] = fmt.Sprintf("%d device(s) on node %s", len(devices), v.NodeName)
	}
	return res
}

func claimDefined(v kubevirt.VMI, alias string) bool { _, ok := v.Claims[alias]; return ok }

func kubevirtClaim(snap model.Snapshot, v kubevirt.VMI, hd kubevirt.HostDevice) (resourcev1.ResourceClaim, bool) {
	for _, c := range snap.ResourceClaims {
		if c.Namespace == v.Namespace && c.Name == hd.ClaimName {
			return c, true
		}
	}
	return resourcev1.ResourceClaim{}, false
}

// deviceNode returns the named node the allocated device is bound to.
func deviceNode(snap model.Snapshot, r resourcev1.DeviceRequestAllocationResult) (string, bool) {
	d, sl, found := model.FindDevice(snap.ResourceSlices, r.Driver, r.Pool, r.Device)
	if !found {
		return "", false
	}
	n := model.DeviceNode(*sl, d).Name
	return n, n != ""
}
