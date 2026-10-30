package check

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/johnahull/k8s-dra-inspect/internal/adapters/host"
	"github.com/johnahull/k8s-dra-inspect/internal/model"
)

// NameVFIO is the Result.Check of VFIO.
const NameVFIO = "check.vfio"

const vfioDriver = "vfio-pci"

// VFIO checks every allocated device that publishes a PCI bus ID against host
// evidence: bound to vfio-pci, IOMMU group peers, and the /dev/vfio group
// node. Devices on another node than the evidence are unavailable, one result
// per other node. A group peer not bound to vfio-pci is listed in a warning;
// PCI/host bridges (class 0600, 0604) are ignored, and a GPU audio function
// (class 0403) counts only when bound to a non-vfio driver. Evidence without
// classes (version 1) treats every peer alike.
func VFIO() Check {
	return func(_ context.Context, snap model.Snapshot) []model.Result {
		if r, ok := need(snap, NameVFIO, "resourceslices", "resourceclaims"); ok {
			return r
		}
		ev, un, ok := loadHost(snap, NameVFIO)
		if !ok {
			return un
		}
		var out []model.Result
		seen, off := map[string]bool{}, map[string]int{}
		for _, c := range allocated(snap) {
			for _, r := range c.Status.Allocation.Devices.Results {
				d, _, found := model.FindDevice(snap.ResourceSlices, r.Driver, r.Pool, r.Device)
				if !found || seen[devName(r)] {
					continue
				}
				bdf, _ := model.AttrString(d, r.Driver, "pciBusID")
				bdf = strings.ToLower(bdf)
				if bdf == "" {
					continue
				}
				seen[devName(r)] = true
				node, tied := deviceNode(snap, r)
				switch {
				case !tied:
					out = append(out, single(NameVFIO, model.StatusUnavailable, devName(r), "device is not tied to a node; cannot match host evidence")...)
				case node != ev.NodeName:
					off[node]++
				default:
					out = append(out, vfioDevice(ev, r.Device, devName(r), bdf))
				}
			}
		}
		for node, n := range off {
			what := "devices"
			if n == 1 {
				what = "device"
			}
			out = append(out, single(NameVFIO, model.StatusUnavailable, "node/"+node, fmt.Sprintf("%d %s on node %s: run on node %s", n, what, node, node))...)
		}
		if len(out) == 0 {
			return single(NameVFIO, model.StatusPass, "", "no allocated device with a pciBusID on node "+ev.NodeName)
		}
		slices.SortFunc(out, func(a, b model.Result) int { return strings.Compare(a.Subject, b.Subject) })
		return out
	}
}

func vfioDevice(ev host.Evidence, device, subject, bdf string) model.Result {
	det := map[string]any{"bdf": bdf, "cdiPresent": cdiHas(ev, device, bdf)}
	pci, ok := ev.PCI[bdf]
	if !ok {
		reason := "PCI device " + bdf + " not found in host evidence"
		det["reason"] = reason
		return model.Result{Check: NameVFIO, Status: model.StatusUnavailable, Subject: subject, Details: det,
			Findings: []model.Finding{{Status: model.StatusUnavailable, Text: reason}}}
	}
	det["driver"], det["iommuGroup"] = pci.Driver, pci.IOMMUGroup
	var fs []finding
	switch pci.Driver {
	case vfioDriver:
	case "":
		fs = append(fs, finding{model.StatusFail, "no driver bound, want " + vfioDriver})
	default:
		fs = append(fs, finding{model.StatusFail, fmt.Sprintf("bound to driver %s, want %s", pci.Driver, vfioDriver)})
	}
	if pci.IOMMUGroup == "" {
		fs = append(fs, finding{model.StatusWarn, "no IOMMU group (IOMMU disabled?)"})
	} else {
		var peers []string
		for _, p := range ev.IOMMUGroups[pci.IOMMUGroup] {
			pd := ev.PCI[p]
			audio := strings.HasPrefix(pd.Class, "0403")
			if p == bdf || pd.Driver == vfioDriver || strings.HasPrefix(pd.Class, "0600") || strings.HasPrefix(pd.Class, "0604") || audio && pd.Driver == "" {
				continue
			}
			drv := "no driver"
			if pd.Driver != "" {
				drv = "driver " + pd.Driver
			}
			if audio {
				drv += ", GPU audio function sharing the group"
			}
			peers = append(peers, fmt.Sprintf("%s (%s)", p, drv))
		}
		if len(peers) > 0 {
			det["nonVfioPeers"] = peers
			fs = append(fs, finding{model.StatusWarn, fmt.Sprintf("IOMMU group %s also holds %s", pci.IOMMUGroup, strings.Join(peers, ", "))})
		}
		if !slices.Contains(ev.VFIODevNodes, pci.IOMMUGroup) {
			fs = append(fs, finding{model.StatusWarn, "/dev/vfio/" + pci.IOMMUGroup + " is missing"})
		}
	}
	st := worst(fs)
	res := model.Result{Check: NameVFIO, Status: st, Subject: subject, Details: det}
	if st != model.StatusPass {
		det["reason"] = reasons(fs)
		res.Findings = findingsOf(fs)
	} else {
		det["summary"] = fmt.Sprintf("%s bound to %s in IOMMU group %s", bdf, vfioDriver, pci.IOMMUGroup)
	}
	return res
}

// cdiHas reports whether any CDI spec lists the device by name or bus ID.
func cdiHas(ev host.Evidence, device, bdf string) bool {
	return slices.ContainsFunc(ev.CDI, func(s host.CDISpec) bool {
		return slices.Contains(s.Devices, device) || slices.Contains(s.Devices, bdf)
	})
}
