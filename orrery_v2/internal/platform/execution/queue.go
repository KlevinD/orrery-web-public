package execution

import (
	"context"
	"errors"
	"time"
)

var ErrRetryable = errors.New("retryable job failure")

type RetryAfterError struct {
	Err   error
	Delay time.Duration
}

func (e RetryAfterError) Error() string { return e.Err.Error() }
func (e RetryAfterError) Unwrap() error { return e.Err }

func RetryAfter(err error, delay time.Duration) error {
	if err == nil {
		return nil
	}
	return RetryAfterError{Err: err, Delay: max(0, delay)}
}

type PermanentError struct{ Err error }

func (e PermanentError) Error() string { return e.Err.Error() }
func (e PermanentError) Unwrap() error { return e.Err }

func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return PermanentError{Err: err}
}

type Job struct {
	ID            string
	Topic         string
	Payload       []byte
	WorkerID      string
	QueueKey      string
	Priority      int
	Status        Status
	Attempts      int
	MaxAttempts   int
	LastError     string
	FailureReason string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	DispatchedAt  time.Time
	StartedAt     time.Time
	LeaseUntil    time.Time
	CompletedAt   time.Time
	NextAttemptAt time.Time
}

type Status string

const (
	StatusQueued     Status = "queued"
	StatusDispatched Status = "dispatched"
	StatusRunning    Status = "running"
	StatusCompleted  Status = "completed"
	StatusFailed     Status = "failed"
	StatusCancelled  Status = "cancelled"
	StatusDeadLetter Status = "dead_letter"
)

type CancelDisposition string

const (
	CancelApplied          CancelDisposition = "applied"
	CancelAlreadyCancelled CancelDisposition = "already_cancelled"
	CancelAlreadyTerminal  CancelDisposition = "already_terminal"
	CancelMissing          CancelDisposition = "missing"
)

type CancelResult struct {
	Disposition CancelDisposition
	Status      Status
}

type Handler func(ctx context.Context, job Job) error
