package check

import (
	"context"
	"fmt"
	"slices"
	"strings"

	resourcev1 "k8s.io/api/resource/v1"

	"github.com/johnahull/k8s-dra-inspect/internal/adapters/host"
	"github.com/johnahull/k8s-dra-inspect/internal/model"
)

// NameVFIO is the Result.Check of VFIO.
const NameVFIO = "check.vfio"

const (
	pciBusIDKey = "resource.kubernetes.io/pciBusID"
	vfioDriver  = "vfio-pci"
)

// VFIO checks every allocated device that publishes a PCI bus ID against host
// evidence: bound to vfio-pci, IOMMU group peers, and the /dev/vfio group
// node. A device on another node than the evidence is unavailable. The
// peer check is deliberately simple: any peer not bound to vfio-pci is
// listed in a warning, bridges are not told apart (no device class is collected).
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
		seen := map[string]bool{}
		for _, c := range allocated(snap) {
			for _, r := range c.Status.Allocation.Devices.Results {
				d, sl, found := model.FindDevice(snap.ResourceSlices, r.Driver, r.Pool, r.Device)
				if !found || seen[devName(r)] {
					continue
				}
				bdf := pciBusID(d, r.Driver)
				if bdf == "" {
					continue
				}
				seen[devName(r)] = true
				switch {
				case sl.Spec.NodeName == nil:
					out = append(out, single(NameVFIO, model.StatusUnavailable, devName(r), "device is not tied to a node; cannot match host evidence")...)
				case *sl.Spec.NodeName != ev.NodeName:
					out = append(out, single(NameVFIO, model.StatusUnavailable, devName(r), offNodeReason(ev.NodeName, "claim", *sl.Spec.NodeName))...)
				default:
					out = append(out, vfioDevice(ev, r.Device, devName(r), bdf))
				}
			}
		}
		if len(out) == 0 {
			return single(NameVFIO, model.StatusPass, "", "no allocated device with a pciBusID on node "+ev.NodeName)
		}
		slices.SortFunc(out, func(a, b model.Result) int { return strings.Compare(a.Subject, b.Subject) })
		return out
	}
}

// pciBusID reads the standard attribute, or the driver's own-domain one.
func pciBusID(d resourcev1.Device, driver string) string {
	a, ok := lookupKey(d.Attributes, driver, pciBusIDKey)
	if !ok {
		a, ok = lookupKey(d.Attributes, driver, driver+"/pciBusID")
	}
	if !ok || a.StringValue == nil {
		return ""
	}
	return strings.ToLower(*a.StringValue)
}

func vfioDevice(ev host.Evidence, device, subject, bdf string) model.Result {
	det := map[string]any{"bdf": bdf, "cdiPresent": cdiHas(ev, device, bdf)}
	pci, ok := ev.PCI[bdf]
	if !ok {
		det["reason"] = "PCI device " + bdf + " not found in host evidence"
		return model.Result{Check: NameVFIO, Status: model.StatusUnavailable, Subject: subject, Details: det}
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
			if p == bdf || ev.PCI[p].Driver == vfioDriver {
				continue
			}
			drv := "no driver"
			if d := ev.PCI[p].Driver; d != "" {
				drv = "driver " + d
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
	if st != model.StatusPass {
		det["reason"] = reasons(fs)
	} else {
		det["summary"] = fmt.Sprintf("%s bound to %s in IOMMU group %s", bdf, vfioDriver, pci.IOMMUGroup)
	}
	return model.Result{Check: NameVFIO, Status: st, Subject: subject, Details: det}
}

// cdiHas reports whether any CDI spec lists the device by name or bus ID.
func cdiHas(ev host.Evidence, device, bdf string) bool {
	return slices.ContainsFunc(ev.CDI, func(s host.CDISpec) bool {
		return slices.Contains(s.Devices, device) || slices.Contains(s.Devices, bdf)
	})
}
