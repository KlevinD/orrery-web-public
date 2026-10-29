package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func awaitHeartbeat(t *testing.T, calls <-chan struct{}) {
	t.Helper()
	select {
	case <-calls:
	case <-time.After(3 * time.Second):
		t.Fatal("notification heartbeat did not run")
	}
}

func TestPersonalAssistantHeartbeatStartupWakeAndCatchup(t *testing.T) {
	h := newProductionPersonalAssistantHeartbeat()
	calls := make(chan struct{}, 10)
	h.Start(context.Background(), 50*time.Millisecond, time.Second, func(context.Context) error { calls <- struct{}{}; return errors.New("transient failure") }, nil)
	t.Cleanup(h.Stop)
	awaitHeartbeat(t, calls)
	h.Wake()
	awaitHeartbeat(t, calls)
	awaitHeartbeat(t, calls)
	h.Stop()
	h.Stop()
}

func TestPersonalAssistantHeartbeatCoalescesAndNeverOverlaps(t *testing.T) {
	h := newProductionPersonalAssistantHeartbeat()
	calls := make(chan struct{}, 10)
	release := make(chan struct{})
	var active, maximum atomic.Int32
	h.Start(context.Background(), time.Hour, time.Second, func(ctx context.Context) error {
		n := active.Add(1)
		if n > maximum.Load() {
			maximum.Store(n)
		}
		defer active.Add(-1)
		calls <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil
	}, nil)
	t.Cleanup(h.Stop)
	awaitHeartbeat(t, calls)
	for i := 0; i < 1000; i++ {
		h.Wake()
	}
	if len(h.wake) != 1 {
		t.Fatalf("wake buffer=%d", len(h.wake))
	}
	close(release)
	awaitHeartbeat(t, calls)
	h.Stop()
	if maximum.Load() != 1 {
		t.Fatalf("overlapping scans=%d", maximum.Load())
	}
}

func TestPersonalAssistantHeartbeatBoundsScanAndStopsOnCancellation(t *testing.T) {
	h := newProductionPersonalAssistantHeartbeat()
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{}, 1)
	finished := make(chan error, 1)
	h.Start(ctx, time.Hour, 20*time.Millisecond, func(ctx context.Context) error {
		started <- struct{}{}
		<-ctx.Done()
		finished <- ctx.Err()
		return ctx.Err()
	}, nil)
	t.Cleanup(h.Stop)
	awaitHeartbeat(t, started)
	select {
	case err := <-finished:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("unbounded notification scan")
	}
	cancel()
	h.Stop()
	select {
	case <-h.done:
	default:
		t.Fatal("worker still running")
	}
}

func TestPersonalAssistantHeartbeatDoesNotScanCanceledStartup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h := newProductionPersonalAssistantHeartbeat()
	var calls atomic.Int32
	h.Start(ctx, time.Millisecond, time.Second, func(context.Context) error { calls.Add(1); return nil }, nil)
	h.Stop()
	if calls.Load() != 0 {
		t.Fatal("scan ran after cancellation")
	}
}
