package host

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var bdfRE = regexp.MustCompile(`^[0-9a-f]{4}:[0-9a-f]{2}:[0-9a-f]{2}\.[0-7]$`)

// linkBase returns the last element of a symlink's target without following it.
func linkBase(path string) string {
	t, err := os.Readlink(path)
	if err != nil {
		return ""
	}
	return filepath.Base(t)
}

// collectPCI reads sys/bus/pci/devices. sysfs lists devices as symlinks to
// real dirs, so a device dir is resolved once and skipped unless it stays
// inside the root; its files are then read without following symlinks.
func (a *Adapter) collectPCI(ev *Evidence) {
	base, ents := a.scan(filepath.Join(a.root(), "sys/bus/pci/devices"))
	for _, e := range ents {
		bdf := e.Name()
		if !bdfRE.MatchString(bdf) {
			continue
		}
		dir, ok := a.confine(filepath.Join(base, bdf))
		if !ok {
			continue
		}
		d := PCIDevice{NUMANode: -1, Driver: linkBase(filepath.Join(dir, "driver")), IOMMUGroup: linkBase(filepath.Join(dir, "iommu_group"))}
		if s, err := readSmall(filepath.Join(dir, "numa_node")); err == nil {
			if n, err := strconv.Atoi(s); err == nil {
				d.NUMANode = n
			}
		}
		d.Vendor, _ = readSmall(filepath.Join(dir, "vendor"))
		d.Device, _ = readSmall(filepath.Join(dir, "device"))
		if s, err := readSmall(filepath.Join(dir, "class")); err == nil {
			d.Class = classRE.FindString(strings.ToLower(strings.TrimPrefix(s, "0x")))
		}
		ev.PCI[bdf] = d
	}
}

// classRE keeps the 6-hex-digit class code (base, sub, interface).
var classRE = regexp.MustCompile(`^[0-9a-f]{6}`)
