package platform

import (
	"context"
	"fmt"
	"time"

	"orrery_v2/internal/platform/execution"
	"orrery_v2/internal/platform/outbox"
)

type OutboxDelivery struct {
	ID             string
	Topic          string
	Payload        []byte
	IdempotencyKey string
	Attempts       int
	MaxAttempts    int
}

type OutboxDeliveryHandler func(context.Context, OutboxDelivery) error

type ExecutionDelivery struct {
	ID          string
	Topic       string
	Payload     []byte
	Priority    int
	QueueKey    string
	Attempts    int
	MaxAttempts int
	lease       execution.LeaseExtender
}

type ExecutionDeliveryHandler func(context.Context, ExecutionDelivery) error

func outboxDeliveryFromJob(job outbox.Job) OutboxDelivery {
	return OutboxDelivery{
		ID:             job.ID,
		Topic:          job.Topic,
		Payload:        append([]byte(nil), job.Payload...),
		IdempotencyKey: job.IdempotencyKey,
		Attempts:       job.Attempts,
		MaxAttempts:    job.MaxAttempts,
	}
}

func executionDeliveryFromContext(ctx context.Context, job execution.Job) ExecutionDelivery {
	return ExecutionDelivery{
		ID:          job.ID,
		Topic:       job.Topic,
		Payload:     append([]byte(nil), job.Payload...),
		Priority:    job.Priority,
		QueueKey:    job.QueueKey,
		Attempts:    job.Attempts,
		MaxAttempts: job.MaxAttempts,
		lease:       execution.LeaseExtenderFromContext(ctx),
	}
}

func executionDeliveryFromJob(job execution.Job) ExecutionDelivery {
	return executionDeliveryFromContext(context.Background(), job)
}

func (d ExecutionDelivery) ExtendLease(ctx context.Context, leaseUntil time.Time) error {
	if d.lease == nil {
		return fmt.Errorf("execution lease extender is not configured")
	}
	return d.lease.ExtendLease(ctx, leaseUntil)
}
