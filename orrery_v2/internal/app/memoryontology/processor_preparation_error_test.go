package memoryontology

import (
	"context"
	"errors"
	"orrery_v2/internal/app/contextgraph"
	memcore "orrery_v2/internal/memory/core"
	"testing"
	"time"
)

type failedSnapshotGraph struct {
	*contextgraph.MemoryStore
	calls int
	err   error
}

func (s *failedSnapshotGraph) Snapshot(context.Context, string) ([]contextgraph.Object, []contextgraph.Link, []contextgraph.Fact, []contextgraph.Evidence, error) {
	s.calls++
	return nil, nil, nil, nil, s.err
}
func TestPreparationFailureIsNotSkippedAndRespectsLimit(t *testing.T) {
	sentinel := errors.New("snapshot unavailable")
	graph := &failedSnapshotGraph{MemoryStore: contextgraph.NewMemoryStore(), err: sentinel}
	records := []memcore.MemoryRecord{{ID: "m1"}, {ID: "m2"}}
	p := NewProcessor(fixedMemoryReader{records: records}, &recordingRunStore{}, &recordingExtractor{}, NewGraphApplier(graph, nil, time.Now), time.Now)
	report, err := p.Sweep(context.Background(), 1)
	if !errors.Is(err, sentinel) || report.Failed != 1 || report.Processed != 1 || report.Skipped != 0 || graph.calls != 1 {
		t.Fatalf("report=%+v calls=%d err=%v", report, graph.calls, err)
	}
}
func TestCanceledSweepStopsBeforeSnapshot(t *testing.T) {
	graph := &failedSnapshotGraph{MemoryStore: contextgraph.NewMemoryStore()}
	p := NewProcessor(fixedMemoryReader{records: []memcore.MemoryRecord{{ID: "m1"}}}, &recordingRunStore{}, &recordingExtractor{}, NewGraphApplier(graph, nil, time.Now), time.Now)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := p.Sweep(ctx, 1)
	if !errors.Is(err, context.Canceled) || graph.calls != 0 {
		t.Fatalf("calls=%d err=%v", graph.calls, err)
	}
}
