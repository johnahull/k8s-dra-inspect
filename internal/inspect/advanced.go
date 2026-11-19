package inspect

import (
	"fmt"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/dynamic-resource-allocation/resourceclaim"

	"github.com/johnahull/k8s-dra-inspect/internal/analyze"
	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/render"
)

func analysisData(s model.Snapshot) *analyze.Snapshot { return analyze.FromModel(s) }

func needAnalysis(s model.Snapshot, check string, keys ...string) ([]model.Result, bool) {
	for _, key := range keys {
		if r, ok := unavailableOf(s, check, key); ok {
			return r, true
		}
	}
	return nil, false
}

func allocationKeys(s model.Snapshot) []string {
	if s.Namespace != "" {
		return []string{"resourceclaims", "globalallocations"}
	}
	return []string{"resourceclaims"}
}

// Map links ResourceClaims to the pods that use them and surfaces unresolved pod references.
func Map() Inspector {
	return Inspector{Spec: Spec{
		Columns: []render.Column{{Header: "NAMESPACE", Min: 10}, {Header: "CONSUMER", Min: 13}, {Header: "CLAIM", Min: 12}, {Header: "REQUEST", Min: 10}, {Header: "DRIVER", Min: 12}, {Header: "POOL", Min: 10}, {Header: "DEVICE", Min: 10}, {Header: "NODE", Min: 10}, {Header: "STATUS", Min: 10}, {Header: "RESERVED", Min: 8}, {Header: "DETAIL", Min: 12, Truncate: render.TruncateTail}},
		Row: func(r model.Result) []string {
			return []string{str(r, "namespace"), dash(str(r, "consumer")), dash(str(r, "claim")), dash(str(r, "request")), dash(str(r, "driver")), dash(str(r, "pool")), dash(str(r, "device")), dash(str(r, "node")), str(r, "state"), fmt.Sprint(r.Details["reserved"]), str(r, "reason")}
		},
	}, Run: func(s model.Snapshot) []model.Result {
		const check = "inspect.map"
		if r, ok := needAnalysis(s, check, "resourceclaims", "pods"); ok {
			return r
		}
		data := analysisData(s)
		mapped := analyze.MapClaims(data.Pods, data.ResourceClaims[:len(s.ResourceClaims)])
		out := make([]model.Result, 0, len(mapped.Claims)+len(mapped.Missing))
		for _, use := range mapped.Claims {
			consumers := use.Consumers
			if len(consumers) == 0 {
				consumers = []analyze.Consumer{{}}
			}
			for i := range consumers {
				consumer := &consumers[i]
				base := map[string]any{"namespace": use.Claim.Namespace, "claim": use.Claim.Name,
					"consumer": consumerName(*consumer), "reserved": consumer.Reserved}
				allocation := use.Claim.Status.Allocation
				if allocation == nil {
					if len(use.Claim.Spec.Devices.Requests) == 0 {
						out = append(out, mapResult(check, model.StatusPass, use.Claim.Namespace+"/"+use.Claim.Name, base, "pending", "claim has no device requests"))
					}
					for _, request := range use.Claim.Spec.Devices.Requests {
						row := cloneDetails(base)
						row["request"] = request.Name
						if consumer.Pod != nil {
							row["node"] = consumer.Pod.Spec.NodeName
						}
						out = append(out, mapResult(check, model.StatusPass, use.Claim.Namespace+"/"+use.Claim.Name, row, "pending", ""))
					}
					continue
				}
				node, err := allocationNode(*consumer, allocation, data.Nodes)
				if err != nil {
					out = append(out, mapResult(check, model.StatusError, use.Claim.Namespace+"/"+use.Claim.Name, base, "error", err.Error()))
					continue
				}
				for _, result := range allocation.Devices.Results {
					row := cloneDetails(base)
					request := resourceclaim.BaseRequestRef(result.Request)
					row["request"], row["driver"], row["pool"], row["device"], row["node"] = request, result.Driver, result.Pool, result.Device, node
					if resourceclaim.IsSubRequestRef(result.Request) {
						row["request"] = result.Request
					}
					row["adminAccess"] = result.AdminAccess != nil && *result.AdminAccess
					out = append(out, mapResult(check, model.StatusPass, use.Claim.Namespace+"/"+use.Claim.Name, row, "allocated", ""))
				}
			}
		}
		for _, missing := range mapped.Missing {
			subject := missing.Pod.Namespace + "/" + missing.Pod.Name + ":" + missing.PodClaim
			state := "missing"
			if missing.ClaimName == "" {
				state = "not created"
			}
			out = append(out, mapResult(check, model.StatusWarn, subject, map[string]any{
				"namespace": missing.Pod.Namespace, "consumer": "pod/" + missing.Pod.Name, "claim": missing.ClaimName,
				"template": missing.Template, "node": missing.Pod.Spec.NodeName, "reserved": false,
			}, state, missing.Reason))
		}
		return out
	}}
}

func consumerName(c analyze.Consumer) string {
	if c.Name == "" {
		return ""
	}
	if c.Pod != nil {
		return "pod/" + c.Pod.Name
	}
	return c.Kind + "/" + c.Name
}

