package platform

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"orrery_v2/internal/platform/execution"
	"orrery_v2/internal/platform/health"
	"orrery_v2/internal/platform/logging"
	"orrery_v2/internal/platform/metrics"
	"orrery_v2/internal/platform/outbox"
)

type OutboxRelayRuntimeConfig struct {
	WorkerID         string
	PollInterval     time.Duration
	RecoverBatchSize int
}

type ExecutionRunnerRuntimeConfig struct {
	WorkerID     string
	PollInterval time.Duration
	Concurrency  int
}

type SweeperRuntimeConfig struct {
	Interval     time.Duration
	InitialDelay time.Duration
}

func (r *Runtime) RegisterMetricEventWriter(name string, writer MetricEventWriter) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("metric writer name is required")
	}
	if writer == nil {
		return fmt.Errorf("metric writer %q is required", name)
	}
	if err := r.beginOperation(); err != nil {
		return err
	}
	defer r.endOperation()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.MetricsRelay != nil {
		return fmt.Errorf("metrics relay already started")
	}
	for _, registered := range r.metricWriters {
		if registered.name == name {
			return fmt.Errorf("metric writer already registered for name %q", name)
		}
	}
	r.metricWriters = append(r.metricWriters, registeredMetricWriter{name: name, writer: writer})
	return nil
}

func (r *Runtime) StartMetricsRelay(ctx context.Context, cfg MetricsRelayRuntimeConfig) error {
	if r == nil || r.Metrics == nil {
		return fmt.Errorf("metrics sink is not configured")
	}
	if err := r.beginOperation(); err != nil {
		return err
	}
	defer r.endOperation()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.MetricsRelay != nil {
		return fmt.Errorf("metrics relay already started")
	}
	if len(r.metricWriters) == 0 {
		return fmt.Errorf("metrics relay requires at least one writer")
	}
	writers := append([]registeredMetricWriter(nil), r.metricWriters...)
	relay, err := metrics.NewBatchRelay(r.Metrics, metrics.BatchRelayConfig{
		BatchSize:  cfg.BatchSize,
		FlushEvery: cfg.FlushEvery,
		Writer:     multiMetricWriter(writers),
	})
	if err != nil {
		return err
	}
	relay.Start()
	r.MetricsRelay = relay
	r.metricsRelayClosed = false
	return nil
}

func (r *Runtime) StartOutboxRelay(ctx context.Context, cfg OutboxRelayRuntimeConfig) error {
	if r == nil || r.Outbox == nil {
		return fmt.Errorf("outbox store is not configured")
	}
	if err := r.beginOperation(); err != nil {
		return err
	}
	defer r.endOperation()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.outboxRelayCancel != nil {
		return fmt.Errorf("outbox relay already started")
	}
	handlers := make(map[string]outbox.Handler, len(r.outboxHandlers))
	for topic, handler := range r.outboxHandlers {
		handlers[topic] = handler
	}
	workerID := cfg.WorkerID
	if workerID == "" {
		workerID = "runtime-outbox-relay"
	}
	if r.IDGenerator == nil {
		return fmt.Errorf("outbox worker id generator is not configured")
	}
	instanceID, err := r.IDGenerator.NewIDContext()
	if err != nil {
		return fmt.Errorf("generate outbox worker instance id: %w", err)
	}
	instanceID = strings.TrimSpace(instanceID)
	if instanceID == "" {
		return fmt.Errorf("generate outbox worker instance id: empty id")
	}
	workerID = workerID + ":" + instanceID
	relay := outbox.NewRelay(r.Outbox, outbox.RelayConfig{
		WorkerID: workerID,
		Clock:    r.Clock,
		Logger:   r.Logger,
		Metrics:  r.Metrics,
		Handlers: handlers,
	})
	relayCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	r.outboxRelayCancel = cancel
	r.outboxRelayDone = done
	r.markWorkerRunningLocked("outbox")
	go func() {
		err := relay.Run(relayCtx, outbox.RunnerConfig{PollInterval: cfg.PollInterval, RecoverBatchSize: cfg.RecoverBatchSize})
		r.markWorkerStopped("outbox", err)
		done <- err
	}()
	return nil
}

