package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	todoapp "orrery_v2/internal/app/todo"
	"orrery_v2/internal/platform/testkit"
)

func TestSchedulerInternalIdempotencyFieldsAreNotSerialized(t *testing.T) {
	task := schedulerTestTask("sched-private", schedulerTestTime)
	task.IdempotencyKey = "secret-task-key"
	run := schedulerTestRun("run-private", task.ScheduledTaskID, RunStatusQueued, schedulerTestTime)
	run.RequestedBy = "operator"
	run.IdempotencyKey = "secret-run-key"
	run.TraceRef = "artifact://private/trace-v2"
	run.TraceVersion = 2
	run.TraceAvailable = true
	run.TraceStatus = "hot"
	payload, err := json.Marshal(struct {
		Task ScheduledTask `json:"task"`
		Run  ScheduledRun  `json:"run"`
	}{Task: task, Run: run})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "secret-task-key") || strings.Contains(string(payload), "secret-run-key") || strings.Contains(string(payload), "requested_by") || strings.Contains(string(payload), "artifact://private/trace-v2") {
		t.Fatalf("serialized scheduler payload leaked internal fields: %s", payload)
	}
	if !strings.Contains(string(payload), `"trace_available":true`) || !strings.Contains(string(payload), `"trace_status":"hot"`) {
		t.Fatalf("serialized scheduler payload omitted safe trace metadata: %s", payload)
	}
}

func TestRepositoryPersistsScheduledRunTraceRefOutsidePublicPayload(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	run := schedulerTestRun("run-trace", "sched-trace", RunStatusCompleted, schedulerTestTime)
	run.TraceRef = "artifact://private/run-trace"
	run.TraceVersion = 2
	run.TraceAvailable = true
	run.TraceStatus = "hot"
	if err := repo.SaveRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetRun(context.Background(), run.ScheduledRunID)
	if err != nil {
		t.Fatal(err)
	}
	if got.TraceRef != run.TraceRef || got.TraceVersion != 2 || !got.TraceAvailable || got.TraceStatus != "hot" {
		t.Fatalf("trace metadata=%+v", got)
	}
}

func TestRepositoryPersistsScheduledRunConfigurationSnapshot(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	run := schedulerTestRun("run-configuration-snapshot", "sched-configuration-snapshot", RunStatusQueued, schedulerTestTime)
	run.ConfigurationSnapshotVersion = 1
	run.TenantID = "tenant-configuration-snapshot"
	run.RuntimeRunID = "runtime-configuration-snapshot"
	run.ScheduledTaskTitle = "Configuration snapshot"
	run.ConfigurationSnapshot = &ScheduledTask{
		TenantID: run.TenantID, ScheduledTaskID: run.ScheduledTaskID, Title: run.ScheduledTaskTitle, Description: "Immutable description",
		Target: Target{
			Mode: TargetModeAgentTask, AgentID: "agent-snapshot", TaskGoal: "Produce report", Prompt: "Use immutable prompt",
			ToolArguments: map[string]any{"nested": []any{"one", map[string]any{"two": true}}},
		},
		Policy: Policy{MaxRetries: 2, RetryDelay: "15s"},
		Execution: ExecutionPolicy{
			TimeoutSeconds: 900, FallbackModels: []string{"fallback-model"}, AllowedToolIDs: []string{"tool-1"},
		},
		CreatedBy: "operator", CreatedByKind: ViewerKindHuman, CreatorProfileID: "human-operator",
	}
	run.ResultContract = AgentResultContract{Artifacts: []ArtifactRequirement{{
		ID: "report", Kind: "report", PathPattern: "/workspace/report.md", MinBytes: 128,
	}}}
	run.Delivery = DeliveryPolicy{
		Mode: "announce", Channel: "feishu", RecipientID: "oc_snapshot", AccountID: "main", ContentArtifactID: "report", Format: "markdown", BestEffort: true,
	}
	if err := repo.SaveRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}

	got, err := repo.GetRun(context.Background(), run.ScheduledRunID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ConfigurationSnapshotVersion != 1 || got.TenantID != run.TenantID || got.RuntimeRunID != run.RuntimeRunID || got.ScheduledTaskTitle != run.ScheduledTaskTitle || len(got.ResultContract.Artifacts) != 1 ||
		got.ResultContract.Artifacts[0] != run.ResultContract.Artifacts[0] || got.Delivery != run.Delivery || got.ConfigurationSnapshot == nil ||
		got.ConfigurationSnapshot.Target.AgentID != "agent-snapshot" || got.ConfigurationSnapshot.Target.Prompt != "Use immutable prompt" ||
		got.ConfigurationSnapshot.Policy.MaxRetries != 2 || got.ConfigurationSnapshot.Execution.FallbackModels[0] != "fallback-model" ||
		got.ConfigurationSnapshot.Target.ToolArguments["nested"].([]any)[1].(map[string]any)["two"] != true {
		t.Fatalf("configuration snapshot round trip = %+v, want %+v", got, run)
	}
}

