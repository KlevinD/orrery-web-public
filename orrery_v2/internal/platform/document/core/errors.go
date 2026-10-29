package core

import "errors"

var (
	ErrInvalid     = errors.New("document invalid")
	ErrUnsupported = errors.New("document unsupported")
)
