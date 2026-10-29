package cli

import (
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
	if o.Output != "table" && o.Output != "json" {
		return fmt.Errorf("invalid output format %q: must be table or json", o.Output)
	}
	return nil
}
