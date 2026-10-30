// Package snapshot writes and reads dra-inspect snapshot files.
package snapshot

import (
	"encoding/json"
	"io"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
)

// Capture writes s as indented JSON with a trailing newline. It stamps the
// schema version and sorts s's lists in place, so the same logical snapshot
// always yields the same bytes. It does not redact.
func Capture(w io.Writer, s model.Snapshot) error {
	s.SchemaVersion = model.SchemaVersion
	s.Normalize()
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}
