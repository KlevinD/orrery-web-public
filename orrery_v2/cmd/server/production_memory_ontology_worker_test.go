package main

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"orrery_v2/internal/app/memoryontology"
)

func TestMemoryOntologyTriggerReturnsWhileExtractorRuns(t *testing.T) {
	processor := newBlockingMemoryOntologyProcessor()
	worker := newTestProductionMemoryOntologyWorker(processor)
	startedAt := time.Now()
	if err := worker.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(startedAt); elapsed > 100*time.Millisecond {
		t.Fatalf("global sweeper waited %s for memory ontology extraction", elapsed)
	}
	waitForMemoryOntologyProcessorStart(t, processor.started)
	close(processor.release)
}

func TestMemoryOntologyWorkerDefaultsUseBoundedBatch(t *testing.T) {
	worker := newProductionMemoryOntologyWorker(&immediateMemoryOntologyProcessor{done: make(chan struct{}, 1)}, productionMemoryOntologyWorkerOptions{})
	if worker.limit != 5 || worker.timeout != 2*time.Minute || worker.cadence != 5*time.Minute {
		t.Fatalf("worker defaults = limit:%d timeout:%s cadence:%s", worker.limit, worker.timeout, worker.cadence)
	}
}

func TestMemoryOntologyTriggerDoesNotOverlapCycles(t *testing.T) {
	processor := newBlockingMemoryOntologyProcessor()
	worker := newTestProductionMemoryOntologyWorker(processor)
	if err := worker.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForMemoryOntologyProcessorStart(t, processor.started)
	if err := worker.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := processor.calls.Load(); got != 1 {
		t.Fatalf("calls = %d, want 1", got)
	}
	close(processor.release)
}

func TestMemoryOntologyWorkersSharingGateDoNotOverlapAndBlockedWorkerRetries(t *testing.T) {
	gate := &sync.Mutex{}
	firstProcessor := newBlockingMemoryOntologyProcessor()
	secondProcessor := newBlockingMemoryOntologyProcessor()
	newWorker := func(processor memoryOntologyCycleProcessor) *productionMemoryOntologyWorker {
		return newProductionMemoryOntologyWorker(processor, productionMemoryOntologyWorkerOptions{
			Gate:    gate,
			Timeout: 20 * time.Second,
			Cadence: time.Minute,
			Limit:   1,
			Now:     time.Now,
		})
	}
	first := newWorker(firstProcessor)
	second := newWorker(secondProcessor)

	if err := first.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForMemoryOntologyProcessorStart(t, firstProcessor.started)
	if err := second.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := secondProcessor.calls.Load(); got != 0 {
		t.Fatalf("blocked worker calls = %d, want 0", got)
	}

	close(firstProcessor.release)
	waitForMemoryOntologyWorkerIdle(t, first)
	if err := second.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForMemoryOntologyProcessorStart(t, secondProcessor.started)
	if got := secondProcessor.calls.Load(); got != 1 {
		t.Fatalf("retried worker calls = %d, want 1", got)
	}
	close(secondProcessor.release)
	waitForMemoryOntologyWorkerIdle(t, second)
}

func TestMemoryOntologyTriggerHonorsCadence(t *testing.T) {
	now := time.Date(2026, 8, 24, 1, 0, 0, 0, time.UTC)
	processor := &immediateMemoryOntologyProcessor{done: make(chan struct{}, 1)}
	worker := newProductionMemoryOntologyWorker(processor, productionMemoryOntologyWorkerOptions{
		Timeout: 20 * time.Second,
		Cadence: time.Minute,
		Limit:   1,
		Now:     func() time.Time { return now },
	})
	if err := worker.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-processor.done:
	case <-time.After(time.Second):
		t.Fatal("memory ontology processor did not run")
	}
	waitForMemoryOntologyWorkerIdle(t, worker)
	now = now.Add(30 * time.Second)
	if err := worker.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := processor.calls.Load(); got != 1 {
		t.Fatalf("calls = %d, want 1", got)
	}
}

func TestMemoryOntologyTriggerClearsRunningAfterPanic(t *testing.T) {
	processor := &panicMemoryOntologyProcessor{}
	worker := newTestProductionMemoryOntologyWorker(processor)
	if err := worker.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForMemoryOntologyWorkerIdle(t, worker)
	worker.mu.Lock()
	running := worker.running
	worker.mu.Unlock()
	if running {
		t.Fatal("worker stayed running after panic")
	}
}

