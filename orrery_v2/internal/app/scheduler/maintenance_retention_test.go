package scheduler

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/testkit"
)

func TestCleanupTaskRunsAppliesBoundedTaskSpecificRetention(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	now := schedulerTestTime.Add(10 * 24 * time.Hour)
	taskID := "internal-responsibility-reconcile"

	runs := []ScheduledRun{
		maintenanceRetentionRun("completed-expired-oldest", taskID, RunStatusCompleted, now.Add(-48*time.Hour)),
		maintenanceRetentionRun("completed-expired-newer", taskID, RunStatusCompleted, now.Add(-25*time.Hour)),
		maintenanceRetentionRun("completed-recent", taskID, RunStatusCompleted, now.Add(-23*time.Hour)),
		maintenanceRetentionRun("failed-expired", taskID, RunStatusFailed, now.Add(-8*24*time.Hour)),
		maintenanceRetentionRun("failed-recent", taskID, RunStatusFailed, now.Add(-6*24*time.Hour)),
		maintenanceRetentionRun("skipped-expired", taskID, RunStatusSkipped, now.Add(-8*24*time.Hour)),
		maintenanceRetentionRun("queued-old", taskID, RunStatusQueued, now.Add(-30*24*time.Hour)),
		maintenanceRetentionRun("running-old", taskID, RunStatusRunning, now.Add(-30*24*time.Hour)),
		maintenanceRetentionRun("other-task-expired", "user-task", RunStatusCompleted, now.Add(-30*24*time.Hour)),
	}
	for _, run := range runs {
		if err := repo.SaveRun(ctx, run); err != nil {
			t.Fatalf("SaveRun(%s): %v", run.ScheduledRunID, err)
		}
	}
	if primed, err := repo.ListRunsForTask(ctx, taskID); err != nil || len(primed) != 8 {
		t.Fatalf("prime run snapshot count=%d err=%v", len(primed), err)
	}
	readAt := now.Add(-time.Hour)
	if err := repo.SetRunReadState(ctx, scheduledRunReadReceipt{
		TenantID: "system", ViewerID: "viewer-a", ViewerKind: ViewerKindHuman,
		ScheduledRunID: "failed-expired", ReadAt: readAt, UpdatedAt: readAt,
	}, true); err != nil {
		t.Fatal(err)
	}

	deleted, err := repo.CleanupTaskRuns(ctx, taskID, now, 24*time.Hour, 7*24*time.Hour, 2)
	if err != nil || deleted != 2 {
		t.Fatalf("first cleanup deleted=%d err=%v", deleted, err)
	}
	for _, id := range []string{"failed-expired", "skipped-expired"} {
		if _, err := repo.GetRun(ctx, id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("GetRun(%s) error = %v, want ErrNotFound", id, err)
		}
	}
	receipts, err := repo.ListRunReadReceipts(ctx, "system", "viewer-a", ViewerKindHuman)
	if err != nil {
		t.Fatal(err)
	}
	if len(receipts) != 0 {
		t.Fatalf("retained orphaned run read receipts: %+v", receipts)
	}
	for _, id := range []string{"completed-expired-oldest", "completed-expired-newer", "completed-recent", "failed-recent", "queued-old", "running-old", "other-task-expired"} {
		if _, err := repo.GetRun(ctx, id); err != nil {
			t.Fatalf("GetRun(%s): %v", id, err)
		}
	}
	if current, err := repo.ListRunsForTask(ctx, taskID); err != nil || len(current) != 6 {
		t.Fatalf("invalidated run snapshot count=%d err=%v", len(current), err)
	}

	deleted, err = repo.CleanupTaskRuns(ctx, taskID, now, 24*time.Hour, 7*24*time.Hour, 2)
	if err != nil || deleted != 2 {
		t.Fatalf("second cleanup deleted=%d err=%v", deleted, err)
	}
	for _, id := range []string{"completed-expired-oldest", "completed-expired-newer"} {
		if _, err := repo.GetRun(ctx, id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("GetRun(%s) error = %v, want ErrNotFound", id, err)
		}
	}
}

func TestCleanupTaskRunsRejectsInvalidPolicy(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)

	if _, err := repo.CleanupTaskRuns(context.Background(), "", schedulerTestTime, time.Hour, 2*time.Hour, 1); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("CleanupTaskRuns error = %v, want ErrInvalidRequest", err)
	}
}

