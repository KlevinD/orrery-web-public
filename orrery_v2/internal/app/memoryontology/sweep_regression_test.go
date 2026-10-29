package memoryontology

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/ontology"
	memcore "orrery_v2/internal/memory/core"
	memstore "orrery_v2/internal/memory/store"
)

type failingSweepGraph struct {
	*contextgraph.MemoryStore
	calls int
	err   error
}

func (g *failingSweepGraph) Snapshot(context.Context, string) ([]contextgraph.Object, []contextgraph.Link, []contextgraph.Fact, []contextgraph.Evidence, error) {
	g.calls++
	return nil, nil, nil, nil, g.err
}

type failingClaimStore struct {
	recordingRunStore
	err error
}

func (s *failingClaimStore) ClaimRun(context.Context, RunKey, string, time.Time) (Run, bool, error) {
	return Run{}, false, s.err
}

func TestProcessorReportsSnapshotAndClaimErrorsAsFailures(t *testing.T) {
	problem := errors.New("database unavailable")
	scope := memcore.MemoryScope{Kind: memcore.ScopeOrganization, ID: "tenant"}
	records := []memcore.MemoryRecord{{ID: "one", Scope: scope}, {ID: "two", Scope: scope}}
	for _, claimFailure := range []bool{false, true} {
		graph := &failingSweepGraph{MemoryStore: contextgraph.NewMemoryStore(), err: problem}
		var store RunStore = &recordingRunStore{}
		var graphStore GraphStore = graph
		if claimFailure {
			store = &failingClaimStore{err: problem}
			graphStore = contextgraph.NewMemoryStore()
		}
		processor := NewProcessor(fixedMemoryReader{records: records}, store, &recordingExtractor{}, NewGraphApplier(graphStore, ontology.NewMemoryRegistry(), time.Now), time.Now)
		report, err := processor.Sweep(context.Background(), 10)
		if !errors.Is(err, problem) || report.Failed != 2 || report.Processed != 2 || report.Skipped != 0 || report.Succeeded != 0 {
			t.Fatalf("claimFailure=%t: report=%+v err=%v", claimFailure, report, err)
		}
		if !claimFailure && graph.calls != 1 {
			t.Fatalf("failed tenant snapshot retried %d times in same cycle", graph.calls)
		}
	}
}

type boundedSweepReader struct {
	fixedMemoryReader
	cursors []string
	limits  []int
}

func (r *boundedSweepReader) ScanRecords(_ context.Context, after string, limit int) (memstore.RecordScanPage, error) {
	r.cursors = append(r.cursors, after)
	r.limits = append(r.limits, limit)
	page := memstore.RecordScanPage{}
	for _, record := range r.records {
		if record.ID <= after {
			continue
		}
		if len(page.Items) == limit {
			page.HasMore = true
			break
		}
		page.Items = append(page.Items, memstore.RecordScanItem{Key: record.ID, Record: record})
		page.NextKey = record.ID
	}
	page.Scanned = len(page.Items)
	return page, nil
}

type completedPrefixRuns struct {
	recordingRunStore
	claims int
}

func (r *completedPrefixRuns) GetRun(_ context.Context, key RunKey) (Run, bool, error) {
	if key.MemoryID < "memory-020" {
		return Run{RunKey: key, Status: RunSucceeded}, true, nil
	}
	return Run{}, false, nil
}
func (r *completedPrefixRuns) ClaimRun(ctx context.Context, key RunKey, model string, now time.Time) (Run, bool, error) {
	r.claims++
	return r.recordingRunStore.ClaimRun(ctx, key, model, now)
}

func TestProcessorBoundedScanReachesPendingRecordsAfterCompletedPrefix(t *testing.T) {
	reader := &boundedSweepReader{}
	graph := contextgraph.NewMemoryStore()
	for i := 0; i < 25; i++ {
		id := fmt.Sprintf("memory-%03d", i)
		record := memcore.MemoryRecord{ID: id, Scope: memcore.MemoryScope{Kind: memcore.ScopeOrganization, ID: "tenant"}}
		reader.records = append(reader.records, record)
		if err := graph.SaveObject(context.Background(), contextgraph.Object{ID: id, TenantID: "tenant", ObjectType: "memory_record", OwnerAccessRef: contextgraph.AccessRef{Kind: "organization", ID: "tenant", TenantID: "tenant"}}); err != nil {
			t.Fatal(err)
		}
	}
	runs := &completedPrefixRuns{}
	extractor := &recordingExtractor{}
	processor := NewProcessor(reader, runs, extractor, NewGraphApplier(graph, ontology.NewMemoryRegistry(), time.Now), time.Now)
	first, err := processor.Sweep(context.Background(), 1)
	if err != nil || first.Scanned != 20 || first.Skipped != 20 || runs.claims != 0 {
		t.Fatalf("first=%+v claims=%d err=%v", first, runs.claims, err)
	}
	second, err := processor.Sweep(context.Background(), 1)
	if err != nil || second.Processed != 1 || second.Succeeded != 1 || extractor.calls != 1 || reader.cursors[1] != "memory-019" {
		t.Fatalf("second=%+v cursors=%v err=%v", second, reader.cursors, err)
	}
	if _, err := processor.Sweep(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if reader.cursors[2] != "memory-020" {
		t.Fatalf("unprocessed tail was skipped: cursors=%v", reader.cursors)
	}
}

func TestProcessorStopsOnDeadlineWithoutReportingSkipped(t *testing.T) {
	processor := NewProcessor(fixedMemoryReader{records: []memcore.MemoryRecord{{ID: "one"}, {ID: "two"}}}, &failingClaimStore{err: context.DeadlineExceeded}, &recordingExtractor{}, NewGraphApplier(contextgraph.NewMemoryStore(), ontology.NewMemoryRegistry(), time.Now), time.Now)
	report, err := processor.Sweep(context.Background(), 10)
	if !errors.Is(err, context.DeadlineExceeded) || report.Failed != 1 || report.Skipped != 0 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
}