func allocationNode(c analyze.Consumer, allocation *resourcev1.AllocationResult, nodes []*corev1.Node) (string, error) {
	if c.Pod != nil && c.Pod.Spec.NodeName != "" {
		return c.Pod.Spec.NodeName, nil
	}
	scope := analyze.NodeScope{NodeSelector: allocation.NodeSelector, AllNodes: allocation.NodeSelector == nil}
	if _, err := scope.Nodes(nodes); err != nil {
		return "", err
	}
	return scope.Describe(nodes), nil
}

func cloneDetails(in map[string]any) map[string]any {
	out := make(map[string]any, len(in)+8)
	for key, value := range in {
		out[key] = value
	}
	return out
}

func mapResult(check string, status model.Status, subject string, details map[string]any, state, reason string) model.Result {
	details["state"] = state
	if reason != "" {
		details["reason"] = reason
	}
	return model.Result{Check: check, Status: status, Subject: subject, Details: details}
}

// Pools reports the newest generation and usability of each driver pool.
func Pools() Inspector {
	return Inspector{Spec: Spec{
		Columns: []render.Column{{Header: "DRIVER", Min: 12}, {Header: "POOL", Min: 12}, {Header: "NODES", Min: 10}, {Header: "GEN", Min: 4}, {Header: "SLICES", Min: 7}, {Header: "DEVICES", Min: 7}, {Header: "ALLOCATED", Min: 9}, {Header: "STATE", Min: 10}, {Header: "DETAIL", Min: 12, Truncate: render.TruncateTail}},
		Row: func(r model.Result) []string {
			return []string{r.Subject, str(r, "pool"), dash(str(r, "nodeScope")), str(r, "generation"), str(r, "slices"), str(r, "devices"), str(r, "allocated"), str(r, "state"), str(r, "detail")}
		},
	}, Run: func(s model.Snapshot) []model.Result {
		const check = "inspect.pools"
		if r, ok := needAnalysis(s, check, "resourceslices"); ok {
			return r
		}
		data := analysisData(s)
		allocations := analyze.BuildAllocations(data.ResourceClaims, true)
		out := []model.Result{}
		for _, p := range analyze.Pools(data.ResourceSlices) {
			state, status := "Complete", model.StatusPass
			if len(p.Problems) > 0 {
				state, status = "Invalid", model.StatusWarn
			} else if !p.Complete() {
				state, status = "Incomplete", model.StatusWarn
			}
			problems := append([]string{}, p.Problems...)
			if !p.Complete() && len(p.Problems) == 0 {
				problems = append(problems, fmt.Sprintf("%d of %d slices observed", len(p.Slices), p.ResourceSliceCount))
			}
			if p.OutdatedSlices > 0 {
				problems = append(problems, fmt.Sprintf("%d older-generation slices ignored", p.OutdatedSlices))
			}
			allocated := 0
			for _, d := range p.Devices() {
				if allocations.InUse(d.ID) {
					allocated++
				}
			}
			expected := fmt.Sprint(p.ResourceSliceCount)
			if p.ResourceSliceCount <= 0 {
				expected = "?"
			}
			out = append(out, model.Result{Check: check, Status: status, Subject: p.ID.Driver, Details: map[string]any{
				"pool": p.ID.Pool, "nodeScope": p.ScopeSummary(), "generation": p.Generation, "slices": fmt.Sprintf("%d/%s", len(p.Slices), expected),
				"slicesSeen": len(p.Slices), "resourceSliceCount": p.ResourceSliceCount, "outdatedSlices": p.OutdatedSlices,
				"devices": len(p.Devices()), "allocated": allocated, "state": state, "problems": problems, "detail": strings.Join(problems, "; "),
			}})
		}
		return out
	}}
}

