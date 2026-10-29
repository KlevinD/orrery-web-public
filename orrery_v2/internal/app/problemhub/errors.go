package problemhub

import "errors"

var (
	ErrInvalidRequest = errors.New("invalid problem hub request")
	ErrNotFound       = errors.New("problem case not found")
	ErrConflict       = errors.New("problem hub conflict")
)
