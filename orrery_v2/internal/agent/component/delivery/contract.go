package delivery

type ValidationCode string

const (
	CodeMissingSummary              ValidationCode = "missing_summary"
	CodeMissingExpectedDeliverable  ValidationCode = "missing_expected_deliverable"
	CodeUnexpectedDeliverable       ValidationCode = "unexpected_deliverable"
	CodeUntraceableDeliverable      ValidationCode = "untraceable_deliverable"
	CodeMissingDeliverableIdentity  ValidationCode = "missing_deliverable_identity"
)

type ValidationError struct {
	Code    ValidationCode
	Field   string
	Message string
}
