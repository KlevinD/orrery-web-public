package contract

import (
	"errors"
	"time"
)

var ErrInvalidRequest = errors.New("ontology view request is invalid")
var ErrUnavailable = errors.New("ontology resource is unavailable")
var ErrInvalidCursor = errors.New("ontology cursor is invalid")
var ErrCursorStale = errors.New("ontology cursor is stale")
var ErrStateConflict = errors.New("ontology state conflict")
var ErrQueryBudgetExceeded = errors.New("ontology query budget exceeded")
var ErrRateLimited = errors.New("ontology request is rate limited")

type RateLimitError struct {
	RetryAfter time.Duration
}

func (e RateLimitError) Error() string {
	return ErrRateLimited.Error()
}

func (e RateLimitError) Unwrap() error {
	return ErrRateLimited
}
