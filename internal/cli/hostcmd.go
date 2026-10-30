package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"

	"github.com/spf13/cobra"

	"github.com/johnahull/k8s-dra-inspect/internal/adapters"
	"github.com/johnahull/k8s-dra-inspect/internal/adapters/host"
	"github.com/johnahull/k8s-dra-inspect/internal/check"
	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/view"
)

// NewHostFunc builds the host adapter; tests replace it with a fake root.
type NewHostFunc func(host.Config) adapters.Adapter

// resolveNodeName picks the cluster node name: --node-name, else $NODE_NAME,
// else the OS hostname (empty when unknown). The adapter only stores this.
func resolveNodeName(flag string, getenv func(string) string, hostname func() (string, error)) string {
	if flag != "" {
		return flag
	}
	if v := getenv("NODE_NAME"); v != "" {
		return v
	}
	h, _ := hostname()
	return h
}

// runAdapter checks availability and collects. ok=false carries the reason
// the evidence is missing; an absent prerequisite is never an error.
func runAdapter(ctx context.Context, name string, a adapters.Adapter) (ev model.Evidence, reason string, ok bool) {
	avail, why, err := a.Available(ctx)
	switch {
	case err != nil:
		return nil, name + " adapter check failed: " + err.Error(), false
	case !avail:
		return nil, why, false
	}
	if ev, err = a.Collect(ctx); err != nil {
		return nil, name + " evidence collection failed: " + err.Error(), false
	}
	return ev, "", true
}

// withEvidence returns snap with Evidence[name] set on a copy of the map.
func withEvidence(snap model.Snapshot, name string, ev model.Evidence) model.Snapshot {
	m := make(map[string]json.RawMessage, len(snap.Evidence)+1)
	maps.Copy(m, snap.Evidence)
	m[name] = ev
	snap.Evidence = m
	return snap
}

// attachHost runs the host adapter and returns snap with Evidence["host"] set.
// Missing prerequisites or failures become evidence that records the reason,
// so the check reports unavailable instead of failing.
func attachHost(ctx context.Context, newHost NewHostFunc, cfg host.Config, snap model.Snapshot) model.Snapshot {
	if newHost == nil {
		return withEvidence(snap, host.Name, host.Unavailable("host adapter is not configured"))
	}
	ev, reason, ok := runAdapter(ctx, host.Name, newHost(cfg))
	if !ok {
		ev = host.Unavailable(reason)
	}
	return withEvidence(snap, host.Name, ev)
}

// rejectHostFlagsWithFrom refuses --host-root/--node-name on a replay.
func rejectHostFlagsWithFrom(cmd *cobra.Command, opts *Options) error {
	if opts.From == "" {
		return nil
	}
	for _, f := range []string{"host-root", "node-name"} {
		if cmd.Flags().Changed(f) {
			return fmt.Errorf("--%s cannot be used with --from (host evidence comes from the snapshot)", f)
		}
	}
	return nil
}

const hostHelp = `Reads files below --host-root and must run on the node itself; anywhere else
the result is unavailable. With --from, the snapshot's host evidence is used
and the node is not read; --host-root and --node-name cannot be combined with
--from. The check is read-only: it never writes, executes commands, or uses
sudo. Evidence is redacted by the collector: CDI specs keep only kind, device
names and counts. The only node identity stored is the resolved cluster node
name: --node-name, else $NODE_NAME, else the OS hostname as a last resort. In a
Job or Pod, set --node-name or $NODE_NAME from the downward API
(spec.nodeName); the hostname there is the pod's, not the node's.

cdiPresent is informational: it reports whether a CDI spec lists the device
and never changes the status.`

func addHostChecks(parent *cobra.Command, opts *Options, d Deps) {
	var root, nodeName string
	add := func(use, short string, text textView, args cobra.PositionalArgs, build func(args []string) check.Check) {
		cmd := &cobra.Command{
			Use: use, Short: short, Long: short + "\n\n" + hostHelp, Args: args,
			RunE: func(cmd *cobra.Command, args []string) error {
				if err := rejectHostFlagsWithFrom(cmd, opts); err != nil {
					return err
				}
				c := build(args)
				return runResults(cmd, opts, d,
					func(ctx context.Context, snap model.Snapshot) (model.Snapshot, []model.Result) {
						// A snapshot (--from) is replayed as is; a live run reads this node first.
						if opts.From == "" {
							cfg := host.Config{Root: root, NodeName: resolveNodeName(nodeName, os.Getenv, os.Hostname)}
							snap = attachHost(ctx, d.NewHost, cfg, snap)
						}
						return snap, c(ctx, snap)
					},
					func(rs []model.Result) error { return renderTable(cmd, opts, d, checkSpec, rs) }, text)
			},
		}
		cmd.Flags().StringVar(&root, "host-root", "/", "root of the node filesystem to read (live runs only)")
		cmd.Flags().StringVar(&nodeName, "node-name", "", "cluster node name of this host (default $NODE_NAME, then the hostname; live runs only)")
		parent.AddCommand(cmd)
	}
	add("cpupinning POD", "Compare a pod's pinned CPUs with its devices' NUMA nodes (host evidence)", view.CPUPinning, cobra.ExactArgs(1),
		func(args []string) check.Check { return check.CPUPinning(args[0], opts.Namespace) })
	add("vfio", "Check allocated PCI devices for vfio-pci binding and IOMMU groups (host evidence)", view.VFIO, cobra.NoArgs,
		func([]string) check.Check { return check.VFIO() })
}
