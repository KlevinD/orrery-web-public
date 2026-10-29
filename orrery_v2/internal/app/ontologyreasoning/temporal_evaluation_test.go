package ontologyreasoning

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"orrery_v2/internal/app/contextgraph"
)

func TestTemporalInferenceExpiresSupportsSeparatelyAndRequiresWholeJoin(t *testing.T) {
	ctx := context.Background()
	pack, rules := inferenceFixture()
	record := evaluationRecord(rules)
	now := record.BusinessAsOf
	firstEnd, secondEnd, linkEnd := now.Add(time.Hour), now.Add(2*time.Hour), now.Add(3*time.Hour)
	pack.Facts[0].Temporal = contextgraph.TemporalMetadata{Mode: "state_interval", BusinessValidTo: &firstEnd}
	pack.Facts[1].Temporal = contextgraph.TemporalMetadata{Mode: "state_interval", BusinessValidTo: &secondEnd}
	pack.NeighborLinks[0].Temporal = contextgraph.TemporalMetadata{Mode: "state_interval", BusinessValidTo: &linkEnd}
	s := NewService(ServiceOptions{Store: NewMemoryStore()})
	first, err := s.Evaluate(ctx, pack, record, nil)
	if err != nil || len(first.Candidates) != 1 || len(first.Request.Evaluation.Closure.Conclusions[0].Derivations) != 2 || first.Request.Evaluation.NextBoundary == nil || !first.Request.Evaluation.NextBoundary.Equal(firstEnd) {
		t.Fatalf("initial temporal supports: %+v %v", first, err)
	}
	record.BusinessAsOf, record.KnownAt = firstEnd, firstEnd
	record.PreviousRunID = first.ID
	second, err := s.Evaluate(ctx, pack, record, nil)
	if err != nil || len(second.Candidates) != 1 || len(second.Request.Evaluation.Closure.Conclusions[0].Derivations) != 1 || len(second.Request.Evaluation.WithdrawnIDs) != 0 {
		t.Fatalf("alternative proof lost: %+v %v", second, err)
	}
	record.BusinessAsOf, record.KnownAt = secondEnd, secondEnd
	record.PreviousRunID = second.ID
	third, err := s.Evaluate(ctx, pack, record, nil)
	if err != nil || len(third.Candidates) != 0 || len(third.Request.Evaluation.WithdrawnIDs) != 1 {
		t.Fatalf("last support survived: %+v %v", third, err)
	}
	pack.Facts[0].Temporal.BusinessValidTo = nil
	record.BusinessAsOf, record.KnownAt = linkEnd, linkEnd
	record.PreviousRunID = ""
	fourth, err := s.Evaluate(ctx, pack, record, nil)
	if err != nil || len(fourth.Candidates) != 0 {
		t.Fatal("join used an expired relation")
	}
	old, _ := s.GetRun(ctx, pack.TenantID, first.ID)
	if len(old.Request.Evaluation.Closure.Conclusions[0].Derivations) != 2 {
		t.Fatal("past derivations were mutated")
	}
}

func TestTemporalInferenceFiltersKnowledgeTimeAndInactiveConflicts(t *testing.T) {
	pack, rules := inferenceFixture()
	record := evaluationRecord(rules)
	now := record.BusinessAsOf
	future := now.Add(time.Hour)
	pack.Facts[1].Value = "healthy"
	pack.Facts[1].Temporal = contextgraph.TemporalMetadata{Mode: "state_interval", BusinessValidFrom: future}
	s := NewService(ServiceOptions{Store: NewMemoryStore()})
	run, err := s.Evaluate(context.Background(), pack, record, nil)
	if err != nil || len(run.Candidates) != 1 || len(run.Request.Evaluation.FactConflicts) != 0 {
		t.Fatal("future assertion became a current conflict")
	}
	pack.Facts[0].Temporal.SourceRecordedAt = future
	run, err = s.Evaluate(context.Background(), pack, record, nil)
	if err != nil || len(run.Candidates) != 0 {
		t.Fatal("future knowledge was used before it was recorded")
	}
	pack.Facts[0].Temporal.SourceRecordedAt = now.Add(-time.Hour)
	pack.Facts[0].Temporal.SourceSupersededAt = &now
	run, err = s.Evaluate(context.Background(), pack, record, nil)
	if err != nil || len(run.Candidates) != 0 {
		t.Fatal("superseded knowledge remained eligible")
	}
}

