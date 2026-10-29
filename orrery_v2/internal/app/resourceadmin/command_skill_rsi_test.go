package resourceadmin

import (
	"context"
	"errors"
	"testing"

	skillres "orrery_v2/internal/resource/skill"
	toolres "orrery_v2/internal/resource/tool"
)

func TestCommandServiceRegistersSkillCandidateWithoutChangingActive(t *testing.T) {
	ctx := context.Background()
	registry := newRSITestSkillRegistry(t)
	service := NewCommandService(toolres.NewMemoryRegistry(), registry)
	viewer := ViewerRef{ViewerID: "rsi", PermissionScope: []string{PermissionResourceManage}}
	candidate := skillres.SkillDefinition{ID: "skill.review", Name: "Review", Version: "2.0.0", Description: "Improved review", Enabled: true}

	dto, err := service.RegisterSkillVersion(ctx, viewer, SkillVersionCandidateRequest{Definition: candidate})
	if err != nil {
		t.Fatalf("RegisterSkillVersion: %v", err)
	}
	if dto.State != skillVersionStateCandidate || dto.Active || dto.ActiveVersion != "1.0.0" {
		t.Fatalf("candidate dto = %+v", dto)
	}
	active, err := registry.Get(ctx, candidate.ID)
	if err != nil {
		t.Fatalf("get active: %v", err)
	}
	if active.Version != "1.0.0" || active.Description != "" {
		t.Fatalf("active changed = %+v", active)
	}
	stored, err := registry.GetVersion(ctx, candidate.ID, candidate.Version)
	if err != nil || stored.Description != candidate.Description {
		t.Fatalf("candidate version = %+v, err=%v", stored, err)
	}
}

