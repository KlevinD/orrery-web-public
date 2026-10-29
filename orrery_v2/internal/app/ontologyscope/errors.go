package ontologyscope

import "errors"

var ErrInvalidBinding = errors.New("ontology scope binding is invalid")
var ErrBindingNotFound = errors.New("ontology scope is unavailable")
var ErrBindingConflict = errors.New("ontology scope binding version conflict")
var ErrAccessDenied = errors.New("ontology scope access denied")
var ErrDependencyUnavailable = errors.New("ontology scope dependency is unavailable")

func IsNonDisclosingReadError(err error) bool {
	return errors.Is(err, ErrAccessDenied) ||
		errors.Is(err, ErrBindingNotFound) ||
		errors.Is(err, ErrBindingConflict) ||
		errors.Is(err, ErrInvalidBinding)
}
