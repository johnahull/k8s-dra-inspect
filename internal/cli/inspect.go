package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/johnahull/k8s-dra-inspect/internal/collect"
	"github.com/johnahull/k8s-dra-inspect/internal/inspect"
	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/render"
	"github.com/johnahull/k8s-dra-inspect/internal/snapshot"
)

// Deps are the environment pieces tests replace.
type Deps struct {
	NewSource func(*Options) (model.Source, error)
	Now       func() time.Time
	IsTTY     bool
	Width     int // table width; 0 means the renderer default
}

// ExitError carries a non-zero exit code from a command that already
// printed its output.
type ExitError struct{ Code int }

func (e *ExitError) Error() string { return fmt.Sprintf("exit status %d", e.Code) }

// DefaultDeps wires the live cluster and the real terminal.
func DefaultDeps() Deps {
	d := Deps{NewSource: func(o *Options) (model.Source, error) { return newSource(o, liveSource) }, Now: time.Now}
	fd := int(os.Stdout.Fd())
	if term.IsTerminal(fd) {
		d.IsTTY = true
		d.Width, _, _ = term.GetSize(fd)
	}
	return d
}

// newSource picks the snapshot file when --from is set (building no live
// clients) and the live constructor otherwise.
func newSource(o *Options, live func(*Options) (model.Source, error)) (model.Source, error) {
	if o.From == "" {
		return live(o)
	}
	return replaySource{snapshot.NewFileSource(o.From), o.Namespace}, nil
}

// replaySource loads the snapshot file and narrows it to the -n namespace.
type replaySource struct {
	file      model.Source
	namespace string
}

func (r replaySource) Snapshot(ctx context.Context) (model.Snapshot, error) {
	s, err := r.file.Snapshot(ctx)
	if err != nil {
		return model.Snapshot{}, err
	}
	return snapshot.Scope(s, r.namespace)
}

func liveSource(o *Options) (model.Source, error) {
	live, err := collect.NewClients(o.Kubeconfig, o.Context, o.Timeout)
	if err != nil {
		return nil, err
	}
	live.Scope = collect.Scope{Namespace: o.Namespace}
	return live, nil
}

func addInspectCommands(parent *cobra.Command, opts *Options, d Deps) {
	var attrs, allAttrs, capacity, compact bool
	var selector string
	add := func(use, short string, build func() (inspect.Inspector, error)) *cobra.Command {
		c := &cobra.Command{
			Use: use, Short: short, Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				in, err := build()
				if err != nil {
					return err
				}
				return runInspector(cmd, opts, d, in)
			},
		}
		parent.AddCommand(c)
		return c
	}
	plain := func(f func() inspect.Inspector) func() (inspect.Inspector, error) {
		return func() (inspect.Inspector, error) { return f(), nil }
	}
	add("slices", "List every ResourceSlice generation (drivers and attributes use only the current generation)", plain(inspect.Slices))
	add("drivers", "Summarize DRA drivers", plain(func() inspect.Inspector { return inspect.Drivers(attrs) })).
		Flags().BoolVar(&attrs, "attributes", false, "also list each driver's attribute keys")
	a := add("attributes", "Show device attributes", plain(func() inspect.Inspector { return inspect.Attributes(allAttrs, capacity) }))
	t := add("topology", "Group devices by node, NUMA node and PCIe root", plain(func() inspect.Inspector { return inspect.Topology(compact) }))
	t.Flags().BoolVarP(&compact, "compact", "t", false, "counts only, no device names")
	add("claims", "List ResourceClaims with allocation, node and consumers", plain(inspect.Claims))
	dc := add("deviceclasses", "List DeviceClasses (selector expressions and config are never shown)", func() (inspect.Inspector, error) {
		sel, err := labels.Parse(selector)
		if err != nil {
			return inspect.Inspector{}, fmt.Errorf("invalid --selector: %w", err)
		}
		return inspect.DeviceClasses(sel), nil
	})
	dc.Flags().StringVar(&selector, "selector", "", "Kubernetes label selector applied to DeviceClass labels")
	a.Flags().BoolVar(&allAttrs, "all-attributes", false, "show every attribute, not only the topology set")
	a.Flags().BoolVar(&capacity, "include-capacity", false, "also show device capacity")
}

func runInspector(cmd *cobra.Command, opts *Options, d Deps, in inspect.Inspector) error {
	return runResults(cmd, opts, d,
		func(_ context.Context, snap model.Snapshot) []model.Result { return in.Run(snap) },
		func(results []model.Result) error { return renderTable(cmd, opts, d, in.Spec, results) })
}

// runResults loads the snapshot, produces results, prints them as JSON or via
// table, and maps their statuses to the exit code.
func runResults(cmd *cobra.Command, opts *Options, d Deps, produce func(context.Context, model.Snapshot) []model.Result, table func([]model.Result) error) error {
	src, err := d.NewSource(opts)
	if err != nil {
		return err
	}
	snap, err := src.Snapshot(cmd.Context())
	if err != nil {
		return err
	}
	results := produce(cmd.Context(), snap)
	if opts.Output == "json" {
		err = render.JSON(cmd.OutOrStdout(), snap.Source, d.Now(), results)
	} else {
		err = table(results)
	}
	if err != nil {
		return err
	}
	statuses := make([]model.Status, len(results))
	for i, r := range results {
		statuses[i] = r.Status
	}
	if code := ExitCode(statuses, opts.Strict); code != 0 {
		return &ExitError{Code: code}
	}
	return nil
}

func renderTable(cmd *cobra.Command, opts *Options, d Deps, in inspect.Spec, results []model.Result) error {
	out := cmd.OutOrStdout()
	if err := render.Unavailable(out, results); err != nil {
		return err
	}
	var rows [][]string
	for _, r := range results {
		if r.Status != model.StatusUnavailable {
			rows = append(rows, in.Row(r))
		}
	}
	if len(rows) == 0 && len(results) > 0 {
		return nil
	}
	return render.Table(out, in.Columns, rows, render.TableOptions{
		Width: d.Width, Color: ColorEnabled(opts.NoColor, os.Getenv, d.IsTTY),
	})
}
