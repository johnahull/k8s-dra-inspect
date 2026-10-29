package host

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/johnahull/k8s-dra-inspect/internal/testutil"
)

func TestParseCPUList(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []int
		bad  bool
	}{
		{"0-3,8,10-11", []int{0, 1, 2, 3, 8, 10, 11}, false},
		{"5", []int{5}, false},
		{"", []int{}, false},
		{" 0-1 , 4 \n", []int{0, 1, 4}, false},
		{"0-3,2-5", []int{0, 1, 2, 3, 4, 5}, false},
		{"8,0", []int{0, 8}, false},
		{"a", nil, true},
		{"3-1", nil, true},
		{"1-", nil, true},
		{"-1", nil, true},
		{"1,,2", nil, true},
		{"0-4294967295", nil, true},
	} {
		got, err := ParseCPUList(tc.in)
		if (err != nil) != tc.bad || (!tc.bad && !slices.Equal(got, tc.want)) {
			t.Errorf("ParseCPUList(%q) = %v, %v; want %v bad=%v", tc.in, got, err, tc.want, tc.bad)
		}
	}
}

func collect(t *testing.T, h *testutil.FakeHost, node string) Evidence {
	t.Helper()
	raw, err := New(Config{Root: h.Root, NodeName: node}).Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var ev Evidence
	if err := json.Unmarshal(raw, &ev); err != nil {
		t.Fatal(err)
	}
	return ev
}

func TestAvailable(t *testing.T) {
	h := testutil.NewFakeHost(t)
	ok, reason, err := New(Config{Root: h.Root}).Available(context.Background())
	if ok || err != nil || !strings.Contains(reason, "no sysfs under "+h.Root+"; run on the node (or pass --host-root)") {
		t.Fatalf("empty root: %v %q %v", ok, reason, err)
	}
	h.NUMA("0", "0-1")
	if ok, reason, err := New(Config{Root: h.Root}).Available(context.Background()); !ok || reason != "" || err != nil {
		t.Fatalf("with node dir: %v %q %v", ok, reason, err)
	}
	h2 := testutil.NewFakeHost(t)
	h2.PCI("0000:3b:00.0", "0", "0x1234", "0x5678", "", "")
	if ok, _, _ := New(Config{Root: h2.Root}).Available(context.Background()); !ok {
		t.Fatal("pci bus alone should be available")
	}
}

func TestCollectEmptyRootIsNotFatal(t *testing.T) {
	ev := collect(t, testutil.NewFakeHost(t), "node-a")
	if ev.Version != 1 || ev.NodeName != "node-a" {
		t.Fatalf("%+v", ev)
	}
	if ev.NUMA == nil || ev.PCI == nil || ev.IOMMUGroups == nil || ev.VFIODevNodes == nil || ev.CDI == nil || ev.Cgroups == nil {
		t.Fatalf("nil collections: %+v", ev)
	}
}

func TestNodeNameIsConfigNeverHostname(t *testing.T) {
	h := testutil.NewFakeHost(t)
	host, _ := os.Hostname()
	raw, _ := New(Config{Root: h.Root}).Collect(context.Background())
	if host != "" && strings.Contains(string(raw), host) {
		t.Fatalf("hostname leaked: %s", raw)
	}
	if ev := collect(t, h, ""); ev.NodeName != "" {
		t.Fatalf("nodeName = %q", ev.NodeName)
	}
}

func TestCollectNUMA(t *testing.T) {
	h := testutil.NewFakeHost(t)
	h.NUMA("0", "0-3,8-11")
	h.NUMA("1", "4-7,12-15")
	h.File("sys/devices/system/node/node2/cpulist", "16-17")
	h.File("sys/devices/system/node/has_cpu", "0-2\n") // not a node dir
	ev := collect(t, h, "n")
	want := map[string]string{"0": "0-3,8-11", "1": "4-7,12-15", "2": "16-17"}
	if len(ev.NUMA) != 3 || ev.NUMA["0"] != want["0"] || ev.NUMA["1"] != want["1"] || ev.NUMA["2"] != want["2"] {
		t.Fatalf("numa = %v", ev.NUMA)
	}
}

