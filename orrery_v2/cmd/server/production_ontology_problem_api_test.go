package main

import (
	"context"
	"errors"
	"testing"
	"time"

	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
	"orrery_v2/internal/app/problemhub"
	httptransport "orrery_v2/internal/transport/http"
)

type problemAPITestRepository struct {
	problem   problemhub.ProblemCase
	listQuery problemhub.ListQuery
	route     problemhub.RouteCommand
}

func (r *problemAPITestRepository) Observe(context.Context, problemhub.Observation, problemhub.ProblemCase) (problemhub.ProblemCase, bool, error) {
	return problemhub.ProblemCase{}, false, problemhub.ErrNotFound
}

func (r *problemAPITestRepository) Get(_ context.Context, tenantID, problemID string) (problemhub.ProblemCase, error) {
	if r.problem.TenantID != tenantID || r.problem.ID != problemID {
		return problemhub.ProblemCase{}, problemhub.ErrNotFound
	}
	return r.problem.Clone(), nil
}

func (r *problemAPITestRepository) GetBySource(context.Context, string, problemhub.ObjectRef) (problemhub.ProblemCase, error) {
	return problemhub.ProblemCase{}, problemhub.ErrNotFound
}

func (r *problemAPITestRepository) List(_ context.Context, query problemhub.ListQuery) ([]problemhub.ProblemCase, error) {
	r.listQuery = query
	if query.TenantID != r.problem.TenantID {
		return []problemhub.ProblemCase{}, nil
	}
	return []problemhub.ProblemCase{r.problem.Clone()}, nil
}

func (r *problemAPITestRepository) SyncStatusBySource(context.Context, string, problemhub.ObjectRef, problemhub.Status, time.Time) (problemhub.ProblemCase, bool, error) {
	return problemhub.ProblemCase{}, false, problemhub.ErrNotFound
}

func (r *problemAPITestRepository) RecordOutcome(context.Context, problemhub.Outcome, time.Time) (problemhub.ProblemCase, bool, error) {
	return problemhub.ProblemCase{}, false, problemhub.ErrNotFound
}

func (r *problemAPITestRepository) Route(_ context.Context, command problemhub.RouteCommand, _ time.Time) (problemhub.ProblemCase, bool, error) {
	if command.TenantID != r.problem.TenantID || command.ProblemCaseID != r.problem.ID {
		return problemhub.ProblemCase{}, false, problemhub.ErrNotFound
	}
	r.route = command
	r.problem.Owner = command.Owner
	r.problem.Version++
	return r.problem.Clone(), true, nil
}

func (r *problemAPITestRepository) SetLessonCandidate(context.Context, string, string, int64, string, time.Time) (problemhub.ProblemCase, bool, error) {
	return problemhub.ProblemCase{}, false, problemhub.ErrNotFound
}

func TestProductionOntologyProblemAPIListsReadsAndRoutesWithServerActor(t *testing.T) {
	now := time.Date(2026, 9, 3, 8, 0, 0, 0, time.UTC)
	repository := &problemAPITestRepository{problem: problemhub.ProblemCase{
		ID: "problem-1", TenantID: "tenant-1", Fingerprint: "reliability:timeout", SourceDomain: "reliability",
		Category: "transient", Severity: "error", Status: problemhub.StatusOpen, Title: "provider timeout",
		RedactedSummary: "provider request timed out", PrimarySource: problemhub.ObjectRef{Kind: "failure_incident", ID: "incident-1"},
		SourceRefs: []problemhub.ObjectRef{{Kind: "failure_incident", ID: "incident-1"}}, EvidenceIDs: []string{"evidence-1"},
		OccurrenceCount: 2, Version: 1, FirstSeenAt: now, LastSeenAt: now, CreatedAt: now, UpdatedAt: now,
	}}
	control := problemhub.NewService(repository, problemhub.WithClock(func() time.Time { return now }))
	api := newProductionOntologyProblemAPI(control, reliabilityTestGate())
	principal := reliabilityTestPrincipal()

	page, err := api.ListProblems(context.Background(), principal, httptransport.OntologyProblemListQuery{
		TenantID: "tenant-1", Status: "open", SourceDomain: "reliability", Search: "timeout", Limit: 1,
	})
	if err != nil || page.Returned != 1 || page.Items[0].ID != "problem-1" || page.Items[0].EvidenceIDs[0] != "evidence-1" {
		t.Fatalf("ListProblems page=%+v err=%v", page, err)
	}
	if repository.listQuery.Limit != 2 || repository.listQuery.Search != "timeout" || repository.listQuery.Status != problemhub.StatusOpen {
		t.Fatalf("domain list query = %+v", repository.listQuery)
	}

	detail, err := api.GetProblem(context.Background(), principal, "tenant-1", "problem-1")
	if err != nil || detail.PrimarySource.ID != "incident-1" || detail.OccurrenceCount != 2 {
		t.Fatalf("GetProblem detail=%+v err=%v", detail, err)
	}

	routed, err := api.RouteProblem(context.Background(), principal, "tenant-1", "problem-1", httptransport.OntologyProblemRouteRequest{
		Owner: httptransport.OntologyProblemActorDTO{Kind: "team", ID: "platform"}, IdempotencyKey: "route-1",
	})
	if err != nil || routed.Owner.ID != "platform" {
		t.Fatalf("RouteProblem routed=%+v err=%v", routed, err)
	}
	if repository.route.Actor.Kind != "human" || repository.route.Actor.ID != "operator-1" || repository.route.ID != "route-1" {
		t.Fatalf("server route actor = %+v command=%+v", repository.route.Actor, repository.route)
	}

	_, err = api.GetProblem(context.Background(), principal, "tenant-1", "missing")
	if !errors.Is(err, ontologycontract.ErrUnavailable) {
		t.Fatalf("missing problem error=%v", err)
	}
}