func (r *Runtime) StartExecutionRunner(ctx context.Context, cfg ExecutionRunnerRuntimeConfig) error {
	if r == nil || r.Execution == nil || r.Execution.Queue == nil {
		return fmt.Errorf("execution queue is not configured")
	}
	if err := r.beginOperation(); err != nil {
		return err
	}
	defer r.endOperation()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.executionCancel != nil {
		return fmt.Errorf("execution runner already started")
	}
	handlers := make(map[string]execution.Handler, len(r.executionHandlers))
	for topic, handler := range r.executionHandlers {
		handlers[topic] = handler
	}
	workerID := cfg.WorkerID
	if workerID == "" {
		workerID = "runtime-execution-runner"
	}
	concurrency := cfg.Concurrency
	if concurrency <= 0 {
		concurrency = 1
	}
	if concurrency > 32 {
		return fmt.Errorf("execution runner concurrency must not exceed 32")
	}
	runnerCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	r.executionCancel = cancel
	r.executionDone = done
	r.markWorkerRunningLocked("execution")
	go func() {
		var workers sync.WaitGroup
		errorsByWorker := make(chan error, concurrency)
		for index := 0; index < concurrency; index++ {
			instanceID := workerID
			if concurrency > 1 {
				instanceID += ":" + strconv.Itoa(index+1)
			}
			runner := execution.NewQueueRunner(r.Execution.Queue, instanceID, dispatchExecutionJob(handlers), r.now).WithLogger(r.Logger)
			workers.Add(1)
			go func() {
				defer workers.Done()
				errorsByWorker <- runExecutionRunner(runnerCtx, runner, cfg.PollInterval, r.Logger)
			}()
		}
		workers.Wait()
		close(errorsByWorker)
		var runnerErrors []error
		for err := range errorsByWorker {
			if err != nil {
				runnerErrors = append(runnerErrors, err)
			}
		}
		err := errors.Join(runnerErrors...)
		r.markWorkerStopped("execution", err)
		done <- err
	}()
	return nil
}

func dispatchExecutionJob(handlers map[string]execution.Handler) execution.Handler {
	return func(ctx context.Context, job execution.Job) error {
		handler := handlers[job.Topic]
		if handler == nil {
			return fmt.Errorf("execution handler not registered for topic %q", job.Topic)
		}
		return handler(ctx, job)
	}
}

func runExecutionRunner(ctx context.Context, runner *execution.QueueRunner, pollInterval time.Duration, logger logging.Logger) error {
	if pollInterval <= 0 {
		pollInterval = time.Second
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}
		processed, err := runner.RunOnce(ctx)
		if err != nil && !errors.Is(err, context.Canceled) && logger != nil {
			logger.Warn(ctx, "execution queue runner failed",
				logging.String("component", "execution"),
				logging.String("operation", "execution.runner"),
				logging.String("error_code", "execution_runner_failed"),
				logging.String("error", err.Error()),
			)
		}
		delay := time.Duration(0)
		if !processed {
			delay = pollInterval
		}
		timer.Reset(delay)
	}
}

func (r *Runtime) StartSweeper(ctx context.Context, cfg SweeperRuntimeConfig) error {
	if r == nil || r.Sweeper == nil {
		return fmt.Errorf("sweeper runner is not configured")
	}
	if err := r.beginOperation(); err != nil {
		return err
	}
	defer r.endOperation()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sweeperCancel != nil {
		return fmt.Errorf("sweeper runner already started")
	}
	runnerCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	r.sweeperCancel = cancel
	r.sweeperDone = done
	r.markWorkerRunningLocked("sweeper")
	go func() {
		err := r.runSweeper(runnerCtx, cfg.Interval, cfg.InitialDelay)
		r.markWorkerStopped("sweeper", err)
		done <- err
	}()
	return nil
}

func (r *Runtime) runSweeper(ctx context.Context, interval, initialDelay time.Duration) error {
	if interval <= 0 {
		interval = time.Minute
	}
	if initialDelay < 0 {
		initialDelay = 0
	}
	timer := time.NewTimer(initialDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}
		r.mu.RLock()
		runner := r.Sweeper
		r.mu.RUnlock()
		if runner != nil {
			runner.SweepOnce(ctx)
		}
		timer.Reset(interval)
	}
}

type registeredSweeper struct {
	name    string
	sweeper Sweeper
}

func (s registeredSweeper) Name() string {
	return s.name
}

func (s registeredSweeper) Sweep(ctx context.Context) error {
	return s.sweeper.Sweep(ctx)
}

