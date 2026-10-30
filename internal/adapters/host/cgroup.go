package host

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

const uuidRE = `[0-9a-f]{8}%[0-9a-f]{4}%[0-9a-f]{4}%[0-9a-f]{4}%[0-9a-f]{12}`

var (
	// systemd driver: kubepods[-burstable|-besteffort]-pod<uid with _>.slice
	systemdPodRE = regexp.MustCompile(`^kubepods(?:-(?:burstable|besteffort))?-pod(` + strings.ReplaceAll(uuidRE, "%", "_") + `)\.slice$`)
	// cgroupfs driver: pod<uid>
	cgroupfsPodRE = regexp.MustCompile(`^pod(` + strings.ReplaceAll(uuidRE, "%", "-") + `)$`)
)

// maxContainers bounds the container cpusets kept for one pod.
const maxContainers = 512

// collectCgroups reads pod-level and per-container cpuset.cpus.effective
// (cgroup v2) for both kubelet cgroup drivers. The static CPU manager pins
// CPUs on the container cgroups below the pod cgroup, which the pod-level file
// does not show. Only real directories are entered.
func (a *Adapter) collectCgroups(ev *Evidence) {
	type layout struct {
		dirs []string
		re   *regexp.Regexp
		sep  string
	}
	for _, l := range []layout{
		{[]string{"kubepods.slice", "kubepods.slice/kubepods-burstable.slice", "kubepods.slice/kubepods-besteffort.slice"}, systemdPodRE, "_"},
		{[]string{"kubepods", "kubepods/burstable", "kubepods/besteffort"}, cgroupfsPodRE, "-"},
	} {
		for _, d := range l.dirs {
			rel := filepath.Join("sys/fs/cgroup", d)
			dir, ents := a.scan(filepath.Join(a.root(), rel))
			for _, e := range ents {
				m := l.re.FindStringSubmatch(e.Name())
				if m == nil || !e.IsDir() { // IsDir is false for a symlink
					continue
				}
				uid := strings.ReplaceAll(m[1], l.sep, "-")
				pod := filepath.Join(dir, e.Name())
				cg := Cgroup{Path: filepath.Join(rel, e.Name())}
				cpus, err := readSmall(filepath.Join(pod, "cpuset.cpus.effective"))
				if err != nil {
					cg.Error = "cpuset.cpus.effective not readable"
				}
				cg.CPUs = cpus
				cg.Containers = containerCpusets(pod)
				ev.Cgroups[uid] = cg
			}
		}
	}
}

// containerCpusets returns the sorted cpuset of every real child cgroup of a
// pod cgroup that has one (container IDs are not kept).
func containerCpusets(pod string) []string {
	ents, _ := os.ReadDir(pod)
	var out []string
	for _, c := range ents {
		if !c.IsDir() || len(out) >= maxContainers {
			continue
		}
		if s, err := readSmall(filepath.Join(pod, c.Name(), "cpuset.cpus.effective")); err == nil {
			out = append(out, s)
		}
	}
	slices.Sort(out)
	return out
}
