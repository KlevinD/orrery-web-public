package main

import (
	"context"
	"sync"
	"time"

	"orrery_v2/internal/platform/logging"
)

// The buffered wake coalesces bursts; persisted candidates and the heartbeat
// recover missed events. Only one scan runs at a time, outside the shared sweeper.
type productionPersonalAssistantHeartbeat struct {
	wake     chan struct{}
	done     chan struct{}
	cancel   context.CancelFunc
	stopOnce sync.Once
}

func newProductionPersonalAssistantHeartbeat() *productionPersonalAssistantHeartbeat {
	return &productionPersonalAssistantHeartbeat{wake: make(chan struct{}, 1), done: make(chan struct{})}
}

func (h *productionPersonalAssistantHeartbeat) Wake() {
	select {
	case h.wake <- struct{}{}:
	default:
	}
}

func (h *productionPersonalAssistantHeartbeat) Start(ctx context.Context, interval, timeout time.Duration, scan func(context.Context) error, logger logging.Logger) {
	ctx, h.cancel = context.WithCancel(ctx)
	if interval <= 0 {
		interval = 30 * time.Second
	}
	if timeout <= 0 {
		timeout = 25 * time.Second
	}
	go func() {
		defer close(h.done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		h.Wake()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			case <-h.wake:
			}
			if ctx.Err() != nil {
				return
			}
			scanCtx, cancel := context.WithTimeout(ctx, timeout)
			err := scan(scanCtx)
			cancel()
			if err != nil && ctx.Err() == nil && logger != nil {
				logger.Warn(ctx, "personal assistant notification heartbeat failed", logging.Any("error", err))
			}
		}
	}()
}

func (h *productionPersonalAssistantHeartbeat) Stop() {
	if h == nil || h.cancel == nil {
		return
	}
	h.stopOnce.Do(func() { h.cancel(); <-h.done })
}
