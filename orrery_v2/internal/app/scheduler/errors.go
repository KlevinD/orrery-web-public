package scheduler

import (
	"errors"
	"fmt"
)

var (
	ErrServiceDependency = errors.New("scheduler service dependency")
	ErrInvalidRequest    = errors.New("invalid scheduler request")
	ErrInvalidSchedule   = errors.New("invalid scheduler schedule")
	ErrInvalidTarget     = errors.New("invalid scheduler target")
	ErrNotFound          = errors.New("scheduler resource not found")
	ErrRevisionConflict  = errors.New("scheduler revision conflict")
)

type RunPersistenceError struct {
	Run ScheduledRun
	Err error
}

func (e RunPersistenceError) Error() string {
	return fmt.Sprintf("persist scheduler run %s: %v", e.Run.ScheduledRunID, e.Err)
}

func (e RunPersistenceError) Unwrap() error { return e.Err }
