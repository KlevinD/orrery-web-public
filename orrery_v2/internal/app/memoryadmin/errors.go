package memoryadmin

import "errors"

var (
	ErrServiceDependency = errors.New("memory admin service dependency missing")
	ErrInvalidViewer     = errors.New("invalid memory admin viewer")
	ErrPermissionDenied  = errors.New("memory admin permission denied")
	ErrInvalidRequest    = errors.New("invalid memory admin request")
	ErrNotFound          = errors.New("memory admin resource not found")
	ErrConflict          = errors.New("memory admin conflict")
)
