package outbox

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"orrery_v2/internal/platform/logging"
	"orrery_v2/internal/platform/metrics"
)

type Handler func(context.Context, Job) error

type Clock interface {
	Now() time.Time
}

type Backoff func(attempts int) time.Duration

type retryDelayError interface {
	RetryDelay() time.Duration
}

var (
	ErrLeaseLost      = errors.New("outbox job lease lost")
	ErrHandlerTimeout = fmt.Errorf("outbox handler timeout: %w", context.DeadlineExceeded)
)

const defaultHandlerTimeoutLeasePeriods = 10

type RelayConfig struct {
	WorkerID       string
	Clock          Clock
	Backoff        Backoff
	HandlerTimeout time.Duration
	Logger         logging.Logger
	Metrics        *metrics.BoundedSink
	Handlers       map[string]Handler
}

type RunnerConfig struct {
	PollInterval       time.Duration
	RecoverBatchSize   int
	CompletedRetention time.Duration
	PruneInterval      time.Duration
	PruneBatchSize     int
}

type Relay struct {
	store          *Store
	workerID       string
	clock          Clock
	backoff        Backoff
	handlerTimeout time.Duration
	logger         logging.Logger
	metrics        *metrics.BoundedSink
	handlers       map[string]Handler
}

func NewRelay(store *Store, cfg RelayConfig) *Relay {
	workerID := cfg.WorkerID
	if workerID == "" {
		workerID = "outbox-relay"
	}
	clock := cfg.Clock
	if clock == nil {
		clock = systemClock{}
	}
	backoff := cfg.Backoff
	if backoff == nil {
		backoff = FixedBackoff(time.Second)
	}
	handlerTimeout := cfg.HandlerTimeout
	if handlerTimeout <= 0 {
		handlerTimeout = defaultHandlerTimeout(store)
	}
	handlers := map[string]Handler{}
	for topic, handler := range cfg.Handlers {
		handlers[topic] = handler
	}
	return &Relay{
		store: store, workerID: workerID, clock: clock, backoff: backoff,
		handlerTimeout: handlerTimeout, logger: cfg.Logger, metrics: cfg.Metrics, handlers: handlers,
	}
}

func (r *Relay) ProcessOne(ctx context.Context) (bool, error) {
	if r == nil || r.store == nil {
		return false, fmt.Errorf("outbox relay requires a store")
	}
	now := r.clock.Now().UTC()
	job, ok, err := r.store.Claim(ctx, r.workerID, now)
	if err != nil || !ok {
		return ok, err
	}
	handler := r.handlers[job.Topic]
	if handler == nil {
		next := now.Add(r.backoff(job.Attempts + 1))
		failErr := r.store.Fail(ctx, job.ID, r.workerID, "missing handler for topic "+job.Topic, next)
		r.observe(ctx, job, "retry", "missing_handler")
		return true, failErr
	}
	handlerCtx, cancelHandler := context.WithCancel(ctx)
	defer cancelHandler()
	leaseResult := r.maintainLease(handlerCtx, cancelHandler, job)
	handlerResult := make(chan error, 1)
	go func() {
		handlerResult <- handler(handlerCtx, job)
	}()

	deadlineAt := time.Now().Add(r.handlerTimeout)
	deadline := time.NewTimer(r.handlerTimeout)
	defer deadline.Stop()

	var handlerErr error
	select {
	case handlerErr = <-handlerResult:
		if err := ctx.Err(); err != nil {
			cancelHandler()
			return true, err
		}
		// If completion and the deadline become ready together, the deadline wins.
		if !time.Now().Before(deadlineAt) || !deadline.Stop() {
			cancelHandler()
			return true, r.handlerTimeoutError(job)
		}
		cancelHandler()
		if leaseErr := <-leaseResult; leaseErr != nil {
			return true, leaseErr
		}
	case leaseErr := <-leaseResult:
		cancelHandler()
		if leaseErr != nil {
			return true, leaseErr
		}
		if err := ctx.Err(); err != nil {
			return true, err
		}
		return true, fmt.Errorf("%w: %s", ErrLeaseLost, job.ID)
	case <-deadline.C:
		cancelHandler()
		return true, r.handlerTimeoutError(job)
	case <-ctx.Done():
		cancelHandler()
		return true, ctx.Err()
	}
	if handlerErr != nil {
		delay := r.backoff(job.Attempts + 1)
		var delayed retryDelayError
		if errors.As(handlerErr, &delayed) {
			if requested := delayed.RetryDelay(); requested > 0 {
				delay = requested
			}
		}
		next := now.Add(delay)
		failErr := r.store.Fail(ctx, job.ID, r.workerID, handlerErr.Error(), next)
		r.observe(ctx, job, "retry", handlerErr.Error())
		return true, failErr
	}
	err = r.store.Complete(ctx, job.ID, r.workerID)
	if err == nil {
		r.observe(ctx, job, "done", "")
	}
	return true, err
}

func (r *Relay) handlerTimeoutError(job Job) error {
	return fmt.Errorf("%w: job %s exceeded %s", ErrHandlerTimeout, job.ID, r.handlerTimeout)
}

