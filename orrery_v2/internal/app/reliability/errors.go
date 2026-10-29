package reliability

import "errors"

var (
	ErrInvalidRequest      = errors.New("invalid reliability request")
	ErrNotFound            = errors.New("reliability record not found")
	ErrConflict            = errors.New("reliability record conflict")
	ErrProviderUnavailable = errors.New("reliability repair provider unavailable")
)
