package view

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/johnahull/k8s-dra-inspect/internal/adapters/host"
	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/render"
)

// vfioTitle is "driver/device (bdf)" for a device result, as both the check
// and the report print it.
func vfioTitle(r model.Result) string {
	title := r.Subject
	if drv, _, dev := splitSubject(r.Subject); dev != "" && drv != "" && strings.Contains(r.Subject, "/") {
		title = drv + "/" + dev
	}
	if bdf := detStr(r, "bdf"); bdf != "" {
		title += " (" + bdf + ")"
	}
	return title
}

// statusOf is the status of the first finding of kind k, or pass when there is none.
func statusOf(r model.Result, k model.Kind) model.Status {
	for _, f := range r.Findings {
		if f.Kind == k {
			return f.Status
		}
	}
	return model.StatusPass
}

// VFIO prints each allocated PCI device: bound driver, IOMMU group, group
// peers not on vfio-pci, the /dev/vfio node and CDI. The driver, the peers and
// the /dev/vfio node carry their finding's marker on their own line, so the
// finding is not said again. The /dev/vfio nodes come from the host evidence
// in the snapshot.
func VFIO(p *render.Printer, snap model.Snapshot, results []model.Result) {
	var ev host.Evidence
	haveEv := json.Unmarshal(snap.Evidence[host.Name], &ev) == nil
	eachResult(p, "VFIO Check", results, "no allocated devices to check", func(r model.Result, d *decoder) {
		p.Line(0, "%s", p.Bold(vfioTitle(r)))
		skip := []model.Kind{}
		if _, ok := r.Details["iommuGroup"]; ok {
			drv := detStr(r, "driver")
			if drv == "" {
				drv = "none"
			}
			if st := statusOf(r, model.KindVFIODriver); st != model.StatusPass {
				p.Note(2, st, "bound driver: "+drv+", want vfio-pci")
				skip = append(skip, model.KindVFIODriver)
			} else {
				p.Line(2, "bound driver: %s", drv)
			}
			group := detStr(r, "iommuGroup")
			if group == "" {
				p.Line(2, "IOMMU group: none")
			} else {
				p.Line(2, "IOMMU group: %s", group)
			}
			if peers := detail[[]string](d, r, "nonVfioPeers"); len(peers) > 0 {
				for _, peer := range peers {
					p.Note(2, model.StatusWarn, "non-vfio peer: "+peer)
				}
				skip = append(skip, model.KindVFIOPeers)
			}
			if group != "" && haveEv {
				if slices.Contains(ev.VFIODevNodes, group) {
					p.Line(2, "/dev/vfio/%s: present", group)
				} else {
					p.Note(2, model.StatusWarn, "/dev/vfio/"+group+": missing")
				}
				skip = append(skip, model.KindDevNode)
			}
		}
		if _, ok := r.Details["cdiPresent"]; ok {
			p.Line(2, "CDI spec lists device: %s", yesNo(detail[bool](d, r, "cdiPresent")))
		}
		if notes(p, 2, r, nil, skip...) == 0 {
			p.Note(2, model.StatusPass, reasonOf(r))
		}
	})
}
