package missioncontrol_test

import (
	"context"
	"errors"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/missioncontrol"
	missioncore "orrery_v2/internal/mission/core"
	missiontask "orrery_v2/internal/mission/task"
	"orrery_v2/internal/platform/testkit"
	"orrery_v2/internal/platform/uow"
)

func TestLifecycleServiceKeepsTaskExecutionEnvelopeImmutable(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	taskRepo := missiontask.NewPlatformRepository()
	service := newAuthorizedLifecycleService(env.UnitOfWork, env.DBRouter, repo, missioncontrol.LifecycleOptions{
		Clock: env.Clock, IDGenerator: env.IDGenerator, TaskRepo: taskRepo,
		AgentDirectory: &fakeLifecycleAgentDirectory{entries: []agentcore.AgentDirectoryEntry{
			{AgentProfileID: "agent-1", AgentInstanceID: "agent-1", AgentKind: agentcore.AgentKindAI, Enabled: true},
			{AgentProfileID: "agent-2", AgentInstanceID: "agent-2", AgentKind: agentcore.AgentKindAI, Enabled: true},
		}},
	})
	viewer := missioncontrol.ViewerRef{ViewerID: "planner-1", ViewerKind: missioncontrol.ViewerKindUser}
	mission, err := service.CreateMission(ctx, viewer, missioncontrol.CreateMissionCommand{
		IdempotencyKey: "mission-key", Title: "Launch", FinalGoal: "Ship v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	taskID := missioncore.TaskID("snapshotted-task")
	envelope := &agentcore.ExecutionEnvelope{
		TenantID: viewer.Tenant(), RunID: "plan-run", MissionID: mission.MissionID, TaskID: taskID.String(),
		AgentID: "agent-1", AgentProfileID: "agent-1", PermissionScopes: []string{"mission.read"},
		AllowedToolIDs: []string{"tool.read"},
	}
	if err := envelope.SealAt(env.Clock.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateTask(ctx, viewer, missioncontrol.CreateTaskCommand{
		IdempotencyKey: "task-key", MissionID: missioncore.MissionID(mission.MissionID), TaskID: taskID,
		AssigneeID: "agent-1", Goal: "Implement route", WorkerPersonalGoal: "Deliver working route",
		ExecutionEnvelope: envelope,
	}); err != nil {
		t.Fatalf("CreateTask returned error: %v", err)
	}

	if _, err := service.CreateTask(ctx, viewer, missioncontrol.CreateTaskCommand{
		IdempotencyKey: "task-revision-without-envelope", MissionID: missioncore.MissionID(mission.MissionID), TaskID: taskID,
		AssigneeID: "agent-1", Goal: "Revise route", WorkerPersonalGoal: "Deliver revised route",
	}); !errors.Is(err, missioncontrol.ErrInvalidRequest) {
		t.Fatalf("revision without envelope error = %v, want ErrInvalidRequest", err)
	}

	changed := agentcore.CloneExecutionEnvelope(envelope)
	changed.AllowedToolIDs = []string{"tool.write"}
	changed.EnvelopeDigest = ""
	if err := changed.SealAt(env.Clock.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateTask(ctx, viewer, missioncontrol.CreateTaskCommand{
		IdempotencyKey: "task-revision-with-new-envelope", MissionID: missioncore.MissionID(mission.MissionID), TaskID: taskID,
		AssigneeID: "agent-1", Goal: "Revise route", WorkerPersonalGoal: "Deliver revised route",
		ExecutionEnvelope: changed,
	}); !errors.Is(err, missioncontrol.ErrConflict) {
		t.Fatalf("revision with a different envelope error = %v, want ErrConflict", err)
	}

	if _, err := service.AssignTask(ctx, viewer, missioncontrol.AssignTaskCommand{
		IdempotencyKey: "assign-other-agent", MissionID: missioncore.MissionID(mission.MissionID), TaskID: taskID,
		AssigneeID: "agent-2", Reason: "rebalance",
	}); !errors.Is(err, missioncontrol.ErrInvalidRequest) {
		t.Fatalf("assignment to another agent error = %v, want ErrInvalidRequest", err)
	}
	if _, err := service.AssignTask(ctx, viewer, missioncontrol.AssignTaskCommand{
		IdempotencyKey: "assign-same-agent", MissionID: missioncore.MissionID(mission.MissionID), TaskID: taskID,
		AssigneeID: "agent-1", Reason: "confirm owner",
	}); err != nil {
		t.Fatalf("assignment to bound agent returned error: %v", err)
	}

	var record missiontask.TaskRecord
	if err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		var err error
		record, err = taskRepo.GetTask(ctx, tx, taskID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if record.Contract.ExecutionEnvelope == nil || record.Contract.ExecutionEnvelope.EnvelopeDigest != envelope.EnvelopeDigest {
		t.Fatalf("stored execution envelope = %+v, want digest %q", record.Contract.ExecutionEnvelope, envelope.EnvelopeDigest)
	}
}
