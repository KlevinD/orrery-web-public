package core

import "errors"

var (
	ErrNotFound          = errors.New("memory not found")
	ErrAccessDenied      = errors.New("memory access denied")
	ErrInvalid           = errors.New("invalid memory")
	ErrCandidateRequired = errors.New("memory candidate required")
)
