package todo

import "errors"

var (
	ErrServiceDependency = errors.New("todo service dependency")
	ErrInvalidRequest    = errors.New("invalid todo request")
	ErrNotFound          = errors.New("todo resource not found")
	ErrForbidden         = errors.New("todo operation forbidden")
	ErrConflict          = errors.New("todo version conflict")
	ErrInvalidTransition = errors.New("invalid workflow transition")
)