func TestHistoricalInferenceCannotTrackPreviewOrExecuteCurrentState(t *testing.T) {
	pack, rules := inferenceFixture()
	record := evaluationRecord(rules)
	record.Historical = true
	s := NewService(ServiceOptions{Store: NewMemoryStore()})
	run, err := s.Evaluate(context.Background(), pack, record, nil)
	if err != nil || len(run.Candidates) != 1 {
		t.Fatalf("read-only historical inference: %v", err)
	}
	var lifecycle LifecycleStore
	if _, err := lifecycle.Track(context.Background(), run, 0, nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal("historical run entered current session")
	}
	if _, err := lifecycle.Preview(context.Background(), run, CandidateState{}, WorkflowContext{}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal("historical run produced an executable preview")
	}
	if _, err := lifecycle.Workflow(context.Background(), "apply", WorkflowRequest{}, run, WorkflowContext{}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal("historical run entered writeback")
	}
	record.Historical = false
	record.PreviousRunID = run.ID
	if _, err := s.Evaluate(context.Background(), pack, record, nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal("historical/current run comparison crossed scope")
	}
}

func TestDisconnectedIdentityRetainedOnlyForOwnedRetraction(t *testing.T) {
	pack, rules := inferenceFixture()
	record := evaluationRecord(rules)
	record.RootID = pack.CandidateObjects[0].ID
	end := record.BusinessAsOf
	pack.NeighborLinks[0].Temporal = contextgraph.TemporalMetadata{Mode: "state_interval", BusinessValidTo: &end}
	filtered, _ := temporalEvaluationPack(pack, record)
	if len(filtered.CandidateObjects) != 1 || len(filtered.RetractionEndpoints) != len(pack.CandidateObjects) {
		t.Fatal("current visible identity not retained for retraction")
	}
	premises, _, err := premisesFromPack(filtered)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range premises {
		if p.Atom.Kind == "object" && p.Atom.Subject != record.RootID {
			t.Fatal("retraction identity became a premise")
		}
	}
	record.Historical = true
	filtered, _ = temporalEvaluationPack(pack, record)
	if len(filtered.RetractionEndpoints) != 0 {
		t.Fatal("historical read retained current writeback identities")
	}
}

func TestInferenceInputStampDetectsClockBoundaryWithoutDatabaseWrite(t *testing.T) {
	ctx := context.Background()
	lifecycle, run, _, _ := workflowFixture(t, workflowDatabase(t, false))
	now := run.Request.Evaluation.BusinessAsOf
	end := now.Add(time.Hour)
	graph := contextgraph.NewPlatformStore(lifecycle.Store)
	f := contextgraph.Fact{ID: "timed-source", TenantID: run.TenantID, Subject: contextgraph.Ref{Kind: "project", ID: "Project-A"}, Predicate: "status", Value: "delayed", SourceStreamID: "test-timed", SourceAccessRef: contextgraph.AccessRef{Kind: "organization", ID: run.TenantID, TenantID: run.TenantID}, Temporal: contextgraph.TemporalMetadata{Mode: "state_interval", BusinessTimeUnspecified: true, SourceRecordedAt: now, BusinessValidTo: &end}, UpdatedAt: now}
	if err := graph.SaveFact(ctx, f); err != nil {
		t.Fatal(err)
	}
	query := func(q string, args ...any) (*sql.Rows, error) { return lifecycle.Store.Query(ctx, q, args...) }
	before, err := inferenceInputStampAt(ctx, query, run.TenantID, now)
	if err != nil {
		t.Fatal(err)
	}
	after, err := inferenceInputStampAt(ctx, query, run.TenantID, end)
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("clock-only expiry did not invalidate continuous observation/commit fence")
	}
	again, err := inferenceInputStampAt(ctx, query, run.TenantID, end.Add(time.Minute))
	if err != nil || again != after {
		t.Fatal("clock stamp changes without a semantic boundary")
	}
}
