package host

import (
	"cmp"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"

	"sigs.k8s.io/yaml"
)

const maxCDI = 1 << 20

// cdiDoc decodes only the fields the evidence keeps. Env, mounts, hooks and
// annotations have no field here, so they cannot reach the output.
type cdiDoc struct {
	Kind    string `json:"kind"`
	Devices []struct {
		Name           string `json:"name"`
		ContainerEdits struct {
			DeviceNodes []json.RawMessage `json:"deviceNodes"`
		} `json:"containerEdits"`
	} `json:"devices"`
	ContainerEdits struct {
		DeviceNodes []json.RawMessage `json:"deviceNodes"`
	} `json:"containerEdits"`
}

func (a *Adapter) collectCDI() []CDISpec {
	var out []CDISpec
	seen := map[string]bool{}
	// var/run is usually a symlink to run: scan each resolved dir once, and
	// never one that resolves outside the root.
	for _, d := range []string{"etc/cdi", "run/cdi", "var/run/cdi"} {
		dir, ents := a.scan(filepath.Join(a.root(), d))
		if seen[dir] {
			continue
		}
		seen[dir] = true
		for _, e := range ents {
			name := e.Name()
			switch strings.ToLower(filepath.Ext(name)) {
			case ".json", ".yaml", ".yml":
			default:
				continue
			}
			spec := CDISpec{File: name}
			// Error text is fixed: parser errors can quote file content.
			b, err := readCapped(filepath.Join(dir, name), maxCDI)
			if err != nil {
				spec.Error = "cannot read spec"
				if strings.Contains(err.Error(), "size cap") {
					spec.Error = "spec exceeds 1 MiB size cap; skipped"
				}
				out = append(out, spec)
				continue
			}
			var doc cdiDoc
			if err := yaml.Unmarshal([]byte(b), &doc); err != nil {
				spec.Error = "invalid JSON/YAML"
				out = append(out, spec)
				continue
			}
			spec.Kind, spec.DeviceNodeCount = doc.Kind, len(doc.ContainerEdits.DeviceNodes)
			for _, dev := range doc.Devices {
				spec.Devices = append(spec.Devices, dev.Name)
				spec.DeviceNodeCount += len(dev.ContainerEdits.DeviceNodes)
			}
			out = append(out, spec)
		}
	}
	slices.SortFunc(out, func(x, y CDISpec) int { return cmp.Or(cmp.Compare(x.File, y.File), cmp.Compare(x.Kind, y.Kind)) })
	return out
}
