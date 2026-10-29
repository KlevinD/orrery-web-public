package scheduler

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/idgen"
	"orrery_v2/internal/platform/testkit"
)

var schedulerTestTime = time.Date(2026, 6, 8, 9, 30, 0, 0, time.UTC)

func schedulerTestViewer() ViewerRef {
	return ViewerRef{
		ViewerID:        "operator",
		ViewerKind:      ViewerKindHuman,
		DisplayName:     "Operator",
		ProfileID:       "human-operator",
		PermissionScope: []string{"scheduler.read", "scheduler.write"},
	}
}

func TestServiceCreateTaskRecordsCreatorProfile(t *testing.T) {
	service := newServiceForTest()

	task, err := service.CreateTask(context.Background(), ViewerRef{
		ViewerID:    "operator",
		ViewerKind:  ViewerKindHuman,
		DisplayName: "Operator",
		ProfileID:   "human-operator",
	}, CreateTaskRequest{
		Title:          "Creator-bound task",
		Schedule:       Schedule{Type: ScheduleTypeOnce, RunAt: schedulerTestTime.Add(time.Hour)},
		Target:         Target{Mode: TargetModeDiscussionWorkflow, Message: "Run with creator permissions."},
		IdempotencyKey: "creator-profile",
	})
	if err != nil {
		t.Fatal(err)
	}

	if task.CreatedBy != "operator" || task.CreatedByKind != ViewerKindHuman || task.CreatedByName != "Operator" || task.CreatorProfileID != "human-operator" {
		t.Fatalf("creator fields = created_by:%q kind:%q name:%q profile:%q", task.CreatedBy, task.CreatedByKind, task.CreatedByName, task.CreatorProfileID)
	}
}

func TestServiceCreateOnceTaskComputesNextRunAt(t *testing.T) {
	service := newServiceForTest()
	runAt := schedulerTestTime.Add(2 * time.Hour)

	task, err := service.CreateTask(context.Background(), schedulerTestViewer(), CreateTaskRequest{
		Title: "Daily competitor scan",
		Schedule: Schedule{
			Type:  ScheduleTypeOnce,
			RunAt: runAt,
		},
		Target: Target{
			Mode:    TargetModeDiscussionWorkflow,
			Message: "Complete the scan and close with evidence.",
		},
		IdempotencyKey: "scan-once",
	})
	if err != nil {
		t.Fatal(err)
	}

	if task.ScheduledTaskID != "sched-000001" {
		t.Fatalf("scheduled task id = %q", task.ScheduledTaskID)
	}
	if !task.NextRunAt.Equal(runAt) {
		t.Fatalf("next_run_at = %s, want %s", task.NextRunAt, runAt)
	}
	if task.Status != TaskStatusEnabled {
		t.Fatalf("status = %q, want enabled", task.Status)
	}
	if task.AgentTaskID != "scheduled-task:sched-000001" {
		t.Fatalf("agent_task_id = %q, want stable scheduled-task identity", task.AgentTaskID)
	}
}

func TestServiceCreateCronTaskComputesNextFutureRunAt(t *testing.T) {
	service := newServiceForTest()

	task, err := service.CreateTask(context.Background(), schedulerTestViewer(), CreateTaskRequest{
		Title: "Daily digest",
		Schedule: Schedule{
			Type:     ScheduleTypeCron,
			Cron:     "45 9 * * *",
			Timezone: "UTC",
		},
		Target: Target{
			Mode:    TargetModeDiscussionWorkflow,
			Message: "Create the daily digest.",
		},
		IdempotencyKey: "digest-daily",
	})
	if err != nil {
		t.Fatal(err)
	}

	want := time.Date(2026, 6, 8, 9, 45, 0, 0, time.UTC)
	if !task.NextRunAt.Equal(want) {
		t.Fatalf("next_run_at = %s, want %s", task.NextRunAt, want)
	}
}

