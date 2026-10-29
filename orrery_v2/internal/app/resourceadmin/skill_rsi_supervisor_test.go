package resourceadmin

import (
	"context"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	skillres "orrery_v2/internal/resource/skill"
	toolres "orrery_v2/internal/resource/tool"
)

func TestRunSkillRSICycleEvaluatesWithoutPromotionByDefault(t *testing.T) {
	ctx := context.Background()
	registry := newRSITestSkillRegistry(t)
	service := NewCommandService(toolres.NewMemoryRegistry(), registry)
	service.SetSkillVersionEvaluationStore(NewMemorySkillVersionEvaluationStore())
	viewer := ViewerRef{ViewerID: "rsi", PermissionScope: []string{PermissionResourceManage}}
	generator := testSkillVersionGenerator{candidate: skillres.SkillDefinition{ID: "skill.review", Name: "Review", Version: "2.0.0", Enabled: true}}
	evaluator := testSkillVersionEvaluator{evaluate: func(_ context.Context, _ skillres.SkillDefinition, candidate skillres.SkillDefinition) SkillVersionEvaluation {
		active, _ := registry.Get(ctx, "skill.review")
		return passingSkillVersionEvaluation("eval-supervisor", active, candidate)
	}}

	result, err := service.RunSkillRSICycle(ctx, viewer, "skill.review", generator, evaluator, SkillRSICycleRequest{})
	if err != nil {
		t.Fatalf("RunSkillRSICycle: %v", err)
	}
	if result.Status != SkillRSICycleStatusAwaitingPromotion || result.Promotion != nil || result.Evaluation.Status != SkillVersionEvaluationStatusPassed {
		t.Fatalf("cycle result = %+v", result)
	}
	active, err := registry.Get(ctx, "skill.review")
	if err != nil || active.Version != "1.0.0" {
		t.Fatalf("active after evaluate-only cycle = %+v, err=%v", active, err)
	}
}

func TestRunSkillRSICyclePromotesOnlyWhenRequested(t *testing.T) {
	ctx := context.Background()
	registry := newRSITestSkillRegistry(t)
	service := NewCommandService(toolres.NewMemoryRegistry(), registry)
	service.SetSkillVersionEvaluationStore(NewMemorySkillVersionEvaluationStore())
	viewer := ViewerRef{ViewerID: "rsi", PermissionScope: []string{PermissionResourceManage}}
	candidate := skillres.SkillDefinition{ID: "skill.review", Name: "Review", Version: "2.0.0", Description: "Improved", Enabled: true}
	result, err := service.RunSkillRSICycle(ctx, viewer, "skill.review", testSkillVersionGenerator{candidate: candidate}, testSkillVersionEvaluator{evaluate: func(_ context.Context, baseline, candidate skillres.SkillDefinition) SkillVersionEvaluation {
		return passingSkillVersionEvaluation("eval-promote", baseline, candidate)
	}}, SkillRSICycleRequest{PromoteOnPass: true, Reason: "supervisor approved"})
	if err != nil {
		t.Fatalf("RunSkillRSICycle: %v", err)
	}
	if result.Status != SkillRSICycleStatusPromoted || result.Promotion == nil || result.Promotion.ActiveVersion != "2.0.0" {
		t.Fatalf("cycle result = %+v", result)
	}
	active, err := registry.Get(ctx, "skill.review")
	if err != nil || active.Version != "2.0.0" {
		t.Fatalf("active after promotion cycle = %+v, err=%v", active, err)
	}
}

func TestRunSkillRSICycleRetainsFailedEvaluation(t *testing.T) {
	ctx := context.Background()
	registry := newRSITestSkillRegistry(t)
	service := NewCommandService(toolres.NewMemoryRegistry(), registry)
	evaluations := NewMemorySkillVersionEvaluationStore()
	service.SetSkillVersionEvaluationStore(evaluations)
	viewer := ViewerRef{ViewerID: "rsi", PermissionScope: []string{PermissionResourceManage}}
	candidate := skillres.SkillDefinition{ID: "skill.review", Name: "Review", Version: "2.0.0", Enabled: true}
	result, err := service.RunSkillRSICycle(ctx, viewer, "skill.review", testSkillVersionGenerator{candidate: candidate}, testSkillVersionEvaluator{evaluate: func(_ context.Context, baseline, candidate skillres.SkillDefinition) SkillVersionEvaluation {
		evaluation := passingSkillVersionEvaluation("eval-failed-supervisor", baseline, candidate)
		evaluation.CandidateScore = 0.80
		evaluation.HeldOutPassed = false
		return evaluation
	}}, SkillRSICycleRequest{PromoteOnPass: true})
	if err != nil {
		t.Fatalf("RunSkillRSICycle failed evaluation: %v", err)
	}
	if result.Status != SkillRSICycleStatusEvaluationFailed || result.Evaluation.Status != SkillVersionEvaluationStatusFailed || result.Promotion != nil {
		t.Fatalf("failed cycle result = %+v", result)
	}
	active, err := registry.Get(ctx, "skill.review")
	if err != nil || active.Version != "1.0.0" {
		t.Fatalf("active after failed cycle = %+v, err=%v", active, err)
	}
	stored, err := evaluations.GetSkillVersionEvaluation(ctx, "skill.review", "eval-failed-supervisor")
	if err != nil || stored.Status != SkillVersionEvaluationStatusFailed {
		t.Fatalf("stored failed evaluation = %+v, err=%v", stored, err)
	}
}

type testSkillVersionGenerator struct {
	candidate skillres.SkillDefinition
}

func (g testSkillVersionGenerator) GenerateSkillVersion(_ context.Context, _ agentcore.SkillID, _ skillres.SkillDefinition) (skillres.SkillDefinition, error) {
	return g.candidate.Clone(), nil
}

type testSkillVersionEvaluator struct {
	evaluate func(context.Context, skillres.SkillDefinition, skillres.SkillDefinition) SkillVersionEvaluation
}

func (e testSkillVersionEvaluator) EvaluateSkillVersion(ctx context.Context, _ agentcore.SkillID, baseline, candidate skillres.SkillDefinition) (SkillVersionEvaluation, error) {
	return e.evaluate(ctx, baseline, candidate), nil
}
