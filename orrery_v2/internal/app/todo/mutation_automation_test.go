package todo

import (
	"context"
	"errors"
	"testing"
	"time"

	missionprojection "orrery_v2/internal/mission/projection"
	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/idgen"
	"orrery_v2/internal/platform/testkit"
)

func TestUpdateTodoInvalidatesApprovalAndPausesActiveMission(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repo := NewRepository(env.DBRouter)
	item, state := seedTodoMissionExecution(t, repo, "owner-a")
	binding := seedActiveTodoMissionBinding(t, repo, item, state, "mission-scope-change")
	pause := &todoMissionPauseStub{}
	service := NewService(ServiceOptions{
		Clock: clock.NewFake(todoTestTime.Add(10 * time.Minute)), IDGenerator: idgen.NewDeterministic("mutation"),
		Repository: repo, MissionPause: pause,
	})
	viewer := testViewer(item.TenantID, "owner-a")
	changedTitle := "Mission execution with revised scope"
	updated, err := service.UpdateTodo(ctx, viewer, UpdateTodoRequest{TodoID: item.TodoID, Title: &changedTitle, Version: &item.Version})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Version != item.Version+1 || len(pause.requests) != 1 || pause.requests[0].MissionID != binding.MissionID {
		t.Fatalf("updated=%+v pause requests=%+v", updated, pause.requests)
	}
	automation, err := repo.GetTodoAutomation(ctx, item.TenantID, item.TodoID)
	if err != nil || automation.Status != TodoAutomationStatusStale || automation.ApprovedPlanDigest != "" {
		t.Fatalf("automation=%+v err=%v", automation, err)
	}
	paused, err := repo.GetTodoMissionBinding(ctx, item.TenantID, item.TodoID)
	if err != nil || paused.Status != TodoMissionBindingStatusPaused || paused.PauseReason == "" {
		t.Fatalf("paused binding=%+v err=%v", paused, err)
	}
	attempts, err := repo.ListTodoExecutionAttempts(ctx, item.TenantID, item.TodoID)
	if err != nil || len(attempts) != 1 || attempts[0].Status != TodoMissionBindingStatusPaused || attempts[0].CompletedAt.IsZero() {
		t.Fatalf("attempts=%+v err=%v", attempts, err)
	}
}

func TestTodoMutationPauseRequestRecoversAfterTransientMissionFailure(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repo := NewRepository(env.DBRouter)
	item, state := seedTodoMissionExecution(t, repo, "owner-a")
	seedActiveTodoMissionBinding(t, repo, item, state, "mission-pause-recovery")
	pause := &todoMissionPauseStub{err: errors.New("Mission runtime temporarily unavailable")}
	discussion := &todoAutomationDiscussionStub{}
	service := NewService(ServiceOptions{
		Clock: clock.NewFake(todoTestTime.Add(10 * time.Minute)), IDGenerator: idgen.NewDeterministic("mutation-recovery"),
		Repository: repo, MissionPause: pause, AutomationDiscussion: discussion, AutomationAgentIDs: []string{"lead-agent"},
	})
	viewer := testViewer(item.TenantID, "owner-a")
	changedDescription := "Revised after execution started"
	if _, err := service.UpdateTodo(ctx, viewer, UpdateTodoRequest{TodoID: item.TodoID, Description: &changedDescription, Version: &item.Version}); err != nil {
		t.Fatal(err)
	}
	pending, err := repo.GetTodoMissionBinding(ctx, item.TenantID, item.TodoID)
	if err != nil || pending.Status != TodoMissionBindingStatusPauseRequested {
		t.Fatalf("pending binding=%+v err=%v", pending, err)
	}
	requests, err := repo.ListTodoMissionPauseRequests(ctx, 10)
	if err != nil || len(requests) != 1 || requests[0].MissionID != pending.MissionID {
		t.Fatalf("pause requests=%+v err=%v", requests, err)
	}
	pause.err = nil
	result, err := service.RunScheduledAutomationScan(ctx, TodoAutomationScanRequest{ScheduledRunID: "pause-recovery-run"})
	if err != nil {
		t.Fatal(err)
	}
	if result.RecoveredCount != 1 || len(pause.requests) != 2 {
		t.Fatalf("scan result=%+v pause requests=%+v", result, pause.requests)
	}
	paused, err := repo.GetTodoMissionBinding(ctx, item.TenantID, item.TodoID)
	if err != nil || paused.Status != TodoMissionBindingStatusPaused {
		t.Fatalf("recovered binding=%+v err=%v", paused, err)
	}
}

func TestStaleMissionProjectionCannotCompleteMutatedTodo(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repo := NewRepository(env.DBRouter)
	item, state := seedTodoMissionExecution(t, repo, "owner-a")
	binding := seedActiveTodoMissionBinding(t, repo, item, state, "mission-stale-completion")
	mutated := item
	mutated.Title = "Changed scope"
	mutated.Version++
	mutated.UpdatedAt = todoTestTime.Add(10 * time.Minute)
	result, err := repo.UpdateTodoAndInvalidateAutomation(ctx, mutated, item.Version, mutated.UpdatedAt)
	if err != nil || result.PauseRequest.MissionID != binding.MissionID {
		t.Fatalf("mutation result=%+v err=%v", result, err)
	}
	model := todoProgressTestMission(item.TenantID, item.TodoID, binding.MissionID, 10, mutated.UpdatedAt.Add(time.Minute))
	for index := range model.TaskBoard.Tasks {
		model.TaskBoard.Tasks[index].Status = missionprojection.TaskStatusApproved
		model.TaskBoard.Tasks[index].QAStatus = missionprojection.QAStatusApproved
	}
	if _, _, err := repo.ProjectTodoExecutionProgress(ctx, model, mutated.UpdatedAt.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	current, err := repo.GetTodoMissionBinding(ctx, item.TenantID, item.TodoID)
	if err != nil || current.Status != TodoMissionBindingStatusPauseRequested {
		t.Fatalf("binding after stale completion=%+v err=%v", current, err)
	}
	automation, err := repo.GetTodoAutomation(ctx, item.TenantID, item.TodoID)
	if err != nil || automation.Status != TodoAutomationStatusStale {
		t.Fatalf("automation after stale completion=%+v err=%v", automation, err)
	}
}

type todoMissionPauseStub struct {
	requests []TodoMissionPauseRequest
	err      error
}

func (s *todoMissionPauseStub) PauseMissionForTodo(_ context.Context, request TodoMissionPauseRequest) error {
	s.requests = append(s.requests, request)
	return s.err
}

func seedActiveTodoMissionBinding(t *testing.T, repo *Repository, item TodoItem, state TodoAutomationState, missionID string) TodoMissionBinding {
	t.Helper()
	binding, acquired, err := repo.BeginTodoMissionExecution(context.Background(), item, state, "owner-a", "claim-active", todoTestTime)
	if err != nil || !acquired {
		t.Fatalf("begin execution binding=%+v acquired=%v err=%v", binding, acquired, err)
	}
	binding.MissionID = missionID
	binding.MissionTaskIDs = []string{"task-1"}
	for _, status := range []string{TodoMissionBindingStatusMaterialized, TodoMissionBindingStatusDispatching, TodoMissionBindingStatusExecuting} {
		binding.Status = status
		binding.UpdatedAt = binding.UpdatedAt.Add(time.Minute)
		if err := repo.SaveTodoMissionBinding(context.Background(), binding); err != nil {
			t.Fatalf("save binding status %s: %v", status, err)
		}
	}
	return binding
}
