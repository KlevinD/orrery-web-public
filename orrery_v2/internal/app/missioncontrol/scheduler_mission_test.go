package missioncontrol

import (
	"context"
	"errors"
	"testing"

	"orrery_v2/internal/app/authorization"
	missioncore "orrery_v2/internal/mission/core"
	"orrery_v2/internal/platform/testkit"
)

func TestSyncSchedulerMissionMapsScheduledTaskLifecycle(t *testing.T) {
	tests := []struct {
		name       string
		taskStatus string
		wantStatus string
	}{
		{name: "enabled", taskStatus: "enabled", wantStatus: "active"},
		{name: "disabled", taskStatus: "disabled", wantStatus: "paused"},
		{name: "archived", taskStatus: "archived", wantStatus: "cancelled"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			env := testkit.New(t)
			defer env.Close()
			repo := NewDBProjectionRepository(env.DBRouter)
			service := NewLifecycleService(env.UnitOfWork, env.DBRouter, repo, LifecycleOptions{
				Clock: env.Clock, IDGenerator: env.IDGenerator,
				AuthorizationService: authorization.NewService(sagaAuthorizationAdapter{}), AuthorizationRelations: authorization.NewRelationProjector(&sagaRelationSink{}),
			})
			viewer := schedulerMissionTestViewer()
			missionID := missioncore.MissionID("scheduled-mission-1")
			if _, err := service.CreateMission(ctx, viewer, CreateMissionCommand{
				MissionID: missionID.String(), IdempotencyKey: "scheduled-task-1", Title: "Old title", FinalGoal: "Old goal",
				OriginKind: "scheduler", OriginID: "scheduled-task-1", InitialAgentProfileID: "agent-1",
			}); err != nil {
				t.Fatal(err)
			}

			synced, err := service.SyncSchedulerMission(ctx, viewer, SyncSchedulerMissionCommand{
				IdempotencyKey: "sync-scheduled-task-1", MissionID: missionID, ScheduledTaskID: "scheduled-task-1",
				Title: "Current title", FinalGoal: "Current goal", ScheduledTaskStatus: test.taskStatus,
			})
			if err != nil {
				t.Fatal(err)
			}
			if synced.Title != "Current title" || synced.FinalGoal != "Current goal" || synced.Status != test.wantStatus {
				t.Fatalf("synced Mission = %+v", synced)
			}
			stored, err := repo.GetMissionControlForTenant(ctx, viewer.Tenant(), missionID)
			if err != nil {
				t.Fatal(err)
			}
			if string(stored.Mission.Status) != test.wantStatus {
				t.Fatalf("stored Mission status = %q, want %q", stored.Mission.Status, test.wantStatus)
			}
		})
	}
}

func TestSyncSchedulerMissionRejectsDifferentSchedulerOrigin(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := NewDBProjectionRepository(env.DBRouter)
	service := NewLifecycleService(env.UnitOfWork, env.DBRouter, repo, LifecycleOptions{
		Clock: env.Clock, IDGenerator: env.IDGenerator,
		AuthorizationService: authorization.NewService(sagaAuthorizationAdapter{}), AuthorizationRelations: authorization.NewRelationProjector(&sagaRelationSink{}),
	})
	viewer := schedulerMissionTestViewer()
	missionID := missioncore.MissionID("scheduled-mission-conflict")
	if _, err := service.CreateMission(ctx, viewer, CreateMissionCommand{
		MissionID: missionID.String(), IdempotencyKey: "other-scheduled-task", Title: "Other title", FinalGoal: "Other goal",
		OriginKind: "scheduler", OriginID: "other-scheduled-task", InitialAgentProfileID: "agent-1",
	}); err != nil {
		t.Fatal(err)
	}

	_, err := service.SyncSchedulerMission(ctx, viewer, SyncSchedulerMissionCommand{
		IdempotencyKey: "sync-conflict", MissionID: missionID, ScheduledTaskID: "scheduled-task-1",
		Title: "Overwritten title", FinalGoal: "Overwritten goal", ScheduledTaskStatus: "enabled",
	})
	if !errors.Is(err, ErrProjectionNotFound) {
		t.Fatalf("SyncSchedulerMission error = %v, want ErrProjectionNotFound", err)
	}
	stored, err := repo.GetMissionControlForTenant(ctx, viewer.Tenant(), missionID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Mission.Title != "Other title" || stored.Mission.FinalGoal != "Other goal" {
		t.Fatalf("conflicting Mission was overwritten: %+v", stored.Mission)
	}
}

func TestSyncSchedulerMissionRejectsUserCaller(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	service := NewLifecycleService(env.UnitOfWork, env.DBRouter, NewDBProjectionRepository(env.DBRouter), LifecycleOptions{
		Clock: env.Clock, IDGenerator: env.IDGenerator,
		AuthorizationService: authorization.NewService(sagaAuthorizationAdapter{}), AuthorizationRelations: authorization.NewRelationProjector(&sagaRelationSink{}),
	})
	_, err := service.SyncSchedulerMission(context.Background(), ViewerRef{ViewerID: "planner", ViewerKind: ViewerKindUser, TenantID: "tenant-a"}, SyncSchedulerMissionCommand{
		IdempotencyKey: "sync-forged", MissionID: "scheduled-mission-1", ScheduledTaskID: "scheduled-task-1",
		Title: "Forged", FinalGoal: "Forged", ScheduledTaskStatus: "enabled",
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("SyncSchedulerMission error = %v, want ErrInvalidRequest", err)
	}
}

func schedulerMissionTestViewer() ViewerRef {
	return ViewerRef{
		ViewerID: "scheduler-agent-task", ViewerKind: ViewerKindSystem, TenantID: "tenant-a",
		AuthorizationSubjectID: "scheduler-agent-task", PermissionScope: []string{"mission.write"},
	}
}