func TestCollectNUMAUnreadableAndOversizedAreAbsent(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read chmod 000 files")
	}
	h := testutil.NewFakeHost(t)
	h.NUMA("0", "0-3")
	h.NUMA("1", "4-7")
	if err := os.Chmod(h.Root+"/sys/devices/system/node/node1/cpulist", 0); err != nil {
		t.Fatal(err)
	}
	h.File("sys/devices/system/node/node2/cpulist", strings.Repeat("1,", 70<<10))
	ev := collect(t, h, "n")
	if len(ev.NUMA) != 1 || ev.NUMA["0"] != "0-3" {
		t.Fatalf("numa = %v", ev.NUMA)
	}
}

func TestCollectPCI(t *testing.T) {
	h := testutil.NewFakeHost(t)
	h.PCI("0000:3b:00.0", "1", "0x1234", "0x5678", "vfio-pci", "7")
	h.PCI("0000:3b:00.1", "-1", "0x1234", "0x9abc", "", "")
	h.File("sys/bus/pci/devices/not-a-bdf/vendor", "0x1\n")
	ev := collect(t, h, "n")
	want := PCIDevice{NUMANode: 1, Driver: "vfio-pci", IOMMUGroup: "7", Vendor: "0x1234", Device: "0x5678"}
	if got := ev.PCI["0000:3b:00.0"]; got != want {
		t.Fatalf("pci = %+v, want %+v", got, want)
	}
	if got := ev.PCI["0000:3b:00.1"]; got != (PCIDevice{NUMANode: -1, Vendor: "0x1234", Device: "0x9abc"}) {
		t.Fatalf("unbound pci = %+v", got)
	}
	if len(ev.PCI) != 2 {
		t.Fatalf("pci = %v", ev.PCI)
	}
}

