package main

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/app/contextgraph"
	schedulerapp "orrery_v2/internal/app/scheduler"
)

func TestScheduledRunOntologyEventsApplyRetentionPolicy(t *testing.T) {
	now := time.Date(2026, 8, 19, 15, 0, 0, 0, time.UTC)
	standardTask := schedulerapp.ScheduledTask{
		TenantID: "tenant-retention", ScheduledTaskID: "scheduled-task-retention",
		Target: schedulerapp.Target{Mode: schedulerapp.TargetModeAgentTask},
	}
	internalTask := defaultResponsibilityReconcileScheduledTask(now)

	tests := []struct {
		name string
		task schedulerapp.ScheduledTask
		run  schedulerapp.ScheduledRun
		want int
	}{
		{
			name: "internal responsibility reconciliation is never projected",
			task: internalTask,
			run:  scheduledRunForRetentionTest("internal-run", internalTask.ScheduledTaskID, schedulerapp.RunStatusCompleted, now.Add(-time.Minute)),
			want: 0,
		},
		{
			name: "expired terminal run is not projected",
			task: standardTask,
			run:  scheduledRunForRetentionTest("expired-run", standardTask.ScheduledTaskID, schedulerapp.RunStatusCompleted, now.Add(-91*24*time.Hour)),
			want: 0,
		},
		{
			name: "recent terminal run remains projected",
			task: standardTask,
			run:  scheduledRunForRetentionTest("recent-run", standardTask.ScheduledTaskID, schedulerapp.RunStatusCompleted, now.Add(-89*24*time.Hour)),
			want: 1,
		},
		{
			name: "expired active run is not projected",
			task: standardTask,
			run:  scheduledRunForRetentionTest("active-run", standardTask.ScheduledTaskID, schedulerapp.RunStatusRunning, now.Add(-120*24*time.Hour)),
			want: 0,
		},
		{
			name: "recent active run remains projected",
			task: standardTask,
			run:  scheduledRunForRetentionTest("recent-active-run", standardTask.ScheduledTaskID, schedulerapp.RunStatusRunning, now.Add(-10*24*time.Hour)),
			want: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := len(scheduledRunOntologyEventsAt(test.task, test.run, now)); got != test.want {
				t.Fatalf("scheduled run event count = %d, want %d", got, test.want)
			}
		})
	}
}

