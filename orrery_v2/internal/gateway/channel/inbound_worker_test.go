package channel

import (
	"context"
	"errors"
	"orrery_v2/internal/gateway/protocol"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestInboundJobWorkerPersistsSanitizedFailureCause(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	queue := &recordingInboundJobQueue{
		job: InboundJob{
			ChannelKey: "weixin", AccountID: "bot-1", EventID: "message-1", ClaimToken: "claim-1", Attempts: 1, MaxAttempts: 3,
		},
		failed: make(chan struct{}),
		cancel: cancel,
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		RunInboundJobWorker(ctx, queue, "weixin", "bot-1", func(context.Context, InboundJob) error {
			return errors.New("PageIndex prompt budget exceeded api_key=do-not-store")
		}, nil)
	}()
	select {
	case <-queue.failed:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not record failure")
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not stop")
	}
	if !strings.Contains(queue.reason, "PageIndex prompt budget exceeded") || strings.Contains(queue.reason, "do-not-store") || !strings.Contains(queue.reason, "api_key=[redacted]") {
		t.Fatalf("failure reason = %q", queue.reason)
	}
}

func TestInboundJobWorkerReleasesClaimImmediatelyOnShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	queue := &recordingInboundJobQueue{
		job: InboundJob{
			ChannelKey: "weixin", AccountID: "bot-1", EventID: "message-1", ClaimToken: "claim-1", Attempts: 1, MaxAttempts: 3,
		},
		released: make(chan struct{}),
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		RunInboundJobWorker(ctx, queue, "weixin", "bot-1", func(ctx context.Context, _ InboundJob) error {
			cancel()
			<-ctx.Done()
			return ctx.Err()
		}, nil)
	}()
	select {
	case <-queue.released:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not release the interrupted claim")
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not stop")
	}
	if queue.reason != "" {
		t.Fatalf("shutdown interruption was recorded as failure: %q", queue.reason)
	}
}

type recordingInboundJobQueue struct {
	mu          sync.Mutex
	job         InboundJob
	claimed     bool
	reason      string
	nextAttempt time.Time
	failed      chan struct{}
	released    chan struct{}
	cancel      context.CancelFunc
}

func (q *recordingInboundJobQueue) EnqueueInboundJob(context.Context, InboundJob) (bool, error) {
	return false, errors.New("not implemented")
}

func (q *recordingInboundJobQueue) ClaimInboundJob(context.Context, string, string) (InboundJob, bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.claimed {
		return InboundJob{}, false, nil
	}
	q.claimed = true
	return q.job, true, nil
}

func (q *recordingInboundJobQueue) RenewInboundJob(context.Context, InboundJob) error {
	return nil
}

func (q *recordingInboundJobQueue) CompleteInboundJob(context.Context, InboundJob) error {
	return errors.New("not implemented")
}

func (q *recordingInboundJobQueue) ReleaseInboundJob(context.Context, InboundJob) error {
	if q.released != nil {
		close(q.released)
	}
	return nil
}

func (q *recordingInboundJobQueue) FailInboundJob(_ context.Context, _ InboundJob, reason string, nextAttempt time.Time) error {
	q.reason = reason
	q.nextAttempt = nextAttempt
	close(q.failed)
	q.cancel()
	return nil
}

func TestInboundJobWorkerDoesNotRetryPermanentFailures(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	queue := &recordingInboundJobQueue{job: InboundJob{Attempts: 1, MaxAttempts: 3}, failed: make(chan struct{}), cancel: cancel}
	done := make(chan struct{})
	go func() {
		defer close(done)
		RunInboundJobWorker(ctx, queue, "weixin", "bot", func(context.Context, InboundJob) error {
			return InboundProcessingError([]protocol.Event{{Type: protocol.EventError, Code: protocol.ErrorCodeGatewayPermissionDenied}})
		}, nil)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not stop")
	}
	if !queue.nextAttempt.IsZero() || !strings.Contains(queue.reason, protocol.ErrorCodeGatewayPermissionDenied) {
		t.Fatalf("permanent failure was scheduled for retry: %+v", queue)
	}
}
