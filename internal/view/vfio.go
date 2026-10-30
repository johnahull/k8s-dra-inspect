package view

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/johnahull/k8s-dra-inspect/internal/adapters/host"
	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/render"
)

// VFIO prints each allocated PCI device: bound driver, IOMMU group, group
// peers not on vfio-pci, the /dev/vfio node and CDI, then the findings. The
// /dev/vfio nodes come from the host evidence in the snapshot.
func VFIO(p *render.Printer, snap model.Snapshot, results []model.Result) {
	var ev host.Evidence
	haveEv := json.Unmarshal(snap.Evidence[host.Name], &ev) == nil
	eachResult(p, "VFIO Check", results, "no allocated devices to check", func(r model.Result) {
		title := r.Subject
		if drv, _, dev := splitSubject(r.Subject); dev != "" && drv != "" && strings.Contains(r.Subject, "/") {
			title = drv + "/" + dev
		}
		if bdf := detStr(r, "bdf"); bdf != "" {
			title += " (" + bdf + ")"
		}
		p.Line(0, "%s", p.Bold(title))
		if _, ok := r.Details["iommuGroup"]; ok {
			drv := detStr(r, "driver")
			if drv == "" {
				drv = "none"
			}
			p.Line(2, "bound driver: %s", drv)
			group := detStr(r, "iommuGroup")
			if group == "" {
				p.Line(2, "IOMMU group: none")
			} else {
				p.Line(2, "IOMMU group: %s", group)
			}
			for _, peer := range strs(r.Details["nonVfioPeers"]) {
				p.Line(2, "non-vfio peer: %s", peer)
			}
			if group != "" && haveEv {
				state := "missing"
				if slices.Contains(ev.VFIODevNodes, group) {
					state = "present"
				}
				p.Line(2, "/dev/vfio/%s: %s", group, state)
			}
		}
		if _, ok := r.Details["cdiPresent"]; ok {
			p.Line(2, "CDI spec lists device: %s", yesNo(r.Details["cdiPresent"]))
		}
		if notes(p, 2, r, nil) == 0 {
			p.Note(2, model.StatusPass, reasonOf(r))
		}
	})
}
