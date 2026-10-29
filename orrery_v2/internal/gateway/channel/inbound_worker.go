package channel

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"
)

const (
	inboundJobPollInterval  = 250 * time.Millisecond
	inboundJobErrorInterval = 2 * time.Second
)

var inboundFailureSecretPattern = regexp.MustCompile(`(?i)\b(password|passwd|authorization|access[_-]?token|refresh[_-]?token|api[_-]?key|secret)\b[[:space:]]*[:=][[:space:]]*(bearer[[:space:]]+)?[^,;[:space:]]+`)

type InboundJobHandler func(context.Context, InboundJob) error

// RunInboundJobWorker processes one channel account's durable callback queue.
// Provider callback goroutines only enqueue work; all long-running agent work
// happens here after the provider has been acknowledged.
func RunInboundJobWorker(
	ctx context.Context,
	queue InboundJobQueue,
	channelKey string,
	accountID string,
	handler InboundJobHandler,
	logger Logger,
) {
	if queue == nil || handler == nil {
		return
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}

		job, found, err := queue.ClaimInboundJob(ctx, channelKey, accountID)
		if err != nil {
			logInboundWorkerError(logger, channelKey, accountID, "claim_failed")
			timer.Reset(inboundJobErrorInterval)
			continue
		}
		if !found {
			timer.Reset(inboundJobPollInterval)
			continue
		}

		processingCtx, stopRenewal := maintainInboundJobClaim(ctx, queue, job)
		handleErr := handler(processingCtx, job)
		renewErr := stopRenewal()
		if handleErr == nil && renewErr == nil {
			completeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err = queue.CompleteInboundJob(completeCtx, job)
			cancel()
			if err != nil {
				logInboundWorkerError(logger, channelKey, accountID, "complete_failed")
			}
		} else if ctx.Err() != nil {
			releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err = queue.ReleaseInboundJob(releaseCtx, job)
			cancel()
			if err != nil {
				logInboundWorkerError(logger, channelKey, accountID, "release_failed")
			}
		} else {
			failureCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			nextAttempt := time.Now().UTC().Add(inboundJobRetryDelay(job.Attempts))
			if renewErr == nil && IsPermanentInboundError(handleErr) {
				nextAttempt = time.Time{}
			}
			err = queue.FailInboundJob(failureCtx, job, inboundJobFailureReason(handleErr, renewErr), nextAttempt)
			cancel()
			if err != nil {
				logInboundWorkerError(logger, channelKey, accountID, "retry_schedule_failed")
			} else if !errors.Is(handleErr, context.Canceled) && !errors.Is(handleErr, context.DeadlineExceeded) {
				logInboundWorkerError(logger, channelKey, accountID, "processing_failed")
			}
		}
		timer.Reset(0)
	}
}

func inboundJobFailureReason(handleErr error, renewErr error) string {
	reason := "gateway inbound processing failed"
	if combined := errors.Join(handleErr, renewErr); combined != nil {
		reason = strings.Join(strings.Fields(combined.Error()), " ")
	}
	return inboundFailureSecretPattern.ReplaceAllString(reason, "$1=[redacted]")
}

func maintainInboundJobClaim(ctx context.Context, queue InboundJobQueue, job InboundJob) (context.Context, func() error) {
	leaseCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	var renewalErr error
	go func() {
		defer close(done)
		ticker := time.NewTicker(inboundEventRenewInterval)
		defer ticker.Stop()
		for {
			select {
			case <-leaseCtx.Done():
				return
			case <-ticker.C:
				renewCtx, renewCancel := context.WithTimeout(leaseCtx, inboundEventRenewTimeout)
				err := queue.RenewInboundJob(renewCtx, job)
				renewCancel()
				if err == nil {
					continue
				}
				if leaseCtx.Err() == nil {
					renewalErr = err
					cancel()
				}
				return
			}
		}
	}()
	var stopped bool
	stop := func() error {
		if !stopped {
			stopped = true
			cancel()
			<-done
		}
		return renewalErr
	}
	return leaseCtx, stop
}

func inboundJobRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := 5 * time.Second
	for current := 1; current < attempt && delay < 5*time.Minute; current++ {
		delay *= 2
		if delay > 5*time.Minute {
			delay = 5 * time.Minute
		}
	}
	return delay
}

func logInboundWorkerError(logger Logger, channelKey, accountID, code string) {
	if logger == nil {
		return
	}
	logger.Warn("gateway inbound worker error", map[string]any{
		"channel_key": strings.TrimSpace(channelKey),
		"account_id":  strings.TrimSpace(accountID),
		"error_code":  code,
	})
}
