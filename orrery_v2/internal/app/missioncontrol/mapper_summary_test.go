package missioncontrol_test

import (
	"testing"
	"time"

	"orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/mission/projection"
)

func TestMapMissionSummaryCountsRuntimeSessionsInsteadOfMissionOrTaskLifecycle(t *testing.T) {
	model := projection.ReadModel{
		Mission: projection.MissionView{MissionID: "mission-1", Status: projection.MissionStatusActive},
		TaskBoard: projection.TaskBoardView{Tasks: []projection.TaskCardView{
			{TaskID: "task-assigned", Status: projection.TaskStatusAssigned},
			{TaskID: "task-blocked", Status: projection.TaskStatusBlocked},
		}},
		RuntimeSessions: []projection.RuntimeSessionView{
			{RunID: "run-queued", Status: projection.RuntimeSessionQueued},
			{RunID: "run-running", Status: projection.RuntimeSessionRunning},
			{RunID: "run-completed", Status: projection.RuntimeSessionCompleted},
			{RunID: "run-failed", Status: projection.RuntimeSessionFailed},
		},
	}

	summary := missioncontrol.MapMissionSummaryDTO(model, missioncontrol.ViewerRef{ViewerID: "system", ViewerKind: missioncontrol.ViewerKindSystem})

	if summary.ActiveTaskCount != 1 {
		t.Fatalf("active task count = %d, want 1", summary.ActiveTaskCount)
	}
	if summary.ActiveRunCount != 2 {
		t.Fatalf("active run count = %d, want 2", summary.ActiveRunCount)
	}
}

func TestMapMissionSummaryDoesNotTreatActiveMissionAsRunning(t *testing.T) {
	model := projection.ReadModel{
		Mission: projection.MissionView{MissionID: "mission-1", Status: projection.MissionStatusActive},
		RuntimeSessions: []projection.RuntimeSessionView{
			{RunID: "run-completed", Status: projection.RuntimeSessionCompleted},
			{RunID: "run-failed", Status: projection.RuntimeSessionFailed},
		},
	}

	summary := missioncontrol.MapMissionSummaryDTO(model, missioncontrol.ViewerRef{ViewerID: "system", ViewerKind: missioncontrol.ViewerKindSystem})

	if summary.ActiveRunCount != 0 {
		t.Fatalf("active run count = %d, want 0", summary.ActiveRunCount)
	}
}

func TestMapMissionSummaryIgnoresSupersededQueuedSessions(t *testing.T) {
	at := time.Date(2026, 9, 3, 3, 30, 0, 0, time.UTC)
	model := projection.ReadModel{
		Mission: projection.MissionView{MissionID: "mission-1", Status: projection.MissionStatusActive},
		Team: projection.TeamView{Members: []projection.TeamMemberView{
			{AgentID: "agent-1", DisplayName: "Agent One", Status: projection.TeamMemberJoined},
			{AgentID: "agent-2", DisplayName: "Agent Two", Status: projection.TeamMemberJoined},
		}},
		RuntimeSessions: []projection.RuntimeSessionView{
			{
				RunID: "responsibility-run-old", AgentID: "agent-1", TaskID: "task-1",
				Status: projection.RuntimeSessionQueued, QueuedAt: at, UpdatedAt: at,
			},
			{
				RunID: "run-terminal", AgentID: "agent-2", TaskID: "task-1",
				Status: projection.RuntimeSessionFailed, StartedAt: at.Add(time.Minute), UpdatedAt: at.Add(2 * time.Minute),
			},
		},
	}
	viewer := missioncontrol.ViewerRef{ViewerID: "system", ViewerKind: missioncontrol.ViewerKindSystem}

	summary := missioncontrol.MapMissionSummaryDTO(model, viewer)
	if summary.ActiveRunCount != 0 {
		t.Fatalf("active run count = %d, want 0 for superseded queued session", summary.ActiveRunCount)
	}

	ops := missioncontrol.MapOpsViewDTO(model, viewer)
	if len(ops.AgentLanes) != 2 || ops.AgentLanes[0].Status == "running" {
		t.Fatalf("agent lanes = %+v, superseded queued session must not mark lane running", ops.AgentLanes)
	}
}

func TestMapMissionSummaryKeepsNewerRunActiveWhenOlderRunFinishesLate(t *testing.T) {
	at := time.Date(2026, 9, 3, 3, 30, 0, 0, time.UTC)
	model := projection.ReadModel{
		Mission: projection.MissionView{MissionID: "mission-1", Status: projection.MissionStatusActive},
		RuntimeSessions: []projection.RuntimeSessionView{
			{
				RunID: "run-current", AgentID: "agent-2", TaskID: "task-1",
				Status: projection.RuntimeSessionQueued, QueuedAt: at.Add(time.Hour), UpdatedAt: at.Add(time.Hour),
			},
			{
				RunID: "run-old", AgentID: "agent-1", TaskID: "task-1",
				Status: projection.RuntimeSessionFailed, QueuedAt: at, StartedAt: at.Add(time.Minute), UpdatedAt: at.Add(2 * time.Hour),
			},
		},
	}

	summary := missioncontrol.MapMissionSummaryDTO(model, missioncontrol.ViewerRef{ViewerID: "system", ViewerKind: missioncontrol.ViewerKindSystem})
	if summary.ActiveRunCount != 1 {
		t.Fatalf("active run count = %d, want newer queued run to remain active", summary.ActiveRunCount)
	}
}
