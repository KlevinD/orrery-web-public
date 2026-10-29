package missioncontrol_test

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/mission/projection"
)

func TestCommandServiceStartsPausedSchedulerLaneFromRunningResponsibilityPlan(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	repo := missioncontrol.NewMemoryProjectionRepository()
	model := pausedSchedulerDelegationModel(at)
	if err := repo.Save(ctx, model); err != nil {
		t.Fatal(err)
	}
	runStore := &capturingRunStore{}
	events := &capturingMissionEventSink{}
	service := newAuthorizedCommandService(nil)
	service.SetRuntimeCommandPorts(runStore, repo, events, &sequenceContextIDGenerator{values: []string{"run-lane", "evt-lane"}}, fixedClock{at: at})

	_, err := service.StartAgentRun(ctx, missioncontrol.ViewerRef{ViewerID: "agent-1", ViewerKind: missioncontrol.ViewerKindAgent}, pausedSchedulerLaneCommand())
	if err != nil {
		t.Fatalf("start delegated responsibility lane: %v", err)
	}
	if len(runStore.runs) != 1 || runStore.runs[0].AgentID != "agent-2" {
		t.Fatalf("delegated runs = %+v", runStore.runs)
	}
	metadata := runStore.runs[0].Request.RuntimeOptions.ProviderSpecific
	if metadata["delegation_source"] != "responsibility_plan" || metadata["delegated_by_run_id"] != "responsibility-parent" || metadata["delegated_by_task_id"] != "task-1" || metadata["delegated_by_responsibility_context_id"] != "responsibility-context-test" {
		t.Fatalf("delegation metadata = %+v", metadata)
	}
	if len(events.events) != 1 || events.events[0].Metadata["delegated_by_run_id"] != "responsibility-parent" {
		t.Fatalf("delegation audit event = %+v", events.events)
	}
}

func TestCommandServiceRejectsPausedSchedulerLaneWithoutTrustedResponsibilityParent(t *testing.T) {
	at := time.Date(2026, 8, 28, 12, 5, 0, 0, time.UTC)
	tests := []struct {
		name   string
		mutate func(*projection.ReadModel, *missioncontrol.StartAgentRunCommand)
	}{
		{name: "missing parent run", mutate: func(_ *projection.ReadModel, cmd *missioncontrol.StartAgentRunCommand) { cmd.ParentRunID = "" }},
		{name: "wrong parent run", mutate: func(_ *projection.ReadModel, cmd *missioncontrol.StartAgentRunCommand) { cmd.ParentRunID = "other-run" }},
		{name: "completed parent", mutate: func(model *projection.ReadModel, _ *missioncontrol.StartAgentRunCommand) {
			model.RuntimeSessions[0].Status = projection.RuntimeSessionCompleted
		}},
		{name: "different assignee", mutate: func(_ *projection.ReadModel, cmd *missioncontrol.StartAgentRunCommand) { cmd.AgentID = "agent-1" }},
		{name: "missing mission scope", mutate: func(model *projection.ReadModel, _ *missioncontrol.StartAgentRunCommand) {
			model.RuntimeSessions[0].ResponsibilityContext.TaskPermissionScopes = nil
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			model := pausedSchedulerDelegationModel(at)
			cmd := pausedSchedulerLaneCommand()
			test.mutate(&model, &cmd)
			repo := missioncontrol.NewMemoryProjectionRepository()
			if err := repo.Save(ctx, model); err != nil {
				t.Fatal(err)
			}
			runStore := &capturingRunStore{}
			service := newAuthorizedCommandService(nil)
			service.SetRuntimeCommandPorts(runStore, repo, &capturingMissionEventSink{}, &sequenceContextIDGenerator{}, fixedClock{at: at})
			_, err := service.StartAgentRun(ctx, missioncontrol.ViewerRef{ViewerID: "agent-1", ViewerKind: missioncontrol.ViewerKindAgent}, cmd)
			if err != missioncontrol.ErrInvalidRequest {
				t.Fatalf("error = %v, want ErrInvalidRequest", err)
			}
			if len(runStore.runs) != 0 {
				t.Fatalf("unauthorized lane runs = %+v", runStore.runs)
			}
		})
	}
}

func pausedSchedulerDelegationModel(at time.Time) projection.ReadModel {
	model := appRuntimeReadModel(at)
	model.Mission.Status = projection.MissionStatusPaused
	model.Mission.OriginKind = "scheduler"
	model.Mission.OriginID = "scheduled-task-1"
	model.TaskBoard.Tasks[0].SourceKind = "scheduler_agent_task"
	model.TaskBoard.Tasks[0].SourceID = "scheduled-task-1"
	model.TaskBoard.Tasks = append(model.TaskBoard.Tasks, projection.TaskCardView{
		TaskID: "task-lane", Assignee: "agent-2", Goal: "Audit one improvement", Status: projection.TaskStatusAssigned, UpdatedAt: at,
	})
	model.Team.Members = append(model.Team.Members, projection.TeamMemberView{
		AgentID: "agent-2", AgentProfileID: "agent-2", AgentInstanceID: "agent-2:mission-1", AgentKind: "ai", Status: projection.TeamMemberJoined, JoinedAt: at,
	})
	responsibility := testResponsibilityExecutionContext("agent-1")
	responsibility.MissionID = "mission-1"
	responsibility.TaskID = "task-1"
	responsibility.TaskPermissionScopes = []string{"mission.write"}
	model.RuntimeSessions = []projection.RuntimeSessionView{{
		RunID: "responsibility-parent", AgentID: "agent-1", TaskID: "task-1", RuntimeKind: "pi",
		Status: projection.RuntimeSessionRunning, ResponsibilityContext: responsibility, StartedAt: at.Add(-time.Minute), UpdatedAt: at,
	}}
	return model
}

func pausedSchedulerLaneCommand() missioncontrol.StartAgentRunCommand {
	return missioncontrol.StartAgentRunCommand{
		RequestID: "plan-lane", IdempotencyKey: "plan-lane", MissionID: "mission-1", TaskID: "task-lane",
		AgentID: "agent-2", RuntimeKind: "pi", ParentRunID: "responsibility-parent",
	}
}
