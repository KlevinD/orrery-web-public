package core_test

import (
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	missioncore "orrery_v2/internal/mission/core"
)

func TestLifecycleCanShareStrongTypedRunIdentifiers(t *testing.T) {
	type taskContractProbe struct {
		ID        missioncore.TaskID
		MissionID missioncore.MissionID
	}
	type agentRunStateProbe struct {
		MissionID    missioncore.MissionID
		AgentID      agentcore.AgentID
		TaskID       missioncore.TaskID
		RunID        agentcore.AgentRunID
		TaskContract taskContractProbe
		ContextPack  agentcore.ContextPack
		AgentResult  agentcore.AgentResult
	}

	state := agentRunStateProbe{
		MissionID: missioncore.MissionID("mission-001"),
		AgentID:   agentcore.AgentID("agent-001"),
		TaskID:    missioncore.TaskID("task-001"),
		RunID:     agentcore.AgentRunID("run-001"),
		TaskContract: taskContractProbe{
			ID:        missioncore.TaskID("task-001"),
			MissionID: missioncore.MissionID("mission-001"),
		},
		ContextPack: agentcore.ContextPack{
			MissionID: missioncore.MissionID("mission-001"),
			TaskID:    missioncore.TaskID("task-001"),
			AgentID:   agentcore.AgentID("agent-001"),
		},
		AgentResult: agentcore.AgentResult{
			MissionID: missioncore.MissionID("mission-001"),
			TaskID:    missioncore.TaskID("task-001"),
			AgentID:   agentcore.AgentID("agent-001"),
			RunID:     agentcore.AgentRunID("run-001"),
		},
	}

	if state.ContextPack.MissionID != state.MissionID || state.AgentResult.TaskID != state.TaskID {
		t.Fatalf("lifecycle contract identifiers drifted: %#v", state)
	}
}