func TestRepositorySavesAndLoadsScheduledTask(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	task := schedulerTestTask("sched-1", schedulerTestTime.Add(time.Hour))
	task.MissionID = "mission-scheduler-1"
	task.Execution = ExecutionPolicy{
		TimeoutSeconds: 1800,
		LLMProfileID:   "llm-minimax",
		SourceModel:    "minimax/MiniMax-M3",
		FallbackModels: []string{"openai-codex-api/gpt-5.6-luna"},
	}
	task.Delivery = DeliveryPolicy{
		Mode: "announce", Channel: "feishu", RecipientID: "oc_group", AccountID: "main", BestEffort: true,
	}

	if err := repo.SaveTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetTask(ctx, "sched-1")
	if err != nil {
		t.Fatal(err)
	}

	if got.ScheduledTaskID != task.ScheduledTaskID || got.MissionID != task.MissionID || got.Title != task.Title || !got.NextRunAt.Equal(task.NextRunAt) {
		t.Fatalf("loaded task = %+v, want %+v", got, task)
	}
	if got.Execution.TimeoutSeconds != 1800 || got.Execution.LLMProfileID != "llm-minimax" || len(got.Execution.FallbackModels) != 1 {
		t.Fatalf("loaded execution policy = %+v", got.Execution)
	}
	if got.Delivery.Channel != "feishu" || got.Delivery.RecipientID != "oc_group" || !got.Delivery.BestEffort {
		t.Fatalf("loaded delivery policy = %+v", got.Delivery)
	}
}

func TestRepositoryAdoptsMissingTaskIdempotencyForExpectedOwner(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	task := schedulerTestTask("sched-adopt-idempotency", schedulerTestTime.Add(time.Hour))
	task.IdempotencyKey = ""
	task.IdempotencyHash = ""
	if err := repo.SaveTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	enabled := task.Enabled
	request := CreateTaskRequest{
		Title: task.Title, Description: task.Description, Schedule: task.Schedule, Target: task.Target,
		Policy: task.Policy, Execution: task.Execution, Delivery: task.Delivery, Enabled: &enabled,
		IdempotencyKey: "adopted-task-key",
	}
	owner := ViewerRef{TenantID: task.TenantID, ViewerID: task.CreatedBy, ProfileID: task.CreatorProfileID}
	if _, err := repo.ListTasks(ctx, true); err != nil {
		t.Fatal(err)
	}

	adopted, err := repo.AdoptTaskIdempotency(ctx, task.ScheduledTaskID, owner, request)
	if err != nil {
		t.Fatal(err)
	}
	if !repo.taskSnapshotAt.IsZero() {
		t.Fatal("adopting task idempotency did not invalidate the task snapshot")
	}
	if adopted.IdempotencyKey != request.IdempotencyKey || adopted.IdempotencyHash == "" {
		t.Fatalf("adopted task = %+v", adopted)
	}
	loaded, err := repo.GetTask(ctx, task.ScheduledTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.IdempotencyKey != adopted.IdempotencyKey || loaded.IdempotencyHash != adopted.IdempotencyHash {
		t.Fatalf("loaded task = %+v", loaded)
	}
	if _, err := repo.AdoptTaskIdempotency(ctx, task.ScheduledTaskID, owner, request); err != nil {
		t.Fatalf("idempotent adoption: %v", err)
	}
	request.IdempotencyKey = "different-key"
	if _, err := repo.AdoptTaskIdempotency(ctx, task.ScheduledTaskID, owner, request); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("different key error = %v, want ErrInvalidRequest", err)
	}
}