func TestProductionScheduledRunOntologyRetentionSweeperPrunesRebuildableProjection(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 19, 16, 0, 0, 0, time.UTC)
	core := newOntologyEventLogTestCore(t, "production-scheduled-run-ontology-retention")
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatal(err)
	}
	sink := newProductionAgentExecutionOntologyTestSink(core, runner, time.Now)

	internalTask := defaultResponsibilityReconcileScheduledTask(now)
	standardTask := schedulerapp.ScheduledTask{
		TenantID: "tenant-retention", ScheduledTaskID: "scheduled-task-retention",
		Title: "Retained scheduled task", Target: schedulerapp.Target{Mode: schedulerapp.TargetModeAgentTask},
		Status: schedulerapp.TaskStatusEnabled, Enabled: true, CreatedAt: now.Add(-180 * 24 * time.Hour), UpdatedAt: now,
	}
	internalRun := scheduledRunForRetentionTest("internal-run", internalTask.ScheduledTaskID, schedulerapp.RunStatusCompleted, now.Add(-time.Hour))
	orphanedInternalRun := scheduledRunForRetentionTest("orphaned-internal-run", internalTask.ScheduledTaskID, schedulerapp.RunStatusCompleted, now.Add(-2*time.Hour))
	expiredRun := scheduledRunForRetentionTest("expired-run", standardTask.ScheduledTaskID, schedulerapp.RunStatusCompleted, now.Add(-91*24*time.Hour))
	recentRun := scheduledRunForRetentionTest("recent-run", standardTask.ScheduledTaskID, schedulerapp.RunStatusCompleted, now.Add(-10*24*time.Hour))
	expiredActiveRun := scheduledRunForRetentionTest("expired-active-run", standardTask.ScheduledTaskID, schedulerapp.RunStatusRunning, now.Add(-120*24*time.Hour))
	recentActiveRun := scheduledRunForRetentionTest("recent-active-run", standardTask.ScheduledTaskID, schedulerapp.RunStatusRunning, now.Add(-10*24*time.Hour))

	seedScheduledRunProjectionForRetentionTest(t, ctx, sink, internalTask, internalRun)
	for _, event := range scheduledRunOntologyEventsForTenant(internalTask, orphanedInternalRun, internalTask.TenantID) {
		if err := sink.events.Record(ctx, event); err != nil {
			t.Fatalf("record orphaned internal event %s: %v", event.ID, err)
		}
	}
	for _, run := range []schedulerapp.ScheduledRun{expiredRun, recentRun, expiredActiveRun, recentActiveRun} {
		seedScheduledRunProjectionForRetentionTest(t, ctx, sink, standardTask, run)
	}

	sweeper := productionScheduledRunOntologyRetentionSweeper{core: core, now: func() time.Time { return now }}
	if err := sweeper.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}

	for _, tenantID := range []string{"system", defaultSystemSupportOrganizationID} {
		objects, _, _, _, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, tenantID)
		if err != nil {
			t.Fatal(err)
		}
		assertContextGraphObjectAbsentForRetentionTest(t, objects, internalRun.ScheduledRunID)
	}
	objects, _, _, evidence, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, standardTask.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	assertContextGraphObjectAbsentForRetentionTest(t, objects, expiredRun.ScheduledRunID)
	assertContextGraphObjectAbsentForRetentionTest(t, objects, expiredActiveRun.ScheduledRunID)
	assertContextGraphObject(t, objects, "scheduled_run", recentRun.ScheduledRunID)
	assertContextGraphObject(t, objects, "scheduled_run", recentActiveRun.ScheduledRunID)
	if len(evidence) != 4 {
		t.Fatalf("tenant evidence count = %d, want four task/run events", len(evidence))
	}

	var eventCount, orphanedIdempotency int
	if err := core.QueryRow(ctx, `SELECT COUNT(*) FROM platform_events WHERE event_type LIKE 'ontology.scheduler.run.%'`).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if eventCount != 3 {
		t.Fatalf("first sweep scheduled run event count = %d, want two retained and one orphaned", eventCount)
	}
	if err := core.QueryRow(ctx, `
SELECT COUNT(*)
FROM platform_event_idempotency i
LEFT JOIN platform_events e ON e.event_id = i.event_id
WHERE e.event_id IS NULL`).Scan(&orphanedIdempotency); err != nil {
		t.Fatal(err)
	}
	if orphanedIdempotency != 0 {
		t.Fatalf("orphaned event idempotency rows = %d, want 0", orphanedIdempotency)
	}

	if err := sweeper.Sweep(ctx); err != nil {
		t.Fatalf("repeat Sweep: %v", err)
	}
	if err := core.QueryRow(ctx, `SELECT COUNT(*) FROM platform_events WHERE event_type LIKE 'ontology.scheduler.run.%'`).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	// An unprojected legacy event has no graph evidence. It is deliberately
	// outside the recurring hot path, which must never scan all event history.
	if eventCount != 3 {
		t.Fatalf("repeat sweep retained event count = %d, want 3", eventCount)
	}
}

func scheduledRunForRetentionTest(id, taskID string, status schedulerapp.RunStatus, at time.Time) schedulerapp.ScheduledRun {
	run := schedulerapp.ScheduledRun{
		ScheduledRunID: id, ScheduledTaskID: taskID, Status: status,
		TargetMode: schedulerapp.TargetModeAgentTask, CreatedAt: at, StartedAt: at,
	}
	if status == schedulerapp.RunStatusCompleted || status == schedulerapp.RunStatusFailed || status == schedulerapp.RunStatusSkipped {
		run.CompletedAt = at
	}
	return run
}

func seedScheduledRunProjectionForRetentionTest(
	t *testing.T,
	ctx context.Context,
	sink *productionAgentExecutionOntologySink,
	task schedulerapp.ScheduledTask,
	run schedulerapp.ScheduledRun,
) {
	t.Helper()
	for _, event := range scheduledTaskOntologyEvents(task) {
		if err := sink.apply(ctx, event); err != nil {
			t.Fatalf("project task event %s: %v", event.ID, err)
		}
	}
	for _, tenantID := range scheduledTaskProjectionTenantIDs(task) {
		for _, event := range scheduledRunOntologyEventsForTenant(task, run, tenantID) {
			if err := sink.apply(ctx, event); err != nil {
				t.Fatalf("project run event %s: %v", event.ID, err)
			}
		}
	}
}

func assertContextGraphObjectAbsentForRetentionTest(t *testing.T, objects []contextgraph.Object, objectID string) {
	t.Helper()
	for _, object := range objects {
		if object.ID == objectID {
			t.Fatalf("context graph object %s was retained: %+v", objectID, object)
		}
	}
}
