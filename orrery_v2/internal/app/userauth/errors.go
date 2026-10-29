package userauth

import "errors"

var ErrServiceDependency = errors.New("user authentication service dependency is not configured")
var ErrInvalidRequest = errors.New("user authentication request is invalid")
var ErrInvalidCredentials = errors.New("user authentication credentials are invalid")
var ErrPlaintextPassword = errors.New("plaintext password payload is not accepted")
var ErrUnauthorized = errors.New("user authentication token is invalid")
var ErrAccountNotFound = errors.New("user authentication account not found")
var ErrAccountConflict = errors.New("user authentication account identity is ambiguous")
var ErrIPLocked = errors.New("login IP is locked")
var ErrUsernameLocked = errors.New("login account is locked")
var ErrLoginThrottled = errors.New("login password verification is busy")
var ErrPasswordChangeRequired = errors.New("password change is required")
var ErrMFARequired = errors.New("multi-factor authentication is required")
var ErrMFAInvalid = errors.New("multi-factor authentication code is invalid")