func (r *Relay) maintainLease(ctx context.Context, cancelHandler context.CancelFunc, job Job) <-chan error {
	result := make(chan error, 1)
	interval := r.store.leaseTTL / 3
	if interval <= 0 {
		interval = time.Nanosecond
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				result <- nil
				return
			case <-ticker.C:
				if ctx.Err() != nil {
					result <- nil
					return
				}
				renewed, err := r.store.RenewLease(ctx, job.ID, r.workerID, r.clock.Now().UTC())
				if err != nil {
					if ctx.Err() != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
						result <- nil
						return
					}
					cancelHandler()
					result <- fmt.Errorf("renew outbox job %s lease: %w", job.ID, err)
					return
				}
				if !renewed {
					cancelHandler()
					result <- fmt.Errorf("%w: %s", ErrLeaseLost, job.ID)
					return
				}
			}
		}
	}()
	return result
}

func defaultHandlerTimeout(store *Store) time.Duration {
	leaseTTL := time.Minute
	if store != nil && store.leaseTTL > 0 {
		leaseTTL = store.leaseTTL
	}
	maxDuration := time.Duration(1<<63 - 1)
	if leaseTTL > maxDuration/defaultHandlerTimeoutLeasePeriods {
		return maxDuration
	}
	return leaseTTL * defaultHandlerTimeoutLeasePeriods
}

func (r *Relay) Run(ctx context.Context, cfg RunnerConfig) error {
	if r == nil || r.store == nil {
		return fmt.Errorf("outbox relay requires a store")
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = time.Second
	}
	if cfg.RecoverBatchSize <= 0 {
		cfg.RecoverBatchSize = 100
	}
	if cfg.CompletedRetention <= 0 {
		cfg.CompletedRetention = 30 * 24 * time.Hour
	}
	if cfg.PruneInterval <= 0 {
		cfg.PruneInterval = time.Hour
	}
	if cfg.PruneBatchSize <= 0 {
		cfg.PruneBatchSize = 1000
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	var lastPrune time.Time
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}
		now := r.clock.Now().UTC()
		if _, err := r.store.RecoverStale(ctx, now, cfg.RecoverBatchSize); err != nil {
			r.logRunnerError(ctx, "recover_stale_failed", err)
		}
		if lastPrune.IsZero() || now.Sub(lastPrune) >= cfg.PruneInterval {
			pruned, err := r.store.PruneCompleted(ctx, now.Add(-cfg.CompletedRetention), cfg.PruneBatchSize)
			if err != nil {
				r.logRunnerError(ctx, "prune_completed_failed", err)
				lastPrune = now
			} else if pruned < cfg.PruneBatchSize {
				lastPrune = now
			}
		}
		processed, err := r.ProcessOne(ctx)
		if err != nil {
			r.logRunnerError(ctx, "process_failed", err)
		}
		delay := time.Duration(0)
		if !processed {
			delay = cfg.PollInterval
		}
		timer.Reset(delay)
	}
}

func (r *Relay) logRunnerError(ctx context.Context, code string, err error) {
	if r.logger == nil || err == nil {
		return
	}
	var captured interface{ DiagnosticCaptured() bool }
	alreadyCaptured := errors.As(err, &captured) && captured.DiagnosticCaptured()
	r.logger.Warn(ctx, "outbox relay runner error",
		logging.String("operation", "outbox.relay.run"),
		logging.String("error_code", code),
		logging.String("worker_id", r.workerID),
		logging.String("error", err.Error()),
		logging.Any("failure_captured", alreadyCaptured),
	)
}

func (r *Relay) observe(ctx context.Context, job Job, status string, reason string) {
	if r.logger != nil {
		fields := []logging.Field{
			logging.String("operation", "outbox.relay.process"),
			logging.String("job_id", job.ID),
			logging.String("topic", job.Topic),
			logging.String("worker_id", r.workerID),
			logging.String("status", status),
		}
		if reason != "" {
			fields = append(fields, logging.String("error", reason))
		}
		if status == "done" {
			r.logger.Info(ctx, "outbox job processed", fields...)
		} else {
			r.logger.Warn(ctx, "outbox job processing failed", fields...)
		}
	}
	if r.metrics != nil {
		r.metrics.Capture(metrics.Event{
			Name: "outbox.job.processed",
			Fields: map[string]any{
				"job_id":    job.ID,
				"topic":     job.Topic,
				"worker_id": r.workerID,
				"status":    status,
			},
		})
	}
}

func FixedBackoff(delay time.Duration) Backoff {
	if delay <= 0 {
		delay = time.Second
	}
	return func(int) time.Duration {
		return delay
	}
}

type systemClock struct{}

func (systemClock) Now() time.Time {
	return time.Now().UTC()
}

type TestClock struct {
	mu  sync.Mutex
	now time.Time
}

func StaticClock(now time.Time) *TestClock {
	return &TestClock{now: now.UTC()}
}

func (c *TestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *TestClock) Set(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = now.UTC()
}
