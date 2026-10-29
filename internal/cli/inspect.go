package cli

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/johnahull/k8s-dra-inspect/internal/collect"
	"github.com/johnahull/k8s-dra-inspect/internal/inspect"
	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/render"
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
	d := Deps{NewSource: liveSource, Now: time.Now}
	fd := int(os.Stdout.Fd())
	if term.IsTerminal(fd) {
		d.IsTTY = true
		d.Width, _, _ = term.GetSize(fd)
	}
	return d
}

func liveSource(o *Options) (model.Source, error) {
	if o.From != "" {
		return nil, errors.New("snapshot replay (--from) is not available yet")
	}
	live, err := collect.NewClients(o.Kubeconfig, o.Context, o.Timeout)
	if err != nil {
		return nil, err
	}
	live.Scope = collect.Scope{Namespace: o.Namespace}
	return live, nil
}

func addInspectCommands(parent *cobra.Command, opts *Options, d Deps) {
	var attrs, allAttrs, capacity bool
	add := func(use, short string, build func() inspect.Inspector) *cobra.Command {
		c := &cobra.Command{
			Use: use, Short: short, Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error { return runInspector(cmd, opts, d, build()) },
		}
		parent.AddCommand(c)
		return c
	}
	add("slices", "List every ResourceSlice generation (drivers and attributes use only the current generation)", inspect.Slices)
	add("drivers", "Summarize DRA drivers", func() inspect.Inspector { return inspect.Drivers(attrs) }).
		Flags().BoolVar(&attrs, "attributes", false, "also list each driver's attribute keys")
	a := add("attributes", "Show device attributes", func() inspect.Inspector { return inspect.Attributes(allAttrs, capacity) })
	a.Flags().BoolVar(&allAttrs, "all-attributes", false, "show every attribute, not only the topology set")
	a.Flags().BoolVar(&capacity, "include-capacity", false, "also show device capacity")
}

func runInspector(cmd *cobra.Command, opts *Options, d Deps, in inspect.Inspector) error {
	src, err := d.NewSource(opts)
	if err != nil {
		return err
	}
	snap, err := src.Snapshot(cmd.Context())
	if err != nil {
		return err
	}
	results := in.Run(snap)
	out := cmd.OutOrStdout()
	if opts.Output == "json" {
		err = render.JSON(out, snap.Source, d.Now(), results)
	} else {
		err = renderTable(cmd, opts, d, in, results)
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

func renderTable(cmd *cobra.Command, opts *Options, d Deps, in inspect.Inspector, results []model.Result) error {
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
