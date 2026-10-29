package tool

import "errors"

var (
	ErrNotFound          = errors.New("tool not found")
	ErrInvalidDefinition = errors.New("invalid tool definition")
	ErrAlreadyExists     = errors.New("tool already exists")
	ErrDisabled          = errors.New("tool disabled")
	ErrPermissionDenied  = errors.New("permission denied")
	ErrVersionMismatch   = errors.New("tool version mismatch")
)
