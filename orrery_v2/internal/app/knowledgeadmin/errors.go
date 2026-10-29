package knowledgeadmin

import "errors"

var (
	ErrInvalidRequest    = errors.New("knowledge invalid request")
	ErrInvalidViewer     = errors.New("knowledge invalid viewer")
	ErrPermissionDenied  = errors.New("knowledge permission denied")
	ErrServiceDependency = errors.New("knowledge service dependency unavailable")
)
