package missioncontrol_test

import (
	"context"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/missioncontrol"
	missioncore "orrery_v2/internal/mission/core"
	missiontask "orrery_v2/internal/mission/task"
	"orrery_v2/internal/platform/testkit"
)

func TestLifecycleServiceImportsHistoricalSchedulerAssignmentForRetiredAgent(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	tasks := missiontask.NewPlatformRepository()
	responsibilities := &capturingTaskResponsibilityAssignments{tasks: tasks}
	service := newAuthorizedLifecycleService(env.UnitOfWork, env.DBRouter, repo, missioncontrol.LifecycleOptions{
		Clock: env.Clock, IDGenerator: env.IDGenerator, TaskRepo: tasks,
		AgentDirectory: &fakeLifecycleAgentDirectory{entries: []agentcore.AgentDirectoryEntry{{
			AgentProfileID: "current-agent", AgentInstanceID: "current-agent", AgentKind: agentcore.AgentKindAI, Enabled: true,
		}}}, TaskResponsibility: responsibilities,
	})
	viewer := missioncontrol.ViewerRef{ViewerID: "scheduler-agent-task", ViewerKind: missioncontrol.ViewerKindSystem, TenantID: "tenant-history", AuthorizationSubjectID: "scheduler-agent-task"}
	mission, err := service.CreateMission(ctx, viewer, missioncontrol.CreateMissionCommand{
		MissionID: "mission-retired-history", IdempotencyKey: "scheduled-retired-history", Title: "Retired scheduler history", FinalGoal: "Materialize history",
		OriginKind: "scheduler", OriginID: "scheduled-retired-history", InitialAgentProfileID: "current-agent",
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.CreateTask(ctx, viewer, missioncontrol.CreateTaskCommand{
		IdempotencyKey: "retired-history-task", MissionID: missioncore.MissionID(mission.MissionID), TaskID: "task-retired-history",
		Goal: "Import one retired historical occurrence", WorkerPersonalGoal: "Import one retired historical occurrence",
		SourceKind: "scheduler_agent_task", SourceID: "scheduled-retired-history", SourceStepID: "run-retired-history",
	})
	if err != nil {
		t.Fatal(err)
	}
	command := missioncontrol.ImportHistoricalTaskAssignmentCommand{
		IdempotencyKey: "retired-history-task:assign", MissionID: missioncore.MissionID(mission.MissionID), TaskID: missioncore.TaskID(created.TaskID),
		ScheduledTaskID: "scheduled-retired-history", ScheduledRunID: "run-retired-history", AssigneeID: "retired-agent",
		ResponsibilityState: "terminated", Reason: "stored failed run", OccurredAt: env.Clock.Now(),
	}
	if _, err := service.ImportHistoricalTaskAssignment(ctx, viewer, command); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ImportHistoricalTaskAssignment(ctx, viewer, command); err != nil {
		t.Fatalf("replay retired historical assignment: %v", err)
	}
	if len(responsibilities.signals) != 0 || len(responsibilities.historicalSignals) != 1 || responsibilities.historicalSignals[0].AgentProfileID != "retired-agent" {
		t.Fatalf("normal signals=%+v historical signals=%+v", responsibilities.signals, responsibilities.historicalSignals)
	}
	view, err := newAuthorizedQueryService(repo).GetMissionControlView(ctx, viewer, missioncore.MissionID(mission.MissionID))
	if err != nil {
		t.Fatal(err)
	}
	if len(view.TaskBoard.Tasks) != 1 || view.TaskBoard.Tasks[0].Status != "assigned" || view.TaskBoard.Tasks[0].Assignee != "retired-agent" {
		t.Fatalf("retired historical Task projection = %+v", view.TaskBoard.Tasks)
	}
	for _, member := range view.Team.Members {
		if member.AgentProfileID == "retired-agent" || member.AgentID == "retired-agent" {
			t.Fatalf("retired historical agent was reactivated as a Mission member: %+v", view.Team)
		}
	}
	for _, assignment := range view.Team.Assignments {
		if assignment.AgentProfileID == "retired-agent" || assignment.AgentID == "retired-agent" {
			t.Fatalf("retired historical agent was reactivated by a Mission assignment: %+v", view.Team)
		}
	}
}
