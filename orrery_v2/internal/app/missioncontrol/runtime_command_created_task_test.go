package missioncontrol_test

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/mission/projection"
)

func TestCommandServiceResponsibilityDispatchUsesCoordinatorForCreatedTask(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 6, 6, 11, 30, 0, 0, time.UTC)
	repo := missioncontrol.NewMemoryProjectionRepository()
	model := appRuntimeReadModel(at)
	model.TaskBoard.Tasks[0].Status = projection.TaskStatusCreated
	if err := repo.SaveForTenant(ctx, "tenant-1", model); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	port := &fakeMissionTaskPort{
		receipt: missioncontrol.CommandReceiptDTO{
			CommandID: "assign-created-task", MissionID: "mission-1", TaskID: "task-1", Status: "accepted", AcceptedAt: at,
		},
	}
	runStore := &capturingRunStore{}
	service := newAuthorizedCommandServiceWithMissionPorts(nil, nil, nil, nil, port)
	service.SetRuntimeCommandPorts(runStore, repo, &capturingMissionEventSink{}, &sequenceContextIDGenerator{values: []string{"evt-created-task"}}, fixedClock{at: at})

	_, err := service.DispatchResponsibilityRun(ctx, missioncontrol.ViewerRef{
		ViewerID: "agent-1", ViewerKind: missioncontrol.ViewerKindAgent, TenantID: "tenant-1",
		AuthorizationSubjectID: "agent-1", PermissionScope: []string{"mission.read"},
	}, missioncontrol.StartAgentRunCommand{
		RequestID: "created-task-dispatch", IdempotencyKey: "created-task-dispatch",
		MissionID: "mission-1", TaskID: "task-1", AgentID: "agent-1", RuntimeKind: "codex", RunID: "run-created-task",
		ResponsibilityContext: testResponsibilityExecutionContext("agent-1"),
	})
	if err != nil {
		t.Fatalf("DispatchResponsibilityRun returned error: %v", err)
	}
	if port.assignViewer.ViewerID != "responsibility-coordinator" || port.assignViewer.ViewerKind != missioncontrol.ViewerKindSystem || port.assignViewer.AuthorizationSubjectID != "responsibility-coordinator" {
		t.Fatalf("assignment viewer = %+v, want responsibility coordinator system viewer", port.assignViewer)
	}
	if port.assignViewer.Tenant() != "tenant-1" || len(port.assignViewer.PermissionScope) != 1 || port.assignViewer.PermissionScope[0] != "mission.write" {
		t.Fatalf("assignment viewer scope/tenant = %+v, want tenant-1 mission.write", port.assignViewer)
	}
	if port.assignTask.AssigneeID != "agent-1" || len(runStore.runs) != 1 || runStore.runs[0].RunID != "run-created-task" {
		t.Fatalf("assignment/run = %+v / %+v", port.assignTask, runStore.runs)
	}
}
