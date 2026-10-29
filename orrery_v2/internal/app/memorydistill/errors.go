package memorydistill

import "errors"

var (
	ErrServiceDependency = errors.New("memory distillation dependency missing")
	ErrInvalidViewer     = errors.New("invalid memory distillation viewer")
	ErrPermissionDenied  = errors.New("memory distillation permission denied")
	ErrInvalidRequest    = errors.New("invalid memory distillation request")
	ErrNoCandidates      = errors.New("memory distillation produced no candidates")
)