func TestMemoryOntologyWorkerRunsMaintenanceBeforeExtraction(t *testing.T) {
	now := time.Date(2026, 8, 24, 1, 0, 0, 0, time.UTC)
	maintenance := &recordingMemoryOntologyMaintenance{}
	processor := &immediateMemoryOntologyProcessor{done: make(chan struct{}, 1)}
	worker := newProductionMemoryOntologyWorker(processor, productionMemoryOntologyWorkerOptions{
		Maintenance: maintenance,
		Timeout:     20 * time.Second,
		Cadence:     time.Minute,
		Limit:       1,
		Now:         func() time.Time { return now },
	})

	if err := worker.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-processor.done:
	case <-time.After(time.Second):
		t.Fatal("memory ontology processor did not run")
	}
	waitForMemoryOntologyWorkerIdle(t, worker)
	if maintenance.calls != 1 || !maintenance.now.Equal(now) ||
		maintenance.staleAfter != 10*time.Minute || maintenance.limit != 100 {
		t.Fatalf("maintenance = %+v", maintenance)
	}
}

func TestMemoryOntologyWorkerStopsCycleWhenMaintenanceFails(t *testing.T) {
	maintenance := &recordingMemoryOntologyMaintenance{err: errors.New("maintenance unavailable")}
	processor := &immediateMemoryOntologyProcessor{done: make(chan struct{}, 1)}
	worker := newProductionMemoryOntologyWorker(processor, productionMemoryOntologyWorkerOptions{
		Maintenance: maintenance,
		Timeout:     20 * time.Second,
		Cadence:     time.Minute,
		Limit:       1,
		Now:         time.Now,
	})

	if err := worker.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForMemoryOntologyWorkerIdle(t, worker)
	if processor.calls.Load() != 0 {
		t.Fatalf("processor calls = %d, want 0", processor.calls.Load())
	}
}

type blockingMemoryOntologyProcessor struct {
	calls   atomic.Int32
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingMemoryOntologyProcessor() *blockingMemoryOntologyProcessor {
	return &blockingMemoryOntologyProcessor{started: make(chan struct{}), release: make(chan struct{})}
}

func newTestProductionMemoryOntologyWorker(processor memoryOntologyCycleProcessor) *productionMemoryOntologyWorker {
	return newProductionMemoryOntologyWorker(processor, productionMemoryOntologyWorkerOptions{
		Timeout: 20 * time.Second,
		Cadence: time.Minute,
		Limit:   1,
		Now:     time.Now,
	})
}

func (p *blockingMemoryOntologyProcessor) Sweep(ctx context.Context, _ int) (memoryontology.SweepReport, error) {
	p.calls.Add(1)
	p.once.Do(func() { close(p.started) })
	select {
	case <-p.release:
		return memoryontology.SweepReport{Processed: 1, Succeeded: 1}, nil
	case <-ctx.Done():
		return memoryontology.SweepReport{}, ctx.Err()
	}
}

type immediateMemoryOntologyProcessor struct {
	calls atomic.Int32
	done  chan struct{}
}

func (p *immediateMemoryOntologyProcessor) Sweep(context.Context, int) (memoryontology.SweepReport, error) {
	p.calls.Add(1)
	p.done <- struct{}{}
	return memoryontology.SweepReport{}, nil
}

type panicMemoryOntologyProcessor struct{}

func (*panicMemoryOntologyProcessor) Sweep(context.Context, int) (memoryontology.SweepReport, error) {
	panic("test panic")
}

type recordingMemoryOntologyMaintenance struct {
	calls      int
	now        time.Time
	staleAfter time.Duration
	limit      int
	err        error
}

func (m *recordingMemoryOntologyMaintenance) FailExhaustedStaleRuns(_ context.Context, now time.Time, staleAfter time.Duration, limit int) (int, error) {
	m.calls++
	m.now = now
	m.staleAfter = staleAfter
	m.limit = limit
	return 1, m.err
}

func waitForMemoryOntologyProcessorStart(t *testing.T, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("memory ontology processor did not start")
	}
}

func waitForMemoryOntologyWorkerIdle(t *testing.T, worker *productionMemoryOntologyWorker) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		worker.mu.Lock()
		running := worker.running
		worker.mu.Unlock()
		if !running {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("memory ontology worker did not become idle")
}
