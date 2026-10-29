package execution_test

import (
	"bytes"
	"context"
	"errors"
	"orrery_v2/internal/platform/execution"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/platform/logging"
)

func TestRunnerContinuesWhenOneSweeperFails(t *testing.T) {
	ctx := context.Background()
	runner := execution.NewSweeperRunner(10)
	first := &fakeSweeper{name: "first", err: errors.New("boom")}
	second := &fakeSweeper{name: "second"}
	runner.Register(first)
	runner.Register(second)

	report := runner.SweepOnce(ctx)

	if !first.called || !second.called {
		t.Fatalf("expected both sweepers to be called")
	}
	if report.Total != 2 || report.Failed != 1 {
		t.Fatalf("report = %+v", report)
	}
}

func TestRunnerTreatsShutdownCancellationAsInterruption(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	var out bytes.Buffer
	logger := logging.NewTextLogger(&out, logging.LevelDebug)
	runner := execution.NewSweeperRunnerWithConfig(execution.SweeperConfig{BatchSize: 10, Logger: logger})
	runner.Register(&blockingSweeper{name: "mission.runtime_lifecycle_repair", started: started})

	finished := make(chan execution.SweeperReport, 1)
	go func() {
		finished <- runner.SweepOnce(ctx)
	}()
	<-started
	cancel()
	report := <-finished

	if report.Total != 1 || report.Failed != 0 {
		t.Fatalf("report = %+v, want interrupted without a failure", report)
	}
	if len(report.Results) != 1 || !errors.Is(report.Results[0].Error, context.Canceled) {
		t.Fatalf("result error = %+v, want context canceled retained in results", report.Results)
	}
	logged := out.String()
	if strings.Contains(logged, "sweeper failed") || strings.Contains(logged, "sweeper run completed with failures") {
		t.Fatalf("shutdown cancellation was logged as a failure: %q", logged)
	}
	if !strings.Contains(logged, "sweeper run interrupted") {
		t.Fatalf("log output %q missing interrupted summary", logged)
	}
}

func TestRunnerReportsIndependentCancellationAsFailure(t *testing.T) {
	var out bytes.Buffer
	logger := logging.NewTextLogger(&out, logging.LevelDebug)
	runner := execution.NewSweeperRunnerWithConfig(execution.SweeperConfig{BatchSize: 10, Logger: logger})
	runner.Register(&fakeSweeper{name: "independent-cancel", err: context.Canceled})

	report := runner.SweepOnce(context.Background())

	if report.Total != 1 || report.Failed != 1 {
		t.Fatalf("report = %+v, want cancellation without parent shutdown to fail", report)
	}
	if !strings.Contains(out.String(), "sweeper failed") {
		t.Fatalf("log output %q missing failure record", out.String())
	}
}

func TestRunnerConvertsSweeperPanicToFailureAndContinues(t *testing.T) {
	ctx := context.Background()
	runner := execution.NewSweeperRunner(10)
	panicSweeper := &panicSweeper{name: "panic"}
	next := &fakeSweeper{name: "next"}
	runner.Register(panicSweeper)
	runner.Register(next)

	report := runner.SweepOnce(ctx)

	if !next.called {
		t.Fatalf("runner stopped after panic sweeper")
	}
	if report.Total != 2 || report.Failed != 1 {
		t.Fatalf("report = %+v, want one failure across two sweepers", report)
	}
	if len(report.Results) != 2 || report.Results[0].Error == nil {
		t.Fatalf("panic result was not converted to error: %+v", report.Results)
	}
}

func TestRunnerAppliesTimeoutAndRecordsDuration(t *testing.T) {
	ctx := context.Background()
	runner := execution.NewSweeperRunnerWithConfig(execution.SweeperConfig{BatchSize: 10, Timeout: time.Millisecond})
	blocked := &blockingSweeper{name: "blocked"}
	runner.Register(blocked)

	report := runner.SweepOnce(ctx)

	if report.Total != 1 || report.Failed != 1 {
		t.Fatalf("report = %+v, want one failed timeout", report)
	}
	if len(report.Results) != 1 || !errors.Is(report.Results[0].Error, context.DeadlineExceeded) {
		t.Fatalf("result error = %+v, want deadline exceeded", report.Results)
	}
	if report.Results[0].Duration <= 0 {
		t.Fatalf("duration = %s, want positive", report.Results[0].Duration)
	}
}

func TestRunnerLogsStructuredSummary(t *testing.T) {
	var out bytes.Buffer
	logger := logging.NewTextLogger(&out, logging.LevelDebug)
	runner := execution.NewSweeperRunnerWithConfig(execution.SweeperConfig{BatchSize: 10, Timeout: time.Second, Logger: logger})
	runner.Register(&fakeSweeper{name: "ok"})
	runner.Register(&fakeSweeper{name: "failed", err: errors.New("boom")})

	report := runner.SweepOnce(logging.WithCorrelationID(context.Background(), "corr-1"))

	if report.Total != 2 || report.Failed != 1 {
		t.Fatalf("report = %+v", report)
	}
	logged := out.String()
	for _, want := range []string{
		"component=sweeper",
		"operation=sweep_once",
		"operation=sweep_one",
		"sweeper_name=failed",
		"error=boom",
		"correlation_id=corr-1",
		"total=2",
		"failed=1",
		"duration_ms=",
	} {
		if !strings.Contains(logged, want) {
			t.Fatalf("log output %q missing %q", logged, want)
		}
	}
}

func TestRunnerLogsSuccessfulSlowSweeperByName(t *testing.T) {
	var out bytes.Buffer
	logger := logging.NewTextLogger(&out, logging.LevelDebug)
	base := time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC)
	times := []time.Time{base, base, base.Add(2 * time.Second), base.Add(2 * time.Second)}
	index := 0
	runner := execution.NewSweeperRunnerWithConfig(execution.SweeperConfig{
		BatchSize: 10, Logger: logger, Slow: time.Second,
		Now: func() time.Time {
			value := times[index]
			index++
			return value
		},
	})
	runner.Register(&fakeSweeper{name: "slow-success"})

	runner.SweepOnce(context.Background())
	logged := out.String()
	for _, want := range []string{"sweeper completed slowly", "sweeper_name=slow-success", "duration_ms=2000"} {
		if !strings.Contains(logged, want) {
			t.Fatalf("log output %q missing %q", logged, want)
		}
	}
}

type fakeSweeper struct {
	name   string
	err    error
	called bool
}

func (f *fakeSweeper) Name() string { return f.name }
func (f *fakeSweeper) Sweep(ctx context.Context) error {
	f.called = true
	return f.err
}

type blockingSweeper struct {
	name    string
	started chan struct{}
}

func (s *blockingSweeper) Name() string { return s.name }
func (s *blockingSweeper) Sweep(ctx context.Context) error {
	if s.started != nil {
		close(s.started)
	}
	<-ctx.Done()
	return ctx.Err()
}

type panicSweeper struct {
	name string
}

func (s *panicSweeper) Name() string { return s.name }
func (s *panicSweeper) Sweep(ctx context.Context) error {
	panic("sweeper boom")
}
