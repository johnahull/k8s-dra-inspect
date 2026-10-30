package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"slices"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/johnahull/k8s-dra-inspect/internal/check"
	"github.com/johnahull/k8s-dra-inspect/internal/inspect"
	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/render"
	"github.com/johnahull/k8s-dra-inspect/internal/view"
)

const supportHelp = `Run the cluster report plus check.vfio, check.kubevirt and one adapter.<name>
result per optional adapter (host, kubevirt): pass when its evidence was
collected, unavailable (with the reason) when not. An unavailable adapter never
blocks the other results. check.cpupinning needs a pod and is not included.

A live run reads files below --host-root on this node (read-only; run it on the
node) and derives KubeVirt evidence from the VMIs already collected. With
--from, the evidence stored in the snapshot (snapshot capture --with-adapters)
is used and --host-root and --node-name are rejected.

With --strict, an unavailable adapter (for example the host adapter when not
running on the node) makes the exit code 3.`

func addReportCommands(parent *cobra.Command, opts *Options, d Deps) {
	var root, nodeName string
	support := &cobra.Command{
		Use:   "support",
		Short: "Run the cluster report plus the host and KubeVirt adapters",
		Long:  supportHelp,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := rejectHostFlagsWithFrom(cmd, opts); err != nil {
				return err
			}
			return runResults(cmd, opts, d, supportResults(opts, d, root, nodeName), func(rs []model.Result) error {
				return renderReport(cmd, opts, d, supportMembers, rs)
			}, view.Report("Support Report", supportMembers))
		},
	}
	support.Flags().StringVar(&root, "host-root", "/", "root of the node filesystem to read (live runs only)")
	support.Flags().StringVar(&nodeName, "node-name", "", "cluster node name of this host (default $NODE_NAME, then the hostname; live runs only)")
	parent.AddCommand(support)
	parent.AddCommand(&cobra.Command{
		Use:   "cluster",
		Short: "Run the core inspectors and checks and summarize the results",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runResults(cmd, opts, d, clusterResults(opts), func(rs []model.Result) error {
				return renderReport(cmd, opts, d, reportMembers, rs)
			}, view.Report("Cluster Report", reportMembers))
		},
	})
}

// clusterResults concatenates the safe core inspectors and checks.
func clusterResults(opts *Options) func(context.Context, model.Snapshot) (model.Snapshot, []model.Result) {
	return func(ctx context.Context, snap model.Snapshot) (model.Snapshot, []model.Result) {
		var out []model.Result
		for _, in := range []inspect.Inspector{
			inspect.Slices(), inspect.Drivers(false), inspect.Topology(true), inspect.DeviceClasses(labels.Everything()), inspect.Claims(),
		} {
			out = append(out, in.Run(snap)...)
		}
		for _, e := range check.Registry(opts.Namespace) {
			out = append(out, e.Run(ctx, snap)...)
		}
		return snap, out
	}
}

var reportMembers = []string{"inspect.slices", "inspect.drivers", "inspect.topology", "inspect.deviceclasses", "inspect.claims",
	check.NameAlignment, check.NameCounters, check.NameCapacity, check.NameTopology}

var supportMembers = append(slices.Clone(reportMembers), check.NameVFIO, check.NameKubeVirt, check.NameAdapterHost, check.NameAdapterKubeVirt)

// supportResults is the cluster report plus vfio, kubevirt and the adapter
// rows. A live run attaches adapter evidence to the in-memory snapshot first.
func supportResults(opts *Options, d Deps, root, nodeName string) func(context.Context, model.Snapshot) (model.Snapshot, []model.Result) {
	cluster := clusterResults(opts)
	return func(ctx context.Context, snap model.Snapshot) (model.Snapshot, []model.Result) {
		if opts.From == "" {
			snap = attachAdapters(ctx, d, hostConfig(root, nodeName), snap)
		}
		_, out := cluster(ctx, snap)
		for _, c := range []check.Check{check.VFIO(), check.KubeVirt("", opts.Namespace), check.AdapterHost(), check.AdapterKubeVirt()} {
			out = append(out, c(ctx, snap)...)
		}
		return snap, out
	}
}

var reportSpec = inspect.Spec{
	Columns: []render.Column{
		{Header: "CHECK", Min: 12}, {Header: "SUBJECT", Min: 12, Truncate: render.TruncateMiddle},
		{Header: "STATUS", Min: 6}, {Header: "DETAIL", Min: 10, Truncate: render.TruncateTail},
	},
	Row: func(r model.Result) []string { return []string{r.Check, r.Subject, string(r.Status), detail(r)} },
}

// renderReport prints a per-check status count table, then every result
// that is not a plain pass.
func renderReport(cmd *cobra.Command, opts *Options, d Deps, members []string, results []model.Result) error {
	out := cmd.OutOrStdout()
	// Every report member gets a summary row even when it produced no results.
	order := slices.Clone(members)
	counts := map[string]map[model.Status]int{}
	for _, c := range order {
		counts[c] = map[model.Status]int{}
	}
	var rest []model.Result
	for _, r := range results {
		if counts[r.Check] == nil {
			counts[r.Check] = map[model.Status]int{}
			order = append(order, r.Check)
		}
		counts[r.Check][r.Status]++
		if r.Status != model.StatusPass {
			rest = append(rest, r)
		}
	}
	var rows [][]string
	for _, c := range order {
		n := counts[c]
		rows = append(rows, []string{c, fmt.Sprint(n[model.StatusPass]), fmt.Sprint(n[model.StatusWarn]), fmt.Sprint(n[model.StatusFail]),
			fmt.Sprint(n[model.StatusUnavailable]), fmt.Sprint(n[model.StatusError])})
	}
	color := ColorEnabled(opts.NoColor, os.Getenv, d.IsTTY)
	cols := []render.Column{{Header: "CHECK", Min: 12}, {Header: "PASS", Min: 4}, {Header: "WARN", Min: 4}, {Header: "FAIL", Min: 4}, {Header: "UNAVAILABLE", Min: 11}, {Header: "ERROR", Min: 5}}
	if err := render.Table(out, cols, rows, render.TableOptions{Width: d.Width, Color: color}); err != nil {
		return err
	}
	if len(rest) == 0 {
		return nil
	}
	if err := blank(out); err != nil {
		return err
	}
	if err := render.Unavailable(out, rest); err != nil {
		return err
	}
	var detailRows [][]string
	for _, r := range rest {
		if r.Status != model.StatusUnavailable {
			detailRows = append(detailRows, reportSpec.Row(r))
		}
	}
	if len(detailRows) == 0 {
		return nil
	}
	return render.Table(out, reportSpec.Columns, detailRows, render.TableOptions{Width: d.Width, Color: color})
}

func blank(w io.Writer) error { _, err := fmt.Fprintln(w); return err }
