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

// attachHost runs the host adapter and returns snap with Evidence["host"] set
// on a copy of the map. Missing prerequisites or failures become evidence that
// records the reason, so the check reports unavailable instead of failing.
func attachHost(ctx context.Context, newHost NewHostFunc, cfg host.Config, snap model.Snapshot) model.Snapshot {
	var ev model.Evidence
	if newHost == nil {
		ev = host.Unavailable("host adapter is not configured")
	} else {
		a := newHost(cfg)
		ok, reason, err := a.Available(ctx)
		switch {
		case err != nil:
			ev = host.Unavailable("host adapter check failed: " + err.Error())
		case !ok:
			ev = host.Unavailable(reason)
		default:
			if ev, err = a.Collect(ctx); err != nil {
				ev = host.Unavailable("host evidence collection failed: " + err.Error())
			}
		}
	}
	m := make(map[string]json.RawMessage, len(snap.Evidence)+1)
	maps.Copy(m, snap.Evidence)
	m[host.Name] = ev
	snap.Evidence = m
	return snap
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
	add := func(use, short string, args cobra.PositionalArgs, build func(args []string) check.Check) {
		cmd := &cobra.Command{
			Use: use, Short: short, Long: short + "\n\n" + hostHelp, Args: args,
			RunE: func(cmd *cobra.Command, args []string) error {
				if opts.From != "" {
					for _, f := range []string{"host-root", "node-name"} {
						if cmd.Flags().Changed(f) {
							return fmt.Errorf("--%s cannot be used with --from (host evidence comes from the snapshot)", f)
						}
					}
				}
				c := build(args)
				return runResults(cmd, opts, d,
					func(ctx context.Context, snap model.Snapshot) []model.Result {
						// A snapshot (--from) is replayed as is; a live run reads this node first.
						if opts.From == "" {
							cfg := host.Config{Root: root, NodeName: resolveNodeName(nodeName, os.Getenv, os.Hostname)}
							snap = attachHost(ctx, d.NewHost, cfg, snap)
						}
						return c(ctx, snap)
					},
					func(rs []model.Result) error { return renderTable(cmd, opts, d, checkSpec, rs) })
			},
		}
		cmd.Flags().StringVar(&root, "host-root", "/", "root of the node filesystem to read (live runs only)")
		cmd.Flags().StringVar(&nodeName, "node-name", "", "cluster node name of this host (default $NODE_NAME, then the hostname; live runs only)")
		parent.AddCommand(cmd)
	}
	add("cpupinning POD", "Compare a pod's pinned CPUs with its devices' NUMA nodes (host evidence)", cobra.ExactArgs(1),
		func(args []string) check.Check { return check.CPUPinning(args[0], opts.Namespace) })
	add("vfio", "Check allocated PCI devices for vfio-pci binding and IOMMU groups (host evidence)", cobra.NoArgs,
		func([]string) check.Check { return check.VFIO() })
}
