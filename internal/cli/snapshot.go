package cli

import (
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
	"github.com/johnahull/k8s-dra-inspect/internal/snapshot"
)

func addSnapshotCommands(parent *cobra.Command, opts *Options, d Deps) {
	var file string
	var noRedact, force bool
	c := &cobra.Command{
		Use:   "capture",
		Short: "Capture a cluster snapshot for offline replay with --from",
		Long: `Collect the cluster state and write it as a JSON snapshot (default: stdout).

Sensitive fields (opaque driver parameters, last-applied annotations,
managedFields, VMI cloud-init data) are removed unless --no-redact is given.
Collections that could not be read are recorded in the file and listed on
stderr; they do not fail the capture unless --strict is set.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Check before collecting so a refusal costs no API calls.
			if _, err := os.Lstat(file); file != "" && !force && err == nil {
				return fmt.Errorf("%s already exists; use --force to overwrite", file)
			}
			src, err := d.NewSource(opts)
			if err != nil {
				return err
			}
			snap, err := src.Snapshot(cmd.Context())
			if err != nil {
				return err
			}
			if noRedact {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "warning: --no-redact: the snapshot may contain sensitive data; do not share it")
			} else {
				snap = model.Redact(snap)
			}
			if err := writeSnapshot(cmd.OutOrStdout(), file, snap); err != nil {
				return err
			}
			bad := notOK(snap.Collection)
			if bad != "" {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "collection incomplete: "+bad)
			}
			if bad != "" && opts.Strict {
				return &ExitError{Code: 3}
			}
			return nil
		},
	}
	c.Flags().StringVarP(&file, "file", "f", "", "write the snapshot to FILE (default: stdout)")
	c.Flags().BoolVar(&noRedact, "no-redact", false, "keep opaque parameters, cloud-init data and other sensitive fields")
	c.Flags().BoolVar(&force, "force", false, "overwrite an existing --file")
	parent.AddCommand(c)
}

// notOK lists the collections that were not read, as "key: state (reason)".
func notOK(c map[string]model.CollectionStatus) string {
	var parts []string
	for _, k := range slices.Sorted(maps.Keys(c)) {
		if st := c[k]; st.State != model.StateOK {
			p := k + ": " + st.State
			if st.Reason != "" {
				p += " (" + st.Reason + ")"
			}
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, "; ")
}

// writeSnapshot writes to stdout, or atomically to path via a 0600 temp file
// in the same directory.
func writeSnapshot(stdout io.Writer, path string, snap model.Snapshot) (err error) {
	if path == "" {
		return snapshot.Capture(stdout, snap)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".dra-inspect-snapshot-*") // created 0600
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}()
	if err = snapshot.Capture(tmp, snap); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