func TestServiceUpdateDisabledCronTaskRecalculatesNextRunAt(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	service := newServiceForTestWithRepository(repo)
	disabled := false
	created, err := service.CreateTask(context.Background(), schedulerTestViewer(), CreateTaskRequest{
		Title:          "Disabled daily digest",
		Schedule:       Schedule{Type: ScheduleTypeCron, Cron: "45 9 * * *", Timezone: "UTC"},
		Target:         Target{Mode: TargetModeDiscussionWorkflow, Message: "Create the daily digest."},
		Enabled:        &disabled,
		IdempotencyKey: "disabled-digest-daily",
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 6, 8, 9, 45, 0, 0, time.UTC); !created.NextRunAt.Equal(want) {
		t.Fatalf("created next_run_at = %s, want %s", created.NextRunAt, want)
	}

	schedule := Schedule{Type: ScheduleTypeCron, Cron: "0 12 * * *", Timezone: "UTC"}
	updated, err := service.UpdateTask(context.Background(), schedulerTestViewer(), UpdateTaskRequest{
		ScheduledTaskID: created.ScheduledTaskID,
		Schedule:        &schedule,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	if updated.Enabled || updated.Status != TaskStatusDisabled {
		t.Fatalf("updated enabled=%t status=%q, want disabled", updated.Enabled, updated.Status)
	}
	if !updated.NextRunAt.Equal(want) {
		t.Fatalf("updated next_run_at = %s, want %s", updated.NextRunAt, want)
	}
}

func TestServiceReconcilesMissingAndStaleDisabledOccurrencesIdempotently(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	sink := &recordingProjectionSink{}
	repo.SetProjectionSink(sink)
	service := newServiceForTestWithRepository(repo)
	ctx := context.Background()

	missing := schedulerTestTask("disabled-missing", time.Time{})
	missing.Schedule = Schedule{Type: ScheduleTypeCron, Cron: "*/15 * * * *", Timezone: "UTC"}
	missing.Enabled = false
	missing.Status = TaskStatusDisabled
	stale := schedulerTestTask("disabled-stale", schedulerTestTime.Add(-time.Hour))
	stale.Schedule = Schedule{Type: ScheduleTypeCron, Cron: "*/15 * * * *", Timezone: "UTC"}
	stale.Enabled = false
	stale.Status = TaskStatusDisabled
	future := schedulerTestTask("disabled-future", schedulerTestTime.Add(30*time.Minute))
	future.Schedule = Schedule{Type: ScheduleTypeCron, Cron: "0 10 * * *", Timezone: "UTC"}
	future.Enabled = false
	future.Status = TaskStatusDisabled
	archived := schedulerTestTask("archived-missing", time.Time{})
	archived.Schedule = Schedule{Type: ScheduleTypeCron, Cron: "*/15 * * * *", Timezone: "UTC"}
	archived.Enabled = false
	archived.Status = TaskStatusArchived
	invalid := schedulerTestTask("disabled-invalid", time.Time{})
	invalid.Schedule = Schedule{Type: ScheduleTypeCron, Cron: "not-a-cron", Timezone: "UTC"}
	invalid.Enabled = false
	invalid.Status = TaskStatusDisabled
	for _, task := range []ScheduledTask{missing, stale, future, archived, invalid} {
		if err := repo.SaveTask(ctx, task); err != nil {
			t.Fatal(err)
		}
	}
	sink.tasks = nil

	report, err := service.ReconcileDisabledOccurrences(ctx, schedulerTestTime)
	if err != nil {
		t.Fatal(err)
	}
	if report.Repaired != 2 || len(report.Issues) != 1 || report.Issues[0].TaskID != invalid.ScheduledTaskID {
		t.Fatalf("reconcile report = %+v", report)
	}
	want := time.Date(2026, 6, 8, 9, 45, 0, 0, time.UTC)
	for _, original := range []ScheduledTask{missing, stale} {
		taskID := original.ScheduledTaskID
		stored, getErr := repo.GetTask(ctx, taskID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if stored.Enabled || stored.Status != TaskStatusDisabled || !stored.NextRunAt.Equal(want) {
			t.Fatalf("reconciled task %s = %+v, want disabled at %s", taskID, stored, want)
		}
		if !stored.UpdatedAt.Equal(original.UpdatedAt) {
			t.Fatalf("reconciled task %s updated_at = %s, want unchanged %s", taskID, stored.UpdatedAt, original.UpdatedAt)
		}
	}
	storedFuture, err := repo.GetTask(ctx, future.ScheduledTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if !storedFuture.NextRunAt.Equal(future.NextRunAt) {
		t.Fatalf("future next_run_at = %s, want unchanged %s", storedFuture.NextRunAt, future.NextRunAt)
	}
	storedArchived, err := repo.GetTask(ctx, archived.ScheduledTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if !storedArchived.NextRunAt.IsZero() {
		t.Fatalf("archived next_run_at = %s, want zero", storedArchived.NextRunAt)
	}
	if len(sink.tasks) != 0 {
		t.Fatalf("projected occurrence-only repairs = %d, want 0", len(sink.tasks))
	}

	second, err := service.ReconcileDisabledOccurrences(ctx, schedulerTestTime)
	if err != nil {
		t.Fatal(err)
	}
	if second.Repaired != 0 || len(second.Issues) != 1 || len(sink.tasks) != 0 {
		t.Fatalf("second reconcile report=%+v projected=%d", second, len(sink.tasks))
	}
}

func TestServiceCreateTaskPreservesExecutionAndDeliveryPolicy(t *testing.T) {
	service := newServiceForTest()
	task, err := service.CreateTask(context.Background(), schedulerTestViewer(), CreateTaskRequest{
		Title:    "Native scheduled task",
		Schedule: Schedule{Type: ScheduleTypeCron, Cron: "30 8 * * *", Timezone: "Asia/Shanghai"},
		Target: Target{Mode: TargetModeAgentTask, AgentID: "tiedan", TaskGoal: "Collect intelligence", ResultContract: AgentResultContract{Artifacts: []ArtifactRequirement{{
			ID: "rss-briefing", Kind: "report", PathPattern: filepath.Join(t.TempDir(), "rss-briefing-*.md"),
		}}}},
		Execution: ExecutionPolicy{
			TimeoutSeconds: 1200, LLMProfileID: " llm-minimax ", SourceModel: " minimax/MiniMax-M3 ",
			AllowedToolIDs: []string{" mcp.marketing.marketing_wechat_draft_create ", "mcp.marketing.marketing_wechat_draft_create"},
		},
		Delivery:       DeliveryPolicy{Mode: " announce ", Channel: " feishu ", RecipientID: " oc_group ", AccountID: " main ", ContentArtifactID: " rss-briefing ", Format: " markdown ", BestEffort: true},
		IdempotencyKey: "native-scheduled-task",
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.Execution.TimeoutSeconds != 1200 || task.Execution.LLMProfileID != "llm-minimax" || task.Execution.SourceModel != "minimax/MiniMax-M3" {
		t.Fatalf("execution policy = %+v", task.Execution)
	}
	if len(task.Execution.AllowedToolIDs) != 1 || task.Execution.AllowedToolIDs[0] != "mcp.marketing.marketing_wechat_draft_create" {
		t.Fatalf("allowed tool IDs = %+v", task.Execution.AllowedToolIDs)
	}
	if task.Delivery.Mode != "announce" || task.Delivery.Channel != "feishu" || task.Delivery.RecipientID != "oc_group" || task.Delivery.AccountID != "main" || task.Delivery.ContentArtifactID != "rss-briefing" || task.Delivery.Format != "markdown" {
		t.Fatalf("delivery policy = %+v", task.Delivery)
	}
}

func TestServiceCreateAgentTaskPersistsMissionBinding(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	missions := &recordingScheduledMissionLifecycle{missionID: "mission-native-schedule"}
	service := NewService(ServiceOptions{
		Clock: clock.NewFake(schedulerTestTime), IDGenerator: idgen.NewDeterministic("sched"), Repository: repo, MissionLifecycle: missions,
	})

	created, err := service.CreateTask(context.Background(), schedulerTestViewer(), CreateTaskRequest{
		Title:          "Native scheduled task",
		Schedule:       Schedule{Type: ScheduleTypeCron, Cron: "30 8 * * *", Timezone: "Asia/Shanghai"},
		Target:         Target{Mode: TargetModeAgentTask, AgentID: "tiedan", TaskGoal: "Collect intelligence"},
		IdempotencyKey: "native-scheduled-mission",
	})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := repo.GetTask(context.Background(), created.ScheduledTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if created.MissionID != missions.missionID || stored.MissionID != missions.missionID {
		t.Fatalf("Mission binding = created:%q stored:%q, want %q", created.MissionID, stored.MissionID, missions.missionID)
	}
	if len(missions.tasks) != 1 || missions.tasks[0].ScheduledTaskID != created.ScheduledTaskID {
		t.Fatalf("Mission lifecycle tasks = %+v", missions.tasks)
	}
}

func TestServiceDoesNotPersistAgentTaskWhenMissionEnsureFails(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	failure := errors.New("Mission store unavailable")
	service := NewService(ServiceOptions{
		Clock: clock.NewFake(schedulerTestTime), IDGenerator: idgen.NewDeterministic("sched"), Repository: repo,
		MissionLifecycle: &recordingScheduledMissionLifecycle{err: failure},
	})

	_, err := service.CreateTask(context.Background(), schedulerTestViewer(), CreateTaskRequest{
		Title:          "Mission-bound schedule",
		Schedule:       Schedule{Type: ScheduleTypeOnce, RunAt: schedulerTestTime.Add(time.Hour)},
		Target:         Target{Mode: TargetModeAgentTask, AgentID: "tiedan", TaskGoal: "Collect intelligence"},
		IdempotencyKey: "mission-ensure-failure",
	})
	if !errors.Is(err, failure) {
		t.Fatalf("CreateTask error = %v, want %v", err, failure)
	}
	page, listErr := service.ListTasks(context.Background(), ListTasksQuery{TenantID: "system", IncludeArchived: true})
	if listErr != nil {
		t.Fatal(listErr)
	}
	if page.Total != 0 {
		t.Fatalf("persisted tasks after Mission failure = %+v", page.Tasks)
	}
}

func TestServiceSynchronizesAgentTaskMissionOnUpdateAndCancel(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	missions := &recordingScheduledMissionLifecycle{missionID: "mission-lifecycle"}
	service := NewService(ServiceOptions{
		Clock: clock.NewFake(schedulerTestTime), IDGenerator: idgen.NewDeterministic("sched"), Repository: repo, MissionLifecycle: missions,
	})
	created, err := service.CreateTask(context.Background(), schedulerTestViewer(), CreateTaskRequest{
		Title:          "Mission lifecycle schedule",
		Schedule:       Schedule{Type: ScheduleTypeCron, Cron: "*/5 * * * *", Timezone: "UTC"},
		Target:         Target{Mode: TargetModeAgentTask, AgentID: "tiedan", TaskGoal: "Collect intelligence"},
		IdempotencyKey: "mission-lifecycle",
	})
	if err != nil {
		t.Fatal(err)
	}
	disabled := false
	updated, err := service.UpdateTask(context.Background(), schedulerTestViewer(), UpdateTaskRequest{ScheduledTaskID: created.ScheduledTaskID, Enabled: &disabled})
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := service.CancelTask(context.Background(), schedulerTestViewer(), CancelTaskRequest{ScheduledTaskID: created.ScheduledTaskID})
	if err != nil {
		t.Fatal(err)
	}
	if len(missions.tasks) != 3 || missions.tasks[1].Status != TaskStatusDisabled || missions.tasks[2].Status != TaskStatusArchived {
		t.Fatalf("Mission lifecycle snapshots = %+v", missions.tasks)
	}
	if updated.MissionID != missions.missionID || cancelled.MissionID != missions.missionID {
		t.Fatalf("Mission binding changed: updated=%q cancelled=%q", updated.MissionID, cancelled.MissionID)
	}
}

func TestServiceSynchronizesAgentTaskMissionAfterOneTimeRun(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	missions := &recordingScheduledMissionLifecycle{missionID: "mission-once"}
	service := NewService(ServiceOptions{
		Clock: clock.NewFake(schedulerTestTime), IDGenerator: idgen.NewDeterministic("sched"), Repository: repo, MissionLifecycle: missions,
	})
	task := schedulerTestTask("agent-once", schedulerTestTime)
	task.TenantID = "system"
	task.MissionID = missions.missionID
	task.Target = Target{Mode: TargetModeAgentTask, AgentID: "tiedan", TaskGoal: "Run once"}
	if err := repo.SaveTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}

	advanced, err := service.AdvanceAfterDueRun(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	if advanced.Status != TaskStatusDisabled || len(missions.tasks) != 1 || missions.tasks[0].Status != TaskStatusDisabled {
		t.Fatalf("advanced task = %+v, Mission snapshots = %+v", advanced, missions.tasks)
	}
}

func TestServiceDoesNotCreateMissionForOtherTargetModes(t *testing.T) {
	missions := &recordingScheduledMissionLifecycle{missionID: "unexpected"}
	service := NewService(ServiceOptions{
		Clock: clock.NewFake(schedulerTestTime), IDGenerator: idgen.NewDeterministic("sched"), MissionLifecycle: missions,
	})
	created, err := service.CreateTask(context.Background(), schedulerTestViewer(), CreateTaskRequest{
		Title:          "Discussion schedule",
		Schedule:       Schedule{Type: ScheduleTypeOnce, RunAt: schedulerTestTime.Add(time.Hour)},
		Target:         Target{Mode: TargetModeDiscussionWorkflow, Message: "Run the discussion"},
		IdempotencyKey: "discussion-no-mission",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.MissionID != "" || len(missions.tasks) != 0 {
		t.Fatalf("non-Agent schedule Mission state = task:%+v calls:%+v", created, missions.tasks)
	}
}

type recordingScheduledMissionLifecycle struct {
	missionID string
	err       error
	tasks     []ScheduledTask
}

func (l *recordingScheduledMissionLifecycle) EnsureScheduledMission(_ context.Context, task ScheduledTask) (string, error) {
	l.tasks = append(l.tasks, task)
	if l.err != nil {
		return "", l.err
	}
	return l.missionID, nil
}

type blockingScheduledMissionLifecycle struct {
	missionID string
	entered   chan struct{}
	release   chan struct{}
	tasks     []ScheduledTask
}

func (l *blockingScheduledMissionLifecycle) EnsureScheduledMission(ctx context.Context, task ScheduledTask) (string, error) {
	l.tasks = append(l.tasks, task)
	select {
	case l.entered <- struct{}{}:
	default:
	}
	select {
	case <-l.release:
		return l.missionID, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

type scheduledTaskMutationResult struct {
	task ScheduledTask
	err  error
}

func waitForScheduledMissionLifecycle(t *testing.T, entered <-chan struct{}) {
	t.Helper()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for scheduled Mission lifecycle")
	}
}

func waitForScheduledTaskMutation(t *testing.T, result <-chan scheduledTaskMutationResult) scheduledTaskMutationResult {
	t.Helper()
	select {
	case got := <-result:
		return got
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for scheduled task mutation")
		return scheduledTaskMutationResult{}
	}
}

func TestServiceArchiveSystemManagedTaskDoesNotInvokeMissionLifecycle(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	lifecycle := &recordingScheduledMissionLifecycle{err: errors.New("Mission service unavailable")}
	service := NewService(ServiceOptions{Clock: env.Clock, IDGenerator: env.IDGenerator, Repository: repo, MissionLifecycle: lifecycle})
	task := ScheduledTask{
		TenantID: "tenant-a", ScheduledTaskID: "legacy-system-agent-task", Title: "Legacy system task",
		Target:  Target{Mode: TargetModeAgentTask, AgentID: "agent-a", TaskGoal: "legacy work", Prompt: "legacy work"},
		Enabled: true, Status: TaskStatusEnabled, CreatedBy: "system:legacy", CreatedByKind: ViewerKindSystem,
		CreatorProfileID: "agent-a", CreatedAt: env.Clock.Now(), UpdatedAt: env.Clock.Now(),
	}
	if err := repo.SaveTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	viewer := ViewerRef{TenantID: task.TenantID, ViewerID: task.CreatedBy, ViewerKind: ViewerKindSystem, ProfileID: task.CreatorProfileID}
	archived, err := service.ArchiveSystemManagedTask(context.Background(), viewer, CancelTaskRequest{ScheduledTaskID: task.ScheduledTaskID})
	if err != nil {
		t.Fatal(err)
	}
	if archived.Enabled || archived.Status != TaskStatusArchived || len(lifecycle.tasks) != 0 {
		t.Fatalf("archived=%+v lifecycle calls=%d", archived, len(lifecycle.tasks))
	}
	if !archived.UpdatedAt.After(task.UpdatedAt) {
		t.Fatalf("archive updated_at = %s, want after %s", archived.UpdatedAt, task.UpdatedAt)
	}

	task.ScheduledTaskID = "user-task"
	task.CreatedByKind = ViewerKindHuman
	if err := repo.SaveTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ArchiveSystemManagedTask(context.Background(), viewer, CancelTaskRequest{ScheduledTaskID: task.ScheduledTaskID}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("user task archive error = %v, want ErrInvalidRequest", err)
	}
}

func TestServiceArchiveSystemManagedTaskWinsAgainstStaleUpdate(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	writerRepository := NewRepository(env.DBRouter)
	archiveRepository := NewRepository(env.DBRouter)
	lifecycle := &blockingScheduledMissionLifecycle{
		missionID: "mission-stale-update",
		entered:   make(chan struct{}, 1),
		release:   make(chan struct{}),
	}
	writer := NewService(ServiceOptions{Clock: env.Clock, Repository: writerRepository, MissionLifecycle: lifecycle})
	archiver := NewService(ServiceOptions{Clock: env.Clock, Repository: archiveRepository})
	task := schedulerTestTask("system-task-stale-update", env.Clock.Now().Add(time.Minute))
	task.TenantID = "tenant-a"
	task.MissionID = lifecycle.missionID
	task.Target = Target{Mode: TargetModeAgentTask, AgentID: "agent-a", TaskGoal: "legacy work", Prompt: "legacy work"}
	task.CreatedBy = "system:scheduler"
	task.CreatedByKind = ViewerKindSystem
	task.CreatorProfileID = "agent-a"
	if err := writerRepository.SaveTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	viewer := ViewerRef{TenantID: task.TenantID, ViewerID: task.CreatedBy, ViewerKind: ViewerKindSystem, ProfileID: task.CreatorProfileID}
	updatedTitle := "stale updated title"
	result := make(chan scheduledTaskMutationResult, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() {
		updated, err := writer.UpdateTask(ctx, viewer, UpdateTaskRequest{ScheduledTaskID: task.ScheduledTaskID, Title: &updatedTitle})
		result <- scheduledTaskMutationResult{task: updated, err: err}
	}()

	waitForScheduledMissionLifecycle(t, lifecycle.entered)
	archived, err := archiver.ArchiveSystemManagedTask(ctx, viewer, CancelTaskRequest{ScheduledTaskID: task.ScheduledTaskID})
	if err != nil {
		t.Fatal(err)
	}
	close(lifecycle.release)
	mutation := waitForScheduledTaskMutation(t, result)
	if !errors.Is(mutation.err, ErrRevisionConflict) {
		t.Fatalf("stale update error = %v, want ErrRevisionConflict", mutation.err)
	}
	stored, err := writerRepository.GetTask(ctx, task.ScheduledTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if archived.Enabled || archived.Status != TaskStatusArchived || stored.Enabled || stored.Status != TaskStatusArchived {
		t.Fatalf("archived task was revived: archived=%+v stored=%+v", archived, stored)
	}
	if stored.Title != task.Title {
		t.Fatalf("stored title = %q, want original %q", stored.Title, task.Title)
	}
	if len(lifecycle.tasks) != 2 || lifecycle.tasks[1].Enabled || lifecycle.tasks[1].Status != TaskStatusArchived || lifecycle.tasks[1].Title != task.Title {
		t.Fatalf("Mission compensation snapshots = %+v, want latest archived task last", lifecycle.tasks)
	}
}

func TestServiceArchiveSystemManagedTaskWinsAgainstStaleAdvance(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	writerRepository := NewRepository(env.DBRouter)
	archiveRepository := NewRepository(env.DBRouter)
	lifecycle := &blockingScheduledMissionLifecycle{
		missionID: "mission-stale-advance",
		entered:   make(chan struct{}, 1),
		release:   make(chan struct{}),
	}
	writer := NewService(ServiceOptions{Clock: env.Clock, Repository: writerRepository, MissionLifecycle: lifecycle})
	archiver := NewService(ServiceOptions{Clock: env.Clock, Repository: archiveRepository})
	task := schedulerTestTask("system-task-stale-advance", env.Clock.Now().Add(-time.Minute))
	task.TenantID = "tenant-a"
	task.Schedule = Schedule{Type: ScheduleTypeCron, Cron: "*/5 * * * *", Timezone: "UTC"}
	task.MissionID = lifecycle.missionID
	task.Target = Target{Mode: TargetModeAgentTask, AgentID: "agent-a", TaskGoal: "legacy work", Prompt: "legacy work"}
	task.CreatedBy = "system:scheduler"
	task.CreatedByKind = ViewerKindSystem
	task.CreatorProfileID = "agent-a"
	if err := writerRepository.SaveTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	viewer := ViewerRef{TenantID: task.TenantID, ViewerID: task.CreatedBy, ViewerKind: ViewerKindSystem, ProfileID: task.CreatorProfileID}
	result := make(chan scheduledTaskMutationResult, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() {
		advanced, err := writer.AdvanceAfterDueRun(ctx, task)
		result <- scheduledTaskMutationResult{task: advanced, err: err}
	}()

	waitForScheduledMissionLifecycle(t, lifecycle.entered)
	archived, err := archiver.ArchiveSystemManagedTask(ctx, viewer, CancelTaskRequest{ScheduledTaskID: task.ScheduledTaskID})
	if err != nil {
		t.Fatal(err)
	}
	close(lifecycle.release)
	mutation := waitForScheduledTaskMutation(t, result)
	if !errors.Is(mutation.err, ErrRevisionConflict) {
		t.Fatalf("stale advance error = %v, want ErrRevisionConflict", mutation.err)
	}
	stored, err := writerRepository.GetTask(ctx, task.ScheduledTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if archived.Enabled || archived.Status != TaskStatusArchived || stored.Enabled || stored.Status != TaskStatusArchived {
		t.Fatalf("archived task was revived: archived=%+v stored=%+v", archived, stored)
	}
	if !stored.NextRunAt.Equal(task.NextRunAt) {
		t.Fatalf("stored next_run_at = %s, want archived snapshot %s", stored.NextRunAt, task.NextRunAt)
	}
	if len(lifecycle.tasks) != 2 || lifecycle.tasks[1].Enabled || lifecycle.tasks[1].Status != TaskStatusArchived || !lifecycle.tasks[1].NextRunAt.Equal(task.NextRunAt) {
		t.Fatalf("Mission compensation snapshots = %+v, want latest archived task last", lifecycle.tasks)
	}
}

func TestServiceRejectsInvalidExecutionAndDeliveryPolicy(t *testing.T) {
	service := newServiceForTest()
	base := CreateTaskRequest{
		Title:          "Invalid native task",
		Schedule:       Schedule{Type: ScheduleTypeCron, Cron: "30 8 * * *", Timezone: "Asia/Shanghai"},
		Target:         Target{Mode: TargetModeAgentTask, AgentID: "tiedan", TaskGoal: "Collect intelligence"},
		IdempotencyKey: "invalid-native-task",
	}
	invalidTimeout := base
	invalidTimeout.Execution.TimeoutSeconds = 24*60*60 + 1
	if _, err := service.CreateTask(context.Background(), schedulerTestViewer(), invalidTimeout); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("invalid timeout error = %v", err)
	}
	invalidDelivery := base
	invalidDelivery.IdempotencyKey = "invalid-native-delivery"
	invalidDelivery.Delivery = DeliveryPolicy{Mode: "announce", Channel: "feishu", AccountID: "main"}
	if _, err := service.CreateTask(context.Background(), schedulerTestViewer(), invalidDelivery); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("invalid delivery error = %v", err)
	}
	invalidFormat := base
	invalidFormat.IdempotencyKey = "invalid-native-delivery-format"
	invalidFormat.Delivery = DeliveryPolicy{Mode: "announce", Channel: "feishu", RecipientID: "oc_group", Format: "html"}
	if _, err := service.CreateTask(context.Background(), schedulerTestViewer(), invalidFormat); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("invalid delivery format error = %v", err)
	}
	missingArtifact := base
	missingArtifact.IdempotencyKey = "invalid-native-delivery-artifact"
	missingArtifact.Delivery = DeliveryPolicy{Mode: "announce", Channel: "feishu", RecipientID: "oc_group", ContentArtifactID: "rss-briefing", Format: "markdown"}
	if _, err := service.CreateTask(context.Background(), schedulerTestViewer(), missingArtifact); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("missing delivery artifact error = %v", err)
	}
}

func TestServiceCreateCronTaskSupportsRangesListsAndSteps(t *testing.T) {
	service := newServiceForTest()

	task, err := service.CreateTask(context.Background(), schedulerTestViewer(), CreateTaskRequest{
		Title: "Weekday quarter-hour digest",
		Schedule: Schedule{
			Type:     ScheduleTypeCron,
			Cron:     "*/15 9-10 * * 1-5",
			Timezone: "UTC",
		},
		Target:         Target{Mode: TargetModeDiscussionWorkflow, Message: "Create the digest."},
		IdempotencyKey: "digest-weekday-step",
	})
	if err != nil {
		t.Fatal(err)
	}

	want := time.Date(2026, 6, 8, 9, 45, 0, 0, time.UTC)
	if !task.NextRunAt.Equal(want) {
		t.Fatalf("next_run_at = %s, want %s", task.NextRunAt, want)
	}
}

func TestServiceRestrictsTodoAutomationTargetsToSystemTasks(t *testing.T) {
	service := newServiceForTest()
	req := CreateTaskRequest{
		Title:          "Todo automation",
		Schedule:       Schedule{Type: ScheduleTypeCron, Cron: "*/5 * * * *", Timezone: "UTC"},
		Target:         Target{Mode: TargetModeTodoAutomationScan},
		IdempotencyKey: "todo-automation",
	}
	if _, err := service.CreateTask(context.Background(), schedulerTestViewer(), req); !errors.Is(err, ErrInvalidTarget) {
		t.Fatalf("human create error = %v, want ErrInvalidTarget", err)
	}
	systemViewer := schedulerTestViewer()
	systemViewer.ViewerKind = ViewerKindSystem
	created, err := service.CreateTask(context.Background(), systemViewer, req)
	if err != nil {
		t.Fatal(err)
	}
	if created.Target.Mode != TargetModeTodoAutomationScan {
		t.Fatalf("target = %+v", created.Target)
	}
}

func TestServiceRestrictsResponsibilityReconciliationTargetsToSystemTasks(t *testing.T) {
	service := newServiceForTest()
	req := CreateTaskRequest{
		Title:          "Responsibility reconciliation",
		Schedule:       Schedule{Type: ScheduleTypeCron, Cron: "* * * * *", Timezone: "UTC"},
		Target:         Target{Mode: TargetModeResponsibilityReconcile},
		IdempotencyKey: "responsibility-reconcile",
	}
	if _, err := service.CreateTask(context.Background(), schedulerTestViewer(), req); !errors.Is(err, ErrInvalidTarget) {
		t.Fatalf("human create error = %v, want ErrInvalidTarget", err)
	}
	systemViewer := schedulerTestViewer()
	systemViewer.ViewerKind = ViewerKindSystem
	created, err := service.CreateTask(context.Background(), systemViewer, req)
	if err != nil {
		t.Fatal(err)
	}
	if created.Target.Mode != TargetModeResponsibilityReconcile || created.CreatedByKind != ViewerKindSystem {
		t.Fatalf("system responsibility target = %+v", created)
	}
}

func TestServiceRestrictsOntologyStateRefreshTargetsToSystemWithoutArguments(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	service := newServiceForTestWithRepository(repo)
	req := CreateTaskRequest{
		Title:          "Daily ontology state refresh",
		Schedule:       Schedule{Type: ScheduleTypeCron, Cron: "10 8 * * *", Timezone: "Asia/Shanghai"},
		Target:         Target{Mode: TargetModeOntologyStateRefresh},
		IdempotencyKey: "ontology-state-refresh",
	}
	if _, err := service.CreateTask(context.Background(), schedulerTestViewer(), req); !errors.Is(err, ErrInvalidTarget) {
		t.Fatalf("human create error = %v, want ErrInvalidTarget", err)
	}
	systemViewer := schedulerTestViewer()
	systemViewer.ViewerKind = ViewerKindSystem
	withArguments := req
	withArguments.IdempotencyKey = "ontology-state-refresh-with-arguments"
	withArguments.Target.Prompt = "scan everything"
	if _, err := service.CreateTask(context.Background(), systemViewer, withArguments); !errors.Is(err, ErrInvalidTarget) {
		t.Fatalf("argument-bearing create error = %v, want ErrInvalidTarget", err)
	}
	created, err := service.CreateTask(context.Background(), systemViewer, req)
	if err != nil {
		t.Fatal(err)
	}
	title := "Operator title"
	if _, err := service.UpdateTask(context.Background(), schedulerTestViewer(), UpdateTaskRequest{ScheduledTaskID: created.ScheduledTaskID, Title: &title}); !errors.Is(err, ErrInvalidTarget) {
		t.Fatalf("human update error = %v, want ErrInvalidTarget", err)
	}
	updated, err := service.UpdateTask(context.Background(), systemViewer, UpdateTaskRequest{ScheduledTaskID: created.ScheduledTaskID, Title: &title})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Title != title || updated.Target.Mode != TargetModeOntologyStateRefresh {
		t.Fatalf("updated ontology refresh task = %+v", updated)
	}
}

func TestServiceRestrictsResponsibilityEvaluationTargetsToSystemAndDoesNotPrecreateMission(t *testing.T) {
	missions := &recordingScheduledMissionLifecycle{missionID: "unexpected-mission"}
	service := NewService(ServiceOptions{
		Clock: clock.NewFake(schedulerTestTime), IDGenerator: idgen.NewDeterministic("sched"), MissionLifecycle: missions,
	})
	req := CreateTaskRequest{
		Title:    "Responsibility evaluation",
		Schedule: Schedule{Type: ScheduleTypeCron, Cron: "0 9 * * *", Timezone: "UTC"},
		Target: Target{
			Mode: TargetModeResponsibilityEvaluation, AgentID: "agent-1",
			SourceScopeKind: ResponsibilityEvaluationPeoplePortfolioScope, SourceScopeID: "binding-1",
		},
		IdempotencyKey: "responsibility-evaluation",
	}
	if _, err := service.CreateTask(context.Background(), schedulerTestViewer(), req); !errors.Is(err, ErrInvalidTarget) {
		t.Fatalf("human create error = %v, want ErrInvalidTarget", err)
	}
	systemViewer := schedulerTestViewer()
	systemViewer.ViewerKind = ViewerKindSystem
	unsafe := req
	unsafe.IdempotencyKey = "responsibility-evaluation-with-prompt"
	unsafe.Target.TaskGoal = "Review all responsibilities"
	unsafe.Target.Prompt = "Choose and execute any work you think is useful."
	if _, err := service.CreateTask(context.Background(), systemViewer, unsafe); !errors.Is(err, ErrInvalidTarget) {
		t.Fatalf("prompt-bearing evaluation error = %v, want ErrInvalidTarget", err)
	}
	created, err := service.CreateTask(context.Background(), systemViewer, req)
	if err != nil {
		t.Fatal(err)
	}
	if created.Target.Mode != TargetModeResponsibilityEvaluation || created.MissionID != "" || len(missions.tasks) != 0 {
		t.Fatalf("system evaluation target = %+v Mission calls=%+v", created, missions.tasks)
	}
}

func TestServiceMigratesLegacyResponsibilityPulseWithoutRetainingMissionBinding(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	missions := &recordingScheduledMissionLifecycle{missionID: "unexpected-mission"}
	service := NewService(ServiceOptions{
		Clock: clock.NewFake(schedulerTestTime), IDGenerator: idgen.NewDeterministic("sched"), Repository: repo, MissionLifecycle: missions,
	})
	task := schedulerTestTask("legacy-responsibility-pulse", schedulerTestTime.Add(time.Hour))
	task.MissionID = "legacy-scheduled-mission"
	task.Target = Target{Mode: TargetModeAgentTask, AgentID: "agent-1", TaskGoal: "Review the whole portfolio", Prompt: "Advance any duty"}
	task.CreatedByKind = ViewerKindSystem
	if err := repo.SaveTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	target := Target{
		Mode: TargetModeResponsibilityEvaluation, AgentID: "agent-1",
		SourceScopeKind: ResponsibilityEvaluationPeoplePortfolioScope, SourceScopeID: "binding-1",
	}
	systemViewer := schedulerTestViewer()
	systemViewer.ViewerKind = ViewerKindSystem
	updated, err := service.UpdateTask(context.Background(), systemViewer, UpdateTaskRequest{
		ScheduledTaskID: task.ScheduledTaskID, Target: &target,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.MissionID != "" || updated.Target.Mode != TargetModeResponsibilityEvaluation || len(missions.tasks) != 0 {
		t.Fatalf("migrated pulse = %+v Mission calls=%+v", updated, missions.tasks)
	}
}

func TestServiceAllowsUsersToConfigureExistingTodoAutomationSchedule(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	service := newServiceForTestWithRepository(repo)
	ctx := context.Background()
	task := schedulerTestTask("todo-automation", schedulerTestTime.Add(time.Minute))
	task.Target = Target{Mode: TargetModeTodoAutomationScan}
	task.CreatedBy = "system:todo-automation-bootstrap"
	task.CreatedByKind = ViewerKindSystem
	if err := repo.SaveTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	schedule := Schedule{Type: ScheduleTypeCron, Cron: "*/15 * * * *", Timezone: "UTC"}
	target := Target{Mode: TargetModeTodoAutomationScan}
	updated, err := service.UpdateTask(ctx, schedulerTestViewer(), UpdateTaskRequest{
		ScheduledTaskID: task.ScheduledTaskID,
		Schedule:        &schedule,
		Target:          &target,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Schedule.Cron != "*/15 * * * *" || updated.Target.Mode != TargetModeTodoAutomationScan {
		t.Fatalf("updated task = %+v", updated)
	}
}

func TestServicePreventsUsersFromChangingSystemManagedTargetKinds(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	service := newServiceForTestWithRepository(repo)
	task := schedulerTestTask("system-managed-target", schedulerTestTime.Add(time.Minute))
	task.Target = Target{Mode: TargetModeTodoAutomationScan}
	task.CreatedByKind = ViewerKindSystem
	if err := repo.SaveTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	changed := Target{Mode: TargetModeResponsibilityReconcile}
	if _, err := service.UpdateTask(context.Background(), schedulerTestViewer(), UpdateTaskRequest{ScheduledTaskID: task.ScheduledTaskID, Target: &changed}); !errors.Is(err, ErrInvalidTarget) {
		t.Fatalf("change system target error = %v, want ErrInvalidTarget", err)
	}
}

func TestServiceKeepsResponsibilityReconciliationScheduleSystemManaged(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	service := newServiceForTestWithRepository(repo)
	ctx := context.Background()
	task := schedulerTestTask("responsibility-reconcile", schedulerTestTime.Add(time.Minute))
	task.Target = Target{Mode: TargetModeResponsibilityReconcile}
	task.CreatedByKind = ViewerKindSystem
	if err := repo.SaveTask(ctx, task); err != nil {
		t.Fatal(err)
	}

	schedule := Schedule{Type: ScheduleTypeCron, Cron: "*/5 * * * *", Timezone: "UTC"}
	if _, err := service.UpdateTask(ctx, schedulerTestViewer(), UpdateTaskRequest{ScheduledTaskID: task.ScheduledTaskID, Schedule: &schedule}); !errors.Is(err, ErrInvalidTarget) {
		t.Fatalf("human update error = %v, want ErrInvalidTarget", err)
	}
	if _, err := service.CancelTask(ctx, schedulerTestViewer(), CancelTaskRequest{ScheduledTaskID: task.ScheduledTaskID}); !errors.Is(err, ErrInvalidTarget) {
		t.Fatalf("human cancel error = %v, want ErrInvalidTarget", err)
	}

	systemViewer := schedulerTestViewer()
	systemViewer.ViewerKind = ViewerKindSystem
	updated, err := service.UpdateTask(ctx, systemViewer, UpdateTaskRequest{ScheduledTaskID: task.ScheduledTaskID, Schedule: &schedule})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Schedule.Cron != schedule.Cron {
		t.Fatalf("system-updated cron = %q, want %q", updated.Schedule.Cron, schedule.Cron)
	}
}

func TestServiceCreateTaskReturnsExistingTaskForIdempotencyKey(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	service := newServiceForTestWithRepository(repo)
	req := CreateTaskRequest{
		Title:          "Idempotent task",
		Schedule:       Schedule{Type: ScheduleTypeOnce, RunAt: schedulerTestTime.Add(time.Hour)},
		Target:         Target{Mode: TargetModeDiscussionWorkflow, Message: "Run once."},
		IdempotencyKey: "create-idempotent",
	}

	first, err := service.CreateTask(context.Background(), schedulerTestViewer(), req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.CreateTask(context.Background(), schedulerTestViewer(), req)
	if err != nil {
		t.Fatal(err)
	}
	if second.ScheduledTaskID != first.ScheduledTaskID || second.Title != first.Title {
		t.Fatalf("idempotent create returned different task: first=%+v second=%+v", first, second)
	}
}

func TestServiceCreateTaskRejectsIdempotencyKeyReuseWithDifferentRequest(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	service := newServiceForTestWithRepository(repo)
	req := CreateTaskRequest{
		Title:          "Original task",
		Schedule:       Schedule{Type: ScheduleTypeOnce, RunAt: schedulerTestTime.Add(time.Hour)},
		Target:         Target{Mode: TargetModeDiscussionWorkflow, Message: "Run once."},
		IdempotencyKey: "create-conflict",
	}
	if _, err := service.CreateTask(context.Background(), schedulerTestViewer(), req); err != nil {
		t.Fatal(err)
	}
	req.Title = "Different task"
	if _, err := service.CreateTask(context.Background(), schedulerTestViewer(), req); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("error = %v, want ErrInvalidRequest", err)
	}
}

func TestServiceRejectsInvalidSchedules(t *testing.T) {
	service := newServiceForTest()

	cases := []struct {
		name     string
		schedule Schedule
	}{
		{name: "once missing run_at", schedule: Schedule{Type: ScheduleTypeOnce}},
		{name: "cron missing timezone", schedule: Schedule{Type: ScheduleTypeCron, Cron: "0 9 * * *"}},
		{name: "cron invalid expression", schedule: Schedule{Type: ScheduleTypeCron, Cron: "not cron", Timezone: "UTC"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := service.CreateTask(context.Background(), schedulerTestViewer(), CreateTaskRequest{
				Title:          "Invalid",
				Schedule:       tc.schedule,
				Target:         Target{Mode: TargetModeDiscussionWorkflow, Message: "Run it."},
				IdempotencyKey: "invalid-" + tc.name,
			})
			if !errors.Is(err, ErrInvalidSchedule) {
				t.Fatalf("error = %v, want ErrInvalidSchedule", err)
			}
		})
	}
}

func TestServiceRejectsUnsupportedRetryPolicy(t *testing.T) {
	service := newServiceForTest()
	base := CreateTaskRequest{
		Title:          "Retry policy",
		Schedule:       Schedule{Type: ScheduleTypeOnce, RunAt: schedulerTestTime.Add(time.Hour)},
		Target:         Target{Mode: TargetModeDiscussionWorkflow, Message: "Run."},
		IdempotencyKey: "retry-policy",
	}
	cases := []Policy{
		{MaxRetries: 21},
		{RetryDelay: "not-a-duration"},
		{RetryDelay: "25h"},
		{FailureMode: "ignore"},
	}
	for _, policy := range cases {
		req := base
		req.Policy = policy
		if _, err := service.CreateTask(context.Background(), schedulerTestViewer(), req); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("policy %+v error = %v, want ErrInvalidRequest", policy, err)
		}
	}
}

func TestServiceScopesTasksAndIdempotencyByTenant(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	service := newServiceForTestWithRepository(repo)
	req := CreateTaskRequest{
		Title:          "Tenant task",
		Schedule:       Schedule{Type: ScheduleTypeOnce, RunAt: schedulerTestTime.Add(time.Hour)},
		Target:         Target{Mode: TargetModeDiscussionWorkflow, Message: "Run."},
		IdempotencyKey: "same-key",
	}
	viewerA := schedulerTestViewer()
	viewerA.TenantID = "tenant-a"
	viewerB := schedulerTestViewer()
	viewerB.TenantID = "tenant-b"
	taskA, err := service.CreateTask(context.Background(), viewerA, req)
	if err != nil {
		t.Fatal(err)
	}
	taskB, err := service.CreateTask(context.Background(), viewerB, req)
	if err != nil {
		t.Fatal(err)
	}
	if taskA.ScheduledTaskID == taskB.ScheduledTaskID || taskA.TenantID != "tenant-a" || taskB.TenantID != "tenant-b" {
		t.Fatalf("tenant tasks = A:%+v B:%+v", taskA, taskB)
	}
	page, err := service.ListTasks(context.Background(), ListTasksQuery{TenantID: "tenant-a"})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || page.Tasks[0].ScheduledTaskID != taskA.ScheduledTaskID {
		t.Fatalf("tenant A page = %+v", page)
	}
	if _, err := service.PrepareManualRun(context.Background(), viewerB, RunNowRequest{ScheduledTaskID: taskA.ScheduledTaskID}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant run error = %v, want ErrNotFound", err)
	}
}

func TestServiceRejectsIncompleteTargets(t *testing.T) {
	service := newServiceForTest()
	tooManyArtifacts := make([]ArtifactRequirement, maxScheduledResultArtifacts+1)
	for index := range tooManyArtifacts {
		tooManyArtifacts[index] = ArtifactRequirement{
			ID: fmt.Sprintf("report-%03d", index), Kind: "report", PathPattern: fmt.Sprintf("/reports/report-%03d.md", index),
		}
	}

	cases := []struct {
		name   string
		target Target
	}{
		{name: "discussion missing message", target: Target{Mode: TargetModeDiscussionWorkflow}},
		{name: "discussion cannot assign an agent task reviewer", target: Target{Mode: TargetModeDiscussionWorkflow, Message: "Review", ApprovalAssigneeID: "human-ding"}},
		{name: "agent task missing agent", target: Target{Mode: TargetModeAgentTask, TaskGoal: "Summarize updates"}},
		{name: "agent task missing goal", target: Target{Mode: TargetModeAgentTask, AgentID: "summarizer"}},
		{name: "agent task result artifact path is relative", target: Target{
			Mode: TargetModeAgentTask, AgentID: "summarizer", TaskGoal: "Summarize updates",
			ResultContract: AgentResultContract{Artifacts: []ArtifactRequirement{{ID: "report", Kind: "report", PathPattern: "reports/latest.md"}}},
		}},
		{name: "agent task result artifact count is unbounded", target: Target{
			Mode: TargetModeAgentTask, AgentID: "summarizer", TaskGoal: "Summarize updates",
			ResultContract: AgentResultContract{Artifacts: tooManyArtifacts},
		}},
		{name: "memory distillation missing topic", target: Target{Mode: TargetModeMemoryExperienceDistillation, SourceScopeKind: "mission", SourceScopeID: "mission-1", TargetScopeKind: "project", TargetScopeID: "project-1"}},
		{name: "memory distillation missing source scope id", target: Target{Mode: TargetModeMemoryExperienceDistillation, MemoryTopic: "qa", SourceScopeKind: "mission", TargetScopeKind: "project", TargetScopeID: "project-1"}},
		{name: "memory distillation missing source scope kind", target: Target{Mode: TargetModeMemoryExperienceDistillation, MemoryTopic: "qa", SourceScopeID: "mission-1", TargetScopeKind: "project", TargetScopeID: "project-1"}},
		{name: "memory distillation missing target scope", target: Target{Mode: TargetModeMemoryExperienceDistillation, MemoryTopic: "qa", SourceScopeKind: "mission", SourceScopeID: "mission-1"}},
		{name: "memory distillation unsupported source scope", target: Target{Mode: TargetModeMemoryExperienceDistillation, MemoryTopic: "qa", SourceScopeKind: "agent", SourceScopeID: "agent-1", TargetScopeKind: "project", TargetScopeID: "project-1"}},
		{name: "memory distillation unsupported target scope", target: Target{Mode: TargetModeMemoryExperienceDistillation, MemoryTopic: "qa", SourceScopeKind: "mission", SourceScopeID: "mission-1", TargetScopeKind: "mission", TargetScopeID: "mission-1"}},
		{name: "world consolidation unsupported source scope", target: Target{Mode: TargetModeMemoryWorldConsolidation, MemoryTopic: "quality", SourceScopeKind: "mission", SourceScopeID: "mission-1", TargetScopeKind: "organization", TargetScopeID: "org-1"}},
		{name: "world consolidation missing source scope id", target: Target{Mode: TargetModeMemoryWorldConsolidation, MemoryTopic: "quality", SourceScopeKind: "project", TargetScopeKind: "organization", TargetScopeID: "org-1"}},
		{name: "world consolidation missing source scope kind", target: Target{Mode: TargetModeMemoryWorldConsolidation, MemoryTopic: "quality", SourceScopeID: "project-1", TargetScopeKind: "organization", TargetScopeID: "org-1"}},
		{name: "world consolidation unsupported target scope", target: Target{Mode: TargetModeMemoryWorldConsolidation, MemoryTopic: "quality", SourceScopeKind: "project", SourceScopeID: "project-1", TargetScopeKind: "project", TargetScopeID: "project-1"}},
		{name: "evaluation review is not a scheduled target mode", target: Target{Mode: TargetMode("evaluation_review")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := service.CreateTask(context.Background(), schedulerTestViewer(), CreateTaskRequest{
				Title:          "Invalid",
				Schedule:       Schedule{Type: ScheduleTypeOnce, RunAt: schedulerTestTime.Add(time.Hour)},
				Target:         tc.target,
				IdempotencyKey: "invalid-" + tc.name,
			})
			if !errors.Is(err, ErrInvalidTarget) {
				t.Fatalf("error = %v, want ErrInvalidTarget", err)
			}
		})
	}
}

func TestServiceAllowsApprovalAssigneeForResponsibilityEvaluation(t *testing.T) {
	service := newServiceForTest()
	systemViewer := schedulerTestViewer()
	systemViewer.ViewerKind = ViewerKindSystem
	task, err := service.CreateTask(context.Background(), systemViewer, CreateTaskRequest{
		Title: "Responsibility evaluation", Schedule: Schedule{Type: ScheduleTypeOnce, RunAt: schedulerTestTime.Add(time.Hour)},
		Target: Target{
			Mode: TargetModeResponsibilityEvaluation, AgentID: "agent-1", ApprovalAssigneeID: "human-manager",
			SourceScopeKind: ResponsibilityEvaluationPeoplePortfolioScope, SourceScopeID: "binding-1",
		},
		IdempotencyKey: "responsibility-evaluation-assignee",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if task.Target.ApprovalAssigneeID != "human-manager" {
		t.Fatalf("approval assignee = %q", task.Target.ApprovalAssigneeID)
	}
}

func TestServiceCreatesMemoryMaintenanceTasksWithoutSourceScope(t *testing.T) {
	service := newServiceForTest()
	tests := []struct {
		name   string
		target Target
	}{
		{
			name: "experience",
			target: Target{
				Mode:            TargetModeMemoryExperienceDistillation,
				MemoryTopic:     "agent_result",
				TargetScopeKind: "organization",
				TargetScopeID:   "org-system-runtime-support",
			},
		},
		{
			name: "world",
			target: Target{
				Mode:            TargetModeMemoryWorldConsolidation,
				MemoryTopic:     "agent_result",
				TargetScopeKind: "organization",
				TargetScopeID:   "org-system-runtime-support",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task, err := service.CreateTask(context.Background(), schedulerTestViewer(), CreateTaskRequest{
				Title:          "Native memory maintenance " + tt.name,
				Schedule:       Schedule{Type: ScheduleTypeOnce, RunAt: schedulerTestTime.Add(time.Hour)},
				Target:         tt.target,
				IdempotencyKey: "native-memory-" + tt.name,
			})
			if err != nil {
				t.Fatalf("create task: %v", err)
			}
			if task.Target.SourceScopeKind != "" || task.Target.SourceScopeID != "" {
				t.Fatalf("source scope = %q/%q, want omitted", task.Target.SourceScopeKind, task.Target.SourceScopeID)
			}
		})
	}
}

func TestServiceCreatesWeeklyEvaluationRequestAsDiscussionWorkflow(t *testing.T) {
	service := newServiceForTest()

	task, err := service.CreateTask(context.Background(), schedulerTestViewer(), CreateTaskRequest{
		Title:    "每周 Agent 综合评价",
		Schedule: Schedule{Type: ScheduleTypeCron, Cron: "0 9 * * 1", Timezone: "Asia/Shanghai"},
		Target: Target{
			Mode:           TargetModeDiscussionWorkflow,
			LeadAgentID:    " reviewer-agent ",
			ConversationID: " discussion-1 ",
			Message:        "对 profile-agent-1 开展综合评价：请基于评估中心真实任务和运行 Token，调用 evaluation_review 工具写入结论与人天估算。",
		},
		IdempotencyKey: "agent-evaluation-weekly",
	})
	if err != nil {
		t.Fatal(err)
	}

	if task.Target.Mode != TargetModeDiscussionWorkflow || task.Target.LeadAgentID != "reviewer-agent" || task.Target.ConversationID != "discussion-1" {
		t.Fatalf("target = %+v", task.Target)
	}
	if got := targetSummary(task.Target); got != "discussion discussion-1" {
		t.Fatalf("target summary = %q", got)
	}
}

func TestServiceCreatesMemoryExperienceDistillationTask(t *testing.T) {
	service := newServiceForTest()

	task, err := service.CreateTask(context.Background(), schedulerTestViewer(), CreateTaskRequest{
		Title:    "Daily QA memory distillation",
		Schedule: Schedule{Type: ScheduleTypeOnce, RunAt: schedulerTestTime.Add(time.Hour)},
		Target: Target{
			Mode:              TargetModeMemoryExperienceDistillation,
			MemoryTopic:       " qa ",
			SourceScopeKind:   " mission ",
			SourceScopeID:     " mission-1 ",
			TargetScopeKind:   " project ",
			TargetScopeID:     " project-1 ",
			MemoryRecordLimit: 25,
		},
		IdempotencyKey: "memory-distill-daily",
	})
	if err != nil {
		t.Fatal(err)
	}

	if task.Target.Mode != TargetModeMemoryExperienceDistillation || task.Target.MemoryTopic != "qa" {
		t.Fatalf("target = %+v", task.Target)
	}
	if got := targetSummary(task.Target); got != "experience distillation qa to project project-1" {
		t.Fatalf("target summary = %q", got)
	}
}

func TestServiceCreatesMemoryWorldConsolidationTask(t *testing.T) {
	service := newServiceForTest()

	task, err := service.CreateTask(context.Background(), schedulerTestViewer(), CreateTaskRequest{
		Title:    "Weekly quality world memory",
		Schedule: Schedule{Type: ScheduleTypeOnce, RunAt: schedulerTestTime.Add(time.Hour)},
		Target: Target{
			Mode:              TargetModeMemoryWorldConsolidation,
			MemoryTopic:       " quality ",
			SourceScopeKind:   " project ",
			SourceScopeID:     " project-1 ",
			TargetScopeKind:   " organization ",
			TargetScopeID:     " org-1 ",
			MemoryRecordLimit: 40,
		},
		IdempotencyKey: "world-consolidate-weekly",
	})
	if err != nil {
		t.Fatal(err)
	}

	if task.Target.Mode != TargetModeMemoryWorldConsolidation || task.Target.MemoryTopic != "quality" {
		t.Fatalf("target = %+v", task.Target)
	}
	if got := targetSummary(task.Target); got != "world consolidation quality to organization org-1" {
		t.Fatalf("target summary = %q", got)
	}
}

func TestServiceCreatesToolCallTask(t *testing.T) {
	service := newServiceForTest()

	task, err := service.CreateTask(context.Background(), schedulerTestViewer(), CreateTaskRequest{
		Title:    "Run todo tool",
		Schedule: Schedule{Type: ScheduleTypeOnce, RunAt: schedulerTestTime.Add(time.Hour)},
		Target: Target{
			Mode:          TargetModeToolCall,
			ToolID:        " todo.create ",
			ToolArguments: map[string]any{"title": "Ship scheduler tool call"},
		},
		IdempotencyKey: "tool-call",
	})
	if err != nil {
		t.Fatal(err)
	}

	if task.Target.Mode != TargetModeToolCall || task.Target.ToolID != "todo.create" || task.Target.ToolArguments["title"] != "Ship scheduler tool call" {
		t.Fatalf("target = %+v", task.Target)
	}
	if got := targetSummary(task.Target); got != "tool todo.create" {
		t.Fatalf("target summary = %q", got)
	}
}

func TestServicePersistsAndListsTasksWithLastRun(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	service := newServiceForTestWithRepository(repo)
	ctx := context.Background()

	task, err := service.CreateTask(ctx, schedulerTestViewer(), CreateTaskRequest{
		Title:          "Persistent task",
		Schedule:       Schedule{Type: ScheduleTypeOnce, RunAt: schedulerTestTime.Add(time.Hour)},
		Target:         Target{Mode: TargetModeDiscussionWorkflow, Message: "Run task."},
		IdempotencyKey: "persist",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveRun(ctx, schedulerTestRun("run-1", task.ScheduledTaskID, RunStatusFailed, schedulerTestTime.Add(time.Minute))); err != nil {
		t.Fatal(err)
	}

	page, err := service.ListTasks(ctx, ListTasksQuery{})
	if err != nil {
		t.Fatal(err)
	}

	if len(page.Tasks) != 1 || page.Tasks[0].ScheduledTaskID != task.ScheduledTaskID || page.Tasks[0].LastRunStatus != RunStatusFailed {
		t.Fatalf("task page = %+v", page)
	}
}

func TestSummarizeTaskExposesTheActualExecutorAgent(t *testing.T) {
	tests := []struct {
		name   string
		target Target
		want   string
	}{
		{name: "agent task", target: Target{Mode: TargetModeAgentTask, AgentID: "agent-worker"}, want: "agent-worker"},
		{name: "responsibility evaluation", target: Target{Mode: TargetModeResponsibilityEvaluation, AgentID: "agent-owner"}, want: "agent-owner"},
		{name: "discussion lead", target: Target{Mode: TargetModeDiscussionWorkflow, LeadAgentID: "agent-lead"}, want: "agent-lead"},
		{name: "system tool call", target: Target{Mode: TargetModeToolCall, ToolID: "todo.create"}, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			summary := summarizeTask(ScheduledTask{Target: tt.target})
			if summary.ExecutorAgentID != tt.want {
				t.Fatalf("executor agent id = %q, want %q", summary.ExecutorAgentID, tt.want)
			}
		})
	}
}

func TestServiceListTasksAppliesManagementFiltersAndSorting(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	service := newServiceForTestWithRepository(repo)
	ctx := context.Background()

	alpha := schedulerTestTask("sched-alpha", schedulerTestTime.Add(90*time.Minute))
	alpha.Title = "Alpha digest"
	alpha.Target.ConversationID = "discussion-alpha"
	beta := schedulerTestTask("sched-beta", schedulerTestTime.Add(30*time.Minute))
	beta.Title = "Beta digest"
	beta.Status = TaskStatusDisabled
	beta.Enabled = false
	gamma := schedulerTestTask("sched-gamma", schedulerTestTime.Add(2*time.Hour))
	gamma.Title = "Gamma report"
	gamma.Target.Message = "Search customer feedback"
	archived := schedulerTestTask("sched-archived", schedulerTestTime.Add(45*time.Minute))
	archived.Title = "Archived digest"
	archived.Status = TaskStatusArchived
	archived.Enabled = false
	for _, task := range []ScheduledTask{alpha, beta, gamma, archived} {
		if err := repo.SaveTask(ctx, task); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.SaveRun(ctx, schedulerTestRun("run-alpha", alpha.ScheduledTaskID, RunStatusFailed, schedulerTestTime)); err != nil {
		t.Fatal(err)
	}

	page, err := service.ListTasks(ctx, ListTasksQuery{
		IncludeArchived: true,
		Q:               "digest",
		Status:          TaskStatusEnabled,
		From:            schedulerTestTime.Add(time.Hour),
		To:              schedulerTestTime.Add(2 * time.Hour),
		Sort:            SortNextRunDesc,
		Limit:           1,
		Offset:          0,
	})
	if err != nil {
		t.Fatal(err)
	}

	if page.Total != 1 || len(page.Tasks) != 1 {
		t.Fatalf("filtered page = %+v", page)
	}
	if page.Tasks[0].ScheduledTaskID != "sched-alpha" || page.Tasks[0].LastRunStatus != RunStatusFailed {
		t.Fatalf("filtered task = %+v", page.Tasks[0])
	}
}

func TestServiceListTasksExpandsCronOccurrencesAcrossDisplayWeek(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	service := newServiceForTestWithRepository(repo)
	ctx := context.Background()

	task := schedulerTestTask("sched-daily", time.Date(2026, 8, 17, 1, 0, 0, 0, time.UTC))
	task.Title = "Daily digest"
	task.Schedule = Schedule{Type: ScheduleTypeCron, Cron: "0 9 * * *", Timezone: "Asia/Shanghai"}
	if err := repo.SaveTask(ctx, task); err != nil {
		t.Fatal(err)
	}

	page, err := service.ListTasks(ctx, ListTasksQuery{
		TenantID:           "system",
		From:               time.Date(2026, 8, 9, 16, 0, 0, 0, time.UTC),
		To:                 time.Date(2026, 8, 16, 16, 0, 0, 0, time.UTC),
		ExpandOccurrences:  true,
		OccurrenceTimezone: "Asia/Shanghai",
	})
	if err != nil {
		t.Fatal(err)
	}

	if page.Total != 1 || len(page.Tasks) != 1 {
		t.Fatalf("expanded page = %+v", page)
	}
	occurrences := page.Tasks[0].ScheduleOccurrences
	if len(occurrences) != 7 {
		t.Fatalf("schedule occurrences = %v, want one per display day", occurrences)
	}
	wantFriday := time.Date(2026, 8, 14, 1, 0, 0, 0, time.UTC)
	if !occurrences[4].Equal(wantFriday) {
		t.Fatalf("Friday occurrence = %s, want %s", occurrences[4], wantFriday)
	}
}

func TestServiceListTasksStatusArchivedIncludesArchivedRows(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	service := newServiceForTestWithRepository(repo)
	ctx := context.Background()

	active := schedulerTestTask("sched-active", schedulerTestTime.Add(time.Hour))
	archived := schedulerTestTask("sched-archived", schedulerTestTime.Add(2*time.Hour))
	archived.Status = TaskStatusArchived
	archived.Enabled = false
	for _, task := range []ScheduledTask{active, archived} {
		if err := repo.SaveTask(ctx, task); err != nil {
			t.Fatal(err)
		}
	}

	page, err := service.ListTasks(ctx, ListTasksQuery{Status: TaskStatusArchived})
	if err != nil {
		t.Fatal(err)
	}

	if page.Total != 1 || len(page.Tasks) != 1 || page.Tasks[0].ScheduledTaskID != archived.ScheduledTaskID {
		t.Fatalf("archived page = %+v", page)
	}
}

func TestServiceListRunsAppliesStatusFilterAndPagination(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	service := newServiceForTestWithRepository(repo)
	ctx := context.Background()
	task := schedulerTestTask("sched-runs", schedulerTestTime.Add(time.Hour))
	if err := repo.SaveTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	runs := []ScheduledRun{
		schedulerTestRun("run-failed-1", task.ScheduledTaskID, RunStatusFailed, schedulerTestTime),
		schedulerTestRun("run-completed", task.ScheduledTaskID, RunStatusCompleted, schedulerTestTime.Add(time.Minute)),
		schedulerTestRun("run-failed-2", task.ScheduledTaskID, RunStatusFailed, schedulerTestTime.Add(2*time.Minute)),
	}
	for _, run := range runs {
		if err := repo.SaveRun(ctx, run); err != nil {
			t.Fatal(err)
		}
	}

	page, err := service.ListRuns(ctx, ListRunsQuery{
		ScheduledTaskID: task.ScheduledTaskID,
		Status:          RunStatusFailed,
		Limit:           1,
		Offset:          1,
	})
	if err != nil {
		t.Fatal(err)
	}

	if page.Total != 2 || len(page.Runs) != 1 || page.Runs[0].ScheduledRunID != "run-failed-2" {
		t.Fatalf("run page = %+v", page)
	}

	page, err = service.ListRuns(ctx, ListRunsQuery{
		ScheduledTaskID: task.ScheduledTaskID,
		Limit:           int(^uint(0) >> 1),
		Offset:          1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 3 || len(page.Runs) != 2 {
		t.Fatalf("max-int run page = %+v", page)
	}
}

func TestServiceListRunsSelectsExactRunForDeepLink(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	service := newServiceForTestWithRepository(repo)
	ctx := context.Background()
	task := schedulerTestTask("sched-run-link", schedulerTestTime.Add(time.Hour))
	if err := repo.SaveTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"run-old", "run-target", "run-new"} {
		if err := repo.SaveRun(ctx, schedulerTestRun(id, task.ScheduledTaskID, RunStatusCompleted, schedulerTestTime)); err != nil {
			t.Fatal(err)
		}
	}
	page, err := service.ListRuns(ctx, ListRunsQuery{ScheduledTaskID: task.ScheduledTaskID, ScheduledRunID: "run-target", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Runs) != 1 || page.Runs[0].ScheduledRunID != "run-target" {
		t.Fatalf("exact run page = %+v", page)
	}
}

func TestServiceCancelTaskArchivesWithoutDeletingRuns(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	service := newServiceForTestWithRepository(repo)
	ctx := context.Background()
	task := schedulerTestTask("sched-1", schedulerTestTime.Add(time.Hour))
	if err := repo.SaveTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveRun(ctx, schedulerTestRun("run-1", task.ScheduledTaskID, RunStatusCompleted, schedulerTestTime)); err != nil {
		t.Fatal(err)
	}

	cancelled, err := service.CancelTask(ctx, ViewerRef{ViewerID: "operator", ViewerKind: ViewerKindUser}, CancelTaskRequest{ScheduledTaskID: task.ScheduledTaskID})
	if err != nil {
		t.Fatal(err)
	}
	runs, err := repo.ListRunsForTask(ctx, task.ScheduledTaskID)
	if err != nil {
		t.Fatal(err)
	}

	if cancelled.Status != TaskStatusArchived || cancelled.Enabled {
		t.Fatalf("cancelled task = %+v", cancelled)
	}
	if len(runs) != 1 || runs[0].ScheduledRunID != "run-1" {
		t.Fatalf("runs after cancel = %+v", runs)
	}
}

func TestServiceUpdateTaskRecomputesScheduleAndRevalidatesTarget(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	service := newServiceForTestWithRepository(repo)
	ctx := context.Background()
	task := schedulerTestTask("sched-update", schedulerTestTime.Add(time.Hour))
	if err := repo.SaveTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	title := "Updated agent task"
	schedule := Schedule{Type: ScheduleTypeCron, Cron: "45 9 * * *", Timezone: "UTC"}
	target := Target{
		Mode:     TargetModeAgentTask,
		AgentID:  "agent-worker",
		TaskGoal: "Produce the weekly trend report.",
	}
	policy := Policy{Concurrency: ConcurrencyAllowParallel, MaxRetries: -1}

	updated, err := service.UpdateTask(ctx, ViewerRef{ViewerID: "operator", ViewerKind: ViewerKindUser}, UpdateTaskRequest{
		ScheduledTaskID: task.ScheduledTaskID,
		Title:           &title,
		Schedule:        &schedule,
		Target:          &target,
		Policy:          &policy,
	})
	if err != nil {
		t.Fatal(err)
	}

	wantNext := time.Date(2026, 6, 8, 9, 45, 0, 0, time.UTC)
	if updated.Title != title || updated.Target.Mode != TargetModeAgentTask || updated.Target.AgentID != "agent-worker" {
		t.Fatalf("updated task = %+v", updated)
	}
	if !updated.NextRunAt.Equal(wantNext) {
		t.Fatalf("next_run_at = %s, want %s", updated.NextRunAt, wantNext)
	}
	if updated.Policy.Concurrency != ConcurrencyAllowParallel || updated.Policy.MaxRetries != 0 {
		t.Fatalf("policy = %+v", updated.Policy)
	}
}

func TestServicePrepareManualRunRespectsConcurrency(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	service := newServiceForTestWithRepository(repo)
	ctx := context.Background()
	task := schedulerTestTask("sched-1", schedulerTestTime.Add(time.Hour))
	if err := repo.SaveTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveRun(ctx, schedulerTestRun("active-run", task.ScheduledTaskID, RunStatusRunning, schedulerTestTime)); err != nil {
		t.Fatal(err)
	}

	skipped, err := service.PrepareManualRun(ctx, ViewerRef{ViewerID: "operator", ViewerKind: ViewerKindUser}, RunNowRequest{ScheduledTaskID: task.ScheduledTaskID})
	if err != nil {
		t.Fatal(err)
	}
	forced, err := service.PrepareManualRun(ctx, ViewerRef{ViewerID: "operator", ViewerKind: ViewerKindUser}, RunNowRequest{ScheduledTaskID: task.ScheduledTaskID, Force: true})
	if err != nil {
		t.Fatal(err)
	}

	if skipped.Status != RunStatusSkipped || skipped.FailureReason == "" {
		t.Fatalf("skipped run = %+v", skipped)
	}
	if forced.Status != RunStatusQueued || forced.TriggerType != RunTriggerManual {
		t.Fatalf("forced run = %+v", forced)
	}
}

func TestServicePrepareDueRunSnapshotsAgentConfiguration(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	service := newServiceForTestWithRepository(repo)
	ctx := context.Background()
	task := schedulerTestTask("sched-configuration-snapshot", schedulerTestTime.Add(time.Hour))
	task.TenantID = "tenant-snapshot"
	task.Title = "Original scheduled report"
	task.Description = "Original scheduled description"
	task.Target = Target{
		Mode: TargetModeAgentTask, AgentID: "agent-snapshot", ApprovalAssigneeID: "human-original",
		TaskGoal: "Produce the snapshot report", Prompt: "Use the original prompt",
		ToolArguments: map[string]any{"nested": map[string]any{"value": "original"}},
		ResultContract: AgentResultContract{Artifacts: []ArtifactRequirement{{
			ID: "report", Kind: "report", PathPattern: "/workspace/report.md", MinBytes: 1,
		}}},
	}
	task.Policy.MaxRetries = 2
	task.Policy.RetryDelay = "15s"
	task.Execution = ExecutionPolicy{
		TimeoutSeconds: 900, LLMProfileID: "llm-original", SourceModel: "model-original",
		FallbackModels: []string{"fallback-original"}, AllowedToolIDs: []string{"tool-original"},
	}
	task.Delivery = DeliveryPolicy{
		Mode: "announce", Channel: "feishu", RecipientID: "oc_original", AccountID: "main", ContentArtifactID: "report", Format: "markdown",
	}
	if err := repo.SaveTask(ctx, task); err != nil {
		t.Fatal(err)
	}

	run, err := service.PrepareDueRun(ctx, task)
	if err != nil {
		t.Fatal(err)
	}
	task.Target.ResultContract.Artifacts[0].ID = "updated-report"
	task.Target.AgentID = "agent-updated"
	task.Target.Prompt = "Use the updated prompt"
	task.Target.ToolArguments["nested"].(map[string]any)["value"] = "updated"
	task.Execution.FallbackModels[0] = "fallback-updated"
	task.Execution.AllowedToolIDs[0] = "tool-updated"
	task.Policy.MaxRetries = 8
	task.Delivery.RecipientID = "oc_updated"
	task.Title = "Updated scheduled report"
	if err := repo.SaveTask(ctx, task); err != nil {
		t.Fatal(err)
	}

	if run.ConfigurationSnapshotVersion != 1 || run.TenantID != "tenant-snapshot" || run.ScheduledTaskTitle != "Original scheduled report" || len(run.ResultContract.Artifacts) != 1 ||
		run.ResultContract.Artifacts[0].ID != "report" || run.Delivery.RecipientID != "oc_original" || run.ConfigurationSnapshot == nil {
		t.Fatalf("returned run configuration snapshot = %+v", run)
	}
	returnedSnapshot := run.ConfigurationSnapshot
	if returnedSnapshot.Target.AgentID != "agent-snapshot" || returnedSnapshot.Target.Prompt != "Use the original prompt" || returnedSnapshot.Policy.MaxRetries != 2 ||
		returnedSnapshot.Execution.FallbackModels[0] != "fallback-original" || returnedSnapshot.Execution.AllowedToolIDs[0] != "tool-original" ||
		returnedSnapshot.Target.ToolArguments["nested"].(map[string]any)["value"] != "original" {
		t.Fatalf("returned full configuration snapshot = %+v", returnedSnapshot)
	}
	stored, err := repo.GetRun(ctx, run.ScheduledRunID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ConfigurationSnapshotVersion != 1 || stored.TenantID != "tenant-snapshot" || stored.ScheduledTaskTitle != "Original scheduled report" || len(stored.ResultContract.Artifacts) != 1 ||
		stored.ResultContract.Artifacts[0].ID != "report" || stored.Delivery.RecipientID != "oc_original" || stored.ConfigurationSnapshot == nil ||
		stored.ConfigurationSnapshot.Target.AgentID != "agent-snapshot" || stored.ConfigurationSnapshot.Target.Prompt != "Use the original prompt" ||
		stored.ConfigurationSnapshot.Execution.FallbackModels[0] != "fallback-original" || stored.ConfigurationSnapshot.Target.ToolArguments["nested"].(map[string]any)["value"] != "original" {
		t.Fatalf("stored run configuration snapshot = %+v", stored)
	}
}

func TestServicePrepareDueRunMarksEmptyConfigurationSnapshot(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	service := newServiceForTestWithRepository(repo)
	ctx := context.Background()
	task := schedulerTestTask("sched-empty-configuration-snapshot", schedulerTestTime.Add(time.Hour))

	run, err := service.PrepareDueRun(ctx, task)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := repo.GetRun(ctx, run.ScheduledRunID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ConfigurationSnapshotVersion != 1 || stored.ConfigurationSnapshot == nil || len(stored.ResultContract.Artifacts) != 0 || stored.Delivery != (DeliveryPolicy{}) {
		t.Fatalf("empty configuration snapshot = %+v", stored)
	}
}

func TestServicePrepareManualRunReturnsExistingRunForIdempotencyKey(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	service := newServiceForTestWithRepository(repo)
	ctx := context.Background()
	task := schedulerTestTask("sched-idempotent-run", schedulerTestTime.Add(time.Hour))
	if err := repo.SaveTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	req := RunNowRequest{ScheduledTaskID: task.ScheduledTaskID, Force: true, IdempotencyKey: "run-now-1"}

	first, err := service.PrepareManualRun(ctx, schedulerTestViewer(), req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.PrepareManualRun(ctx, schedulerTestViewer(), req)
	if err != nil {
		t.Fatal(err)
	}
	if second.ScheduledRunID != first.ScheduledRunID || second.IdempotencyKey != req.IdempotencyKey {
		t.Fatalf("idempotent run returned different record: first=%+v second=%+v", first, second)
	}
}

func TestServicePrepareManualRunRejectsIdempotencyKeyReuseWithDifferentForce(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	service := newServiceForTestWithRepository(repo)
	ctx := context.Background()
	task := schedulerTestTask("sched-run-conflict", schedulerTestTime.Add(time.Hour))
	if err := repo.SaveTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	req := RunNowRequest{ScheduledTaskID: task.ScheduledTaskID, IdempotencyKey: "run-conflict"}
	if _, err := service.PrepareManualRun(ctx, schedulerTestViewer(), req); err != nil {
		t.Fatal(err)
	}
	req.Force = true
	if _, err := service.PrepareManualRun(ctx, schedulerTestViewer(), req); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("error = %v, want ErrInvalidRequest", err)
	}
}

func newServiceForTest() *Service {
	return NewService(ServiceOptions{
		Clock:            clock.NewFake(schedulerTestTime),
		IDGenerator:      idgen.NewDeterministic("sched"),
		MissionLifecycle: &recordingScheduledMissionLifecycle{missionID: "mission-test"},
	})
}

func newServiceForTestWithRepository(repo *Repository) *Service {
	return NewService(ServiceOptions{
		Clock:            clock.NewFake(schedulerTestTime),
		IDGenerator:      idgen.NewDeterministic("sched"),
		Repository:       repo,
		MissionLifecycle: &recordingScheduledMissionLifecycle{missionID: "mission-test"},
	})
}