func attachRegisteredSweepers(runner *execution.SweeperRunner, sweepers []registeredSweeper) {
	if runner == nil {
		return
	}
	for _, sweeper := range sweepers {
		runner.Register(sweeper)
	}
}

func (r *Runtime) registerPlatformHousekeeping() {
	if r == nil {
		return
	}
	r.sweepers = append(r.sweepers,
		registeredSweeper{name: "platform.queue.recover_expired", sweeper: SweeperFunc(func(ctx context.Context) error {
			if r.Execution == nil || r.Execution.Queue == nil {
				return nil
			}
			_, err := r.Execution.Queue.RecoverExpired(ctx, r.now(), r.Config.Queue.SweepBatchSize)
			return err
		})},
		registeredSweeper{name: "platform.claim.recover_stale", sweeper: SweeperFunc(func(ctx context.Context) error {
			if r.ExecutionClaims == nil {
				return nil
			}
			_, err := r.ExecutionClaims.RecoverStale(ctx, r.Config.Queue.SweepBatchSize)
			return err
		})},
		registeredSweeper{name: "platform.liveness.sweep_stale", sweeper: SweeperFunc(func(ctx context.Context) error {
			if r.Execution == nil || r.Execution.Liveness == nil {
				return nil
			}
			_, err := r.Execution.Liveness.SweepStale(ctx, r.now(), r.Config.Queue.SweepBatchSize)
			return err
		})},
		registeredSweeper{name: "platform.lock.sweep_expired", sweeper: SweeperFunc(func(ctx context.Context) error {
			if r.Lock == nil {
				return nil
			}
			_, err := r.Lock.SweepExpired(ctx, r.now(), r.Config.Queue.SweepBatchSize)
			return err
		})},
	)
}

func (r *Runtime) now() time.Time {
	if r == nil || r.Clock == nil {
		return time.Now().UTC()
	}
	return r.Clock.Now().UTC()
}

type workerStatus struct {
	running bool
	err     error
	stopped bool
}

func (r *Runtime) markWorkerRunningLocked(name string) {
	if r.workerStatus == nil {
		r.workerStatus = map[string]workerStatus{}
	}
	r.workerStatus[name] = workerStatus{running: true}
}

func (r *Runtime) markWorkerStopped(name string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	status := workerStatus{running: false, stopped: true}
	if err != nil {
		status.err = err
	}
	if status.err == nil {
		status.err = fmt.Errorf("worker stopped unexpectedly")
	}
	r.workerStatus[name] = status
}

func (r *Runtime) workerReadyCheck(ctx context.Context) health.Check {
	if r == nil {
		return health.Check{Name: "runtime.workers", OK: false, Message: "runtime is not configured"}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.workerStatus) == 0 {
		return health.Check{Name: "runtime.workers", OK: true}
	}
	failed := []string{}
	for _, name := range []string{"sweeper", "execution", "outbox"} {
		status, ok := r.workerStatus[name]
		if !ok || status.running {
			continue
		}
		if status.err != nil {
			failed = append(failed, name+": "+status.err.Error())
			continue
		}
		if status.stopped {
			failed = append(failed, name+": stopped")
		}
	}
	if len(failed) > 0 {
		return health.Check{Name: "runtime.worker." + strings.Split(failed[0], ":")[0], OK: false, Message: strings.Join(failed, "; ")}
	}
	return health.Check{Name: "runtime.workers", OK: true}
}

func stopSweeperRunner(cancel func(), done chan error) error {
	if cancel == nil {
		return nil
	}
	cancel()
	if done == nil {
		return nil
	}
	select {
	case err := <-done:
		return err
	case <-time.After(time.Second):
		return fmt.Errorf("sweeper runner did not stop")
	}
}

func stopExecutionRunner(cancel func(), done chan error) error {
	if cancel == nil {
		return nil
	}
	cancel()
	if done == nil {
		return nil
	}
	select {
	case err := <-done:
		return err
	case <-time.After(time.Second):
		return fmt.Errorf("execution runner did not stop")
	}
}

func stopOutboxRelay(cancel func(), done chan error) error {
	if cancel == nil {
		return nil
	}
	cancel()
	if done == nil {
		return nil
	}
	select {
	case err := <-done:
		return err
	case <-time.After(time.Second):
		return fmt.Errorf("outbox relay did not stop")
	}
}
