package missioncontrol_test

import (
	"testing"
	"time"

	"orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/mission/projection"
)

func TestMapViewDTODoesNotResolveTaskAssigneeFromRuntimeInstanceIdentity(t *testing.T) {
	at := time.Date(2026, 6, 6, 10, 0, 0, 0, time.UTC)
	model := projection.ReadModel{
		Mission: projection.MissionView{MissionID: "mission-1", Title: "Stage 4", FinalGoal: "Build app DTOs", Status: projection.MissionStatusActive, CreatedAt: at},
		Team: projection.TeamView{Members: []projection.TeamMemberView{{
			AgentID: "profile-worker", AgentProfileID: "profile-worker", AgentInstanceID: "profile-worker:runtime", AgentKind: "ai", DisplayName: "Ari", MissionRole: "worker", Status: projection.TeamMemberJoined, JoinedAt: at,
		}}},
		TaskBoard: projection.TaskBoardView{Tasks: []projection.TaskCardView{{
			TaskID: "task-1", Assignee: "profile-worker:runtime", Goal: "Build DTOs", Status: projection.TaskStatusAssigned, UpdatedAt: at,
		}}},
		Checkpoint: projection.Checkpoint{MissionID: "mission-1", Version: 1, UpdatedAt: at},
		UpdatedAt:  at,
	}

	dto := missioncontrol.MapViewDTO(model, missioncontrol.ViewerRef{ViewerID: "viewer", ViewerKind: missioncontrol.ViewerKindSystem})

	if len(dto.TaskBoard.Tasks) != 1 {
		t.Fatalf("task count = %d, want 1", len(dto.TaskBoard.Tasks))
	}
	task := dto.TaskBoard.Tasks[0]
	if task.AssigneeDisplayName != "" || task.AssigneeType != "" {
		t.Fatalf("task assignee resolved from runtime instance identity: %+v", task)
	}
}
