package missioncontrol_test

import (
	"context"
	"errors"
	"testing"

	missioncontrol "orrery_v2/internal/app/missioncontrol"
	missioncore "orrery_v2/internal/mission/core"
	"orrery_v2/internal/platform/testkit"
)

func TestLifecycleServiceRevisesDraftTaskWithNewIdempotencyKey(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	service := newAuthorizedLifecycleService(env.UnitOfWork, env.DBRouter, repo, missioncontrol.LifecycleOptions{
		Clock: env.Clock, IDGenerator: env.IDGenerator,
	})
	viewer := missioncontrol.ViewerRef{ViewerID: "planner-1", ViewerKind: missioncontrol.ViewerKindUser}
	mission, err := service.CreateMission(ctx, viewer, missioncontrol.CreateMissionCommand{
		IdempotencyKey: "mission-key", Title: "Launch", FinalGoal: "Ship v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	taskID := missioncore.TaskID("root-" + mission.MissionID)
	if _, err := service.CreateTask(ctx, viewer, missioncontrol.CreateTaskCommand{
		IdempotencyKey: "root:tool-call-1", MissionID: missioncore.MissionID(mission.MissionID), TaskID: taskID, Goal: "Ship v1",
	}); err != nil {
		t.Fatal(err)
	}
	revised, err := service.CreateTask(ctx, viewer, missioncontrol.CreateTaskCommand{
		IdempotencyKey: "root:tool-call-2", MissionID: missioncore.MissionID(mission.MissionID), TaskID: taskID, Goal: "Ship v2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if revised.TaskID != taskID.String() || revised.Goal != "Ship v2" {
		t.Fatalf("revised task = %+v", revised)
	}
	if _, err := service.CreateTask(ctx, viewer, missioncontrol.CreateTaskCommand{
		IdempotencyKey: "root:tool-call-2", MissionID: missioncore.MissionID(mission.MissionID), TaskID: taskID, Goal: "Ship v3",
	}); !errors.Is(err, missioncontrol.ErrInvalidRequest) {
		t.Fatalf("same idempotency key with a different task contract error = %v", err)
	}
}
