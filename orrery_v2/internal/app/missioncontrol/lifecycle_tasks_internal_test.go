package missioncontrol

import (
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	missioncore "orrery_v2/internal/mission/core"
	projection "orrery_v2/internal/mission/projection"
	missiontask "orrery_v2/internal/mission/task"
)

func TestTaskContractGeneratesRequiredArtifactEvidenceForAcceptanceCriteria(t *testing.T) {
	contract := taskContractFromCommand(CreateTaskCommand{
		MissionID: "mission-1", TaskID: "task-1", Goal: "Produce report", WorkerPersonalGoal: "Produce report",
		AcceptanceCriteria: []string{"Report includes findings.", "Report includes risks."},
	}, taskAssigneeBinding{ProfileID: "profile-worker", StableAgentID: "profile-worker"}, taskQAPolicy{})

	if len(contract.EvidenceRequirements) != 2 {
		t.Fatalf("evidence requirements = %+v", contract.EvidenceRequirements)
	}
	for index, requirement := range contract.EvidenceRequirements {
		if requirement.ID == "" || !requirement.Required || string(requirement.Kind) != string(agentcore.EvidenceKindArtifact) || requirement.Description != contract.AcceptanceCriteria[index].Description {
			t.Fatalf("evidence requirement[%d] = %+v", index, requirement)
		}
	}
}

func TestTaskContractFromAutoplanCommandRequiresExplicitToolScopes(t *testing.T) {
	contract := taskContractFromCommand(CreateTaskCommand{
		MissionID: "mission-1", TaskID: "task-1", Goal: "Assess source", WorkerPersonalGoal: "Assess source",
		SourceKind: missiontask.SourceKindResponsibilityAutoplan,
	}, taskAssigneeBinding{ProfileID: "profile-worker", StableAgentID: "profile-worker"}, taskQAPolicy{})

	if !contract.RequireExplicitToolScopes || !contract.RequiresExplicitToolScopes() {
		t.Fatalf("autoplan contract did not retain the explicit tool scope boundary: %+v", contract)
	}
}

func TestIsQAAgentEntryIgnoresRuntimeInstanceIdentifier(t *testing.T) {
	entry := agentcore.AgentDirectoryEntry{
		AgentProfileID:  "profile-worker",
		AgentInstanceID: "qa-plan-agent",
		AgentKind:       agentcore.AgentKindAI,
		Enabled:         true,
		QAEnabled:       true,
	}

	if isQAAgentEntry(entry) {
		t.Fatalf("runtime instance id should not classify a stable worker profile as a QA agent")
	}
}

func TestCanonicalTaskAssigneeIDsExcludesKnownHumanTeamMembers(t *testing.T) {
	tasks := []projection.TaskCardView{
		{TaskID: missioncore.TaskID("task-human-agent"), Assignee: agentcore.AgentID("human-agent")},
		{TaskID: missioncore.TaskID("task-human-profile"), Assignee: agentcore.AgentID("human-profile")},
		{TaskID: missioncore.TaskID("task-human-instance"), Assignee: agentcore.AgentID("human-instance")},
		{TaskID: missioncore.TaskID("task-ai"), Assignee: agentcore.AgentID("agent-1")},
		{TaskID: missioncore.TaskID("task-legacy"), Assignee: agentcore.AgentID("legacy-unknown")},
		{TaskID: missioncore.TaskID("task-ai-duplicate"), Assignee: agentcore.AgentID("agent-1")},
	}
	members := []projection.TeamMemberView{
		{
			AgentID:         agentcore.AgentID("human-agent"),
			AgentProfileID:  agentcore.AgentProfileID("human-profile"),
			AgentInstanceID: agentcore.AgentInstanceID("human-instance"),
			AgentKind:       agentcore.AgentKindHuman,
		},
		{
			AgentID:        agentcore.AgentID("agent-1"),
			AgentProfileID: agentcore.AgentProfileID("agent-1"),
			AgentKind:      agentcore.AgentKindAI,
		},
	}

	got := canonicalTaskAssigneeIDs(tasks, members)
	if len(got) != 2 || got[0] != "agent-1" || got[1] != "legacy-unknown" {
		t.Fatalf("canonical task agent assignees = %v", got)
	}
}
