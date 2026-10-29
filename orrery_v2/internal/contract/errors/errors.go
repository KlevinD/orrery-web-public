package errors

type Code string

const (
	CodeMissingRequiredField     Code = "missing_required_field"
	CodeInvalidRef               Code = "invalid_ref"
	CodeInvalidState             Code = "invalid_state"
	CodeInvalidAgentKind         Code = "invalid_agent_kind"
	CodeToolRefNotAgent          Code = "tool_ref_not_agent"
	CodeConflict                 Code = "conflict"
	CodeMissingSummary           Code = "missing_summary"
	CodeMissingDeliverable       Code = "missing_deliverable"
	CodeMissingRequiredEvidence  Code = "missing_required_evidence"
	CodeMissingAcceptanceMapping Code = "missing_acceptance_mapping"
	CodeMemoryDigestRequired     Code = "memory_digest_required"
	CodeContextSourceRequired    Code = "context_source_required"
)

type Error struct {
	Code      Code
	Field     string
	Message   string
	Retryable bool
	Metadata  map[string]string
}

type AsContractError interface {
	ContractError() Error
}

func New(code Code, field, message string) Error {
	return Error{
		Code:     code,
		Field:    field,
		Message:  message,
		Metadata: map[string]string{},
	}
}
