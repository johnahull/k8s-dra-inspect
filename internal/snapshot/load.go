package snapshot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
)

// maxBytes caps the snapshot size Read accepts.
var maxBytes int64 = 256 << 20

// Read decodes and validates a snapshot. The result is normalized and marked
// as replayed (Source = SourceSnapshot).
func Read(r io.Reader) (model.Snapshot, error) {
	var s model.Snapshot
	lr := &io.LimitedReader{R: r, N: maxBytes + 1}
	tooBig := errors.New("snapshot larger than 256 MiB")
	dec := json.NewDecoder(lr)
	if err := dec.Decode(&s); err != nil {
		if lr.N <= 0 {
			return model.Snapshot{}, tooBig
		}
		return model.Snapshot{}, err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		if lr.N <= 0 {
			return model.Snapshot{}, tooBig
		}
		return model.Snapshot{}, errors.New("unexpected data after snapshot")
	}
	if lr.N <= 0 {
		return model.Snapshot{}, tooBig
	}
	switch s.SchemaVersion {
	case model.SchemaVersion:
	case "":
		return model.Snapshot{}, errors.New("not a dra-inspect snapshot (no schemaVersion)")
	default:
		return model.Snapshot{}, fmt.Errorf("snapshot schemaVersion %q is not supported (this dra-inspect supports %q); re-capture with this dra-inspect version",
			s.SchemaVersion, model.SchemaVersion)
	}
	for _, k := range slices.Sorted(maps.Keys(s.Collection)) {
		switch st := s.Collection[k].State; st {
		case model.StateOK, model.StateForbidden, model.StateNotFound, model.StateError:
		default:
			return model.Snapshot{}, fmt.Errorf("collection %q has invalid state %q (want ok|forbidden|notFound|error)", k, st)
		}
	}
	s.Source = model.SourceSnapshot
	s.Normalize()
	return s, nil
}

// Load reads the snapshot file at path.
func Load(path string) (model.Snapshot, error) {
	f, err := os.Open(path)
	if err != nil {
		return model.Snapshot{}, err
	}
	defer func() { _ = f.Close() }()
	s, err := Read(f)
	if err != nil {
		return model.Snapshot{}, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

type fileSource string

func (p fileSource) Snapshot(context.Context) (model.Snapshot, error) { return Load(string(p)) }

// NewFileSource returns a Source that loads the file at path; it never
// touches the network.
func NewFileSource(path string) model.Source { return fileSource(path) }

// Scope narrows s to namespace ns: claims, pods and VMIs outside it are
// dropped. It never widens: a snapshot captured for another namespace is an
// error. An empty ns returns s unchanged.
func Scope(s model.Snapshot, ns string) (model.Snapshot, error) {
	if ns == "" || ns == s.Namespace {
		return s, nil
	}
	if s.Namespace != "" {
		return model.Snapshot{}, fmt.Errorf("snapshot was captured for namespace %q; cannot widen to %q", s.Namespace, ns)
	}
	s.Namespace = ns
	s.ResourceClaims = filter(s.ResourceClaims, ns)
	s.VMIs = filter(s.VMIs, ns)
	pods := make([]model.PodRef, 0, len(s.Pods))
	for _, p := range s.Pods {
		if p.Namespace == ns {
			pods = append(pods, p)
		}
	}
	s.Pods = pods
	s.Collection = maps.Clone(s.Collection)
	for _, k := range []string{"resourceclaims", "pods", "virtualmachineinstances"} {
		if st, ok := s.Collection[k]; ok && st.State == model.StateOK {
			s.Collection[k] = model.CollectionStatus{State: model.StateOK, Reason: "namespace " + ns + " only"}
		}
	}
	return s, nil
}

func filter[T any, P interface {
	*T
	GetNamespace() string
}](in []T, ns string) []T {
	out := make([]T, 0, len(in))
	for i := range in {
		if P(&in[i]).GetNamespace() == ns {
			out = append(out, in[i])
		}
	}
	return out
}
