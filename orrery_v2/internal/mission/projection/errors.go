package projection

import "errors"

var (
	ErrOutOfOrderEvent      = errors.New("out-of-order mission event")
	ErrInvalidEventSequence = errors.New("invalid mission event sequence")
	ErrUnknownEventType     = errors.New("unknown mission event type")
	ErrUnknownEventVersion  = errors.New("unknown mission event version")
	ErrMissionMismatch      = errors.New("mission event does not belong to read model")
	ErrProjectionDependency = errors.New("projection dependency is not configured")
)
