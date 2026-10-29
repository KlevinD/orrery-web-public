package main

import (
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	missioncore "orrery_v2/internal/mission/core"
	missiontask "orrery_v2/internal/mission/task"
)

func TestQAChildExecutionContractCarriesParentBoundsAndRebindsIdentity(t *testing.T) {
	issuedAt := time.Date(2026, 9, 21, 1, 2, 3, 0, time.UTC)
	parentEnvelope := &agentcore.ExecutionEnvelope{
		TenantID: "tenant-a", RunID: "parent-run", MissionID: "mission-a", TaskID: "parent-task",
		AgentID: "worker-a", AgentProfileID: "worker-a", PrincipalID: "worker-a", PrincipalKind: agentcore.ExecutionPrincipalAgent,
		OrganizationID: "org-a", OrganizationName: "Operations", PeopleTenantID: "people-a",
		PortfolioID: "portfolio-a", PortfolioBindingID: "binding-a", PortfolioVersionID: "version-a",
		PermissionScopes: []string{"mission.read", "mission.write"}, DataScopes: []string{"people.read"},
		AllowedToolIDs: []string{"tool.read", "tool.inspect"}, ForbiddenActions: []string{"tool.delete"},
		IssuedAt: issuedAt, ExpiresAt: issuedAt.Add(2 * time.Hour),
	}
	if err := parentEnvelope.SealAt(issuedAt); err != nil {
		t.Fatalf("seal parent envelope: %v", err)
	}
	parent := missiontask.TaskRecord{Contract: missiontask.TaskContract{
		ID: missiontask.TaskID("parent-task"), MissionID: missioncore.MissionID("mission-a"),
		ExecutionPermissionScopes: []string{"mission.read"}, ExecutionAllowedToolIDs: []string{"tool.read"},
		ExecutionEnvelope: parentEnvelope,
	}}

	scopes, tools, child, err := qaChildExecutionContract(parent, "tenant-a", "mission-a", "qa-task", "qa-agent", "qa_plan")
	if err != nil {
		t.Fatalf("qaChildExecutionContract: %v", err)
	}
	if len(scopes) != 1 || scopes[0] != "mission.read" || len(tools) != 1 || tools[0] != "tool.read" {
		t.Fatalf("contract scopes/tools = %#v/%#v, want parent task bounds", scopes, tools)
	}
	if child == nil {
		t.Fatal("expected child execution envelope")
	}
	if err := child.Verify(); err != nil {
		t.Fatalf("verify child envelope: %v", err)
	}
	if child.RunID != "qa:qa_plan:qa-task" || child.MissionID != "mission-a" || child.TaskID != "qa-task" ||
		child.AgentID != "qa-agent" || child.AgentProfileID != "qa-agent" || child.PrincipalID != "qa-agent" ||
		child.PrincipalKind != agentcore.ExecutionPrincipalAgent {
		t.Fatalf("child identity = %#v", child)
	}
	if child.OrganizationID != "org-a" || child.PeopleTenantID != "people-a" || child.PortfolioID != "portfolio-a" ||
		child.PortfolioBindingID != "binding-a" || child.PortfolioVersionID != "version-a" {
		t.Fatalf("child organization/portfolio bounds were not carried: %#v", child)
	}
	if child.ResponsibilityID != "" || child.PolicyID != "" || child.ResponsibilityContext != nil {
		t.Fatalf("child retained parent responsibility context: %#v", child)
	}
	if len(child.PermissionScopes) != 1 || child.PermissionScopes[0] != "mission.read" || len(child.AllowedToolIDs) != 1 || child.AllowedToolIDs[0] != "tool.read" {
		t.Fatalf("child permission/tool bounds = %#v/%#v", child.PermissionScopes, child.AllowedToolIDs)
	}
	if len(child.DataScopes) != 1 || child.DataScopes[0] != "people.read" || len(child.ForbiddenActions) != 1 || child.ForbiddenActions[0] != "tool.delete" {
		t.Fatalf("child data/forbidden bounds = %#v/%#v", child.DataScopes, child.ForbiddenActions)
	}
	if parent.Contract.ExecutionEnvelope.RunID != "parent-run" || parent.Contract.ExecutionEnvelope.AgentID != "worker-a" {
		t.Fatal("parent envelope was mutated")
	}
}

func TestQAChildExecutionContractRejectsTamperedParent(t *testing.T) {
	parent := missiontask.TaskRecord{Contract: missiontask.TaskContract{
		ID: missiontask.TaskID("parent-task"), MissionID: missioncore.MissionID("mission-a"),
		ExecutionEnvelope: &agentcore.ExecutionEnvelope{TenantID: "tenant-a", RunID: "parent-run", MissionID: "mission-a", TaskID: "parent-task", AgentID: "worker-a", AgentProfileID: "worker-a", IssuedAt: time.Unix(1, 0).UTC(), EnvelopeID: "execution-envelope:parent-run", EnvelopeDigest: "tampered"},
	}}
	if _, _, _, err := qaChildExecutionContract(parent, "tenant-a", "mission-a", "qa-task", "qa-agent", "qa_check"); err == nil {
		t.Fatal("expected tampered parent envelope to be rejected")
	}
}

func TestQAChildExecutionContractRejectsBroaderTaskScopes(t *testing.T) {
	issuedAt := time.Date(2026, 9, 21, 1, 2, 3, 0, time.UTC)
	parentEnvelope := &agentcore.ExecutionEnvelope{
		TenantID: "tenant-a", RunID: "parent-run", MissionID: "mission-a", TaskID: "parent-task",
		AgentID: "worker-a", AgentProfileID: "worker-a", PermissionScopes: []string{"mission.read"},
		AllowedToolIDs: []string{"tool.read"}, IssuedAt: issuedAt,
	}
	if err := parentEnvelope.SealAt(issuedAt); err != nil {
		t.Fatalf("seal parent envelope: %v", err)
	}
	parent := missiontask.TaskRecord{Contract: missiontask.TaskContract{
		ID: missiontask.TaskID("parent-task"), MissionID: missioncore.MissionID("mission-a"),
		ExecutionPermissionScopes: []string{"mission.write"}, ExecutionAllowedToolIDs: []string{"tool.delete"},
		ExecutionEnvelope: parentEnvelope,
	}}
	if _, _, _, err := qaChildExecutionContract(parent, "tenant-a", "mission-a", "qa-task", "qa-agent", "qa_check"); err == nil {
		t.Fatal("expected broader QA task scopes to be rejected")
	}
}
