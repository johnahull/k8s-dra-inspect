package testutil

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// FakeHost builds a synthetic node filesystem under a temp dir.
type FakeHost struct {
	Root string
	t    testing.TB
}

// NewFakeHost returns an empty fake root.
func NewFakeHost(t testing.TB) *FakeHost { return &FakeHost{Root: t.TempDir(), t: t} }

// File writes content at rel, creating parents.
func (h *FakeHost) File(rel, content string) {
	h.t.Helper()
	p := filepath.Join(h.Root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		h.t.Fatal(err)
	}
}

// Symlink creates rel pointing at target (verbatim, may be absolute or outside the root).
func (h *FakeHost) Symlink(rel, target string) {
	h.t.Helper()
	p := filepath.Join(h.Root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.Symlink(target, p); err != nil {
		h.t.Fatal(err)
	}
}

// NUMA adds a NUMA node with its cpulist, plus the sysfs node dir.
func (h *FakeHost) NUMA(id, cpulist string) {
	h.t.Helper()
	h.File("sys/devices/system/node/node"+id+"/cpulist", cpulist+"\n")
}

// PCI adds a PCI function as sysfs lays it out: a real dir under
// sys/devices and a relative symlink in sys/bus/pci/devices. Empty driver or
// group omits that symlink; a group also lists the device under iommu_groups.
func (h *FakeHost) PCI(bdf, numa, vendor, device, driver, group string) {
	h.t.Helper()
	dir := "sys/devices/pci0000:3a/" + bdf
	h.File(dir+"/numa_node", numa+"\n")
	h.File(dir+"/vendor", vendor+"\n")
	h.File(dir+"/device", device+"\n")
	h.Symlink("sys/bus/pci/devices/"+bdf, "../../../devices/pci0000:3a/"+bdf)
	if driver != "" {
		h.Symlink(dir+"/driver", "../../../bus/pci/drivers/"+driver)
	}
	if group != "" {
		h.Symlink(dir+"/iommu_group", "../../../kernel/iommu_groups/"+group)
		h.Symlink("sys/kernel/iommu_groups/"+group+"/devices/"+bdf, "../../../../devices/pci0000:3a/"+bdf)
	}
}

// VFIONode adds a /dev/vfio entry.
func (h *FakeHost) VFIONode(name string) { h.File("dev/vfio/"+name, "") }

// PCIClass sets a PCI function's class file as sysfs prints it ("0x030000").
func (h *FakeHost) PCIClass(bdf, class string) {
	h.t.Helper()
	h.File("sys/devices/pci0000:3a/"+bdf+"/class", "0x"+class+"\n")
}

// CgroupSystemd adds a pod cgroup in the systemd driver layout. qos is
// "guaranteed", "burstable" or "besteffort". Each containers entry adds a
// container scope below the pod slice holding that cpuset.
func (h *FakeHost) CgroupSystemd(uid, qos, cpus string, containers ...string) {
	h.t.Helper()
	u := strings.ReplaceAll(uid, "-", "_")
	dir, name := "sys/fs/cgroup/kubepods.slice/", "kubepods-pod"+u+".slice"
	if qos != "guaranteed" {
		dir += "kubepods-" + qos + ".slice/"
		name = "kubepods-" + qos + "-pod" + u + ".slice"
	}
	h.File(dir+name+"/cpuset.cpus.effective", cpus+"\n")
	runtimes := []string{"cri-containerd", "crio", "docker"}
	for i, c := range containers {
		h.File(fmt.Sprintf("%s%s/%s-%064x.scope/cpuset.cpus.effective", dir, name, runtimes[i%len(runtimes)], i+1), c+"\n")
	}
}

// CgroupFS adds a pod cgroup in the cgroupfs driver layout; containers
// entries add container-ID directories below it.
func (h *FakeHost) CgroupFS(uid, qos, cpus string, containers ...string) {
	h.t.Helper()
	dir := "sys/fs/cgroup/kubepods/"
	if qos != "guaranteed" {
		dir += qos + "/"
	}
	h.File(dir+"pod"+uid+"/cpuset.cpus.effective", cpus+"\n")
	for i, c := range containers {
		h.File(fmt.Sprintf("%spod%s/%064x/cpuset.cpus.effective", dir, uid, i+1), c+"\n")
	}
}
