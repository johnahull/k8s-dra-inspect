package cli

import (
	"errors"
	"fmt"
	"time"
)

// Options holds the global flags shared by every command.
type Options struct {
	Kubeconfig string
	Context    string
	Namespace  string
	Output     string
	NoColor    bool
	Timeout    time.Duration
	From       string // snapshot path; empty means live cluster
	Strict     bool
}

// Validate rejects unsupported flag values.
func (o *Options) Validate() error {
	if o.Output != "text" && o.Output != "table" && o.Output != "json" {
		return fmt.Errorf("invalid output format %q: must be text, table or json", o.Output)
	}
	if o.Timeout < 0 {
		return fmt.Errorf("invalid --timeout %v: must be >= 0 (0 means no deadline)", o.Timeout)
	}
	if o.From != "" && (o.Kubeconfig != "" || o.Context != "") {
		return errors.New("--from cannot be combined with --kubeconfig or --context")
	}
	return nil
}
