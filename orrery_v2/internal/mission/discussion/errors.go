package discussion

import "errors"

var (
	ErrInvalidArgument  = errors.New("discussion invalid argument")
	ErrPermissionDenied = errors.New("discussion permission denied")
	ErrNotFound         = errors.New("discussion not found")
	ErrConflict         = errors.New("discussion conflict")
)
