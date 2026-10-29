package missioncontrol

import (
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/mission/projection"
)

func TestExecutionEnvelopeWithinBoundsRejectsBroaderIdentityAndAuthority(t *testing.T) {
	at := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	bounds := &agentcore.ExecutionEnvelope{
		TenantID:         "tenant-a",
		RunID:            "run-a",
		ScheduledTaskID:  "scheduled-a",
		ScheduledRunID:   "occurrence-a",
		MissionID:        "mission-a",
		TaskID:           "task-a",
		AgentID:          "agent-a",
		AgentProfileID:   "profile-a",
		PrincipalID:      "agent-a",
		PrincipalKind:    agentcore.ExecutionPrincipalAgent,
		OrganizationID:   "org-a",
		PortfolioID:      "portfolio-a",
		PermissionScopes: []string{"mission.read"},
		AllowedToolIDs:   []string{"mission.read"},
		IssuedAt:         at,
	}
	if err := bounds.SealAt(at); err != nil {
		t.Fatalf("seal bounds: %v", err)
	}
	if !executionEnvelopeWithinBounds(agentcore.CloneExecutionEnvelope(bounds), bounds) {
		t.Fatal("identical envelope was rejected")
	}
	value := agentcore.CloneExecutionEnvelope(bounds)
	value.PermissionScopes = nil
	value.AllowedToolIDs = nil
	value.PortfolioID = "portfolio-b"
	if err := value.SealAt(at); err != nil {
		t.Fatalf("seal value: %v", err)
	}
	if executionEnvelopeWithinBounds(value, bounds) {
		t.Fatal("broader or mismatched envelope was accepted")
	}
}

func TestExecutionEnvelopeWithinBoundsRejectsExtraContext(t *testing.T) {
	at := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	bounds := &agentcore.ExecutionEnvelope{
		TenantID:       "tenant-a",
		RunID:          "run-a",
		MissionID:      "mission-a",
		TaskID:         "task-a",
		AgentID:        "agent-a",
		AgentProfileID: "profile-a",
		IssuedAt:       at,
	}
	if err := bounds.SealAt(at); err != nil {
		t.Fatalf("seal bounds: %v", err)
	}
	value := agentcore.CloneExecutionEnvelope(bounds)
	value.ResponsibilityContext = &agentcore.ResponsibilityContext{
		ContextID:             "ctx-a",
		ResponsibilityID:      "responsibility-a",
		ResponsibilityVersion: 1,
		PolicyID:              "policy-a",
		PolicyVersion:         1,
		AgentProfileID:        "profile-a",
	}
	if err := value.SealAt(at); err != nil {
		t.Fatalf("seal value: %v", err)
	}
	if executionEnvelopeWithinBounds(value, bounds) {
		t.Fatal("extra responsibility context was accepted")
	}
}

func TestRuntimeExecutionEnvelopeRetainsTaskSnapshotBounds(t *testing.T) {
	at := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	parent := &agentcore.ExecutionEnvelope{
		TenantID:           "tenant-a",
		RunID:              "scheduled-run-a",
		MissionID:          "mission-a",
		TaskID:             "task-a",
		AgentID:            "agent-a",
		AgentProfileID:     "agent-a",
		PrincipalID:        "human-a",
		PrincipalKind:      agentcore.ExecutionPrincipalUser,
		OrganizationID:     "org-a",
		PeopleTenantID:     "people-a",
		PortfolioID:        "portfolio-a",
		PortfolioBindingID: "binding-a",
		PortfolioVersionID: "version-a",
		PermissionScopes:   []string{"mission.*"},
		DataScopes:         []string{"people.*"},
		AllowedToolIDs:     []string{"tool.*"},
		IssuedAt:           at,
	}
	if err := parent.SealAt(at); err != nil {
		t.Fatalf("seal parent: %v", err)
	}
	context := &agentcore.ResponsibilityContext{
		ContextID:             "context-a",
		ResponsibilityID:      "responsibility-a",
		ResponsibilityVersion: 1,
		PolicyID:              "policy-a",
		PolicyVersion:         1,
		AgentProfileID:        "agent-a",
		MissionID:             "mission-a",
		TaskID:                "task-a",
		AllowedTools:          []string{"tool.read"},
		AllowedDataScopes:     []string{"people.read"},
		TaskPermissionScopes:  []string{"mission.read"},
		OrganizationID:        "org-a",
		OrganizationName:      "Organization A",
	}
	got := runtimeExecutionEnvelope(
		StartAgentRunCommand{MissionID: "mission-a", TaskID: "task-a", PermissionScope: []string{"mission.read"}},
		"runtime-run-a", "agent-a", "agent-a",
		projection.ReadModel{TenantID: "tenant-a"},
		projection.TaskCardView{
			TaskID:                    "task-a",
			ExecutionPermissionScopes: []string{"mission.read"},
			ExecutionAllowedToolIDs:   []string{"tool.read"},
			ExecutionEnvelope:         parent,
		},
		agentcore.ContextPack{ResponsibilityContext: context}, at.Add(time.Minute))
	if got == nil {
		t.Fatal("runtime envelope was rejected")
	}
	if err := got.Verify(); err != nil {
		t.Fatalf("verify derived envelope: %v", err)
	}
	if got.RunID != "runtime-run-a" || got.PrincipalID != "human-a" || got.PrincipalKind != agentcore.ExecutionPrincipalUser {
		t.Fatalf("derived identity was not rekeyed/preserved: %+v", got)
	}
	if got.OrganizationID != "org-a" || got.PeopleTenantID != "people-a" || got.PortfolioBindingID != "binding-a" || got.PortfolioVersionID != "version-a" {
		t.Fatalf("organization or portfolio bounds were lost: %+v", got)
	}
	if len(got.PermissionScopes) != 1 || got.PermissionScopes[0] != "mission.read" || len(got.AllowedToolIDs) != 1 || got.AllowedToolIDs[0] != "tool.read" || len(got.DataScopes) != 1 || got.DataScopes[0] != "people.read" {
		t.Fatalf("derived selectors were not narrowed: %+v", got)
	}

	parent.OrganizationID = "org-b"
	if err := parent.SealAt(at); err != nil {
		t.Fatalf("reseal mismatched parent: %v", err)
	}
	if got := runtimeExecutionEnvelope(
		StartAgentRunCommand{MissionID: "mission-a", TaskID: "task-a", PermissionScope: []string{"mission.read"}},
		"runtime-run-b", "agent-a", "agent-a", projection.ReadModel{TenantID: "tenant-a"},
		projection.TaskCardView{TaskID: "task-a", ExecutionPermissionScopes: []string{"mission.read"}, ExecutionAllowedToolIDs: []string{"tool.read"}, ExecutionEnvelope: parent},
		agentcore.ContextPack{ResponsibilityContext: context}, at.Add(time.Minute)); got != nil {
		t.Fatal("mismatched task snapshot organization was accepted")
	}
}
