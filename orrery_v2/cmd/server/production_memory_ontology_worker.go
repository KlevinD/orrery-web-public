package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"orrery_v2/internal/app/memoryontology"
	"orrery_v2/internal/platform/logging"
)

const (
	productionMemoryOntologyCycleTimeout        = 2 * time.Minute
	productionMemoryOntologyCycleCadence        = productionIncrementalMaintenanceInterval
	productionMemoryOntologyCycleLimit          = 5
	productionMemoryOntologyStaleRunAge         = 10 * time.Minute
	productionMemoryOntologyStaleRunRepairLimit = 100
)

type memoryOntologyCycleProcessor interface {
	Sweep(context.Context, int) (memoryontology.SweepReport, error)
}

type memoryOntologyRunMaintenance interface {
	FailExhaustedStaleRuns(context.Context, time.Time, time.Duration, int) (int, error)
}

type productionMemoryOntologyWorkerOptions struct {
	Name        string
	Logger      logging.Logger
	Maintenance memoryOntologyRunMaintenance
	Gate        *sync.Mutex
	Timeout     time.Duration
	Cadence     time.Duration
	Limit       int
	Now         func() time.Time
}

type productionMemoryOntologyWorker struct {
	name        string
	processor   memoryOntologyCycleProcessor
	maintenance memoryOntologyRunMaintenance
	logger      logging.Logger
	gate        *sync.Mutex
	timeout     time.Duration
	cadence     time.Duration
	limit       int
	now         func() time.Time

	mu          sync.Mutex
	running     bool
	lastStarted time.Time
	closed      bool
	cancel      context.CancelFunc
	done        chan struct{}
}

func newProductionMemoryOntologyWorker(processor memoryOntologyCycleProcessor, options productionMemoryOntologyWorkerOptions) *productionMemoryOntologyWorker {
	if options.Timeout <= 0 {
		options.Timeout = productionMemoryOntologyCycleTimeout
	}
	if options.Cadence <= 0 {
		options.Cadence = productionMemoryOntologyCycleCadence
	}
	if options.Limit <= 0 {
		options.Limit = productionMemoryOntologyCycleLimit
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Gate == nil {
		options.Gate = &sync.Mutex{}
	}
	return &productionMemoryOntologyWorker{
		name:        options.Name,
		processor:   processor,
		maintenance: options.Maintenance,
		logger:      options.Logger,
		gate:        options.Gate,
		timeout:     options.Timeout,
		cadence:     options.Cadence,
		limit:       options.Limit,
		now:         options.Now,
	}
}

func (w *productionMemoryOntologyWorker) Sweep(ctx context.Context) error {
	if w == nil || w.processor == nil {
		return fmt.Errorf("memory ontology worker processor is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	w.mu.Lock()
	now := w.now().UTC()
	if w.closed || w.running || (!w.lastStarted.IsZero() && now.Sub(w.lastStarted) < w.cadence) {
		w.mu.Unlock()
		return nil
	}
	if !w.gate.TryLock() {
		w.mu.Unlock()
		return nil
	}
	w.running = true
	w.lastStarted = now
	workerCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), w.timeout)
	w.cancel = cancel
	w.done = make(chan struct{})
	w.mu.Unlock()

	go w.run(workerCtx, cancel)
	return nil
}

func (w *productionMemoryOntologyWorker) run(ctx context.Context, cancel context.CancelFunc) {
	defer cancel()
	defer func() {
		recovered := recover()
		w.gate.Unlock()
		if recovered != nil && w.logger != nil {
			w.logger.Error(context.WithoutCancel(ctx), "memory ontology worker panicked",
				logging.String("error", boundedMemoryOntologyWorkerError(fmt.Sprint(recovered))))
		}
		w.mu.Lock()
		w.running = false
		w.cancel = nil
		close(w.done)
		w.mu.Unlock()
	}()

	startedAt := w.now().UTC()
	maintained := 0
	if w.maintenance != nil {
		var err error
		maintained, err = w.maintenance.FailExhaustedStaleRuns(
			ctx, startedAt, productionMemoryOntologyStaleRunAge, productionMemoryOntologyStaleRunRepairLimit,
		)
		if err != nil {
			if w.logger != nil {
				w.logger.Warn(context.WithoutCancel(ctx), "memory ontology stale run maintenance failed",
					logging.String("error", boundedMemoryOntologyWorkerError(err.Error())))
			}
			return
		}
	}
	report, err := w.processor.Sweep(ctx, w.limit)
	if w.logger == nil {
		return
	}
	fields := []logging.Field{
		logging.String("worker_name", w.name),
		logging.Int("scanned", report.Scanned),
		logging.Int("processed", report.Processed),
		logging.Int("succeeded", report.Succeeded),
		logging.Int("failed", report.Failed),
		logging.Int("skipped", report.Skipped),
		logging.Int("linked", report.Linked),
		logging.Int("stale_runs_failed", maintained),
		logging.Any("duration", w.now().UTC().Sub(startedAt)),
	}
	if err != nil {
		fields = append(fields, logging.String("error", boundedMemoryOntologyWorkerError(err.Error())))
		w.logger.Warn(context.WithoutCancel(ctx), "memory ontology worker cycle failed", fields...)
		return
	}
	w.logger.Info(context.WithoutCancel(ctx), "memory ontology worker cycle completed", fields...)
}

func (w *productionMemoryOntologyWorker) Close() error {
	w.mu.Lock()
	w.closed = true
	cancel, done := w.cancel, w.done
	w.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	select {
	case <-done:
		return nil
	case <-time.After(10 * time.Second):
		return fmt.Errorf("memory ontology worker did not stop after cancellation")
	}
}

func boundedMemoryOntologyWorkerError(value string) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) > 1000 {
		return string(runes[:1000])
	}
	return value
}
