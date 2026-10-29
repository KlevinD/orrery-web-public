package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/ontologyreasoning"
)

func TestProductionOntologyHistoricalInferenceIsolatedAndTimeFiltered(t *testing.T) {
	a, core, p, req := productionInferenceWorkflowFixture(t)
	ctx := context.Background()
	graph := contextgraph.NewPlatformStore(core)
	now := time.Now().UTC()
	start, end := now.Add(-2*time.Hour), now.Add(-time.Hour)
	access := contextgraph.AccessRef{Kind: "organization", ID: req.TenantID, TenantID: req.TenantID}
	if err := graph.SaveLink(ctx, contextgraph.Link{ID: "source", TenantID: req.TenantID, LinkType: "workflow_source", FromObjectID: "node-a", ToObjectID: "node-b", Status: "active", SourceAccessRef: access, SourceStreamID: "timed-source", SourceVersion: 2, UpdatedAt: now, Temporal: contextgraph.TemporalMetadata{Mode: "state_interval", BusinessValidFrom: start, BusinessValidTo: &end, SourceRecordedAt: start, OrreryIngestedAt: start}}); err != nil {
		t.Fatal(err)
	}
	current, err := a.Evaluate(ctx, p, req)
	if err != nil || len(current.Candidates) != 0 {
		t.Fatalf("expired current relation: candidates=%d %v", len(current.Candidates), err)
	}
	query := current.Request.Evaluation.QueryHash
	before, err := a.lifecycle.Get(ctx, ontologyreasoning.SessionKey{Tenant: req.TenantID, Actor: current.Request.Evaluation.ActorHash, Query: query})
	if err != nil {
		t.Fatal(err)
	}
	business, known := start.Add(30*time.Minute), now
	req.BusinessAsOf, req.KnownAt = &business, &known
	history, err := a.Evaluate(ctx, p, req)
	if err != nil || len(history.Candidates) != 1 || !history.Request.Evaluation.Historical || history.Request.Evaluation.QueryHash == query || len(history.CurrentCandidates) != 0 {
		t.Fatalf("historical read: candidates=%d %v", len(history.Candidates), err)
	}
	after, err := a.lifecycle.Get(ctx, ontologyreasoning.SessionKey{Tenant: req.TenantID, Actor: current.Request.Evaluation.ActorHash, Query: query})
	if err != nil || after.Revision != before.Revision {
		t.Fatal("historical query mutated current candidate session")
	}
	if len(history.Proposals) != 0 {
		t.Fatal("historical read created writeback proposals")
	}
	if _, err := a.service.CreateProposals(ctx, req.TenantID, history.ID); !errors.Is(err, ontologyreasoning.ErrInvalidRequest) {
		t.Fatal("historical run entered proposal workflow")
	}
	if _, err := a.CandidateWorkflow(ctx, p, "apply", ontologyreasoning.WorkflowRequest{TenantID: req.TenantID, RunID: history.ID, CandidateID: history.Candidates[0].ID}); !errors.Is(err, ontologyreasoning.ErrInvalidRequest) {
		t.Fatal("historical result entered writeback")
	}
	req.KnownAt = nil
	if _, err := a.Evaluate(ctx, p, req); !errors.Is(err, ontologyreasoning.ErrInvalidRequest) {
		t.Fatal("partial timeline accepted")
	}
	future := now.Add(time.Hour)
	req.KnownAt = &future
	if _, err := a.Evaluate(ctx, p, req); !errors.Is(err, ontologyreasoning.ErrInvalidRequest) {
		t.Fatal("future system knowledge accepted")
	}
	req.KnownAt = &known
	overflow := time.Date(3000, 1, 1, 0, 0, 0, 0, time.UTC)
	req.BusinessAsOf = &overflow
	if _, err := a.Evaluate(ctx, p, req); !errors.Is(err, ontologyreasoning.ErrInvalidRequest) {
		t.Fatal("overflowing history coordinate accepted")
	}
}

func TestProductionOntologyTemporalExpiryRetractsThroughModel(t *testing.T) {
	w, _, judge, set := continuousInferenceFixture(t)
	ctx := context.Background()
	evaluation := continuousInferenceEvaluationJob(set)
	if err := w.Evaluate(ctx, evaluation); err != nil {
		t.Fatal(err)
	}
	jobs := continuousInferenceJobs(t, w, ontologyreasoning.InferenceDecisionTopic)
	if len(jobs) != 1 {
		t.Fatal("initial decision missing")
	}
	if err := w.Decide(ctx, jobs[0]); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	end := now.Add(-time.Minute)
	access := contextgraph.AccessRef{Kind: "organization", ID: set.TenantID, TenantID: set.TenantID}
	if err := contextgraph.NewPlatformStore(w.api.lifecycle.Store).SaveLink(ctx, contextgraph.Link{ID: "source", TenantID: set.TenantID, LinkType: "workflow_source", FromObjectID: "node-a", ToObjectID: "node-b", Status: "active", SourceAccessRef: access, UpdatedAt: now, Temporal: contextgraph.TemporalMetadata{Mode: "state_interval", BusinessValidFrom: now.Add(-time.Hour), BusinessValidTo: &end, SourceRecordedAt: now.Add(-time.Hour)}}); err != nil {
		t.Fatal(err)
	}
	if err := w.Evaluate(ctx, evaluation); err != nil {
		t.Fatal(err)
	}
	jobs = continuousInferenceJobs(t, w, ontologyreasoning.InferenceDecisionTopic)
	if len(jobs) != 2 {
		t.Fatal("expiry did not queue model judgment")
	}
	if err := w.Decide(ctx, jobs[1]); err != nil {
		t.Fatal(err)
	}
	if judge.calls != 2 || judge.inputs[1].CurrentSupport || judge.inputs[1].Preview.Effect != "retract" {
		t.Fatal("expiry did not retract through the model")
	}
	var owned int
	if err := w.api.lifecycle.Store.QueryRow(ctx, `SELECT COUNT(*) FROM context_graph_links WHERE tenant_id=? AND id LIKE 'inference-link:%'`, set.TenantID).Scan(&owned); err != nil || owned != 0 {
		t.Fatalf("expired derived relation remains: %d %v", owned, err)
	}
}
