package contextgraph

import (
	"encoding/json"
	"time"
)

// Persistent graph timestamps are signed nanoseconds. Reject coordinates that
// would wrap around rather than silently selecting another point in history.
func TemporalCoordinateValid(at time.Time) bool {
	year := at.UTC().Year()
	return !at.IsZero() && year >= 1678 && year <= 2261
}

// TemporalEligible is shared by current inference and explicit as-of queries.
// Legacy static data has no recoverable knowledge-time history. Callers that
// require historical fidelity must separately exclude unversioned assertions.
func TemporalEligible(t TemporalMetadata, business, known time.Time) bool {
	return (t.BusinessValidFrom.IsZero() || !business.Before(t.BusinessValidFrom)) &&
		(t.BusinessValidTo == nil || business.Before(*t.BusinessValidTo)) &&
		(t.SourceRecordedAt.IsZero() || !known.Before(t.SourceRecordedAt)) &&
		(t.SourceSupersededAt == nil || known.Before(*t.SourceSupersededAt))
}

// A source conflict choice applies only where the offered assertion windows
// overlap. It cannot dispute an otherwise uncontested earlier/later period.
func AssertionStatusAt(f Fact, business time.Time) string {
	if f.Provenance.Kind != "knowledge_instances" || f.Status != "disputed" {
		return f.Status
	}
	raw, ok := f.Properties["adjudication"]
	if !ok {
		return f.Status
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return f.Status
	}
	var window struct {
		From time.Time  `json:"business_valid_from"`
		To   *time.Time `json:"business_valid_to"`
	}
	if json.Unmarshal(data, &window) != nil {
		return f.Status
	}
	if (!window.From.IsZero() && business.Before(window.From)) || (window.To != nil && !business.Before(*window.To)) {
		return "active"
	}
	return f.Status
}
