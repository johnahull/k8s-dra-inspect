// Package model holds the types shared by collectors, inspectors, and checks.
package model

// Status is the outcome of a single result.
type Status string

const (
	StatusPass        Status = "pass"
	StatusWarn        Status = "warn"
	StatusFail        Status = "fail"
	StatusUnavailable Status = "unavailable"
	StatusError       Status = "error"
)

// Result is the single result type returned by inspectors and checks.
type Result struct {
	Check   string         `json:"check"`
	Status  Status         `json:"status"`
	Subject string         `json:"subject"`
	Details map[string]any `json:"details,omitempty"`
}
