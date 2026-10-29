package execution

import (
	"context"
	"fmt"
	"time"
)

type LeaseExtender interface {
	ExtendLease(context.Context, time.Time) error
}

type leaseExtenderKey struct{}

func LeaseExtenderFromContext(ctx context.Context) LeaseExtender {
	if ctx == nil {
		return nil
	}
	extender, _ := ctx.Value(leaseExtenderKey{}).(LeaseExtender)
	return extender
}

func withLeaseExtender(ctx context.Context, extender LeaseExtender) context.Context {
	if extender == nil {
		return ctx
	}
	return context.WithValue(ctx, leaseExtenderKey{}, extender)
}

type queueLeaseExtender struct {
	queue    *Queue
	jobID    string
	workerID string
}

func (e queueLeaseExtender) ExtendLease(ctx context.Context, leaseUntil time.Time) error {
	if e.queue == nil {
		return fmt.Errorf("queue is not configured")
	}
	return e.queue.ExtendLease(ctx, e.jobID, e.workerID, leaseUntil)
}
