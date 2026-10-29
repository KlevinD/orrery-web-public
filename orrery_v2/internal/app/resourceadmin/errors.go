package resourceadmin

import "errors"

var (
	ErrServiceDependency = errors.New("resource admin service dependency missing")
	ErrInvalidViewer     = errors.New("invalid resource admin viewer")
	ErrPermissionDenied  = errors.New("resource admin permission denied")
	ErrInvalidRequest    = errors.New("invalid resource admin request")
	ErrNotFound          = errors.New("resource admin resource not found")
	ErrConflict          = errors.New("resource admin resource conflict")
)