func TestCleanupTaskRunsDeletesResponsibilityEvaluationBatchChildren(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	now := schedulerTestTime.Add(10 * 24 * time.Hour)
	run := maintenanceRetentionRun("expired-responsibility-batch", "responsibility-batch-task", RunStatusRunning, now.Add(-48*time.Hour))
	run.TenantID = "tenant-a"
	run.Attempt = 1
	if err := repo.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	target := batchEvaluationTarget("binding-1", "agent-1")
	target.TenantID = run.TenantID
	if _, err := repo.EnsureResponsibilityEvaluationBatchManifest(ctx, ResponsibilityEvaluationBatchManifest{
		TenantID: run.TenantID, ScheduledTaskID: run.ScheduledTaskID, ScheduledRunID: run.ScheduledRunID,
		ScopeID: "people-a", Attempt: run.Attempt, Targets: []ResponsibilityEvaluationBatchTarget{target}, CreatedAt: run.CreatedAt,
	}); err != nil {
		t.Fatal(err)
	}
	item := ResponsibilityEvaluationItem{
		TenantID: run.TenantID, ScheduledTaskID: run.ScheduledTaskID, ScheduledRunID: run.ScheduledRunID,
		PortfolioID: target.PortfolioID, PortfolioBindingID: target.PortfolioBindingID,
		PortfolioVersionID: target.PortfolioVersionID, AgentProfileID: target.AgentProfileID,
		Status: ResponsibilityEvaluationItemCompleted, Attempt: run.Attempt, CreatedAt: run.CreatedAt, UpdatedAt: run.CreatedAt,
	}
	if err := repo.SaveResponsibilityEvaluationItem(ctx, item); err != nil {
		t.Fatal(err)
	}
	run.Status = RunStatusCompleted
	run.CompletedAt = now.Add(-48 * time.Hour)
	if err := repo.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	deleted, err := repo.CleanupTaskRuns(ctx, run.ScheduledTaskID, now, 24*time.Hour, 7*24*time.Hour, 1)
	if err != nil || deleted != 1 {
		t.Fatalf("cleanup deleted=%d err=%v", deleted, err)
	}
	if _, err := repo.GetRun(ctx, run.ScheduledRunID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("run error = %v, want ErrNotFound", err)
	}
	if _, err := repo.GetResponsibilityEvaluationBatchManifest(ctx, run.ScheduledRunID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("manifest error = %v, want ErrNotFound", err)
	}
	itemID := ResponsibilityEvaluationItemID(run.ScheduledRunID, target.PortfolioBindingID, target.PortfolioVersionID)
	if _, err := repo.GetResponsibilityEvaluationItem(ctx, itemID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("item error = %v, want ErrNotFound", err)
	}
}

func TestCleanupTaskRunsStopsAtLimit(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	now := schedulerTestTime.Add(10 * 24 * time.Hour)
	for index := 0; index < 501; index++ {
		run := maintenanceRetentionRun(fmt.Sprintf("expired-%03d", index), "internal-task", RunStatusCompleted, now.Add(-48*time.Hour-time.Duration(index)*time.Second))
		if err := repo.SaveRun(ctx, run); err != nil {
			t.Fatalf("SaveRun(%d): %v", index, err)
		}
	}

	deleted, err := repo.CleanupTaskRuns(ctx, "internal-task", now, 24*time.Hour, 7*24*time.Hour, 500)
	if err != nil || deleted != 500 {
		t.Fatalf("cleanup deleted=%d err=%v", deleted, err)
	}
	runs, err := repo.ListRunsForTask(ctx, "internal-task")
	if err != nil || len(runs) != 1 {
		t.Fatalf("remaining runs=%d err=%v", len(runs), err)
	}
}

