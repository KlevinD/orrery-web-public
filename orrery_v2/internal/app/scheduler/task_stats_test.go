package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"orrery_v2/internal/platform/db"
)

func TestServiceTaskStatsAreUnpaginatedAndTenantScoped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tasks := make([]ScheduledTask, 0, 255)
		for i := 0; i < 250; i++ {
			tasks = append(tasks, ScheduledTask{
				TenantID: "tenant-a", ScheduledTaskID: fmt.Sprintf("task-%03d", i),
				Status: TaskStatusEnabled, Enabled: true,
				Target: Target{Mode: TargetModeAgentTask, AgentID: " agent-a "},
			})
		}
		tasks = append(tasks,
			ScheduledTask{TenantID: "tenant-a", ScheduledTaskID: "disabled", Status: TaskStatusDisabled, Target: Target{Mode: TargetModeDiscussionWorkflow, LeadAgentID: "agent-b"}},
			ScheduledTask{TenantID: "tenant-a", ScheduledTaskID: "system-job", Status: TaskStatusEnabled, Enabled: true, Target: Target{Mode: TargetModeResponsibilityReconcile, AgentID: "ignored"}},
			ScheduledTask{TenantID: "tenant-a", ScheduledTaskID: "archived", Status: TaskStatusArchived, Enabled: true, Target: Target{Mode: TargetModeAgentTask, AgentID: "archived-agent"}},
			ScheduledTask{TenantID: "tenant-b", ScheduledTaskID: "foreign", Status: TaskStatusEnabled, Enabled: true},
			ScheduledTask{ScheduledTaskID: "legacy-system", Status: TaskStatusDisabled},
		)
		old := time.Now().Add(-30 * 24 * time.Hour)
		runs := []ScheduledRun{
			{ScheduledTaskID: "task-000", ScheduledRunID: "old", Status: RunStatusFailed, CreatedAt: old},
			{ScheduledTaskID: "task-000", ScheduledRunID: "new", Status: RunStatusCompleted, CreatedAt: time.Now()},
			{ScheduledTaskID: "task-249", ScheduledRunID: "beyond-page", Status: RunStatusFailed, CreatedAt: old},
			{ScheduledTaskID: "archived", ScheduledRunID: "archived-run", Status: RunStatusFailed, CreatedAt: old},
			{ScheduledTaskID: "foreign", ScheduledRunID: "foreign-run", Status: RunStatusFailed, CreatedAt: old},
		}
		service := serviceWithTaskStatsSnapshots(t, tasks, runs)
		got, err := service.GetTaskStats(context.Background(), " tenant-a ")
		want := ScheduledTaskStats{Total: 252, Enabled: 251, Executors: 2, System: 1, Failed: 2}
		if err != nil || got != want {
			t.Fatalf("stats=%+v err=%v, want %+v", got, err, want)
		}
		page, err := service.ListTasks(context.Background(), ListTasksQuery{TenantID: "tenant-a", IncludeArchived: true, Limit: 100})
		if err != nil || len(page.Tasks) != 100 || page.Total != 253 {
			t.Fatalf("list tasks=%d total=%d err=%v", len(page.Tasks), page.Total, err)
		}
		statsJSON, _ := json.Marshal(got)
		pageJSON, _ := json.Marshal(page)
		if len(statsJSON) >= len(pageJSON) {
			t.Fatalf("summary bytes=%d list bytes=%d", len(statsJSON), len(pageJSON))
		}
		t.Logf("fixture response: summary=%d bytes, 100-task page=%d bytes", len(statsJSON), len(pageJSON))

		for _, tenantID := range []string{"", "system", " system "} {
			got, err := service.GetTaskStats(context.Background(), tenantID)
			if err != nil || got != (ScheduledTaskStats{Total: 1, System: 1}) {
				t.Fatalf("legacy tenant %q stats=%+v err=%v", tenantID, got, err)
			}
		}
		got, err = service.GetTaskStats(context.Background(), "empty-tenant")
		if err != nil || got != (ScheduledTaskStats{}) {
			t.Fatalf("empty tenant stats=%+v err=%v", got, err)
		}
	})
}

func TestServiceTaskStatsPropagateCancellationAndErrors(t *testing.T) {
	var missing *Service
	if _, err := missing.GetTaskStats(context.Background(), "system"); !errors.Is(err, ErrServiceDependency) {
		t.Fatalf("missing service err=%v", err)
	}
	if _, err := (&Service{}).GetTaskStats(context.Background(), "system"); !errors.Is(err, ErrServiceDependency) {
		t.Fatalf("missing repository err=%v", err)
	}
	if _, err := (&Service{repo: &Repository{}}).GetTaskStats(context.Background(), "system"); !errors.Is(err, ErrServiceDependency) {
		t.Fatalf("missing storage err=%v", err)
	}
	synctest.Test(t, func(t *testing.T) {
		service := serviceWithTaskStatsSnapshots(t, nil, nil)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := service.GetTaskStats(ctx, "system"); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled err=%v", err)
		}
		service.repo.taskSnapshot = []ScheduledTask{{ScheduledTaskID: "broken-run"}}
		service.repo.runSnapshot = []db.Record{{"scheduled_task_id": "broken-run", schedulerPayload: "{"}}
		if _, err := service.GetTaskStats(context.Background(), "system"); err == nil {
			t.Fatal("invalid run payload returned a successful summary")
		}
	})
}

// Seed the existing short-lived repository caches without opening a database.
func serviceWithTaskStatsSnapshots(t *testing.T, tasks []ScheduledTask, runs []ScheduledRun) *Service {
	t.Helper()
	rows := make([]db.Record, 0, len(runs))
	for _, run := range runs {
		row, err := scheduledRunRecord(run)
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
	}
	return &Service{repo: &Repository{
		router: &db.Router{}, taskSnapshotAt: time.Now(), taskSnapshot: tasks,
		runSnapshotAt: time.Now(), runSnapshot: rows,
	}}
}
