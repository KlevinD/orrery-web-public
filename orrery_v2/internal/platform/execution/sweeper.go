package execution

import (
	"context"
	"errors"
	"fmt"
	"time"

	"orrery_v2/internal/platform/logging"
)

const defaultSlowSweeperThreshold = time.Second

type Sweeper interface {
	Name() string
	Sweep(ctx context.Context) error
}

type SweeperResult struct {
	Name     string
	Error    error
	Duration time.Duration
}

type SweeperReport struct {
	Total    int
	Failed   int
	Duration time.Duration
	Results  []SweeperResult
}

type SweeperRunner struct {
	batchSize int
	timeout   time.Duration
	logger    logging.Logger
	slow      time.Duration
	now       func() time.Time
	sweepers  []Sweeper
}

type SweeperConfig struct {
	BatchSize int
	Timeout   time.Duration
	Logger    logging.Logger
	Slow      time.Duration
	Now       func() time.Time
}

func NewSweeperRunner(batchSize int) *SweeperRunner {
	return NewSweeperRunnerWithConfig(SweeperConfig{BatchSize: batchSize})
}

func NewSweeperRunnerWithConfig(cfg SweeperConfig) *SweeperRunner {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	slow := cfg.Slow
	if slow <= 0 {
		slow = defaultSlowSweeperThreshold
	}
	return &SweeperRunner{
		batchSize: cfg.BatchSize,
		timeout:   cfg.Timeout,
		logger:    cfg.Logger,
		slow:      slow,
		now:       now,
	}
}

func (r *SweeperRunner) Register(s Sweeper) {
	r.sweepers = append(r.sweepers, s)
}

func (r *SweeperRunner) SweepOnce(ctx context.Context) SweeperReport {
	startedAt := r.now()
	limit := len(r.sweepers)
	if r.batchSize > 0 && r.batchSize < limit {
		limit = r.batchSize
	}
	report := SweeperReport{}
	for _, s := range r.sweepers[:limit] {
		result := r.sweepOne(ctx, s)
		report.Total++
		if result.Error != nil && !isExpectedShutdownCancellation(ctx, result.Error) {
			report.Failed++
		}
		report.Results = append(report.Results, result)
	}
	report.Duration = r.now().Sub(startedAt)
	r.logSummary(ctx, report)
	return report
}

func (r *SweeperRunner) sweepOne(ctx context.Context, s Sweeper) (result SweeperResult) {
	startedAt := r.now()
	name := ""
	if s != nil {
		name = s.Name()
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			result = SweeperResult{
				Name:     name,
				Error:    fmt.Errorf("sweeper panic: %v", recovered),
				Duration: r.now().Sub(startedAt),
			}
		}
	}()
	runCtx := ctx
	cancel := func() {}
	if r.timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, r.timeout)
	}
	defer cancel()
	err := s.Sweep(runCtx)
	return SweeperResult{Name: name, Error: err, Duration: r.now().Sub(startedAt)}
}

func isExpectedShutdownCancellation(ctx context.Context, err error) bool {
	return err != nil && errors.Is(ctx.Err(), context.Canceled) && errors.Is(err, context.Canceled)
}

func (r *SweeperRunner) logSummary(ctx context.Context, report SweeperReport) {
	if r.logger == nil {
		return
	}
	interrupted := false
	for _, result := range report.Results {
		if result.Error == nil {
			continue
		}
		if isExpectedShutdownCancellation(ctx, result.Error) {
			interrupted = true
			continue
		}
		r.logger.Warn(ctx, "sweeper failed",
			logging.String("component", "sweeper"),
			logging.String("operation", "sweep_one"),
			logging.String("sweeper_name", result.Name),
			logging.Any("error", result.Error),
			logging.Int("duration_ms", int(result.Duration.Milliseconds())),
		)
	}
	for _, result := range report.Results {
		if result.Error != nil || result.Duration < r.slow {
			continue
		}
		r.logger.Warn(ctx, "sweeper completed slowly",
			logging.String("component", "sweeper"),
			logging.String("operation", "sweep_one"),
			logging.String("sweeper_name", result.Name),
			logging.Int("duration_ms", int(result.Duration.Milliseconds())),
		)
	}
	fields := []logging.Field{
		logging.String("component", "sweeper"),
		logging.String("operation", "sweep_once"),
		logging.Int("total", report.Total),
		logging.Int("failed", report.Failed),
		logging.Int("duration_ms", int(report.Duration.Milliseconds())),
	}
	if interrupted && report.Failed == 0 {
		r.logger.Info(ctx, "sweeper run interrupted", fields...)
		return
	}
	if report.Failed > 0 {
		r.logger.Warn(ctx, "sweeper run completed with failures", fields...)
		return
	}
	r.logger.Info(ctx, "sweeper run completed", fields...)
}
