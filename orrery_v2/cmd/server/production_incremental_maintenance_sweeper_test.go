package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"orrery_v2/internal/platform"
)

func TestProductionCadencedSweeperRunsAtFiveMinuteIntervals(t *testing.T) {
	now := time.Date(2026, 8, 27, 8, 0, 0, 0, time.UTC)
	var calls atomic.Int32
	sweeper := newProductionCadencedSweeper(platform.SweeperFunc(func(context.Context) error {
		calls.Add(1)
		return nil
	}), func() time.Time { return now }, 0)

	if err := sweeper.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(4*time.Minute + 59*time.Second)
	if err := sweeper.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("calls before interval = %d, want 1", got)
	}
	now = now.Add(time.Second)
	if err := sweeper.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("calls after interval = %d, want 2", got)
	}
}

func TestProductionCadencedSweeperWaitsForIntervalAfterSlowCompletion(t *testing.T) {
	now := time.Date(2026, 9, 1, 4, 0, 0, 0, time.UTC)
	var calls atomic.Int32
	sweeper := newProductionCadencedSweeper(platform.SweeperFunc(func(context.Context) error {
		calls.Add(1)
		now = now.Add(2 * time.Minute)
		return nil
	}), func() time.Time { return now }, 5*time.Minute)

	if err := sweeper.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(4*time.Minute + 59*time.Second)
	if err := sweeper.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("calls before post-completion interval = %d, want 1", got)
	}
	now = now.Add(time.Second)
	if err := sweeper.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("calls after post-completion interval = %d, want 2", got)
	}
}
