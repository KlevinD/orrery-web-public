package memorydiary

import "errors"

var (
	ErrServiceDependency = errors.New("memory diary service dependency missing")
	ErrInvalidRequest    = errors.New("invalid memory diary request")
	ErrForbidden         = errors.New("memory diary access forbidden")
	ErrNotFound          = errors.New("memory diary resource not found")
)
