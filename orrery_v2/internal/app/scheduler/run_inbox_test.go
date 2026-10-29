package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"orrery_v2/internal/platform/testkit"
)

func TestServiceRunInboxIsTenantScopedNewestFirstAndTracksReadPerViewer(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	service := newServiceForTestWithRepository(repo)
	ctx := context.Background()

	taskA := schedulerTestTask("task-a", schedulerTestTime.Add(time.Hour))
	taskA.TenantID = "tenant-a"
	taskA.Title = "Daily AI briefing"
	taskB := schedulerTestTask("task-b", schedulerTestTime.Add(time.Hour))
	taskB.TenantID = "tenant-b"
	for _, task := range []ScheduledTask{taskA, taskB} {
		if err := repo.SaveTask(ctx, task); err != nil {
			t.Fatal(err)
		}
	}

	completed := schedulerTestRun("run-completed", taskA.ScheduledTaskID, RunStatusCompleted, schedulerTestTime.Add(-2*time.Hour))
	completed.TenantID = taskA.TenantID
	completed.CompletedAt = completed.CreatedAt.Add(time.Minute)
	completed.Result.Summary = "Knowledge report was indexed."
	failed := schedulerTestRun("run-failed", taskA.ScheduledTaskID, RunStatusFailed, schedulerTestTime.Add(-time.Hour))
	failed.TenantID = taskA.TenantID
	failed.ScheduledTaskTitle = "Snapshot title"
	failed.CompletedAt = failed.CreatedAt.Add(time.Minute)
	failed.FailureReason = "Source fetch failed"
	running := schedulerTestRun("run-running", taskA.ScheduledTaskID, RunStatusRunning, schedulerTestTime)
	running.TenantID = taskA.TenantID
	otherTenant := schedulerTestRun("run-other-tenant", taskB.ScheduledTaskID, RunStatusCompleted, schedulerTestTime.Add(time.Minute))
	otherTenant.TenantID = taskB.TenantID
	for _, run := range []ScheduledRun{completed, failed, running, otherTenant} {
		if err := repo.SaveRun(ctx, run); err != nil {
			t.Fatal(err)
		}
	}

	viewerA := ViewerRef{TenantID: "tenant-a", ViewerID: "viewer-a", ViewerKind: ViewerKindHuman}
	page, err := service.ListRunInbox(ctx, viewerA, ListRunInboxQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 || page.UnreadTotal != 2 || page.AttentionTotal != 1 || len(page.Items) != 2 {
		t.Fatalf("initial inbox = %+v", page)
	}
	if page.Items[0].Run.ScheduledRunID != failed.ScheduledRunID || page.Items[1].Run.ScheduledRunID != completed.ScheduledRunID {
		t.Fatalf("inbox order = %q, %q", page.Items[0].Run.ScheduledRunID, page.Items[1].Run.ScheduledRunID)
	}
	if page.Items[0].TaskTitle != taskA.Title || !page.Items[0].AttentionRequired {
		t.Fatalf("failed inbox item = %+v", page.Items[0])
	}

	state, err := service.SetRunReadState(ctx, viewerA, SetRunReadStateRequest{ScheduledRunID: failed.ScheduledRunID, Read: true})
	if err != nil {
		t.Fatal(err)
	}
	if !state.IsRead || !state.ReadAt.Equal(schedulerTestTime) {
		t.Fatalf("read state = %+v", state)
	}

	// A fresh service instance proves the receipt is persisted rather than cached in-process.
	service = newServiceForTestWithRepository(NewRepository(env.DBRouter))
	page, err = service.ListRunInbox(ctx, viewerA, ListRunInboxQuery{ReadState: RunInboxReadUnread})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || page.UnreadTotal != 1 || len(page.Items) != 1 || page.Items[0].Run.ScheduledRunID != completed.ScheduledRunID {
		t.Fatalf("viewer-a unread inbox = %+v", page)
	}

	viewerB := ViewerRef{TenantID: "tenant-a", ViewerID: "viewer-b", ViewerKind: ViewerKindHuman}
	page, err = service.ListRunInbox(ctx, viewerB, ListRunInboxQuery{AttentionOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || page.UnreadTotal != 2 || len(page.Items) != 1 || page.Items[0].IsRead || !page.Items[0].AttentionRequired {
		t.Fatalf("viewer-b attention inbox = %+v", page)
	}
	viewerKindCollision := ViewerRef{TenantID: "tenant-a", ViewerID: "viewer-a", ViewerKind: ViewerKindAgent}
	page, err = service.ListRunInbox(ctx, viewerKindCollision, ListRunInboxQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if page.UnreadTotal != 2 {
		t.Fatalf("same viewer id with a different kind inherited read state: %+v", page)
	}

	state, err = service.SetRunReadState(ctx, viewerA, SetRunReadStateRequest{ScheduledRunID: failed.ScheduledRunID, Read: false})
	if err != nil {
		t.Fatal(err)
	}
	if state.IsRead || !state.ReadAt.IsZero() {
		t.Fatalf("unread state = %+v", state)
	}
	page, err = service.ListRunInbox(ctx, viewerA, ListRunInboxQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if page.UnreadTotal != 2 || page.Items[0].IsRead {
		t.Fatalf("inbox after marking unread = %+v", page)
	}
}

func TestServiceRunInboxRejectsCrossTenantAndNonTerminalReadState(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	service := newServiceForTestWithRepository(repo)
	ctx := context.Background()

	terminal := schedulerTestRun("run-tenant-b", "task-b", RunStatusCompleted, schedulerTestTime)
	terminal.TenantID = "tenant-b"
	running := schedulerTestRun("run-running", "task-a", RunStatusRunning, schedulerTestTime)
	running.TenantID = "tenant-a"
	for _, run := range []ScheduledRun{terminal, running} {
		if err := repo.SaveRun(ctx, run); err != nil {
			t.Fatal(err)
		}
	}
	viewer := ViewerRef{TenantID: "tenant-a", ViewerID: "viewer-a", ViewerKind: ViewerKindHuman}
	if _, err := service.SetRunReadState(ctx, viewer, SetRunReadStateRequest{ScheduledRunID: terminal.ScheduledRunID, Read: true}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant error = %v, want ErrNotFound", err)
	}
	if _, err := service.SetRunReadState(ctx, viewer, SetRunReadStateRequest{ScheduledRunID: running.ScheduledRunID, Read: true}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("non-terminal error = %v, want ErrInvalidRequest", err)
	}
}

func TestServiceRunInboxExcludesSystemManagedRunsFromItemsAndCounts(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	service := newServiceForTestWithRepository(repo)
	ctx := context.Background()
	viewer := ViewerRef{TenantID: "tenant-a", ViewerID: "viewer-a", ViewerKind: ViewerKindHuman}

	userTask := schedulerTestTask("user-report", schedulerTestTime.Add(time.Hour))
	userTask.TenantID = viewer.TenantID
	userTask.Title = "Daily AI briefing"
	userTask.CreatedByKind = ViewerKindSystem
	userTask.Target = Target{Mode: TargetModeAgentTask}
	if err := repo.SaveTask(ctx, userTask); err != nil {
		t.Fatal(err)
	}
	userRun := schedulerTestRun("run-user-report", userTask.ScheduledTaskID, RunStatusCompleted, schedulerTestTime)
	userRun.TenantID = viewer.TenantID
	userRun.TargetMode = TargetModeAgentTask
	if err := repo.SaveRun(ctx, userRun); err != nil {
		t.Fatal(err)
	}

	systemModes := []TargetMode{
		TargetModeTodoAutomationScan,
		TargetModeResponsibilityEvaluation,
		TargetModeResponsibilityReconcile,
		TargetModeOntologyStateRefresh,
	}
	for index, mode := range systemModes {
		task := schedulerTestTask("system-task-"+string(mode), schedulerTestTime.Add(time.Hour))
		task.TenantID = viewer.TenantID
		task.Target = Target{Mode: mode}
		task.CreatedByKind = ViewerKindSystem
		if err := repo.SaveTask(ctx, task); err != nil {
			t.Fatal(err)
		}
		run := schedulerTestRun("run-system-"+string(mode), task.ScheduledTaskID, RunStatusFailed, schedulerTestTime.Add(time.Duration(index+1)*time.Minute))
		run.TenantID = viewer.TenantID
		run.TargetMode = mode
		if err := repo.SaveRun(ctx, run); err != nil {
			t.Fatal(err)
		}
	}

	page, err := service.ListRunInbox(ctx, viewer, ListRunInboxQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || page.UnreadTotal != 1 || page.AttentionTotal != 0 || len(page.Items) != 1 || page.Items[0].Run.ScheduledRunID != userRun.ScheduledRunID {
		t.Fatalf("inbox with system-managed runs = %+v", page)
	}
	hiddenRunID := "run-system-" + string(TargetModeResponsibilityReconcile)
	page, err = service.ListRunInbox(ctx, viewer, ListRunInboxQuery{ScheduledRunID: hiddenRunID})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 0 || len(page.Items) != 0 {
		t.Fatalf("system-managed exact run appeared in inbox: %+v", page)
	}
	if _, err := service.SetRunReadState(ctx, viewer, SetRunReadStateRequest{ScheduledRunID: hiddenRunID, Read: true}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("system-managed read state error = %v, want ErrNotFound", err)
	}
}

func TestRunInboxVisibilityPreservesHistoricalExecutionMode(t *testing.T) {
	maintenance := ScheduledTask{Target: Target{Mode: TargetModeResponsibilityReconcile}}
	report := ScheduledTask{Target: Target{Mode: TargetModeAgentTask}}
	for _, tc := range []struct {
		name    string
		run     ScheduledRun
		task    ScheduledTask
		visible bool
	}{
		{"report before task changed", ScheduledRun{TargetMode: TargetModeAgentTask}, maintenance, true},
		{"maintenance before task changed", ScheduledRun{TargetMode: TargetModeResponsibilityReconcile}, report, false},
		{"legacy snapshot maintenance", ScheduledRun{ConfigurationSnapshot: &maintenance}, report, false},
		{"legacy snapshot report", ScheduledRun{ConfigurationSnapshot: &report}, maintenance, true},
		{"legacy task fallback", ScheduledRun{}, maintenance, false},
		{"deleted historical report task", ScheduledRun{TargetMode: TargetModeAgentTask}, ScheduledTask{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := scheduledRunVisibleInInbox(tc.run, tc.task); got != tc.visible {
				t.Fatalf("visibility = %v, want %v", got, tc.visible)
			}
		})
	}
}

func TestServiceRunInboxValidatesFiltersAndPaginates(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	service := newServiceForTestWithRepository(repo)
	ctx := context.Background()
	viewer := ViewerRef{TenantID: "tenant-a", ViewerID: "viewer-a", ViewerKind: ViewerKindHuman}

	for index, id := range []string{"run-a", "run-b", "run-c"} {
		run := schedulerTestRun(id, "task-a", RunStatusCompleted, schedulerTestTime.Add(time.Duration(index)*time.Minute))
		run.TenantID = viewer.TenantID
		if err := repo.SaveRun(ctx, run); err != nil {
			t.Fatal(err)
		}
	}
	page, err := service.ListRunInbox(ctx, viewer, ListRunInboxQuery{Limit: 1, Offset: 1})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 3 || len(page.Items) != 1 || page.Items[0].Run.ScheduledRunID != "run-b" || page.Limit != 1 || page.Offset != 1 {
		t.Fatalf("paginated inbox = %+v", page)
	}
	for _, query := range []ListRunInboxQuery{
		{ReadState: RunInboxReadState("unknown")},
		{Status: RunStatus("unknown")},
		{Limit: 101},
		{Offset: -1},
	} {
		if _, err := service.ListRunInbox(ctx, viewer, query); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("query %+v error = %v, want ErrInvalidRequest", query, err)
		}
	}
}
