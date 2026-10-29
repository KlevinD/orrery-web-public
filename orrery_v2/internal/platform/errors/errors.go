package errors

import (
	stderrors "errors"
	"fmt"
)

type Code string

const (
	CodeValidationError       Code = "validation_error"
	CodeNotFound              Code = "not_found"
	CodeConflict              Code = "conflict"
	CodePermissionDenied      Code = "permission_denied"
	CodeDependencyUnavailable Code = "dependency_unavailable"
	CodeTimeout               Code = "timeout"
	CodeCancelled             Code = "cancelled"
	CodeInternalError         Code = "internal_error"
)

type Error struct {
	Code      Code
	Message   string
	Cause     error
	Field     string
	Retryable bool
	Metadata  map[string]string
}

func New(code Code, message string) *Error {
	return &Error{Code: code, Message: message, Metadata: map[string]string{}}
}

func Wrap(code Code, message string, cause error) *Error {
	return &Error{Code: code, Message: message, Cause: cause, Metadata: map[string]string{}}
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error {
	return e.Cause
}

func (e *Error) WithField(field string) *Error {
	e.Field = field
	return e
}

func (e *Error) WithRetryable(retryable bool) *Error {
	e.Retryable = retryable
	return e
}

func (e *Error) WithMetadata(key, value string) *Error {
	if e.Metadata == nil {
		e.Metadata = map[string]string{}
	}
	e.Metadata[key] = value
	return e
}

func IsCode(err error, code Code) bool {
	var platformErr *Error
	if !stderrors.As(err, &platformErr) {
		return false
	}
	return platformErr.Code == code
}
