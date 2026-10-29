package evidence

type ValidationCode string

const (
	CodeMissingRequiredEvidence ValidationCode = "missing_required_evidence"
	CodeMissingAcceptanceMapping ValidationCode = "missing_acceptance_mapping"
	CodeEvidenceKindMismatch     ValidationCode = "evidence_kind_mismatch"
	CodeUntraceableEvidence      ValidationCode = "untraceable_evidence"
	CodeUnknownEvidence          ValidationCode = "unknown_evidence"
)

type ValidationError struct {
	Code    ValidationCode
	Field   string
	Message string
}
