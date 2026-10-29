package task

import (
	"errors"

	contracterrors "orrery_v2/internal/contract/errors"
)

var ErrNotFound = errors.New("task record not found")

type ValidationCode string

const (
	CodeMissingRequiredField     ValidationCode = "missing_required_field"
	CodeInvalidRef               ValidationCode = "invalid_ref"
	CodeToolRefNotAgent          ValidationCode = "tool_ref_not_agent"
	CodeConflict                 ValidationCode = "conflict"
	CodeMissingSummary           ValidationCode = "missing_summary"
	CodeMissingDeliverable       ValidationCode = "missing_deliverable"
	CodeMissingRequiredEvidence  ValidationCode = "missing_required_evidence"
	CodeMissingAcceptanceMapping ValidationCode = "missing_acceptance_mapping"
)

type ValidationError struct {
	Code      ValidationCode
	Field     string
	Message   string
	Retryable bool
	Metadata  map[string]string
}

func validationError(code ValidationCode, field, message string) ValidationError {
	return ValidationError{
		Code:     code,
		Field:    field,
		Message:  message,
		Metadata: map[string]string{},
	}
}

func (e ValidationError) Error() string {
	return e.Message
}

func (e ValidationError) ContractError() contracterrors.Error {
	return contracterrors.Error{
		Code:      contracterrors.Code(e.Code),
		Field:     e.Field,
		Message:   e.Message,
		Retryable: e.Retryable,
		Metadata:  cloneMetadata(e.Metadata),
	}
}

type ConflictError struct {
	Code         ValidationCode
	ConflictCode string
	Field        string
	Message      string
	Retryable    bool
	Metadata     map[string]string
}

func NewConflictError(conflictCode, field, message string) ConflictError {
	return ConflictError{
		Code:         CodeConflict,
		ConflictCode: conflictCode,
		Field:        field,
		Message:      message,
		Metadata:     map[string]string{},
	}
}

func (e ConflictError) Error() string {
	return e.Message
}

func cloneMetadata(metadata map[string]string) map[string]string {
	if metadata == nil {
		return map[string]string{}
	}
	cloned := make(map[string]string, len(metadata))
	for key, value := range metadata {
		cloned[key] = value
	}
	return cloned
}
