package execution_test

import (
	"bytes"
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"orrery_v2/internal/platform/execution"
	"orrery_v2/internal/platform/logging"
)

func TestBackgroundSweeperDoesNotBlockAndClosesItsWorker(t *testing.T) {
	started, stopped := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	sweeper := execution.NewBackgroundSweeper("slow", backgroundSweepFunc(func(ctx context.Context) error {
		calls.Add(1)
		close(started)
		<-ctx.Done()
		close(stopped)
		return ctx.Err()
	}), time.Minute, nil)
	done := make(chan error, 1)
	go func() { done <- sweeper.Sweep(context.Background()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("slow integration blocked maintenance caller")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("worker not started")
	}
	for i := 0; i < 100; i++ {
		if err := sweeper.Sweep(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("overlapping cycles=%d", calls.Load())
	}
	if err := sweeper.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	default:
		t.Fatal("Close returned before worker stopped")
	}
	if err := sweeper.Sweep(context.Background()); err != nil {
		t.Fatalf("normal shutdown emitted a repair fault: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatal("closed worker restarted")
	}
}

func TestBackgroundSweeperRetainsTimeoutAndFailure(t *testing.T) {
	done := make(chan struct{})
	sweeper := execution.NewBackgroundSweeper("timeout", backgroundSweepFunc(func(ctx context.Context) error {
		<-ctx.Done()
		close(done)
		return ctx.Err()
	}), 20*time.Millisecond, nil)
	if err := sweeper.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker ignored timeout")
	}
	if err := sweeper.Close(); err != nil {
		t.Fatal(err)
	}
	if err := sweeper.Sweep(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("failure was swallowed: %v", err)
	}
}

type backgroundSweepFunc func(context.Context) error

func TestBackgroundSweeperOnlySuppressesCloseCancellation(t *testing.T) {
	for _, failure := range []error{context.Canceled, errors.New("provider failed")} {
		t.Run(failure.Error(), func(t *testing.T) {
			logs := &sweepLogSignal{done: make(chan struct{})}
			s := execution.NewBackgroundSweeper("diagnosis", backgroundSweepFunc(func(context.Context) error { return failure }), time.Minute, logging.NewJSONLogger(logs, logging.LevelDebug))
			if err := s.Sweep(context.Background()); err != nil {
				t.Fatal(err)
			}
			<-logs.done
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			if err := s.Sweep(context.Background()); !errors.Is(err, failure) {
				t.Fatalf("real failure swallowed: %v", err)
			}
			if !bytes.Contains(logs.Buffer.Bytes(), []byte("background sweeper failed")) {
				t.Fatal("real failure was not logged")
			}
		})
	}
}

type sweepLogSignal struct {
	bytes.Buffer
	done chan struct{}
}

func (w *sweepLogSignal) Write(p []byte) (int, error) {
	n, err := w.Buffer.Write(p)
	close(w.done)
	return n, err
}

func (f backgroundSweepFunc) Sweep(ctx context.Context) error { return f(ctx) }
