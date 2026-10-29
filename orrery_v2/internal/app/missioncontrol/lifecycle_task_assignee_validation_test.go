package missioncontrol_test

import (
	"context"
	"errors"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/missioncontrol"
	missioncore "orrery_v2/internal/mission/core"
	projection "orrery_v2/internal/mission/projection"
	missiontask "orrery_v2/internal/mission/task"
	"orrery_v2/internal/platform/testkit"
	"orrery_v2/internal/platform/uow"
)

func TestLifecycleServiceRejectsMissionTeamAssigneeWithoutProfileIdentity(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	service := newAuthorizedLifecycleService(env.UnitOfWork, env.DBRouter, repo, missioncontrol.LifecycleOptions{Clock: env.Clock, IDGenerator: env.IDGenerator, TaskRepo: missiontask.NewPlatformRepository()})
	missionID := missioncore.MissionID("mission-1")
	model := projection.ReadModel{
		Mission:    projection.MissionView{MissionID: missionID, Title: "Launch", FinalGoal: "Ship", Status: projection.MissionStatusActive, CreatedAt: env.Clock.Now().UTC()},
		Discussion: projection.DiscussionView{DiscussionGroupID: "discussion-1"},
		Team: projection.TeamView{Members: []projection.TeamMemberView{{
			AgentID: "agent-stable", AgentInstanceID: "agent-stable", AgentKind: agentcore.AgentKindAI,
			Status: projection.TeamMemberJoined, MissionRole: "worker", JoinedAt: env.Clock.Now().UTC(),
		}}},
	}
	if err := repo.Save(ctx, model); err != nil {
		t.Fatal(err)
	}
	_, err := service.CreateTask(ctx, missioncontrol.ViewerRef{ViewerID: "planner-1", ViewerKind: missioncontrol.ViewerKindUser}, missioncontrol.CreateTaskCommand{
		IdempotencyKey: "task-key", MissionID: missionID, AssigneeID: "agent-stable", Goal: "Implement route", WorkerPersonalGoal: "Deliver working route",
	})
	if !errors.Is(err, missioncontrol.ErrInvalidRequest) {
		t.Fatalf("CreateTask error = %v, want ErrInvalidRequest", err)
	}
}

func TestLifecycleServiceRejectsUnknownTaskAssignee(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	service := newAuthorizedLifecycleService(env.UnitOfWork, env.DBRouter, repo, missioncontrol.LifecycleOptions{
		Clock: env.Clock, IDGenerator: env.IDGenerator,
		AgentDirectory: &fakeLifecycleAgentDirectory{entries: []agentcore.AgentDirectoryEntry{{AgentProfileID: "agent-1", AgentInstanceID: "agent-1", AgentKind: agentcore.AgentKindAI, Enabled: true}}},
	})
	viewer := missioncontrol.ViewerRef{ViewerID: "planner-1", ViewerKind: missioncontrol.ViewerKindUser}
	mission, err := service.CreateMission(ctx, viewer, missioncontrol.CreateMissionCommand{IdempotencyKey: "mission-key", Title: "Launch", FinalGoal: "Ship v2"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.CreateTask(ctx, viewer, missioncontrol.CreateTaskCommand{IdempotencyKey: "task-key", MissionID: missioncore.MissionID(mission.MissionID), TaskID: "task-1", AssigneeID: "tool.deploy", Goal: "Implement route", WorkerPersonalGoal: "Deliver working route", Scope: "backend"})
	if !errors.Is(err, missioncontrol.ErrInvalidRequest) {
		t.Fatalf("CreateTask error = %v, want ErrInvalidRequest", err)
	}
}

func TestLifecycleServiceRejectsTaskAssigneeWithoutAgentDirectory(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	service := newAuthorizedLifecycleService(env.UnitOfWork, env.DBRouter, repo, missioncontrol.LifecycleOptions{Clock: env.Clock, IDGenerator: env.IDGenerator})
	viewer := missioncontrol.ViewerRef{ViewerID: "planner-1", ViewerKind: missioncontrol.ViewerKindUser}
	mission, err := service.CreateMission(ctx, viewer, missioncontrol.CreateMissionCommand{IdempotencyKey: "mission-key", Title: "Launch", FinalGoal: "Ship v2"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.CreateTask(ctx, viewer, missioncontrol.CreateTaskCommand{IdempotencyKey: "task-key", MissionID: missioncore.MissionID(mission.MissionID), TaskID: "task-1", AssigneeID: "agent-1", Goal: "Implement route", WorkerPersonalGoal: "Deliver working route", Scope: "backend"})
	if !errors.Is(err, missioncontrol.ErrServiceDependency) {
		t.Fatalf("CreateTask error = %v, want ErrServiceDependency", err)
	}
}

func TestLifecycleServiceRecomputesQAPolicyWhenTaskIsReassigned(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	projectionRepo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	taskRepo := missiontask.NewPlatformRepository()
	agents := &fakeLifecycleAgentDirectory{entries: []agentcore.AgentDirectoryEntry{
		{AgentInstanceID: "profile-plain", AgentProfileID: "profile-plain", AgentKind: agentcore.AgentKindAI, Enabled: true},
		{AgentInstanceID: "profile-qa", AgentProfileID: "profile-qa", AgentKind: agentcore.AgentKindAI, QAEnabled: true, Enabled: true},
	}}
	service := newAuthorizedLifecycleService(env.UnitOfWork, env.DBRouter, projectionRepo, missioncontrol.LifecycleOptions{Clock: env.Clock, IDGenerator: env.IDGenerator, TaskRepo: taskRepo, AgentDirectory: agents})
	viewer := missioncontrol.ViewerRef{ViewerID: "planner-1", ViewerKind: missioncontrol.ViewerKindUser}
	mission, err := service.CreateMission(ctx, viewer, missioncontrol.CreateMissionCommand{IdempotencyKey: "mission-key", Title: "Launch", FinalGoal: "Ship v2"})
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.CreateTask(ctx, viewer, missioncontrol.CreateTaskCommand{IdempotencyKey: "task-key", MissionID: missioncore.MissionID(mission.MissionID), AssigneeID: "profile-plain", Goal: "Implement route", WorkerPersonalGoal: "Deliver working route"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AssignTask(ctx, viewer, missioncontrol.AssignTaskCommand{IdempotencyKey: "assign-key", MissionID: missioncore.MissionID(mission.MissionID), TaskID: missioncore.TaskID(created.TaskID), AssigneeID: "profile-qa", Reason: "needs QA gate"}); err != nil {
		t.Fatal(err)
	}
	var record missiontask.TaskRecord
	if err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		var getErr error
		record, getErr = taskRepo.GetTask(ctx, tx, missiontask.TaskID(created.TaskID))
		return getErr
	}); err != nil {
		t.Fatal(err)
	}
	if record.Contract.Assignee.AgentProfileRef == nil || record.Contract.Assignee.AgentProfileRef.ID != "profile-qa" || !record.Contract.QAEnabled || !record.Contract.QAPlanRequired || !record.Contract.QACheckRequired || record.Contract.QAPlanCompleted {
		t.Fatalf("reassigned QA-enabled worker task contract = %+v", record.Contract)
	}
}
