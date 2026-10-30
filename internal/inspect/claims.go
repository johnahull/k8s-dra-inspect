package inspect

import (
	"fmt"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/render"
)

// nodeSelectorNames lists node names an allocation nodeSelector pins via
// metadata.name / hostname In terms, else the generic selector placeholder.
func nodeSelectorNames(ns *corev1.NodeSelector) []string {
	var names []string
	for _, t := range ns.NodeSelectorTerms {
		for _, e := range append(slices.Clone(t.MatchFields), t.MatchExpressions...) {
			if e.Operator == corev1.NodeSelectorOpIn && (e.Key == "metadata.name" || e.Key == "kubernetes.io/hostname") {
				names = append(names, e.Values...)
			}
		}
	}
	if len(names) == 0 {
		return []string{model.SelectorLabel}
	}
	return names
}

func sortedUnique(v []string) []string {
	slices.Sort(v)
	return append([]string{}, slices.Compact(v)...)
}

// requestsOf describes claim requests without selector expressions.
func requestsOf(c resourcev1.ResourceClaim) []map[string]any {
	out := []map[string]any{}
	for _, r := range c.Spec.Devices.Requests {
		m := map[string]any{"name": r.Name}
		if e := r.Exactly; e != nil {
			m["deviceClassName"] = e.DeviceClassName
			if e.AllocationMode != "" {
				m["allocationMode"] = string(e.AllocationMode)
			}
			if e.Count != 0 {
				m["count"] = e.Count
			}
		}
		if len(r.FirstAvailable) > 0 {
			subs := []map[string]any{}
			for _, s := range r.FirstAvailable {
				sm := map[string]any{"name": s.Name, "deviceClassName": s.DeviceClassName}
				if s.AllocationMode != "" {
					sm["allocationMode"] = string(s.AllocationMode)
				}
				if s.Count != 0 {
					sm["count"] = s.Count
				}
				subs = append(subs, sm)
			}
			m["firstAvailable"] = subs
		}
		out = append(out, m)
	}
	return out
}

// vmiOf matches a virt-launcher-<vmi>-<suffix> pod to a VMI in the namespace.
func vmiOf(snap model.Snapshot, ns string, pods []string) string {
	for _, p := range pods {
		rest, ok := strings.CutPrefix(p, "virt-launcher-")
		if !ok {
			continue
		}
		for _, v := range snap.VMIs {
			// shortcut: a single-segment suffix tells vm1-x-abcde (VMI vm1-x) from vm1-abcde (VMI vm1), but
			// KubeVirt truncates long generated pod names and a VMI "vm1-abcde" vs a "vm1" pod is ambiguous;
			// upgrade: match pod label kubevirt.io/created-by=<vmi uid>, which needs the collector to keep labels.
			if suffix, ok := strings.CutPrefix(rest, v.GetName()+"-"); ok && v.GetNamespace() == ns && suffix != "" && !strings.Contains(suffix, "-") {
				return v.GetName()
			}
		}
	}
	return ""
}

// Claims lists ResourceClaims with their allocation, nodes and consumers.
func Claims() Inspector {
	return Inspector{
		Spec: Spec{
			Columns: []render.Column{
				{Header: "CLAIM", Min: 12, Truncate: render.TruncateMiddle},
				{Header: "ALLOCATED", Min: 9},
				{Header: "DEVICES", Min: 7},
				{Header: "NODE", Min: 8},
				{Header: "USED BY", Min: 10},
			},
			Row: func(r model.Result) []string {
				alloc := "no"
				if r.Details["allocated"] == true {
					alloc = "yes"
				}
				nodes, _ := r.Details["nodes"].([]string)
				used, _ := r.Details["usedBy"].([]string)
				usedBy := strings.Join(used, ",")
				if str(r, "usedByState") != "" {
					usedBy = "unavailable"
				}
				if v := str(r, "vmi"); v != "" && !strings.HasPrefix(v, "unavailable") {
					usedBy += " [vmi " + v + "]"
				}
				return []string{r.Subject, alloc, str(r, "deviceCount"), dash(strings.Join(nodes, ",")), dash(usedBy)}
			},
		},
		Run: func(snap model.Snapshot) []model.Result {
			const check = "inspect.claims"
			if res, ok := unavailableOf(snap, check, "resourceclaims"); ok {
				return res
			}
			slicesOK := snap.Collection["resourceslices"].State == model.StateOK
			vmiSt := snap.Collection["virtualmachineinstances"]
			out := []model.Result{}
			for _, c := range snap.ResourceClaims {
				al := c.Status.Allocation
				results := []map[string]string{}
				var nodes, missing []string
				if al != nil {
					for _, r := range al.Devices.Results {
						results = append(results, map[string]string{"request": r.Request, "driver": r.Driver, "pool": r.Pool, "device": r.Device})
						if !slicesOK {
							continue
						}
						d, s, ok := model.FindDevice(snap.ResourceSlices, r.Driver, r.Pool, r.Device)
						if !ok || s == nil {
							missing = append(missing, fmt.Sprintf("%s/%s/%s", r.Driver, r.Pool, r.Device))
							continue
						}
						nodes = append(nodes, model.DeviceNode(*s, d).String())
					}
					// An allocation nodeSelector overrides device-derived nodes; selectors without a
					// node name render as "(selector)".
					if al.NodeSelector != nil {
						nodes = nodeSelectorNames(al.NodeSelector)
					}
					if !slicesOK && al.NodeSelector == nil {
						nodes = []string{"unavailable"}
					}
				}
				reserved := []string{}
				for _, r := range c.Status.ReservedFor {
					reserved = append(reserved, r.Name)
				}
				used := model.PodsUsing(snap, c)
				det := map[string]any{
					"deviceRequests": requestsOf(c), "allocated": al != nil, "results": results, "deviceCount": len(results),
					"nodes": sortedUnique(nodes), "reservedFor": reserved, "usedBy": used,
				}
				// usedBy stays a list in JSON; when pods could not be listed, usedByState carries the
				// reason so an unknown consumer set is not mistaken for an unused claim.
				if podSt := snap.Collection["pods"]; podSt.State != model.StateOK {
					reason := podSt.Reason
					if reason == "" {
						reason = "pods collection state: " + cmpState(podSt.State)
					}
					det["usedByState"] = "unavailable: " + reason
				}
				switch {
				case vmiSt.State != model.StateOK:
					reason := vmiSt.Reason
					if reason == "" {
						reason = "virtualmachineinstances collection state: " + cmpState(vmiSt.State)
					}
					det["vmi"] = "unavailable: " + reason
				default:
					if v := vmiOf(snap, c.Namespace, used); v != "" {
						det["vmi"] = v
					}
				}
				status := model.StatusPass
				if len(missing) > 0 {
					status = model.StatusWarn
					det["missingDevices"] = missing
					det["reason"] = "allocated device not found in ResourceSlices: " + strings.Join(missing, ", ")
				}
				out = append(out, model.Result{Check: check, Status: status, Subject: c.Namespace + "/" + c.Name, Details: det})
			}
			return out
		},
	}
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
