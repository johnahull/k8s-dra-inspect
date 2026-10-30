package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/johnahull/k8s-dra-inspect/internal/adapters"
	"github.com/johnahull/k8s-dra-inspect/internal/adapters/host"
	"github.com/johnahull/k8s-dra-inspect/internal/collect"
	"github.com/johnahull/k8s-dra-inspect/internal/inspect"
	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/render"
	"github.com/johnahull/k8s-dra-inspect/internal/snapshot"
	"github.com/johnahull/k8s-dra-inspect/internal/view"
)

// Deps are the environment pieces tests replace.
type Deps struct {
	NewSource func(*Options) (model.Source, error)
	NewHost   NewHostFunc // host adapter factory; only the host checks call it
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
	d := Deps{
		NewSource: func(o *Options) (model.Source, error) { return newSource(o, liveSource) },
		NewHost:   func(c host.Config) adapters.Adapter { return host.New(c) },
		Now:       time.Now,
	}
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
	add := func(use, short string, text textView, build func() (inspect.Inspector, error)) *cobra.Command {
		c := &cobra.Command{
			Use: use, Short: short, Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				in, err := build()
				if err != nil {
					return err
				}
				return runInspector(cmd, opts, d, in, text)
			},
		}
		parent.AddCommand(c)
		return c
	}
	plain := func(f func() inspect.Inspector) func() (inspect.Inspector, error) {
		return func() (inspect.Inspector, error) { return f(), nil }
	}
	add("slices", "List every ResourceSlice generation (drivers and attributes use only the current generation)", view.Slices, plain(inspect.Slices))
	add("drivers", "Summarize DRA drivers", nil, plain(func() inspect.Inspector { return inspect.Drivers(attrs) })).
		Flags().BoolVar(&attrs, "attributes", false, "also list each driver's attribute keys")
	a := add("attributes", "Show device attributes", nil, plain(func() inspect.Inspector { return inspect.Attributes(allAttrs, capacity) }))
	t := add("topology", "Group devices by node, NUMA node and PCIe root", nil, plain(func() inspect.Inspector { return inspect.Topology(compact) }))
	t.Flags().BoolVarP(&compact, "compact", "t", false, "counts only, no device names")
	add("claims", "List ResourceClaims with allocation, node and consumers", view.Claims, plain(inspect.Claims))
	dc := add("deviceclasses", "List DeviceClasses (selector expressions and config are never shown)", nil, func() (inspect.Inspector, error) {
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

func runInspector(cmd *cobra.Command, opts *Options, d Deps, in inspect.Inspector, text textView) error {
	return runResults(cmd, opts, d,
		func(_ context.Context, snap model.Snapshot) (model.Snapshot, []model.Result) {
			return snap, in.Run(snap)
		},
		func(results []model.Result) error { return renderTable(cmd, opts, d, in.Spec, results) }, text)
}

// runResults loads the snapshot, produces results (and the snapshot they were
// computed from, which may carry adapter evidence), prints them as JSON, text
// or table, and maps their statuses to the exit code. A nil text view falls
// back to the table.
func runResults(cmd *cobra.Command, opts *Options, d Deps, produce func(context.Context, model.Snapshot) (model.Snapshot, []model.Result), table func([]model.Result) error, text textView) error {
	src, err := d.NewSource(opts)
	if err != nil {
		return err
	}
	snap, err := src.Snapshot(cmd.Context())
	if err != nil {
		return err
	}
	snap, results := produce(cmd.Context(), snap)
	switch {
	case opts.Output == "json":
		err = render.JSON(cmd.OutOrStdout(), snap.Source, d.Now(), results)
	case opts.Output == "text" && text != nil:
		err = renderText(cmd.OutOrStdout(), render.Style{Color: ColorEnabled(opts.NoColor, os.Getenv, d.IsTTY), Width: d.Width}, snap, results, text)
	default:
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

// textView prints one command's results through the Printer.
type textView func(p *render.Printer, snap model.Snapshot, results []model.Result)

// renderText prints UNAVAILABLE lines first, exactly as the table does, then
// the view. When every result is unavailable the output is just those lines
// (no banner, never wrapped), so scripts that grep them keep working.
func renderText(w io.Writer, st render.Style, snap model.Snapshot, results []model.Result, view textView) error {
	if err := render.Unavailable(w, results); err != nil {
		return err
	}
	if len(results) > 0 && !slices.ContainsFunc(results, func(r model.Result) bool { return r.Status != model.StatusUnavailable }) {
		return nil
	}
	p := render.NewPrinter(w, st)
	view(p, snap, results)
	return p.Err()
}
