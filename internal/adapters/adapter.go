// Package adapters defines optional, read-only evidence collectors that sit
// beside the core Kubernetes API inspection.
package adapters

import (
	"context"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
)

// Adapter collects evidence from outside the Kubernetes API. Available
// reports (false, reason, nil) when prerequisites are absent; absence is
// never an error. Collect must redact its own output: Snapshot redaction does
// not touch Evidence.
type Adapter interface {
	Name() string
	Available(ctx context.Context) (bool, string, error)
	Collect(ctx context.Context) (model.Evidence, error)
}
