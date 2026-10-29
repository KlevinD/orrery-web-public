package scheduler

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/platform/testkit"
	toolstd "orrery_v2/internal/resource/tool/std"
)

func TestSchedulerHistoryToolReadsRequestedDayAndOldActiveBlockers(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	service := newServiceForTestWithRepository(repo)
	executor := NewToolExecutor(ToolExecutorOptions{Service: service})
	ctx := context.Background()
	task := schedulerTestTask("daily-report", schedulerTestTime)
	task.TenantID = "tenant-a"
	if err := repo.SaveTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	from, _ := time.Parse(time.RFC3339, "2026-10-13T00:00:00+08:00")
	before := from.Add(24 * time.Hour)
	old := schedulerTestRun("september-blocker", task.ScheduledTaskID, RunStatusRunning, from.Add(-30*24*time.Hour))
	old.ResponsibilityID = "responsibility-old"
	first := schedulerTestRun("yesterday-first", task.ScheduledTaskID, RunStatusSkipped, from)
	first.FailureReason = "active run already exists"
	last := schedulerTestRun("yesterday-last", task.ScheduledTaskID, RunStatusFailed, before.Add(-time.Nanosecond))
	last.CompletedAt = before.Add(-time.Nanosecond)
	last.Result.Summary = "Report prepared but could not be sent"
	last.DeliveryStatus, last.DeliveryError = "failed", "provider rejected delivery"
	last.Result.Artifacts = []RunArtifact{{ID: "internal-report", URI: "/private/storage/report.md", FileName: "report.md", ContentType: "text/markdown"}}
	today := schedulerTestRun("today", task.ScheduledTaskID, RunStatusCompleted, before)
	for _, run := range []ScheduledRun{old, first, last, today} {
		if err := repo.SaveRun(ctx, run); err != nil {
			t.Fatal(err)
		}
	}
	result, err := executor.Execute(ctx, ToolExecutionRequest{
		ToolID: string(toolstd.ScheduledTaskList), Viewer: ViewerRef{TenantID: "tenant-a"},
		Arguments: map[string]any{"view": "runs", "scheduled_task_id": task.ScheduledTaskID,
			"created_from": from.Format(time.RFC3339), "created_before": before.Format(time.RFC3339), "limit": 1, "offset": 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output["total"] != 2 || result.Output["has_more"] != false {
		t.Fatalf("history pagination = %+v", result.Output)
	}
	runs := result.Output["runs"].([]map[string]any)
	if len(runs) != 1 || runs[0]["scheduled_run_id"] != last.ScheduledRunID {
		t.Fatalf("runs = %+v", runs)
	}
	if runs[0]["delivery_status"] != "failed" || runs[0]["delivery_error"] != last.DeliveryError || runs[0]["result_summary"] != last.Result.Summary {
		t.Fatalf("missing execution/delivery evidence: %+v", runs[0])
	}
	active := result.Output["active_runs"].([]map[string]any)
	if result.Output["active_total"] != 1 || len(active) != 1 || active[0]["scheduled_run_id"] != old.ScheduledRunID || active[0]["responsibility_id"] != old.ResponsibilityID {
		t.Fatalf("old blocker = %+v", active)
	}
	artifact := runs[0]["artifacts"].([]map[string]any)[0]
	if artifact["artifact_id"] == "internal-report" || artifact["uri"] != nil {
		t.Fatalf("storage reference exposed: %+v", artifact)
	}
	if runs[0]["configuration_snapshot"] != nil || runs[0]["execution_envelope"] != nil {
		t.Fatalf("internal run configuration exposed: %+v", runs[0])
	}
}

func TestSchedulerHistoryToolRejectsWrongTenantAndInvalidWindow(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	executor := NewToolExecutor(ToolExecutorOptions{Service: newServiceForTestWithRepository(repo)})
	task := schedulerTestTask("private-schedule", schedulerTestTime)
	task.TenantID = "tenant-a"
	if err := repo.SaveTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, tenant string
		args         map[string]any
		want         error
	}{
		{"wrong-tenant", "tenant-b", map[string]any{"view": "runs", "scheduled_task_id": task.ScheduledTaskID}, ErrNotFound},
		{"missing-task", "tenant-a", map[string]any{"view": "runs"}, ErrInvalidRequest},
		{"bad-date", "tenant-a", map[string]any{"view": "runs", "scheduled_task_id": task.ScheduledTaskID, "created_from": "yesterday"}, ErrInvalidRequest},
		{"reversed-window", "tenant-a", map[string]any{"view": "runs", "scheduled_task_id": task.ScheduledTaskID, "created_from": "2026-10-14T00:00:00+08:00", "created_before": "2026-10-13T00:00:00+08:00"}, ErrInvalidRequest},
		{"forgot-view", "tenant-a", map[string]any{"scheduled_task_id": task.ScheduledTaskID, "created_from": "2026-10-13T00:00:00+08:00"}, ErrInvalidRequest},
		{"unknown-view", "tenant-a", map[string]any{"view": "logs"}, ErrInvalidRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := executor.Execute(context.Background(), ToolExecutionRequest{ToolID: string(toolstd.ScheduledTaskList), Viewer: ViewerRef{TenantID: tc.tenant}, Arguments: tc.args})
			if !errors.Is(err, tc.want) {
				t.Fatalf("error=%v want=%v", err, tc.want)
			}
		})
	}
}

func TestSchedulerHistoryToolFiltersStatusAndClampsPages(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	executor := NewToolExecutor(ToolExecutorOptions{Service: newServiceForTestWithRepository(repo)})
	task := schedulerTestTask("status-schedule", schedulerTestTime)
	if err := repo.SaveTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	for _, run := range []ScheduledRun{
		schedulerTestRun("skip", task.ScheduledTaskID, RunStatusSkipped, schedulerTestTime),
		schedulerTestRun("done", task.ScheduledTaskID, RunStatusCompleted, schedulerTestTime),
	} {
		if err := repo.SaveRun(context.Background(), run); err != nil {
			t.Fatal(err)
		}
	}
	result, err := executor.Execute(context.Background(), ToolExecutionRequest{ToolID: string(toolstd.ScheduledTaskList), Arguments: map[string]any{
		"view": "runs", "scheduled_task_id": task.ScheduledTaskID, "status": "skipped", "limit": 9999, "offset": -1,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output["total"] != 1 || result.Output["limit"] != 100 || result.Output["offset"] != 0 {
		t.Fatalf("filtered page=%+v", result.Output)
	}
}

func TestSchedulerHistoryToolReadsBoundedArtifactWithTenantAndRunChecks(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	store := &recordingSchedulerArtifactStore{}
	service := newServiceForTestWithRepository(repo)
	service.artifactStore = store
	executor := NewToolExecutor(ToolExecutorOptions{Service: service})
	ctx := context.Background()
	task := schedulerTestTask("artifact-schedule", schedulerTestTime)
	task.TenantID = "tenant-a"
	if err := repo.SaveTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	run := schedulerTestRun("artifact-run", task.ScheduledTaskID, RunStatusCompleted, schedulerTestTime)
	artifact := RunArtifact{ID: "private-key", URI: "/private/report.md", ContentType: "text/markdown"}
	run.Result.Artifacts = []RunArtifact{artifact}
	if err := repo.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	args := map[string]any{"view": "artifact", "scheduled_task_id": task.ScheduledTaskID, "scheduled_run_id": run.ScheduledRunID, "artifact_id": PublicRunArtifactID(run.ScheduledRunID, 0, artifact)}
	_, err := executor.Execute(ctx, ToolExecutionRequest{ToolID: string(toolstd.ScheduledTaskList), Viewer: ViewerRef{TenantID: "tenant-b"}, Arguments: args})
	if !errors.Is(err, ErrNotFound) || store.calls != 0 {
		t.Fatalf("unauthorized artifact opened: calls=%d err=%v", store.calls, err)
	}
	args["scheduled_run_id"] = "other-run"
	if _, err := executor.Execute(ctx, ToolExecutionRequest{ToolID: string(toolstd.ScheduledTaskList), Viewer: ViewerRef{TenantID: "tenant-a"}, Arguments: args}); !errors.Is(err, ErrNotFound) || store.calls != 0 {
		t.Fatalf("wrong run opened: calls=%d err=%v", store.calls, err)
	}
	args["scheduled_run_id"] = run.ScheduledRunID
	args["artifact_id"] = artifact.ID
	if _, err := executor.Execute(ctx, ToolExecutionRequest{ToolID: string(toolstd.ScheduledTaskList), Viewer: ViewerRef{TenantID: "tenant-a"}, Arguments: args}); !errors.Is(err, ErrNotFound) || store.calls != 0 {
		t.Fatalf("raw storage id opened: calls=%d err=%v", store.calls, err)
	}
	args["artifact_id"] = PublicRunArtifactID(run.ScheduledRunID, 0, artifact)
	store.content = RunArtifactContent{Reader: io.NopCloser(strings.NewReader(strings.Repeat("r", 40*1024))), ContentType: "text/markdown; charset=utf-8", SizeBytes: 40 * 1024}
	result, err := executor.Execute(ctx, ToolExecutionRequest{ToolID: string(toolstd.ScheduledTaskList), Viewer: ViewerRef{TenantID: "tenant-a"}, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output["truncated"] != true || len(result.Output["content"].(string)) != 32*1024 || result.Output["size_bytes"] != int64(40*1024) {
		t.Fatalf("bounded artifact=%+v", result.Output)
	}
	store.content = RunArtifactContent{Reader: io.NopCloser(strings.NewReader("binary")), ContentType: "application/pdf"}
	if _, err := executor.Execute(ctx, ToolExecutionRequest{ToolID: string(toolstd.ScheduledTaskList), Viewer: ViewerRef{TenantID: "tenant-a"}, Arguments: args}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("binary error=%v", err)
	}
}