func TestRepositoryListsTasksWithInvalidUnrelatedRecord(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	task := schedulerTestTask("sched-isolated", schedulerTestTime.Add(time.Hour))
	if err := repo.SaveTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.Exec(ctx, `INSERT INTO platform_records (table_name, record_key, payload_json) VALUES (?, ?, ?)`, "aaa_unrelated", "broken", "{"); err != nil {
		t.Fatalf("insert invalid unrelated record: %v", err)
	}

	due, err := repo.ListDueTasks(ctx, schedulerTestTime.Add(2*time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].ScheduledTaskID != task.ScheduledTaskID {
		t.Fatalf("due tasks = %+v, want %s", due, task.ScheduledTaskID)
	}
}

func TestRepositoryTaskIdempotencyBypassesWorkspaceSnapshot(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	if _, err := repo.ListTasks(ctx, true); err != nil {
		t.Fatal(err)
	}

	task := schedulerTestTask("sched-external-idempotency", schedulerTestTime.Add(time.Hour))
	task.TenantID = "tenant-external"
	task.IdempotencyKey = "external-task-key"
	record, err := scheduledTaskRecord(task)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.DBRouter.PutRecord(ctx, scheduledTaskTable, task.ScheduledTaskID, record); err != nil {
		t.Fatal(err)
	}
	found, ok, err := repo.FindTaskByIdempotency(ctx, task.TenantID, task.CreatedBy, task.IdempotencyKey)
	if err != nil || !ok || found.ScheduledTaskID != task.ScheduledTaskID {
		t.Fatalf("external task idempotency lookup = %+v ok=%v err=%v", found, ok, err)
	}
}

func TestRepositoryListsDueEnabledTasksSortedByNextRunAt(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	later := schedulerTestTask("sched-later", schedulerTestTime.Add(2*time.Hour))
	earlier := schedulerTestTask("sched-earlier", schedulerTestTime.Add(time.Hour))
	disabled := schedulerTestTask("sched-disabled", schedulerTestTime.Add(30*time.Minute))
	disabled.Enabled = false
	disabled.Status = TaskStatusDisabled
	for _, task := range []ScheduledTask{later, disabled, earlier} {
		if err := repo.SaveTask(ctx, task); err != nil {
			t.Fatal(err)
		}
	}

	due, err := repo.ListDueTasks(ctx, schedulerTestTime.Add(90*time.Minute), 10)
	if err != nil {
		t.Fatal(err)
	}

	if len(due) != 1 || due[0].ScheduledTaskID != "sched-earlier" {
		t.Fatalf("due tasks = %+v", due)
	}
}

func TestRepositoryTaskSnapshotReusesReadsAndInvalidatesOnWrites(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	first := schedulerTestTask("sched-cache-first", schedulerTestTime.Add(time.Hour))
	if err := repo.SaveTask(ctx, first); err != nil {
		t.Fatal(err)
	}

	listed, err := repo.ListTasks(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	loadedAt := repo.taskSnapshotAt
	listed[0].Title = "caller mutation"
	again, err := repo.ListTasks(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if !repo.taskSnapshotAt.Equal(loadedAt) {
		t.Fatal("second list refreshed an unexpired task snapshot")
	}
	if len(again) != 1 || again[0].Title != first.Title {
		t.Fatalf("cached tasks = %+v, want an independent copy of %q", again, first.Title)
	}

	second := schedulerTestTask("sched-cache-second", schedulerTestTime.Add(2*time.Hour))
	if err := repo.SaveTask(ctx, second); err != nil {
		t.Fatal(err)
	}
	if !repo.taskSnapshotAt.IsZero() {
		t.Fatal("saving a task did not invalidate the task snapshot")
	}
	listed, err = repo.ListTasks(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 {
		t.Fatalf("tasks after save = %+v, want 2 tasks", listed)
	}

	if err := repo.DeleteTask(ctx, first.ScheduledTaskID); err != nil {
		t.Fatal(err)
	}
	if !repo.taskSnapshotAt.IsZero() {
		t.Fatal("deleting a task did not invalidate the task snapshot")
	}
	listed, err = repo.ListTasks(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].ScheduledTaskID != second.ScheduledTaskID {
		t.Fatalf("tasks after delete = %+v, want only %s", listed, second.ScheduledTaskID)
	}
}

func TestRepositorySavesRunsAndFindsLastRun(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	task := schedulerTestTask("sched-1", schedulerTestTime.Add(time.Hour))
	if err := repo.SaveTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	first := schedulerTestRun("run-1", "sched-1", RunStatusFailed, schedulerTestTime)
	last := schedulerTestRun("run-2", "sched-1", RunStatusCompleted, schedulerTestTime.Add(time.Minute))
	if err := repo.SaveRun(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveRun(ctx, last); err != nil {
		t.Fatal(err)
	}

	got, ok, err := repo.LastRunForTask(ctx, "sched-1")
	if err != nil {
		t.Fatal(err)
	}

	if !ok || got.ScheduledRunID != "run-2" || got.Status != RunStatusCompleted {
		t.Fatalf("last run = %+v ok=%v", got, ok)
	}
}

func TestRepositoryFindsLastRunsForTasksInOneBatch(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	for _, run := range []ScheduledRun{
		schedulerTestRun("run-a-1", "sched-a", RunStatusFailed, schedulerTestTime),
		schedulerTestRun("run-a-2", "sched-a", RunStatusCompleted, schedulerTestTime.Add(2*time.Minute)),
		schedulerTestRun("run-b-1", "sched-b", RunStatusRunning, schedulerTestTime.Add(time.Minute)),
		schedulerTestRun("run-ignored", "sched-ignored", RunStatusCompleted, schedulerTestTime.Add(3*time.Minute)),
	} {
		if err := repo.SaveRun(ctx, run); err != nil {
			t.Fatal(err)
		}
	}

	lastRuns, err := repo.LastRunsForTasks(ctx, []string{"sched-a", "sched-b", "sched-missing"})
	if err != nil {
		t.Fatal(err)
	}
	if len(lastRuns) != 2 || lastRuns["sched-a"].ScheduledRunID != "run-a-2" || lastRuns["sched-b"].ScheduledRunID != "run-b-1" {
		t.Fatalf("last runs = %+v", lastRuns)
	}
}

func TestRepositoryRunSnapshotReusesReadsAndInvalidatesOnWrites(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	first := schedulerTestRun("run-cache-first", "sched-cache", RunStatusCompleted, schedulerTestTime)
	if err := repo.SaveRun(ctx, first); err != nil {
		t.Fatal(err)
	}

	lastRuns, err := repo.LastRunsForTasks(ctx, []string{first.ScheduledTaskID})
	if err != nil || lastRuns[first.ScheduledTaskID].ScheduledRunID != first.ScheduledRunID {
		t.Fatalf("first last runs = %+v err=%v", lastRuns, err)
	}
	loadedAt := repo.runSnapshotAt
	if _, err := repo.LastRunsForTasks(ctx, []string{first.ScheduledTaskID}); err != nil {
		t.Fatal(err)
	}
	if !repo.runSnapshotAt.Equal(loadedAt) {
		t.Fatal("second run list refreshed an unexpired snapshot")
	}

	second := schedulerTestRun("run-cache-second", first.ScheduledTaskID, RunStatusRunning, schedulerTestTime.Add(time.Minute))
	if err := repo.SaveRun(ctx, second); err != nil {
		t.Fatal(err)
	}
	if !repo.runSnapshotAt.IsZero() {
		t.Fatal("saving a run did not invalidate the run snapshot")
	}
	lastRuns, err = repo.LastRunsForTasks(ctx, []string{first.ScheduledTaskID})
	if err != nil || lastRuns[first.ScheduledTaskID].ScheduledRunID != second.ScheduledRunID {
		t.Fatalf("last runs after save = %+v err=%v", lastRuns, err)
	}

	if _, err := repo.MutateRun(ctx, second.ScheduledRunID, func(current *ScheduledRun) error {
		current.FailureReason = "updated"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !repo.runSnapshotAt.IsZero() {
		t.Fatal("mutating a run did not invalidate the run snapshot")
	}
	runs, err := repo.ListRunsForTask(ctx, first.ScheduledTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 || runs[1].FailureReason != "updated" {
		t.Fatalf("runs after mutation = %+v", runs)
	}
}

func TestRepositoryManualRunIdempotencyBypassesRunSnapshot(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	if _, err := repo.LastRunsForTasks(ctx, []string{"sched-external-run"}); err != nil {
		t.Fatal(err)
	}

	run := schedulerTestRun("run-external-idempotency", "sched-external-run", RunStatusQueued, schedulerTestTime)
	run.TriggerType = RunTriggerManual
	run.RequestedBy = "operator"
	run.IdempotencyKey = "external-run-key"
	record, err := scheduledRunRecord(run)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.DBRouter.PutRecord(ctx, scheduledRunTable, run.ScheduledRunID, record); err != nil {
		t.Fatal(err)
	}
	found, ok, err := repo.FindManualRunByIdempotency(ctx, run.ScheduledTaskID, run.RequestedBy, run.IdempotencyKey)
	if err != nil || !ok || found.ScheduledRunID != run.ScheduledRunID {
		t.Fatalf("external run idempotency lookup = %+v ok=%v err=%v", found, ok, err)
	}
}

func TestRepositoryDetectsActiveRuns(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	if err := repo.SaveRun(ctx, schedulerTestRun("run-1", "sched-1", RunStatusQueued, schedulerTestTime)); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveRun(ctx, schedulerTestRun("run-2", "sched-2", RunStatusCompleted, schedulerTestTime)); err != nil {
		t.Fatal(err)
	}

	active, err := repo.HasActiveRun(ctx, "sched-1")
	if err != nil {
		t.Fatal(err)
	}
	inactive, err := repo.HasActiveRun(ctx, "sched-2")
	if err != nil {
		t.Fatal(err)
	}

	if !active || inactive {
		t.Fatalf("active sched-1=%v sched-2=%v", active, inactive)
	}
}

func TestRepositoryListsOnlyIncompleteScheduledDeliveries(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	newRun := func(id, deliveryStatus string) ScheduledRun {
		run := schedulerTestRun(id, "sched-delivery", RunStatusCompleted, schedulerTestTime)
		run.TargetMode = TargetModeAgentTask
		run.ConfigurationSnapshotVersion = 1
		run.Delivery = DeliveryPolicy{Mode: "announce", Channel: "feishu"}
		run.DeliveryStatus = deliveryStatus
		return run
	}
	failedWithResult := newRun("run-delivery-failed-pending", "pending")
	failedWithResult.Status = RunStatusFailed
	failedWithResult.FailureReason = "acceptance criteria blocked"
	failedWithResult.Result = RunResult{Summary: failedWithResult.FailureReason}
	failedWithoutResult := newRun("run-delivery-failed-empty", "pending")
	failedWithoutResult.Status = RunStatusFailed
	for _, run := range []ScheduledRun{
		newRun("run-delivery-empty", ""),
		failedWithResult,
		failedWithoutResult,
		newRun("run-delivery-pending", "pending"),
		newRun("run-delivery-retrying", "retrying"),
		newRun("run-delivery-sending", "sending"),
		newRun("run-delivery-delivered", "delivered"),
		newRun("run-delivery-dead", "dead_letter"),
		schedulerTestRun("run-delivery-active", "sched-delivery", RunStatusRunning, schedulerTestTime),
	} {
		if err := repo.SaveRun(ctx, run); err != nil {
			t.Fatal(err)
		}
	}
	runs, err := repo.ListPendingDeliveryRuns(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 5 {
		t.Fatalf("pending delivery runs = %+v", runs)
	}
	for i, id := range []string{"run-delivery-empty", "run-delivery-failed-pending", "run-delivery-pending", "run-delivery-retrying", "run-delivery-sending"} {
		if runs[i].ScheduledRunID != id {
			t.Fatalf("pending delivery run %d = %q, want %q", i, runs[i].ScheduledRunID, id)
		}
	}
}

func TestRepositoryMutateRunSerializesConcurrentDiagnostics(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	run := schedulerTestRun("run-concurrent-mutation", "sched-concurrent-mutation", RunStatusRunning, schedulerTestTime)
	run.TargetMode = TargetModeTodoAutomationScan
	if err := repo.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errs := make(chan error, 2)
	go func() {
		<-start
		_, err := repo.MutateRun(ctx, run.ScheduledRunID, func(current *ScheduledRun) error {
			current.CommandID = "todo-automation:run-concurrent-mutation"
			return nil
		})
		errs <- err
	}()
	go func() {
		<-start
		_, err := repo.MutateRun(ctx, run.ScheduledRunID, func(current *ScheduledRun) error {
			current.TodoAutomationScan = &todoapp.TodoAutomationScanResult{EligibleCount: 1, PlannedCount: 1}
			return nil
		})
		errs <- err
	}()
	close(start)
	for index := 0; index < 2; index++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	stored, err := repo.GetRun(ctx, run.ScheduledRunID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.CommandID == "" || stored.TodoAutomationScan == nil || stored.TodoAutomationScan.PlannedCount != 1 {
		t.Fatalf("concurrent diagnostics = %+v", stored)
	}
}

func TestRepositoryMutateRunWithTaskUsesLatestLockedTaskConfiguration(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repository := NewRepository(env.DBRouter)
	ctx := context.Background()
	task := schedulerTestTask("sched-run-with-task", schedulerTestTime)
	task.Target.ResultContract = AgentResultContract{Artifacts: []ArtifactRequirement{{ID: "original", Kind: "report", PathPattern: "/workspace/original.md"}}}
	if err := repository.SaveTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	run := schedulerTestRun("run-with-task", task.ScheduledTaskID, RunStatusCompleted, schedulerTestTime)
	if err := repository.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	task.Target.ResultContract.Artifacts[0].ID = "updated"
	if err := repository.SaveTask(ctx, task); err != nil {
		t.Fatal(err)
	}

	updated, err := repository.MutateRunWithTask(ctx, task.ScheduledTaskID, run.ScheduledRunID, func(currentTask ScheduledTask, current *ScheduledRun) error {
		if len(currentTask.Target.ResultContract.Artifacts) != 1 || currentTask.Target.ResultContract.Artifacts[0].ID != "updated" {
			t.Fatalf("locked task = %+v", currentTask.Target.ResultContract)
		}
		current.DeliveryStatus = "migrated"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.DeliveryStatus != "migrated" {
		t.Fatalf("updated run = %+v", updated)
	}
	persisted, err := repository.GetRun(ctx, run.ScheduledRunID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.DeliveryStatus != "migrated" {
		t.Fatalf("persisted run = %+v", persisted)
	}
}

func TestCleanupTodoAutomationRunsAppliesRetentionWithoutDeletingActiveRuns(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	now := schedulerTestTime.Add(100 * 24 * time.Hour)
	empty := todoapp.TodoAutomationScanResult{}
	nonEmpty := todoapp.TodoAutomationScanResult{EligibleCount: 1}
	runs := []ScheduledRun{
		retentionTestRun("empty-expired", RunStatusCompleted, now.Add(-8*24*time.Hour), &empty),
		retentionTestRun("empty-recent-occurrence", RunStatusCompleted, now.Add(-20*24*time.Hour), &empty),
		retentionTestRun("non-empty-expired", RunStatusCompleted, now.Add(-91*24*time.Hour), &nonEmpty),
		retentionTestRun("failed-expired", RunStatusFailed, now.Add(-91*24*time.Hour), nil),
		retentionTestRun("skipped-expired", RunStatusSkipped, now.Add(-91*24*time.Hour), nil),
		retentionTestRun("non-empty-retained", RunStatusCompleted, now.Add(-89*24*time.Hour), &nonEmpty),
		retentionTestRun("skipped-retained", RunStatusSkipped, now.Add(-89*24*time.Hour), nil),
		retentionTestRun("queued-retained", RunStatusQueued, now.Add(-200*24*time.Hour), nil),
		retentionTestRun("running-retained", RunStatusRunning, now.Add(-200*24*time.Hour), nil),
	}
	runs[1].LastOccurrenceAt = now.Add(-24 * time.Hour)
	for _, run := range runs {
		if err := repo.SaveRun(ctx, run); err != nil {
			t.Fatalf("SaveRun(%s): %v", run.ScheduledRunID, err)
		}
	}
	if _, err := repo.LastRunsForTasks(ctx, []string{"todo-automation-retention"}); err != nil {
		t.Fatal(err)
	}
	readAt := now.Add(-time.Hour)
	if err := repo.SetRunReadState(ctx, scheduledRunReadReceipt{
		TenantID: "system", ViewerID: "viewer-a", ViewerKind: ViewerKindHuman,
		ScheduledRunID: "empty-expired", ReadAt: readAt, UpdatedAt: readAt,
	}, true); err != nil {
		t.Fatal(err)
	}
	deleted, err := repo.CleanupTodoAutomationRuns(ctx, now, 7*24*time.Hour, 90*24*time.Hour)
	if err != nil || deleted != 4 {
		t.Fatalf("cleanup deleted=%d err=%v", deleted, err)
	}
	if !repo.runSnapshotAt.IsZero() {
		t.Fatal("cleaning up runs did not invalidate the run snapshot")
	}
	for _, id := range []string{"empty-expired", "non-empty-expired", "failed-expired", "skipped-expired"} {
		if _, err := repo.GetRun(ctx, id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("GetRun(%s) error = %v, want ErrNotFound", id, err)
		}
	}
	receipts, err := repo.ListRunReadReceipts(ctx, "system", "viewer-a", ViewerKindHuman)
	if err != nil {
		t.Fatal(err)
	}
	if len(receipts) != 0 {
		t.Fatalf("retained orphaned Todo automation run read receipts: %+v", receipts)
	}
	for _, id := range []string{"empty-recent-occurrence", "non-empty-retained", "skipped-retained", "queued-retained", "running-retained"} {
		if _, err := repo.GetRun(ctx, id); err != nil {
			t.Fatalf("GetRun(%s): %v", id, err)
		}
	}
}

func TestCoalesceEmptyTodoAutomationRunsSerializesConcurrentOccurrences(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	empty := todoapp.TodoAutomationScanResult{}
	runs := []ScheduledRun{
		retentionTestRun("empty-concurrent-a", RunStatusCompleted, schedulerTestTime, &empty),
		retentionTestRun("empty-concurrent-b", RunStatusCompleted, schedulerTestTime.Add(time.Second), &empty),
	}
	for index := range runs {
		runs[index].ScheduledTaskID = "todo-automation-concurrent"
	}
	var wait sync.WaitGroup
	errorsByRun := make([]error, len(runs))
	for index := range runs {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			_, _, errorsByRun[index] = repo.CoalesceEmptyTodoAutomationRun(ctx, runs[index], 24*time.Hour)
		}(index)
	}
	wait.Wait()
	for index, err := range errorsByRun {
		if err != nil {
			t.Fatalf("coalesce run %d: %v", index, err)
		}
	}
	stored, err := repo.ListRunsForTask(ctx, "todo-automation-concurrent")
	if err != nil || len(stored) != 1 || stored[0].AggregatedOccurrences != 2 || !stored[0].LastOccurrenceAt.Equal(schedulerTestTime.Add(time.Second)) {
		t.Fatalf("concurrent aggregate = %+v err=%v", stored, err)
	}
}

func TestCoalesceEmptyTodoAutomationRunHandlesOutOfOrderCompletion(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	empty := todoapp.TodoAutomationScanResult{}
	later := retentionTestRun("empty-out-of-order-later", RunStatusCompleted, schedulerTestTime.Add(time.Minute), &empty)
	earlier := retentionTestRun("empty-out-of-order-earlier", RunStatusCompleted, schedulerTestTime, &empty)
	later.ScheduledTaskID = "todo-automation-out-of-order"
	earlier.ScheduledTaskID = later.ScheduledTaskID

	if _, aggregated, err := repo.CoalesceEmptyTodoAutomationRun(ctx, later, 24*time.Hour); err != nil || aggregated {
		t.Fatalf("store later occurrence: aggregated=%v err=%v", aggregated, err)
	}
	if _, err := repo.LastRunsForTasks(ctx, []string{later.ScheduledTaskID}); err != nil {
		t.Fatal(err)
	}
	if aggregatedInto, aggregated, err := repo.CoalesceEmptyTodoAutomationRun(ctx, earlier, 24*time.Hour); err != nil || !aggregated || aggregatedInto != later.ScheduledRunID {
		t.Fatalf("coalesce earlier occurrence: into=%q aggregated=%v err=%v", aggregatedInto, aggregated, err)
	}
	if !repo.runSnapshotAt.IsZero() {
		t.Fatal("coalescing runs did not invalidate the run snapshot")
	}
	stored, err := repo.ListRunsForTask(ctx, later.ScheduledTaskID)
	if err != nil || len(stored) != 1 || stored[0].AggregatedOccurrences != 2 || !stored[0].LastOccurrenceAt.Equal(later.CompletedAt) {
		t.Fatalf("out-of-order aggregate = %+v err=%v", stored, err)
	}
}

func retentionTestRun(id string, status RunStatus, at time.Time, result *todoapp.TodoAutomationScanResult) ScheduledRun {
	run := schedulerTestRun(id, "todo-automation-retention", status, at)
	run.TargetMode = TargetModeTodoAutomationScan
	run.TodoAutomationScan = result
	if status == RunStatusCompleted || status == RunStatusFailed {
		run.CompletedAt = at
	}
	return run
}

func schedulerTestTask(id string, nextRunAt time.Time) ScheduledTask {
	return ScheduledTask{
		ScheduledTaskID:  id,
		Title:            "Task " + id,
		Schedule:         Schedule{Type: ScheduleTypeOnce, RunAt: nextRunAt},
		Target:           Target{Mode: TargetModeDiscussionWorkflow, Message: "Run task."},
		Policy:           Policy{Misfire: MisfireRunOnce, Concurrency: ConcurrencySkipIfRunning},
		Status:           TaskStatusEnabled,
		Enabled:          true,
		NextRunAt:        nextRunAt,
		CreatedBy:        "operator",
		CreatedByKind:    ViewerKindHuman,
		CreatedByName:    "Operator",
		CreatorProfileID: "human-operator",
		CreatedAt:        schedulerTestTime,
		UpdatedAt:        schedulerTestTime,
	}
}

func schedulerTestRun(id string, taskID string, status RunStatus, createdAt time.Time) ScheduledRun {
	return ScheduledRun{
		ScheduledRunID:  id,
		ScheduledTaskID: taskID,
		TriggerType:     RunTriggerDue,
		Status:          status,
		TargetMode:      TargetModeDiscussionWorkflow,
		Attempt:         1,
		CreatedAt:       createdAt,
	}
}
