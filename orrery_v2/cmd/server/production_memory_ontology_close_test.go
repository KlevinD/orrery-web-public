package main

import (
	"context"
	"testing"
)

func TestMemoryOntologyWorkerCloseCancelsAndDoesNotRestart(t *testing.T) {
	processor := newBlockingMemoryOntologyProcessor()
	worker := newTestProductionMemoryOntologyWorker(processor)
	if err := worker.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForMemoryOntologyProcessorStart(t, processor.started)
	if err := worker.Close(); err != nil {
		t.Fatal(err)
	}
	waitForMemoryOntologyWorkerIdle(t, worker)
	if err := worker.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if processor.calls.Load() != 1 {
		t.Fatalf("closed worker restarted: calls=%d", processor.calls.Load())
	}
}
