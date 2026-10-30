package model

import "context"

// Source yields a Snapshot, from a live cluster or a snapshot file.
type Source interface {
	Snapshot(ctx context.Context) (Snapshot, error)
}
