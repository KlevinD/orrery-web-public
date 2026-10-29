package main

import (
	"context"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/app/resourceadmin"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/testkit"
	skillres "orrery_v2/internal/resource/skill"
)

func TestProductionSkillAuditWiringDeliversThroughPlatformSweeper(t *testing.T) {
	ctx := context.Background()
	rt := testkit.NewTestRuntime(t)
	core, err := rt.Databases.Store(db.RoleCore)
	if err != nil {
		t.Fatal(err)
	}
	registry := skillres.NewPlatformRegistry(core)
	base := skillres.SkillDefinition{ID: "skill.wired-audit", Name: "Wired audit", Version: "1", Enabled: true}
	candidate := base.Clone()
	candidate.Version = "2"
	if err := registry.Register(ctx, base); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterVersion(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	audit := operations.NewAuditStore(rt.DBRouter, rt.Clock, rt.IDGenerator)
	command := resourceadmin.NewCommandService(nil, registry)
	if err := configureProductionResourceCommand(rt, command, audit, agentcore.NewPlatformProfileRepository(rt.UnitOfWork), nil); err != nil {
		t.Fatal(err)
	}
	evaluations := resourceadmin.NewPlatformSkillVersionEvaluationStore(core)
	command.SetSkillVersionEvaluationStore(evaluations)
	if err := evaluations.SaveSkillVersionEvaluation(ctx, base.ID, resourceadmin.SkillVersionEvaluation{EvaluationRef: "eval-wiring", CandidateVersion: "2", BaselineVersion: "1", CandidateDefinitionHash: skillres.DefinitionHash(candidate), BaselineDefinitionHash: skillres.DefinitionHash(base), DatasetRef: "test-dataset", PolicyVersion: "test-policy", EvaluatorRef: "test-evaluator", BaselineScore: .8, CandidateScore: .9, HeldOutPassed: true, SafetyPassed: true, EvidenceRefs: []string{"test-run"}}); err != nil {
		t.Fatal(err)
	}
	viewer := resourceadmin.ViewerRef{ViewerID: "rsi-wiring-test", PermissionScope: []string{resourceadmin.PermissionResourceManage}}
	if _, err := command.PromoteSkillVersion(ctx, viewer, string(base.ID), "2", resourceadmin.PromoteSkillVersionRequest{EvaluationRef: "eval-wiring"}); err != nil {
		t.Fatal(err)
	}
	if _, err := command.RollbackSkillVersion(ctx, viewer, string(base.ID), "1", resourceadmin.RollbackSkillVersionRequest{ExpectedActiveVersion: "2", Reason: "test rollback"}); err != nil {
		t.Fatal(err)
	}
	report := rt.Sweeper.SweepOnce(ctx)
	found := false
	for _, result := range report.Results {
		if result.Name == "resource.skill_version_audits" {
			found = true
			if result.Error != nil {
				t.Fatalf("registered audit relay failed: %v", result.Error)
			}
		}
	}
	if !found {
		t.Fatal("production platform did not run the skill audit relay")
	}
	page, err := audit.ListAuditEvents(ctx, operations.AuditLogQuery{EntityID: string(base.ID)})
	if err != nil || len(page.Events) != 2 {
		t.Fatalf("production skill audit projection = %+v, %v", page, err)
	}
	actions := map[string]bool{}
	for _, event := range page.Events {
		actions[event.Action] = true
	}
	if !actions["resource.skill.version.promote"] || !actions["resource.skill.version.rollback"] {
		t.Fatalf("missing committed transitions: %+v", actions)
	}
}
