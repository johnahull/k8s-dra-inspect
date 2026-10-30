// Package model holds the types shared by collectors, inspectors, and checks.
package model

import "strconv"

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

// Kind says what a Finding is about, so views choose their layout from it
// and never from the wording of Text.
type Kind string

const (
	KindOther              Kind = "other"
	KindUnavailable        Kind = "unavailable"         // a collection or input is missing
	KindError              Kind = "error"               // the check itself failed
	KindMissingDevice      Kind = "missing-device"      // an allocated device is in no ResourceSlice
	KindStaleDevice        Kind = "stale-device"        // only in a stale pool generation
	KindStaleSlice         Kind = "stale-slice"         // a slice of a stale generation
	KindIncomplete         Kind = "incomplete"          // fewer slices than the pool announces
	KindNUMASplit          Kind = "numa-split"          // devices share no NUMA node
	KindNUMAMissing        Kind = "numa-missing"        // devices publish no NUMA node
	KindPCIeRootMissing    Kind = "pcie-root-missing"   // devices publish no PCIe root
	KindAttrMissing        Kind = "attr-missing"        // a topology attribute is not published; Items names it
	KindTopologyConflict   Kind = "topology-conflict"   // vendor keys disagree at one tier
	KindInvalid            Kind = "invalid"             // an attribute or input is malformed
	KindConstraintViolated Kind = "constraint-violated" // matchAttribute: no common value
	KindConstraintType     Kind = "constraint-type"     // matchAttribute: equal text, different types
	KindConstraintMissing  Kind = "constraint-missing"  // matchAttribute: attribute not published
	KindNotEvaluated       Kind = "not-evaluated"       // derived or distinctAttribute
	KindCapacity           Kind = "capacity"
	KindCounter            Kind = "counter"
	KindClaimRef           Kind = "claim-ref" // a VMI points at a claim or request that does not resolve
	KindNode               Kind = "node"      // node unknown, unscheduled or different
	KindGuestNUMA          Kind = "guest-numa"
	KindVFIODriver         Kind = "vfio-driver"
	KindVFIOPeers          Kind = "vfio-peers" // IOMMU group peers not on vfio-pci; Items lists them
	KindIOMMUGroup         Kind = "iommu-group"
	KindDevNode            Kind = "dev-node"
	KindPinning            Kind = "pinning"
)

// Finding is one sentence a view prints, with the status it contributes. Text
// is the full sentence (the same words as the JSON reason). A finding that
// lists devices also sets Head, the sentence stem with a count, and Items, so
// a view can print one item per group instead of one long line.
type Finding struct {
	Status Status
	Kind   Kind
	Text   string
	Head   string
	Items  []string
}

// Plural is "1 device", "2 devices".
func Plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}
