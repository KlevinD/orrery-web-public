package responsibility

import (
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

func TestResponsibilityExecutionEnvelopeRekeysAndBoundsTaskSnapshot(t *testing.T) {
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
	context := agentcore.ResponsibilityContext{
		ContextID:             "context-a",
		ResponsibilityID:      "responsibility-a",
		ResponsibilityVersion: 1,
		PolicyID:              "policy-a",
		PolicyVersion:         1,
		AgentProfileID:        "agent-a",
		MissionID:             "mission-a",
		TaskID:                "task-a",
		OrganizationID:        "org-a",
		AllowedTools:          []string{"tool.read"},
		AllowedDataScopes:     []string{"people.read"},
		TaskPermissionScopes:  []string{"mission.read"},
		ExpiresAt:             at.Add(time.Hour),
	}
	got, err := newResponsibilityExecutionEnvelope(
		"tenant-a", "mission-a", "task-a", "agent-a", "runtime-run-a",
		"scheduler_agent_task", "scheduled-task-a", "scheduled-run-a",
		[]string{"mission.read"}, context, parent, at.Add(time.Minute))
	if err != nil {
		t.Fatalf("derive responsibility envelope: %v", err)
	}
	if got.RunID != "runtime-run-a" || got.EnvelopeID == parent.EnvelopeID || got.PrincipalID != "human-a" || got.PrincipalKind != agentcore.ExecutionPrincipalUser {
		t.Fatalf("envelope was not rekeyed/preserved: %+v", got)
	}
	if got.OrganizationID != "org-a" || got.PeopleTenantID != "people-a" || got.PortfolioID != "portfolio-a" || got.PortfolioVersionID != "version-a" {
		t.Fatalf("organization or portfolio bounds were lost: %+v", got)
	}
	if len(got.AllowedToolIDs) != 1 || got.AllowedToolIDs[0] != "tool.read" || len(got.DataScopes) != 1 || got.DataScopes[0] != "people.read" {
		t.Fatalf("authority selectors were not narrowed: %+v", got)
	}
	if err := got.Verify(); err != nil {
		t.Fatalf("verify derived envelope: %v", err)
	}

	if _, err := newResponsibilityExecutionEnvelope(
		"tenant-a", "mission-a", "task-a", "agent-a", "runtime-run-b",
		"scheduler_agent_task", "scheduled-task-a", "scheduled-run-a",
		[]string{"admin.write"}, context, parent, at.Add(time.Minute)); err == nil {
		t.Fatal("broader responsibility scope was accepted")
	}
}
