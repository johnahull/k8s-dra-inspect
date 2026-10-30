package cli

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/johnahull/k8s-dra-inspect/internal/check"
	"github.com/johnahull/k8s-dra-inspect/internal/inspect"
	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/render"
)

// checkSpec lays out any check result: subject, status, then the reason for
// a non-pass result or a short summary otherwise.
var checkSpec = inspect.Spec{
	Columns: []render.Column{
		{Header: "SUBJECT", Min: 12, Truncate: render.TruncateMiddle},
		{Header: "STATUS", Min: 6},
		{Header: "DETAIL", Min: 10, Truncate: render.TruncateTail},
	},
	Row: func(r model.Result) []string { return []string{r.Subject, string(r.Status), detail(r)} },
}

func detail(r model.Result) string {
	for _, k := range []string{"reason", "summary"} {
		if s, ok := r.Details[k].(string); ok && s != "" {
			return s
		}
	}
	return "-"
}

const alignmentHelp = `Check allocated devices against claim constraints and NUMA/PCIe placement.

Only matchAttribute constraints are evaluated. distinctAttribute constraints
and derived attributes (alpha, declared on a request) are not evaluated; they
are reported as unavailable.

A claim's status is the worst of its findings, in this order:
error > fail > unavailable > warn > pass.`

const capacityHelp = `Check consumable-capacity allocations against published capacity.

Of a capacity requestPolicy, only the validRange min and max are validated;
step, validValues and default are not.`

const kubevirtHelp = `Map each VMI host device to its ResourceClaim allocation. VMI is a name or
ns/name; -n scopes it. Fails when the claim or its allocation result is
missing or a device is on another node than the VMI; warns when devices span
NUMA nodes without guestMappingPassthrough, or when guestMappingPassthrough is
set without dedicatedCpuPlacement. Claims generated from
resourceClaimTemplateName are not resolved in v1 and stay unavailable (upgrade path: join through the virt-launcher pod's
resourceClaimStatuses). Works from the VMIs already in the
snapshot and calls no extra API. Only host-device, claim, NUMA and CPU fields
of a VMI are read; cloud-init data, credentials and interfaces never are.`

func addCheckCommands(parent *cobra.Command, opts *Options, d Deps) {
	add := func(use, short, long string, args cobra.PositionalArgs, build func(args []string) check.Check) {
		parent.AddCommand(&cobra.Command{
			Use: use, Short: short, Long: long, Args: args,
			RunE: func(cmd *cobra.Command, args []string) error {
				c := build(args)
				return runResults(cmd, opts, d,
					func(ctx context.Context, snap model.Snapshot) (model.Snapshot, []model.Result) {
						return snap, c(ctx, snap)
					},
					func(rs []model.Result) error { return renderTable(cmd, opts, d, checkSpec, rs) }, nil)
			},
		})
	}
	add("alignment [POD|CLAIM]", "Check allocated devices against claim constraints and NUMA/PCIe placement", alignmentHelp, cobra.MaximumNArgs(1),
		func(args []string) check.Check {
			target := ""
			if len(args) == 1 {
				target = args[0]
			}
			return check.Alignment(target, opts.Namespace)
		})
	add("counters", "Check shared-counter capacity against allocated claims", "", cobra.NoArgs, func([]string) check.Check { return check.Counters() })
	add("capacity", "Check consumable-capacity allocations against published capacity", capacityHelp, cobra.NoArgs, func([]string) check.Check { return check.Capacity() })
	add("topology", "Validate published NUMA and PCIe-root attributes", "", cobra.NoArgs, func([]string) check.Check { return check.Topology() })
	add("kubevirt [VMI]", "Check KubeVirt VMI host devices against their DRA claims (derived from the snapshot's VMIs)", kubevirtHelp, cobra.MaximumNArgs(1),
		func(args []string) check.Check {
			target := ""
			if len(args) == 1 {
				target = args[0]
			}
			return check.KubeVirt(target, opts.Namespace)
		})
	addHostChecks(parent, opts, d)
}
