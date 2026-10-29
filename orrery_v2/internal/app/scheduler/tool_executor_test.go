package scheduler

import (
	"context"
	"testing"
	"time"

	discussionsession "orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/idgen"
	"orrery_v2/internal/platform/testkit"
	toolstd "orrery_v2/internal/resource/tool/std"
)

func TestToolExecutorCreatesUpdatesListsAndCancelsScheduledTasks(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	service := NewService(ServiceOptions{
		Clock:       clock.NewFake(schedulerTestTime),
		IDGenerator: idgen.NewDeterministic("sched"),
		Repository:  repo,
	})
	executor := NewToolExecutor(ToolExecutorOptions{Service: service})
	ctx := context.Background()

	created, err := executor.Execute(ctx, ToolExecutionRequest{
		ToolID: string(toolstd.ScheduledTaskCreate),
		Viewer: ViewerRef{
			ViewerID:    "operator",
			ViewerKind:  ViewerKindHuman,
			DisplayName: "Operator",
			ProfileID:   "human-operator",
		},
		Arguments: map[string]any{
			"title": "Daily digest",
			"schedule": map[string]any{
				"type":   "once",
				"run_at": schedulerTestTime.Add(time.Hour).Format(time.RFC3339),
			},
			"target": map[string]any{
				"mode":    "discussion_workflow",
				"message": "Summarize yesterday's activity.",
			},
			"idempotency_key": "tool-create-1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	taskID := stringOutput(created.Output, "scheduled_task_id")
	if taskID != "sched-000001" || stringOutput(created.Output, "status") != "enabled" {
		t.Fatalf("created output = %+v", created.Output)
	}

	updated, err := executor.Execute(ctx, ToolExecutionRequest{
		ToolID: string(toolstd.ScheduledTaskUpdate),
		Viewer: ViewerRef{
			ViewerID:    "operator",
			ViewerKind:  ViewerKindHuman,
			DisplayName: "Operator",
			ProfileID:   "human-operator",
		},
		Arguments: map[string]any{
			"scheduled_task_id": taskID,
			"title":             "Paused daily digest",
			"enabled":           false,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if stringOutput(updated.Output, "status") != "disabled" {
		t.Fatalf("updated output = %+v", updated.Output)
	}

	listed, err := executor.Execute(ctx, ToolExecutionRequest{
		ToolID: string(toolstd.ScheduledTaskList),
		Viewer: ViewerRef{
			ViewerID:    "operator",
			ViewerKind:  ViewerKindHuman,
			DisplayName: "Operator",
			ProfileID:   "human-operator",
		},
		Arguments: map[string]any{"limit": 20},
	})
	if err != nil {
		t.Fatal(err)
	}
	if intOutput(listed.Output, "total") != 1 {
		t.Fatalf("listed output = %+v", listed.Output)
	}

	cancelled, err := executor.Execute(ctx, ToolExecutionRequest{
		ToolID: string(toolstd.ScheduledTaskCancel),
		Viewer: ViewerRef{
			ViewerID:    "operator",
			ViewerKind:  ViewerKindHuman,
			DisplayName: "Operator",
			ProfileID:   "human-operator",
		},
		Arguments: map[string]any{"scheduled_task_id": taskID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if stringOutput(cancelled.Output, "status") != "archived" {
		t.Fatalf("cancelled output = %+v", cancelled.Output)
	}
}

func TestToolExecutorRunNowDispatchesQueuedRun(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	task := schedulerTestTask("sched-run-now", schedulerTestTime.Add(time.Hour))
	if err := repo.SaveTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	discussion := &fakeDiscussionPort{conversation: discussionsession.ConversationDTO{ConversationID: "conversation-now"}}
	service := NewService(ServiceOptions{
		Clock:       clock.NewFake(schedulerTestTime),
		IDGenerator: idgen.NewDeterministic("run"),
		Repository:  repo,
	})
	executor := NewToolExecutor(ToolExecutorOptions{
		Service: service,
		Dispatcher: NewDispatcher(DispatcherOptions{
			Clock:      clock.NewFake(schedulerTestTime.Add(time.Minute)),
			Repository: repo,
			Discussion: discussion,
		}),
	})

	result, err := executor.Execute(ctx, ToolExecutionRequest{
		ToolID: string(toolstd.ScheduledTaskRunNow),
		Viewer: ViewerRef{
			ViewerID:    "operator",
			ViewerKind:  ViewerKindHuman,
			DisplayName: "Operator",
			ProfileID:   "human-operator",
		},
		Arguments: map[string]any{"scheduled_task_id": task.ScheduledTaskID},
	})
	if err != nil {
		t.Fatal(err)
	}

	if stringOutput(result.Output, "status") != "completed" || stringOutput(result.Output, "scheduled_run_id") != "run-000001" {
		t.Fatalf("run-now output = %+v", result.Output)
	}
	if discussion.cmd.IdempotencyKey != "scheduled-run:run-000001" {
		t.Fatalf("discussion command = %+v", discussion.cmd)
	}
}

func TestToolExecutorRunNowReturnsSkippedRunWithoutDispatch(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	task := schedulerTestTask("sched-skip-now", schedulerTestTime.Add(time.Hour))
	if err := repo.SaveTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveRun(ctx, schedulerTestRun("active-run", task.ScheduledTaskID, RunStatusRunning, schedulerTestTime)); err != nil {
		t.Fatal(err)
	}
	discussion := &fakeDiscussionPort{conversation: discussionsession.ConversationDTO{ConversationID: "conversation-now"}}
	service := NewService(ServiceOptions{
		Clock:       clock.NewFake(schedulerTestTime),
		IDGenerator: idgen.NewDeterministic("run"),
		Repository:  repo,
	})
	executor := NewToolExecutor(ToolExecutorOptions{
		Service: service,
		Dispatcher: NewDispatcher(DispatcherOptions{
			Clock:      clock.NewFake(schedulerTestTime.Add(time.Minute)),
			Repository: repo,
			Discussion: discussion,
		}),
	})

	result, err := executor.Execute(ctx, ToolExecutionRequest{
		ToolID: string(toolstd.ScheduledTaskRunNow),
		Viewer: ViewerRef{
			ViewerID:    "operator",
			ViewerKind:  ViewerKindHuman,
			DisplayName: "Operator",
			ProfileID:   "human-operator",
		},
		Arguments: map[string]any{"scheduled_task_id": task.ScheduledTaskID},
	})
	if err != nil {
		t.Fatal(err)
	}

	if stringOutput(result.Output, "status") != "skipped" || stringOutput(result.Output, "failure_reason") == "" {
		t.Fatalf("run-now output = %+v", result.Output)
	}
	if discussion.cmd.IdempotencyKey != "" {
		t.Fatalf("discussion should not be dispatched: %+v", discussion.cmd)
	}
}

func stringOutput(output map[string]any, key string) string {
	value, _ := output[key].(string)
	return value
}

func intOutput(output map[string]any, key string) int {
	switch value := output[key].(type) {
	case int:
		return value
	case float64:
		return int(value)
	default:
		return 0
	}
}
