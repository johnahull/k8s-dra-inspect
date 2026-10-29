package host

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const uuidRE = `[0-9a-f]{8}%[0-9a-f]{4}%[0-9a-f]{4}%[0-9a-f]{4}%[0-9a-f]{12}`

var (
	// systemd driver: kubepods[-burstable|-besteffort]-pod<uid with _>.slice
	systemdPodRE = regexp.MustCompile(`^kubepods(?:-(?:burstable|besteffort))?-pod(` + strings.ReplaceAll(uuidRE, "%", "_") + `)\.slice$`)
	// cgroupfs driver: pod<uid>
	cgroupfsPodRE = regexp.MustCompile(`^pod(` + strings.ReplaceAll(uuidRE, "%", "-") + `)$`)
)

// collectCgroups reads pod-level cpuset.cpus.effective (cgroup v2) for both
// kubelet cgroup drivers. Only real directories are entered.
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
			ents, err := os.ReadDir(filepath.Join(a.root(), rel))
			if err != nil {
				continue
			}
			for _, e := range ents {
				m := l.re.FindStringSubmatch(e.Name())
				if m == nil || !e.IsDir() { // IsDir is false for a symlink
					continue
				}
				uid := strings.ReplaceAll(m[1], l.sep, "-")
				cg := Cgroup{Path: filepath.Join(rel, e.Name())}
				cpus, err := readSmall(filepath.Join(a.root(), cg.Path, "cpuset.cpus.effective"))
				if err != nil {
					cg.Error = "cpuset.cpus.effective not readable"
				}
				cg.CPUs = cpus
				ev.Cgroups[uid] = cg
			}
		}
	}
}
