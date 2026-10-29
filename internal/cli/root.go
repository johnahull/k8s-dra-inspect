// Package cli wires the dra-inspect command tree.
package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
)

// ColorEnabled reports whether colored output is allowed.
func ColorEnabled(noColor bool, getenv func(string) string, isTTY bool) bool {
	return !noColor && getenv("NO_COLOR") == "" && isTTY
}

// NewRootCommand builds the command tree; flags bind to opts.
func NewRootCommand(opts *Options, version string) *cobra.Command {
	root := &cobra.Command{
		Use:           "dra-inspect",
		Short:         "Inspect and diagnose Kubernetes Dynamic Resource Allocation",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(*cobra.Command, []string) error {
			return opts.Validate()
		},
	}
	f := root.PersistentFlags()
	f.StringVar(&opts.Kubeconfig, "kubeconfig", "", "path to the kubeconfig file")
	f.StringVar(&opts.Context, "context", "", "kubeconfig context to use")
	f.StringVarP(&opts.Namespace, "namespace", "n", "", "namespace filter")
	f.StringVarP(&opts.Output, "output", "o", "table", "output format: table or json")
	f.BoolVar(&opts.NoColor, "no-color", false, "disable colored output")
	f.DurationVar(&opts.Timeout, "timeout", 30*time.Second, "timeout for cluster requests")
	f.StringVar(&opts.From, "from", "", "read from a snapshot file instead of a live cluster")
	f.BoolVar(&opts.Strict, "strict", false, "exit 3 when any result is unavailable")

	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print the dra-inspect version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), version)
			return err
		},
	})
	for _, g := range []struct{ name, short string }{
		{"inspect", "Inspect DRA resources"},
		{"check", "Run topology and allocation checks"},
		{"report", "Produce cluster and support reports"},
		{"snapshot", "Capture and replay cluster snapshots"},
	} {
		root.AddCommand(&cobra.Command{
			Use:   g.name,
			Short: g.short,
			Args:  cobra.NoArgs,
			RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
		})
	}
	return root
}

// Execute runs the CLI and returns the process exit code.
func Execute(version string) int {
	opts := &Options{}
	if err := NewRootCommand(opts, version).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	return 0
}