func TestPCINeverFollowsSymlinksOutOfRoot(t *testing.T) {
	outside := t.TempDir()
	for f, v := range map[string]string{"vendor": "0xdead", "device": "0xbeef", "numa_node": "5"} {
		if err := os.WriteFile(outside+"/"+f, []byte(v), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	h := testutil.NewFakeHost(t)
	h.PCI("0000:3b:00.0", "0", "0x1234", "0x5678", "", "1")
	// A file symlink to outside must not be read.
	h.Symlink("sys/devices/pci0000:3a/0000:3b:00.1/vendor", outside+"/vendor")
	h.File("sys/devices/pci0000:3a/0000:3b:00.1/device", "0x9abc")
	h.Symlink("sys/bus/pci/devices/0000:3b:00.1", "../../../devices/pci0000:3a/0000:3b:00.1")
	// A device dir symlink to outside must be skipped.
	h.Symlink("sys/bus/pci/devices/0000:99:00.0", outside)
	// A driver link to outside is recorded by name only.
	h.Symlink("sys/devices/pci0000:3a/0000:3b:00.0/driver", outside)
	ev := collect(t, h, "n")
	if _, ok := ev.PCI["0000:99:00.0"]; ok {
		t.Fatalf("escaping device dir followed: %+v", ev.PCI)
	}
	if got := ev.PCI["0000:3b:00.1"]; got.Vendor != "" || got.Device != "0x9abc" {
		t.Fatalf("symlinked file read: %+v", got)
	}
	if got := ev.PCI["0000:3b:00.0"].Driver; got != filepath.Base(outside) {
		t.Fatalf("driver = %q", got)
	}
}

func TestCollectIOMMUGroupsAndVFIONodes(t *testing.T) {
	h := testutil.NewFakeHost(t)
	h.PCI("0000:3b:00.1", "0", "0x1", "0x2", "vfio-pci", "7")
	h.PCI("0000:3b:00.0", "0", "0x1", "0x2", "vfio-pci", "7")
	h.PCI("0000:5e:00.0", "1", "0x1", "0x2", "", "12")
	h.VFIONode("vfio")
	h.VFIONode("7")
	ev := collect(t, h, "n")
	if g := ev.IOMMUGroups["7"]; !slices.Equal(g, []string{"0000:3b:00.0", "0000:3b:00.1"}) {
		t.Fatalf("groups = %v", ev.IOMMUGroups)
	}
	if len(ev.IOMMUGroups) != 2 || !slices.Equal(ev.IOMMUGroups["12"], []string{"0000:5e:00.0"}) {
		t.Fatalf("groups = %v", ev.IOMMUGroups)
	}
	if !slices.Equal(ev.VFIODevNodes, []string{"7", "vfio"}) {
		t.Fatalf("vfio = %v", ev.VFIODevNodes)
	}
}

const cdiJSON = `{"cdiVersion":"0.6.0","kind":"vendor.example.com/gpu",
 "annotations":{"note":"SECRET-ANNOTATION"},
 "devices":[{"name":"gpu0","annotations":{"a":"SECRET-ANNOTATION"},
   "containerEdits":{"env":["TOKEN=SECRET-ENV"],"deviceNodes":[{"path":"/dev/gpu0"},{"path":"/dev/ctl"}],
     "mounts":[{"hostPath":"/SECRET-MOUNT","containerPath":"/x"}],"hooks":[{"hookName":"createContainer","path":"/SECRET-HOOK"}]}},
  {"name":"gpu1","containerEdits":{"deviceNodes":[{"path":"/dev/gpu1"}]}}],
 "containerEdits":{"env":["T=SECRET-ENV"],"deviceNodes":[{"path":"/dev/common"}]}}`

const cdiYAML = `cdiVersion: "0.6.0"
kind: other.example.com/nic
devices:
- name: nic0
  containerEdits:
    env: ["X=SECRET-ENV"]
    mounts:
    - {hostPath: /SECRET-MOUNT, containerPath: /y}
`

func TestCollectCDIKeepsOnlyKindNamesCounts(t *testing.T) {
	h := testutil.NewFakeHost(t)
	h.File("var/run/cdi/gpu.json", cdiJSON)
	h.File("etc/cdi/nic.yaml", cdiYAML)
	h.File("etc/cdi/readme.txt", "ignored")
	raw, err := New(Config{Root: h.Root}).Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"SECRET-ENV", "SECRET-MOUNT", "SECRET-ANNOTATION", "SECRET-HOOK", "/dev/gpu0", "readme"} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("evidence leaks %q: %s", secret, raw)
		}
	}
	var ev Evidence
	_ = json.Unmarshal(raw, &ev)
	want := []CDISpec{
		{File: "gpu.json", Kind: "vendor.example.com/gpu", Devices: []string{"gpu0", "gpu1"}, DeviceNodeCount: 4},
		{File: "nic.yaml", Kind: "other.example.com/nic", Devices: []string{"nic0"}},
	}
	if len(ev.CDI) != 2 || !reflect.DeepEqual(ev.CDI, want) {
		t.Fatalf("cdi = %+v\nwant %+v", ev.CDI, want)
	}
}

func TestCollectCDIErrorsAreRecordedNotFatal(t *testing.T) {
	h := testutil.NewFakeHost(t)
	h.File("etc/cdi/bad.json", `{"kind": SECRET-GARBAGE`)
	h.File("etc/cdi/big.yaml", "kind: x/y\n#"+strings.Repeat("a", 1<<20))
	h.File("etc/cdi/ok.json", `{"kind":"a.example.com/b","devices":[{"name":"d"}]}`)
	raw, err := New(Config{Root: h.Root}).Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "SECRET-GARBAGE") {
		t.Fatalf("parse error leaks content: %s", raw)
	}
	var ev Evidence
	_ = json.Unmarshal(raw, &ev)
	by := map[string]CDISpec{}
	for _, s := range ev.CDI {
		by[s.File] = s
	}
	if by["bad.json"].Error == "" || by["bad.json"].Kind != "" {
		t.Errorf("bad.json = %+v", by["bad.json"])
	}
	if !strings.Contains(by["big.yaml"].Error, "exceeds") {
		t.Errorf("big.yaml = %+v", by["big.yaml"])
	}
	if by["ok.json"].Error != "" || len(by["ok.json"].Devices) != 1 {
		t.Errorf("ok.json = %+v", by["ok.json"])
	}
}

