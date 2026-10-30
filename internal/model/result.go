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
	// Findings are the sentences behind a non-pass status, one per problem,
	// for text views. Details["reason"] is their "; "-join. Never serialized.
	Findings []Finding `json:"-"`
}

// Finding is one sentence a view prints, with the status it contributes.
type Finding struct {
	Status Status
	Text   string
}
