package actioncontract

import "errors"

var ErrInvalidRequest = errors.New("action contract request is invalid")
var ErrAlreadyExists = errors.New("action contract record already exists")
var ErrNotFound = errors.New("action contract record not found")