// Devices reports allocation, taint, and node status for every current device.
func Devices() Inspector {
	return Inspector{Spec: Spec{
		Columns: []render.Column{{Header: "DRIVER", Min: 12}, {Header: "POOL", Min: 10}, {Header: "DEVICE", Min: 12}, {Header: "NODES", Min: 10}, {Header: "STATUS", Min: 12}, {Header: "CLAIMS", Min: 12, Truncate: render.TruncateTail}, {Header: "TAINTS", Min: 12, Truncate: render.TruncateTail}, {Header: "ATTRIBUTES", Min: 12, Truncate: render.TruncateTail}},
		Row: func(r model.Result) []string {
			attributes, _ := r.Details["attributes"].(map[string]string)
			return []string{str(r, "driver"), str(r, "pool"), str(r, "device"), dash(str(r, "scope")), str(r, "state"), dash(str(r, "claims")), dash(str(r, "taints")), kv(attributes)}
		},
	}, Run: func(s model.Snapshot) []model.Result {
		const check = "inspect.devices"
		if r, ok := needAnalysis(s, check, append([]string{"resourceslices"}, append(allocationKeys(s), "devicetaintrules")...)...); ok {
			return r
		}
		data := analysisData(s)
		pools := analyze.Pools(data.ResourceSlices)
		allocations := analyze.BuildAllocations(data.ResourceClaims, true)
		out := []model.Result{}
		for _, d := range analyze.Devices(pools, allocations, data.DeviceTaintRules) {
			state, status := string(d.Status), model.StatusPass
			if d.Status == analyze.DeviceTainted || d.Status == analyze.DeviceUnavailable {
				status = model.StatusWarn
			}
			holders := holderNames(d.Holders, s.Namespace)
			attributes := map[string]string{}
			for k, v := range d.Device.Attributes {
				attributes[string(k)] = attrDisplay(v)
			}
			taints := make([]string, len(d.Taints))
			for i := range d.Taints {
				taints[i] = d.Taints[i].String()
			}
			out = append(out, model.Result{Check: check, Status: status, Subject: d.ID.String(), Details: map[string]any{
				"driver": d.Slice.Spec.Driver, "pool": d.Slice.Spec.Pool.Name, "device": d.Device.Name, "state": state,
				"scope": d.Scope.String(), "holders": holders, "claims": strings.Join(holders, ", "),
				"claimDetails": deviceHolderRows(d.Holders, s.Namespace), "taintDetails": d.Taints, "taints": strings.Join(taints, ", "),
				"attributes": attributes, "capacity": d.Device.Capacity, "detail": d.Reason,
			}})
		}
		return out
	}}
}

func holderNames(holders []analyze.Holder, namespace string) []string {
	var names []string
	for _, h := range holders {
		if h.Claim.Namespace == "" && namespace != "" {
			names = append(names, "allocated outside namespace")
			continue
		}
		name := h.Claim.Namespace + "/" + h.Claim.Name
		if h.AdminAccess {
			name += " (admin)"
		}
		names = append(names, name)
	}
	return uniqueSorted(names)
}

func deviceHolderRows(holders []analyze.Holder, namespace string) []map[string]any {
	rows := make([]map[string]any, 0, len(holders))
	for _, h := range holders {
		ns, name := h.Claim.Namespace, h.Claim.Name
		if ns == "" && namespace != "" {
			rows = append(rows, map[string]any{"name": "allocated outside namespace"})
			continue
		}
		row := map[string]any{"namespace": ns, "name": name, "request": h.Request, "subRequest": h.SubRequest, "adminAccess": h.AdminAccess}
		if h.ShareID != nil {
			row["shareID"] = string(*h.ShareID)
		}
		rows = append(rows, row)
	}
	return rows
}

func joinStrings(value any) string {
	items, _ := value.([]string)
	return strings.Join(items, ", ")
}

// Nodes compares node-local plugin pods with the devices each driver publishes.
func Nodes() Inspector {
	return Inspector{Spec: Spec{
		Columns: []render.Column{{Header: "NODE", Min: 14}, {Header: "DRIVERS", Min: 12}, {Header: "DEVICES", Min: 7}, {Header: "ALLOCATED", Min: 9}, {Header: "FREE", Min: 5}, {Header: "TAINTED", Min: 7}, {Header: "UNAVAILABLE", Min: 11}, {Header: "REACHABLE", Min: 9}, {Header: "ISSUES", Min: 12, Truncate: render.TruncateTail}},
		Row: func(r model.Result) []string {
			return []string{r.Subject, dash(joinStrings(r.Details["drivers"])), str(r, "devices"), str(r, "allocated"), str(r, "free"), str(r, "tainted"), str(r, "unavailable"), str(r, "reachable"), dash(joinStrings(r.Details["issues"]))}
		},
	}, Run: func(s model.Snapshot) []model.Result {
		const check = "inspect.nodes"
		if r, ok := needAnalysis(s, check, append([]string{"resourceslices", "nodes"}, append(allocationKeys(s), "devicetaintrules", "nodepluginpods")...)...); ok {
			return r
		}
		data := analysisData(s)
		pools := analyze.Pools(data.ResourceSlices)
		devices := analyze.Devices(pools, analyze.BuildAllocations(data.ResourceClaims, true), data.DeviceTaintRules)
		summaries, err := analyze.Nodes(data.Nodes, data.NodePluginPods, pools, devices)
		if err != nil {
			return []model.Result{{Check: check, Status: model.StatusError, Details: map[string]any{"reason": err.Error()}}}
		}
		out := []model.Result{}
		for _, n := range summaries {
			status := model.StatusPass
			if len(n.Issues) > 0 {
				status = model.StatusWarn
			}
			var pluginPods []string
			for _, p := range n.PluginPods {
				pluginPods = append(pluginPods, p.Namespace+"/"+p.Name)
			}
			out = append(out, model.Result{Check: check, Status: status, Subject: n.Name, Details: map[string]any{
				"exists": n.Node != nil, "drivers": n.Drivers, "devices": n.Devices, "allocated": n.Allocated, "free": n.Free,
				"tainted": n.Tainted, "unavailable": n.Unavailable, "reachable": n.Reachable, "pluginPods": pluginPods, "issues": n.Issues,
			}})
		}
		return out
	}}
}

func uniqueSorted(in []string) []string {
	slices.Sort(in)
	return slices.Compact(in)
}
