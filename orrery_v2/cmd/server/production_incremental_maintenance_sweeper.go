package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"orrery_v2/internal/platform"
)

const productionIncrementalMaintenanceInterval = 5 * time.Minute

type productionCadencedSweeper struct {
	delegate platform.Sweeper
	now      func() time.Time
	interval time.Duration

	mu            sync.Mutex
	running       bool
	lastCompleted time.Time
}

func newProductionCadencedSweeper(delegate platform.Sweeper, now func() time.Time, interval time.Duration) *productionCadencedSweeper {
	if now == nil {
		now = time.Now
	}
	if interval <= 0 {
		interval = productionIncrementalMaintenanceInterval
	}
	return &productionCadencedSweeper{delegate: delegate, now: now, interval: interval}
}

func (s *productionCadencedSweeper) Sweep(ctx context.Context) error {
	if s == nil || s.delegate == nil {
		return fmt.Errorf("cadenced sweeper delegate is required")
	}
	s.mu.Lock()
	now := s.now().UTC()
	if s.running || (!s.lastCompleted.IsZero() && now.Sub(s.lastCompleted) < s.interval) {
		s.mu.Unlock()
		return nil
	}
	s.running = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.running = false
		s.lastCompleted = s.now().UTC()
		s.mu.Unlock()
	}()
	return s.delegate.Sweep(ctx)
}
