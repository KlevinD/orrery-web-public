package skill

import "errors"

var (
	ErrNotFound          = errors.New("skill not found")
	ErrInvalidDefinition = errors.New("invalid skill definition")
	ErrAlreadyExists     = errors.New("skill already exists")
	ErrDisabled          = errors.New("skill disabled")
	ErrVersionMismatch   = errors.New("skill version mismatch")
	ErrHydration         = errors.New("skill hydration failed")
	ErrConflict          = errors.New("skill definition changed")
)
