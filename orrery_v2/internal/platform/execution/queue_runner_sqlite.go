package execution

import (
	"context"
	"errors"
	"fmt"
	"time"

	"orrery_v2/internal/platform/logging"
)

type QueueRunner struct {
	queue    *Queue
	workerID string
	handler  Handler
	now      func() time.Time
	logger   logging.Logger
}

func NewQueueRunner(queue *Queue, workerID string, handler Handler, now func() time.Time) *QueueRunner {
	if now == nil {
		now = time.Now
	}
	return &QueueRunner{queue: queue, workerID: workerID, handler: handler, now: now}
}

func (r *QueueRunner) WithLogger(logger logging.Logger) *QueueRunner {
	r.logger = logger
	return r
}

func (r *QueueRunner) RunOnce(ctx context.Context) (bool, error) {
	if r.queue == nil {
		return false, fmt.Errorf("sqlite queue runner requires queue")
	}
	if r.handler == nil {
		return false, fmt.Errorf("sqlite queue runner requires handler")
	}
	claimedAt := r.now()
	job, ok, err := r.queue.Claim(ctx, r.workerID, claimedAt)
	if err != nil {
		r.logClaim(ctx, "error", "", err)
		return false, err
	}
	if !ok {
		r.logClaim(ctx, "empty", "", nil)
		return false, err
	}
	r.logClaim(ctx, "claimed", job.ID, nil)
	startedAt := r.now()
	if err := r.queue.Start(ctx, job.ID, r.workerID, startedAt); err != nil {
		return true, err
	}
	job.Status = StatusRunning
	job.StartedAt = startedAt.UTC()
	job.LeaseUntil = startedAt.UTC().Add(r.queue.claimTTL)
	handlerCtx, cancelHandler := context.WithCancel(withLeaseExtender(ctx, queueLeaseExtender{queue: r.queue, jobID: job.ID, workerID: r.workerID}))
	registration, err := r.queue.registerActiveHandler(ctx, job.ID, cancelHandler)
	if err != nil {
		cancelHandler()
		return true, err
	}
	defer r.queue.unregisterActiveHandler(job.ID, registration)
	heartbeatDone := make(chan error, 1)
	go func() {
		heartbeatErr := r.keepLeaseAlive(handlerCtx, job.ID)
		if heartbeatErr != nil {
			cancelHandler()
		}
		heartbeatDone <- heartbeatErr
	}()
	handlerErr := r.handler(handlerCtx, job)
	handlerWasCancelled := handlerCtx.Err() != nil
	cancelHandler()
	if heartbeatErr := <-heartbeatDone; heartbeatErr != nil {
		handlerErr = errors.Join(handlerErr, heartbeatErr)
	}
	if handlerWasCancelled {
		current, found, statusErr := r.queue.Get(ctx, job.ID)
		if statusErr != nil {
			return true, errors.Join(handlerErr, statusErr)
		}
		if found && current.Status == StatusCancelled {
			return true, nil
		}
	}
	if handlerErr != nil {
		failedAt := r.now()
		var permanent PermanentError
		if errors.As(handlerErr, &permanent) {
			if failErr := r.queue.FailPermanently(ctx, job.ID, r.workerID, handlerErr.Error(), failedAt); failErr != nil {
				return true, errors.Join(handlerErr, failErr)
			}
			return true, handlerErr
		}
		nextAttemptAt := failedAt
		var retryAfter RetryAfterError
		if errors.As(handlerErr, &retryAfter) && retryAfter.Delay > 0 {
			nextAttemptAt = failedAt.Add(retryAfter.Delay)
		}
		if failErr := r.queue.FailAt(ctx, job.ID, r.workerID, handlerErr.Error(), failedAt, nextAttemptAt); failErr != nil {
			return true, errors.Join(handlerErr, failErr)
		}
		return true, handlerErr
	}
	return true, r.queue.Complete(ctx, job.ID, r.workerID, r.now())
}

func (r *QueueRunner) keepLeaseAlive(ctx context.Context, jobID string) error {
	interval := r.queue.claimTTL / 3
	if interval <= 0 {
		interval = time.Nanosecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := r.queue.renewLease(ctx, jobID, r.workerID, r.now()); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return fmt.Errorf("keep queue job lease alive: %w", err)
			}
		}
	}
}

func (r *QueueRunner) logClaim(ctx context.Context, result, jobID string, err error) {
	if r.logger == nil {
		return
	}
	fields := []logging.Field{
		logging.String("component", "queue"),
		logging.String("operation", "queue.claim"),
		logging.String("worker_id", r.workerID),
		logging.String("claim_result", result),
	}
	if jobID != "" {
		fields = append(fields, logging.String("job_id", jobID))
	}
	if err != nil {
		fields = append(fields,
			logging.String("error_code", "queue_claim_failed"),
			logging.String("error", err.Error()),
		)
		r.logger.Warn(ctx, "queue claim failed", fields...)
		return
	}
	r.logger.Debug(ctx, "queue claim result", fields...)
}
