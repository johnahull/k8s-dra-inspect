// Package host reads node files below a root (default "/") into evidence for
// the cpupinning and vfio checks. It only reads: no writes, no exec, no /proc.
package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
)

// Name is the key under Snapshot.Evidence.
const Name = "host"

// Version is the evidence schema version written; MinVersion is the oldest
// readable one (version 1 has no container cpusets and no PCI classes).
const (
	Version    = 2
	MinVersion = 1
)

// Config selects the filesystem root and the cluster node name to record.
type Config struct {
	Root     string
	NodeName string
}

// PCIDevice is one PCI function.
type PCIDevice struct {
	NUMANode   int    `json:"numaNode"` // -1 when unknown
	Driver     string `json:"driver"`
	IOMMUGroup string `json:"iommuGroup"`
	Vendor     string `json:"vendor"`
	Device     string `json:"device"`
	Class      string `json:"class,omitempty"` // 6 hex digits, e.g. 030000
}

// CDISpec keeps only kind, device names and counts of one CDI spec file.
type CDISpec struct {
	File            string   `json:"file"`
	Kind            string   `json:"kind,omitempty"`
	Devices         []string `json:"devices,omitempty"`
	DeviceNodeCount int      `json:"deviceNodeCount,omitempty"`
	Error           string   `json:"error,omitempty"`
}

// Cgroup is a pod-level cpuset.
type Cgroup struct {
	Path       string   `json:"path,omitempty"`
	CPUs       string   `json:"cpus,omitempty"`
	Containers []string `json:"containers,omitempty"` // each container cgroup's cpuset, sorted, no IDs
	Error      string   `json:"error,omitempty"`
}

// Evidence is the producer-redacted host evidence.
type Evidence struct {
	Version      int                  `json:"version"`
	Error        string               `json:"error,omitempty"` // set instead of data when collection was not possible
	NodeName     string               `json:"nodeName"`
	NUMA         map[string]string    `json:"numa"`
	PCI          map[string]PCIDevice `json:"pci"`
	IOMMUGroups  map[string][]string  `json:"iommuGroups"`
	VFIODevNodes []string             `json:"vfioDevNodes"`
	CDI          []CDISpec            `json:"cdi"`
	Cgroups      map[string]Cgroup    `json:"cgroups"`
}

// Adapter is the host adapter.
type Adapter struct{ cfg Config }

// New returns an adapter reading below cfg.Root (default "/").
func New(cfg Config) *Adapter { return &Adapter{cfg} }

// Name implements adapters.Adapter.
func (a *Adapter) Name() string { return Name }

func (a *Adapter) root() string {
	if a.cfg.Root == "" {
		return "/"
	}
	return a.cfg.Root
}

// Available implements adapters.Adapter: true when sysfs NUMA or PCI dirs exist.
func (a *Adapter) Available(context.Context) (bool, string, error) {
	for _, d := range []string{"sys/devices/system/node", "sys/bus/pci"} {
		if fi, err := os.Lstat(filepath.Join(a.root(), d)); err == nil && fi.IsDir() {
			return true, "", nil
		}
	}
	return false, "no sysfs under " + a.root() + "; run on the node (or pass --host-root)", nil
}

// Collect implements adapters.Adapter. Absent or unreadable files leave the
// matching entries out; they never fail collection.
func (a *Adapter) Collect(ctx context.Context) (model.Evidence, error) {
	ev := Evidence{
		Version: Version, NodeName: a.cfg.NodeName,
		NUMA: map[string]string{}, PCI: map[string]PCIDevice{}, IOMMUGroups: map[string][]string{},
		VFIODevNodes: []string{}, CDI: []CDISpec{}, Cgroups: map[string]Cgroup{},
	}
	r := a.root()
	nodeDir, ents := a.scan(filepath.Join(r, "sys/devices/system/node"))
	for _, e := range ents {
		id, ok := strings.CutPrefix(e.Name(), "node")
		if _, err := strconv.Atoi(id); !ok || err != nil {
			continue
		}
		if d, ok := a.confine(filepath.Join(nodeDir, e.Name())); ok {
			if s, err := readSmall(filepath.Join(d, "cpulist")); err == nil {
				ev.NUMA[id] = s
			}
		}
	}
	a.collectPCI(&ev)
	a.collectCgroups(&ev)
	groups, gents := a.scan(filepath.Join(r, "sys/kernel/iommu_groups"))
	for _, g := range gents {
		if _, devs := a.scan(filepath.Join(groups, g.Name(), "devices")); len(devs) > 0 {
			ev.IOMMUGroups[g.Name()] = names(devs)
		}
	}
	ev.CDI = append(ev.CDI, a.collectCDI()...)
	_, vfio := a.scan(filepath.Join(r, "dev/vfio"))
	ev.VFIODevNodes = append(ev.VFIODevNodes, names(vfio)...)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return json.Marshal(ev)
}

// ParseCPUList parses a kernel cpulist such as "0-3,8,10-11" into a sorted,
// de-duplicated list. CPU ids above maxCPU are rejected and a fixed bitset
// bounds memory, so a hostile list cannot allocate more than maxCPU+1 flags.
func ParseCPUList(s string) ([]int, error) {
	out := []int{}
	s = strings.TrimSpace(s)
	if s == "" {
		return out, nil
	}
	var set [maxCPU + 1]bool
	for _, part := range strings.Split(s, ",") {
		lo, hi, isRange := strings.Cut(strings.TrimSpace(part), "-")
		a, err := parseCPU(lo)
		if err != nil {
			return nil, badCPUList(s)
		}
		b := a
		if isRange {
			if b, err = parseCPU(hi); err != nil || b < a {
				return nil, badCPUList(s)
			}
		}
		for i := a; i <= b; i++ {
			set[i] = true
		}
	}
	for i, ok := range set {
		if ok {
			out = append(out, i)
		}
	}
	return out, nil
}

func parseCPU(s string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err == nil && (n < 0 || n > maxCPU) || strings.ContainsAny(s, "+-") {
		err = errors.New("out of range")
	}
	return n, err
}

// maxCPU is the highest CPU id accepted (Linux CONFIG_NR_CPUS tops out near 8192).
const maxCPU = 16384

// Unavailable is evidence recording why collection was not possible.
func Unavailable(reason string) model.Evidence {
	b, _ := json.Marshal(Evidence{Version: Version, Error: reason})
	return b
}

// maxRead caps every file read.
const maxRead = 64 << 10

// badCPUList quotes at most 64 runes of the input so a hostile file cannot
// inflate the error.
func badCPUList(s string) error {
	if r := []rune(s); len(r) > 64 {
		s = string(r[:64]) + "…"
	}
	return fmt.Errorf("invalid cpulist %q", s)
}