const (
	uidA = "00000000-0000-4000-8000-000000000001"
	uidB = "00000000-0000-4000-8000-000000000002"
	uidC = "00000000-0000-4000-8000-000000000003"
)

func TestCollectCgroupsSystemdAndCgroupfs(t *testing.T) {
	sd := testutil.NewFakeHost(t)
	sd.CgroupSystemd(uidA, "guaranteed", "2-5")
	sd.CgroupSystemd(uidB, "burstable", "0-15")
	sd.CgroupSystemd(uidC, "besteffort", "0-15")
	sd.File("sys/fs/cgroup/kubepods.slice/cpuset.cpus.effective", "0-15")
	ev := collect(t, sd, "n")
	if len(ev.Cgroups) != 3 {
		t.Fatalf("systemd cgroups = %+v", ev.Cgroups)
	}
	if g := ev.Cgroups[uidA]; g.CPUs != "2-5" || g.Path != "sys/fs/cgroup/kubepods.slice/kubepods-pod00000000_0000_4000_8000_000000000001.slice" {
		t.Errorf("guaranteed = %+v", g)
	}
	if g := ev.Cgroups[uidB]; g.CPUs != "0-15" || !strings.Contains(g.Path, "kubepods-burstable.slice/kubepods-burstable-pod") {
		t.Errorf("burstable = %+v", g)
	}
	if g := ev.Cgroups[uidC]; g.CPUs != "0-15" || !strings.Contains(g.Path, "kubepods-besteffort-pod") {
		t.Errorf("besteffort = %+v", g)
	}

	fs := testutil.NewFakeHost(t)
	fs.CgroupFS(uidA, "guaranteed", "2-5")
	fs.CgroupFS(uidB, "burstable", "6")
	fs.CgroupFS(uidC, "besteffort", "7")
	ev = collect(t, fs, "n")
	if len(ev.Cgroups) != 3 || ev.Cgroups[uidA].CPUs != "2-5" || ev.Cgroups[uidB].CPUs != "6" || ev.Cgroups[uidC].CPUs != "7" ||
		ev.Cgroups[uidA].Path != "sys/fs/cgroup/kubepods/pod"+uidA {
		t.Fatalf("cgroupfs cgroups = %+v", ev.Cgroups)
	}
}

func TestCollectCgroupsMissingOrUnreadableCpusetIsRecorded(t *testing.T) {
	h := testutil.NewFakeHost(t)
	h.File("sys/fs/cgroup/kubepods.slice/kubepods-pod00000000_0000_4000_8000_000000000001.slice/memory.max", "max")
	ev := collect(t, h, "n")
	if g, ok := ev.Cgroups[uidA]; !ok || g.CPUs != "" || g.Error == "" {
		t.Fatalf("missing cpuset = %+v", ev.Cgroups)
	}
	if os.Geteuid() == 0 {
		t.Skip("root can read chmod 000 files")
	}
	h.CgroupFS(uidB, "guaranteed", "1")
	if err := os.Chmod(h.Root+"/sys/fs/cgroup/kubepods/pod"+uidB+"/cpuset.cpus.effective", 0); err != nil {
		t.Fatal(err)
	}
	ev = collect(t, h, "n")
	if g := ev.Cgroups[uidB]; g.CPUs != "" || g.Error == "" {
		t.Fatalf("unreadable cpuset = %+v", g)
	}
}

func TestCgroupScanIgnoresSymlinksAndForeignNames(t *testing.T) {
	outside := t.TempDir()
	if err := os.WriteFile(outside+"/cpuset.cpus.effective", []byte("9"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := testutil.NewFakeHost(t)
	h.Symlink("sys/fs/cgroup/kubepods.slice/kubepods-pod"+strings.ReplaceAll(uidA, "-", "_")+".slice", outside)
	h.File("sys/fs/cgroup/kubepods.slice/kubepods-podNOTAUID.slice/cpuset.cpus.effective", "1")
	h.File("sys/fs/cgroup/kubepods.slice/system.slice/cpuset.cpus.effective", "1")
	if ev := collect(t, h, "n"); len(ev.Cgroups) != 0 {
		t.Fatalf("cgroups = %+v", ev.Cgroups)
	}
}
