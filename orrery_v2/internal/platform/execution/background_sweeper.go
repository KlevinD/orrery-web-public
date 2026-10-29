package execution

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"orrery_v2/internal/platform/logging"
)

type backgroundSweepWorker interface {
	Sweep(context.Context) error
}

// BackgroundSweeper isolates a slow integration from the serial maintenance
// loop. At most one cycle runs; Close cancels it before databases are closed.
type BackgroundSweeper struct {
	inner   backgroundSweepWorker
	name    string
	timeout time.Duration
	logger  logging.Logger
	mu      sync.Mutex
	cancel  context.CancelFunc
	done    chan struct{}
	closed  bool
	lastErr error
}

func NewBackgroundSweeper(name string, inner backgroundSweepWorker, timeout time.Duration, logger logging.Logger) *BackgroundSweeper {
	return &BackgroundSweeper{name: name, inner: inner, timeout: timeout, logger: logger}
}

func (s *BackgroundSweeper) Sweep(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.inner == nil || s.timeout <= 0 {
		return fmt.Errorf("background sweeper requires worker and timeout")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.lastErr
	s.lastErr = nil
	if s.closed || s.cancel != nil {
		return err
	}
	workerBase, stop := context.WithCancelCause(context.WithoutCancel(ctx))
	workerCtx, cancel := context.WithTimeout(workerBase, s.timeout)
	s.cancel = func() { stop(errBackgroundSweeperClosed) }
	s.done = make(chan struct{})
	go s.run(workerCtx, func() { cancel(); stop(nil) }, s.done)
	return err
}

func (s *BackgroundSweeper) run(ctx context.Context, cancel context.CancelFunc, done chan struct{}) {
	started := time.Now()
	var err error
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("background sweeper panic: %v", recovered)
		}
		if errors.Is(err, context.Canceled) && errors.Is(context.Cause(ctx), errBackgroundSweeperClosed) {
			err = nil
		}
		cancel()
		if s.logger != nil {
			fields := []logging.Field{logging.String("sweeper_name", s.name), logging.Int("duration_ms", int(time.Since(started).Milliseconds()))}
			if err != nil {
				fields = append(fields, logging.String("error", err.Error()))
				s.logger.Warn(context.WithoutCancel(ctx), "background sweeper failed", fields...)
			} else if time.Since(started) >= time.Second {
				s.logger.Info(context.WithoutCancel(ctx), "background sweeper completed", fields...)
			}
		}
		s.mu.Lock()
		s.lastErr = err
		s.cancel = nil
		close(done)
		s.mu.Unlock()
	}()
	err = s.inner.Sweep(ctx)
	if err == nil {
		err = ctx.Err()
	}
}

func (s *BackgroundSweeper) Close() error {
	s.mu.Lock()
	s.closed = true
	cancel, done := s.cancel, s.done
	s.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	select {
	case <-done:
		return nil
	case <-time.After(5 * time.Second):
		return fmt.Errorf("background sweeper %s did not stop after cancellation", s.name)
	}
}

var errBackgroundSweeperClosed = errors.New("background sweeper closed")
