package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
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
	var noRedact, force, withAdapters bool
	var root, nodeName string
	c := &cobra.Command{
		Use:   "capture",
		Short: "Capture a cluster snapshot for offline replay with --from",
		Long: `Collect the cluster state and write it as a JSON snapshot (default: stdout).

Sensitive fields (opaque driver parameters, last-applied annotations,
managedFields, VMI cloud-init data) are removed unless --no-redact is given;
Nodes and VMIs are reduced to an allow-list. CEL selector text is kept in the
snapshot (replay needs it), as are Node labels, reservedFor pod names and VMI
names, UIDs and node names: review a snapshot before sharing it.
Collections that could not be read are recorded in the file and listed on
stderr; they do not fail the capture unless --strict is set.

--with-adapters also stores the evidence of the optional adapters in the file
so that report support --from can replay it: host evidence read below
--host-root (only when this machine has a node sysfs; otherwise the reason is
recorded) and KubeVirt evidence derived from the collected VMIs. Adapter
evidence is redacted by its producer and is kept even with --no-redact.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !withAdapters {
				for _, f := range []string{"host-root", "node-name"} {
					if cmd.Flags().Changed(f) {
						return fmt.Errorf("--%s requires --with-adapters", f)
					}
				}
			}
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
			if withAdapters {
				snap = attachAdapters(cmd.Context(), d, hostConfig(root, nodeName), snap)
			}
			if noRedact {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "warning: --no-redact: the snapshot may contain sensitive data; do not share it")
			} else {
				snap = model.Redact(snap)
			}
			if err := writeSnapshot(cmd.OutOrStdout(), file, force, snap); err != nil {
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
	c.Flags().BoolVar(&noRedact, "no-redact", false, "keep opaque parameters, cloud-init data and full Node/VMI objects (default removes them; CEL selector text is kept either way)")
	c.Flags().BoolVar(&force, "force", false, "overwrite an existing --file")
	c.Flags().BoolVar(&withAdapters, "with-adapters", false, "also store host and KubeVirt adapter evidence (replay with report support --from)")
	c.Flags().StringVar(&root, "host-root", "/", "root of the node filesystem to read (with --with-adapters)")
	c.Flags().StringVar(&nodeName, "node-name", "", "cluster node name of this host (with --with-adapters; default $NODE_NAME, then the hostname)")
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

// syncFile flushes the temp file to disk; a variable so tests can fail it.
var syncFile = func(f *os.File) error { return f.Sync() }

// writeSnapshot writes to stdout, or atomically to path via a 0600 temp file
// in the same directory.
func writeSnapshot(stdout io.Writer, path string, force bool, snap model.Snapshot) (err error) {
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
	if err = syncFile(tmp); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if force {
		return os.Rename(tmp.Name(), path)
	}
	// Link fails with EEXIST if the file appeared after the pre-check; rename would clobber it.
	switch lerr := os.Link(tmp.Name(), path); {
	case lerr == nil:
		_ = os.Remove(tmp.Name())
		return nil
	case errors.Is(lerr, fs.ErrExist):
		return fmt.Errorf("%s already exists; use --force to overwrite", path)
	default:
		// shortcut: filesystems without hard links fall back to the racy rename.
		return os.Rename(tmp.Name(), path)
	}
}
