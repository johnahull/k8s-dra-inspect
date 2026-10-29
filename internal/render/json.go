// Package render prints Results as JSON or as terminal tables.
package render

import (
	"encoding/json"
	"io"
	"time"

	"github.com/johnahull/k8s-dra-inspect/internal/model"
)

type envelope struct {
	SchemaVersion string         `json:"schemaVersion"`
	GeneratedAt   time.Time      `json:"generatedAt"`
	Source        string         `json:"source"`
	Results       []model.Result `json:"results"`
}

// JSON writes the versioned result envelope. Names are never abbreviated.
func JSON(w io.Writer, source string, generatedAt time.Time, results []model.Result) error {
	if results == nil {
		results = []model.Result{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(envelope{model.SchemaVersion, generatedAt, source, results})
}