func TestCommandServicePromotionRequiresStoredPassingEvaluation(t *testing.T) {
	ctx := context.Background()
	registry := newRSITestSkillRegistry(t)
	service := NewCommandService(toolres.NewMemoryRegistry(), registry)
	evaluations := NewMemorySkillVersionEvaluationStore()
	service.SetSkillVersionEvaluationStore(evaluations)
	viewer := ViewerRef{ViewerID: "rsi", PermissionScope: []string{PermissionResourceManage}}
	candidate := skillres.SkillDefinition{ID: "skill.review", Name: "Review", Version: "2.0.0", Enabled: true}
	if err := registry.RegisterVersion(ctx, candidate); err != nil {
		t.Fatalf("register candidate: %v", err)
	}
	active, err := registry.Get(ctx, "skill.review")
	if err != nil {
		t.Fatalf("get active: %v", err)
	}

	_, err = service.PromoteSkillVersion(ctx, viewer, "skill.review", "2.0.0", PromoteSkillVersionRequest{EvaluationRef: "missing"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing stored evaluation error = %v, want ErrNotFound", err)
	}
	regression := passingSkillVersionEvaluation("regression", active, candidate)
	regression.CandidateScore = 0.89
	if err := evaluations.SaveSkillVersionEvaluation(ctx, "skill.review", regression); err != nil {
		t.Fatalf("regression evaluation save error = %v", err)
	}
	if _, err := service.PromoteSkillVersion(ctx, viewer, "skill.review", "2.0.0", PromoteSkillVersionRequest{EvaluationRef: "regression"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("regression promotion error = %v, want ErrInvalidRequest", err)
	}
	if err := evaluations.SaveSkillVersionEvaluation(ctx, "skill.review", SkillVersionEvaluation{EvaluationRef: "stale", CandidateVersion: "2.0.0", BaselineVersion: "0.9.0", CandidateDefinitionHash: skillres.DefinitionHash(candidate), BaselineDefinitionHash: skillres.DefinitionHash(active), DatasetRef: "heldout-v1", PolicyVersion: "policy-v1", EvaluatorRef: "eval-1", BaselineScore: 0.90, CandidateScore: 0.93, HeldOutPassed: true, SafetyPassed: true, EvidenceRefs: []string{"run-1"}}); err != nil {
		t.Fatalf("save stale evaluation: %v", err)
	}
	_, err = service.PromoteSkillVersion(ctx, viewer, "skill.review", "2.0.0", PromoteSkillVersionRequest{EvaluationRef: "stale"})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("stale evaluation error = %v, want ErrInvalidRequest", err)
	}
	active, err = registry.Get(ctx, "skill.review")
	if err != nil {
		t.Fatalf("get active: %v", err)
	}
	if active.Version != "1.0.0" {
		t.Fatalf("failed evaluation changed active version = %s", active.Version)
	}
}

func TestCommandServicePromotesAndRollsBackSkillVersionWithAudit(t *testing.T) {
	ctx := context.Background()
	registry := newRSITestSkillRegistry(t)
	service := NewCommandService(toolres.NewMemoryRegistry(), registry)
	evaluations := NewMemorySkillVersionEvaluationStore()
	service.SetSkillVersionEvaluationStore(evaluations)
	audit := &failingActionAuditRecorder{}
	service.SetAuditRecorder(audit)
	viewer := ViewerRef{ViewerID: "rsi", PermissionScope: []string{PermissionResourceManage}, RequestID: "req-1"}
	candidate := skillres.SkillDefinition{ID: "skill.review", Name: "Review", Version: "2.0.0", Description: "Improved", Enabled: true}
	if err := registry.RegisterVersion(ctx, candidate); err != nil {
		t.Fatalf("register candidate: %v", err)
	}
	active, err := registry.Get(ctx, "skill.review")
	if err != nil {
		t.Fatalf("get active: %v", err)
	}
	if err := evaluations.SaveSkillVersionEvaluation(ctx, "skill.review", passingSkillVersionEvaluation("eval-1", active, candidate)); err != nil {
		t.Fatalf("save evaluation: %v", err)
	}

	dto, err := service.PromoteSkillVersion(ctx, viewer, "skill.review", "2.0.0", PromoteSkillVersionRequest{ExpectedActiveVersion: "1.0.0", EvaluationRef: "eval-1", Reason: "held-out improvement"})
	if err != nil {
		t.Fatalf("PromoteSkillVersion: %v", err)
	}
	if !dto.Active || dto.State != skillVersionStateActive || dto.ActiveVersion != "2.0.0" {
		t.Fatalf("promotion dto = %+v", dto)
	}
	active, err = registry.Get(ctx, "skill.review")
	if err != nil || active.Version != "2.0.0" {
		t.Fatalf("active after promotion = %+v, err=%v", active, err)
	}
	if len(audit.events) != 1 || audit.events[0].Action != "resource.skill.version.promote" || audit.events[0].Metadata["evaluation_ref"] != "eval-1" {
		t.Fatalf("promotion audit = %+v", audit.events)
	}

	rollback, err := service.RollbackSkillVersion(ctx, viewer, "skill.review", "1.0.0", RollbackSkillVersionRequest{ExpectedActiveVersion: "2.0.0", Reason: "regression detected"})
	if err != nil {
		t.Fatalf("RollbackSkillVersion: %v", err)
	}
	if !rollback.Active || rollback.ActiveVersion != "1.0.0" {
		t.Fatalf("rollback dto = %+v", rollback)
	}
	active, err = registry.Get(ctx, "skill.review")
	if err != nil || active.Version != "1.0.0" {
		t.Fatalf("active after rollback = %+v, err=%v", active, err)
	}
	if len(audit.events) != 2 || audit.events[1].Action != "resource.skill.version.rollback" {
		t.Fatalf("rollback audit = %+v", audit.events)
	}
}

func TestCommandServiceRollbackRejectsNeverActiveCandidate(t *testing.T) {
	ctx := context.Background()
	registry := newRSITestSkillRegistry(t)
	service := NewCommandService(toolres.NewMemoryRegistry(), registry)
	viewer := ViewerRef{ViewerID: "rsi", PermissionScope: []string{PermissionResourceManage}}
	candidate := skillres.SkillDefinition{ID: "skill.review", Name: "Review", Version: "2.0.0", Enabled: true}
	if err := registry.RegisterVersion(ctx, candidate); err != nil {
		t.Fatalf("register candidate: %v", err)
	}
	_, err := service.RollbackSkillVersion(ctx, viewer, "skill.review", "2.0.0", RollbackSkillVersionRequest{ExpectedActiveVersion: "1.0.0"})
	if !errors.Is(err, ErrInvalidRequest) && !errors.Is(err, skillres.ErrVersionMismatch) {
		t.Fatalf("candidate rollback error = %v", err)
	}
	active, err := registry.Get(ctx, "skill.review")
	if err != nil || active.Version != "1.0.0" {
		t.Fatalf("candidate rollback changed active = %+v, err=%v", active, err)
	}
}

func TestCommandServicePromotionRejectsStaleActiveVersion(t *testing.T) {
	ctx := context.Background()
	registry := newRSITestSkillRegistry(t)
	service := NewCommandService(toolres.NewMemoryRegistry(), registry)
	evaluations := NewMemorySkillVersionEvaluationStore()
	service.SetSkillVersionEvaluationStore(evaluations)
	viewer := ViewerRef{ViewerID: "rsi", PermissionScope: []string{PermissionResourceManage}}
	candidate := skillres.SkillDefinition{ID: "skill.review", Name: "Review", Version: "2.0.0", Enabled: true}
	if err := registry.RegisterVersion(ctx, candidate); err != nil {
		t.Fatalf("register candidate: %v", err)
	}
	active, err := registry.Get(ctx, "skill.review")
	if err != nil {
		t.Fatalf("get active: %v", err)
	}
	if err := evaluations.SaveSkillVersionEvaluation(ctx, "skill.review", passingSkillVersionEvaluation("eval-1", active, candidate)); err != nil {
		t.Fatalf("save evaluation: %v", err)
	}
	_, err = service.PromoteSkillVersion(ctx, viewer, "skill.review", "2.0.0", PromoteSkillVersionRequest{ExpectedActiveVersion: "0.9.0", EvaluationRef: "eval-1"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("stale active error = %v, want ErrConflict", err)
	}
}

func passingSkillVersionEvaluation(ref string, active, candidate skillres.SkillDefinition) SkillVersionEvaluation {
	return SkillVersionEvaluation{EvaluationRef: ref, CandidateVersion: candidate.Version, BaselineVersion: active.Version, CandidateDefinitionHash: skillres.DefinitionHash(candidate), BaselineDefinitionHash: skillres.DefinitionHash(active), DatasetRef: "heldout-v1", PolicyVersion: "policy-v1", EvaluatorRef: "eval-1", BaselineScore: 0.90, CandidateScore: 0.93, HeldOutPassed: true, SafetyPassed: true, EvidenceRefs: []string{"run-1"}}
}

func newRSITestSkillRegistry(t *testing.T) *skillres.MemoryRegistry {
	t.Helper()
	registry := skillres.NewMemoryRegistry()
	if err := registry.Register(context.Background(), skillres.SkillDefinition{ID: "skill.review", Name: "Review", Version: "1.0.0", Enabled: true}); err != nil {
		t.Fatalf("register active: %v", err)
	}
	return registry
}