func TestCleanupTaskRunCandidatesRechecksLockedStateAndFillsLimit(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	now := schedulerTestTime.Add(10 * 24 * time.Hour)
	taskID := "internal-task"
	staleStatus := maintenanceRetentionRun("expired-stale-status", taskID, RunStatusCompleted, now.Add(-96*time.Hour))
	staleTask := maintenanceRetentionRun("expired-stale-task", taskID, RunStatusCompleted, now.Add(-72*time.Hour))
	staleRetention := maintenanceRetentionRun("expired-stale-retention", taskID, RunStatusCompleted, now.Add(-60*time.Hour))
	eligible := maintenanceRetentionRun("expired-eligible", taskID, RunStatusCompleted, now.Add(-48*time.Hour))
	for _, run := range []ScheduledRun{staleStatus, staleTask, staleRetention, eligible} {
		if err := repo.SaveRun(ctx, run); err != nil {
			t.Fatalf("SaveRun(%s): %v", run.ScheduledRunID, err)
		}
	}
	store, table, err := env.DBRouter.StoreForTable(scheduledRunTable)
	if err != nil {
		t.Fatal(err)
	}
	deleted := 0
	err = store.WithTx(ctx, func(tx db.Tx) error {
		staleStatus.Status = RunStatusRunning
		staleStatus.CompletedAt = time.Time{}
		staleTask.ScheduledTaskID = "other-task"
		staleRetention.CompletedAt = now.Add(-time.Hour)
		for _, run := range []ScheduledRun{staleStatus, staleTask, staleRetention} {
			record, err := scheduledRunRecord(run)
			if err != nil {
				return err
			}
			if err := db.PutRecordTx(ctx, tx, table, run.ScheduledRunID, record); err != nil {
				return err
			}
		}
		deleted, err = cleanupTaskRunCandidatesTx(
			ctx,
			tx,
			table,
			responsibilityEvaluationManifestTable,
			responsibilityEvaluationItemTable,
			scheduledRunReadReceiptTable,
			taskID,
			[]taskRunRetentionCandidate{
				{key: staleStatus.ScheduledRunID, anchorAt: now.Add(-96 * time.Hour)},
				{key: staleTask.ScheduledRunID, anchorAt: now.Add(-72 * time.Hour)},
				{key: staleRetention.ScheduledRunID, anchorAt: now.Add(-60 * time.Hour)},
				{key: eligible.ScheduledRunID, anchorAt: now.Add(-48 * time.Hour)},
			},
			now,
			24*time.Hour,
			7*24*time.Hour,
			1,
		)
		return err
	})
	if err != nil || deleted != 1 {
		t.Fatalf("cleanup candidates deleted=%d err=%v", deleted, err)
	}
	for _, id := range []string{staleStatus.ScheduledRunID, staleTask.ScheduledRunID, staleRetention.ScheduledRunID} {
		if _, err := repo.GetRun(ctx, id); err != nil {
			t.Fatalf("stale candidate %s error = %v", id, err)
		}
	}
	if _, err := repo.GetRun(ctx, eligible.ScheduledRunID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("eligible candidate error = %v, want ErrNotFound", err)
	}
}

func TestCleanupTaskRunCandidatesRejectsChangedRecordIdentity(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	now := schedulerTestTime.Add(10 * 24 * time.Hour)
	run := maintenanceRetentionRun("expired-identity", "internal-task", RunStatusCompleted, now.Add(-48*time.Hour))
	if err := repo.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	store, table, err := env.DBRouter.StoreForTable(scheduledRunTable)
	if err != nil {
		t.Fatal(err)
	}
	err = store.WithTx(ctx, func(tx db.Tx) error {
		candidateKey := run.ScheduledRunID
		run.ScheduledRunID = "other-run"
		record, err := scheduledRunRecord(run)
		if err != nil {
			return err
		}
		if err := db.PutRecordTx(ctx, tx, table, candidateKey, record); err != nil {
			return err
		}
		_, err = cleanupTaskRunCandidatesTx(
			ctx, tx, table, responsibilityEvaluationManifestTable, responsibilityEvaluationItemTable, scheduledRunReadReceiptTable, run.ScheduledTaskID,
			[]taskRunRetentionCandidate{{key: candidateKey, anchorAt: run.CompletedAt}},
			now, 24*time.Hour, 7*24*time.Hour, 1,
		)
		if !errors.Is(err, ErrServiceDependency) {
			return fmt.Errorf("cleanup identity error = %v, want ErrServiceDependency", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func maintenanceRetentionRun(id, taskID string, status RunStatus, at time.Time) ScheduledRun {
	run := schedulerTestRun(id, taskID, status, at)
	if status == RunStatusCompleted || status == RunStatusFailed || status == RunStatusSkipped {
		run.CompletedAt = at
	}
	return run
}
