package todo

import (
	"context"
	"testing"

	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/idgen"
	"orrery_v2/internal/platform/testkit"
)

func TestTodoExecutionProgressFiltersUnauthorizedMissionAttempts(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repo := NewRepository(env.DBRouter)
	progress := &executionProgressStub{allowedMissionID: "mission-visible"}
	service := NewService(ServiceOptions{
		Clock: clock.NewFake(todoTestTime), IDGenerator: idgen.NewDeterministic("task"),
		Repository: repo, ExecutionProgress: progress,
	})
	viewer := testViewer("tenant-a", "owner")
	item := mustCreateTask(t, service, ctx, viewer, CreateTodoRequest{Title: "Mission progress"})

	for attempt, missionID := range []string{"mission-hidden", "mission-visible"} {
		if err := repo.SaveTodoExecutionAttempt(ctx, TodoExecutionAttempt{
			TenantID: item.TenantID, TodoID: item.TodoID, ExecutionAttempt: attempt + 1,
			MissionID: missionID, ProposalID: "proposal", TodoVersion: item.Version,
			PlanDigest: "digest", Status: TodoMissionBindingStatusExecuting,
			StartedAt: todoTestTime, UpdatedAt: todoTestTime,
		}); err != nil {
			t.Fatalf("SaveTodoExecutionAttempt(%s): %v", missionID, err)
		}
	}

	got, err := service.GetTodoExecutionProgress(ctx, viewer, item.TodoID)
	if err != nil {
		t.Fatalf("GetTodoExecutionProgress: %v", err)
	}
	if len(got.Attempts) != 1 || got.Attempts[0].MissionID != "mission-visible" || got.Attempts[0].Progress == nil {
		t.Fatalf("visible attempts = %+v", got.Attempts)
	}
	if got.Attempts[0].Progress.MissionProjectionVersion != 7 || got.Attempts[0].Progress.DeliveryRate != 0.5 {
		t.Fatalf("progress must come from Mission projection adapter: %+v", got.Attempts[0].Progress)
	}
}

type executionProgressStub struct{ allowedMissionID string }

func (s *executionProgressStub) GetTodoExecutionProgress(_ context.Context, _ ViewerRef, attempt TodoExecutionAttempt) (TodoExecutionProgress, bool, error) {
	if attempt.MissionID != s.allowedMissionID {
		return TodoExecutionProgress{}, false, nil
	}
	return TodoExecutionProgress{
		TodoExecutionProgressProjection: TodoExecutionProgressProjection{
			TodoID: attempt.TodoID, MissionID: attempt.MissionID, ExecutionAttempt: attempt.ExecutionAttempt,
			MissionStatus: "active", DeliveryRate: 0.5, MissionProjectionVersion: 7,
		},
		CanOpenControl: true,
	}, true, nil
}
