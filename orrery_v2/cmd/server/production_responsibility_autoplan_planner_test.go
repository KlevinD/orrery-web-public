package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/app/responsibility"
)

func TestProductionResponsibilityAutoplanPlannerUsesOneMinuteTimeout(t *testing.T) {
	options := productionResponsibilityAutoplanPlannerOptions(nil)
	if options.Timeout != time.Minute {
		t.Fatalf("production responsibility autoplan timeout = %s, want 1m", options.Timeout)
	}
}

func TestProductionResponsibilityAutoplanPlannerFallsBackOnInvalidModelOutput(t *testing.T) {
	fallbackCalls := 0
	request := responsibility.AutoplanPlanningRequest{
		PlanRequest: responsibility.AutoplanRequest{AgentProfileID: "A000010"},
		Portfolio: responsibility.PeopleResponsibilityPortfolio{
			SubjectID:      "employee-1",
			SourceRevision: "1:1",
		},
		Responsibility: responsibility.PeopleResolvedResponsibility{
			ID: "responsibility-1", Title: "Improve assigned responsibilities", Revision: 1,
		},
	}
	planner := productionResponsibilityAutoplanPlanner{
		primary: responsibilityAutoplanPlannerFunc(func(context.Context, responsibility.AutoplanPlanningRequest) (responsibility.AutoplanProposal, error) {
			return responsibility.AutoplanProposal{}, responsibility.ErrModelAutoplanOutput
		}),
		fallback: responsibilityAutoplanPlannerFunc(func(ctx context.Context, request responsibility.AutoplanPlanningRequest) (responsibility.AutoplanProposal, error) {
			fallbackCalls++
			return (responsibility.BaselineAutoplanPlanner{}).PlanAutoplan(ctx, request)
		}),
	}

	proposal, err := planner.PlanAutoplan(context.Background(), request)
	if err != nil {
		t.Fatalf("PlanAutoplan() error = %v", err)
	}
	if err := proposal.Validate(); err != nil {
		t.Fatalf("fallback proposal validation error = %v", err)
	}
	if fallbackCalls != 1 {
		t.Fatalf("fallback calls = %d, want 1", fallbackCalls)
	}
	if !strings.Contains(proposal.Rationale, "deterministic read-only baseline") {
		t.Fatalf("proposal rationale = %q, want fallback audit reason", proposal.Rationale)
	}
	if !containsString(proposal.RiskSummary, responsibilityAutoplanModelOutputFallback) {
		t.Fatalf("proposal risk summary = %v, want %q", proposal.RiskSummary, responsibilityAutoplanModelOutputFallback)
	}
}

func TestProductionResponsibilityAutoplanPlannerDoesNotMaskModelFailure(t *testing.T) {
	modelErr := errors.New("model provider unavailable")
	fallbackCalls := 0
	planner := productionResponsibilityAutoplanPlanner{
		primary: responsibilityAutoplanPlannerFunc(func(context.Context, responsibility.AutoplanPlanningRequest) (responsibility.AutoplanProposal, error) {
			return responsibility.AutoplanProposal{}, modelErr
		}),
		fallback: responsibilityAutoplanPlannerFunc(func(context.Context, responsibility.AutoplanPlanningRequest) (responsibility.AutoplanProposal, error) {
			fallbackCalls++
			return responsibility.AutoplanProposal{}, nil
		}),
	}

	_, err := planner.PlanAutoplan(context.Background(), responsibility.AutoplanPlanningRequest{})
	if !errors.Is(err, modelErr) {
		t.Fatalf("PlanAutoplan() error = %v, want model failure", err)
	}
	if fallbackCalls != 0 {
		t.Fatalf("fallback calls = %d, want 0", fallbackCalls)
	}
}

type responsibilityAutoplanPlannerFunc func(context.Context, responsibility.AutoplanPlanningRequest) (responsibility.AutoplanProposal, error)

func (f responsibilityAutoplanPlannerFunc) PlanAutoplan(ctx context.Context, request responsibility.AutoplanPlanningRequest) (responsibility.AutoplanProposal, error) {
	return f(ctx, request)
}
