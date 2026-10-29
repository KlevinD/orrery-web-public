package operations

import "errors"

var ErrServiceDependency = errors.New("operations service dependency is not configured")
var ErrNotFound = errors.New("operations read model not found")
var ErrInvalidRequest = errors.New("operations request is invalid")
var ErrPermissionDenied = errors.New("operations permission denied")
var ErrVersionConflict = errors.New("operations version conflict")
