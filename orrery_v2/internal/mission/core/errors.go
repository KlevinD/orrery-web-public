package core

import contracterrors "orrery_v2/internal/contract/errors"

type ValidationCode string

const (
	CodeMissingRequiredField ValidationCode = "missing_required_field"
	CodeInvalidRef           ValidationCode = "invalid_ref"
	CodeInvalidState         ValidationCode = "invalid_state"
	CodeConflict             ValidationCode = "conflict"
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
