package main

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"orrery_v2/internal/app/ontology"
	schedulerapp "orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/platform/db"
)

func TestProductionOntologyStateRefreshReadsOnlyCurrentTasksAndBoundsDailyWork(t *testing.T) {
	ctx := context.Background()
	core := newOntologyEventLogTestCore(t, "production-ontology-state-refresh-bounded")
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 21, 1, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	sink := newProductionAgentExecutionOntologyTestSink(core, runner, clock)
	repository := &recordingOntologyStateRefreshRepository{tasks: []schedulerapp.ScheduledTask{ontologyStateRefreshTask(now)}}
	refresher := newProductionOntologyStateRefresher(core, repository, sink, clock)

	first, err := refresher.Refresh(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if first.SourceCount != 1 || first.ChangedCount != 4 || first.SnapshotOnlyCount != 0 || first.NoopCount != 0 || first.StaleCount != 0 {
		t.Fatalf("first refresh = %+v", first)
	}
	if repository.listTasksCalls != 1 || repository.listRunsCalls != 0 {
		t.Fatalf("repository calls = tasks:%d runs:%d", repository.listTasksCalls, repository.listRunsCalls)
	}

	second, err := refresher.Refresh(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if second.Fingerprint != first.Fingerprint || second.SourceCount != 1 || second.ChangedCount != 0 || second.SnapshotOnlyCount != 0 || second.NoopCount != 0 || second.StaleCount != 0 {
		t.Fatalf("second refresh = %+v", second)
	}

	now = now.Add(24 * time.Hour)
	third, err := refresher.Refresh(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if third.Fingerprint == first.Fingerprint || third.SourceCount != 1 || third.SnapshotOnlyCount != 4 || third.ChangedCount != 0 || third.NoopCount != 0 || third.StaleCount != 0 {
		t.Fatalf("next-day refresh = %+v", third)
	}
	if repository.listTasksCalls != 3 || repository.listRunsCalls != 0 {
		t.Fatalf("repository calls = tasks:%d runs:%d", repository.listTasksCalls, repository.listRunsCalls)
	}
	assertOntologyStateRefreshManifestCount(t, ctx, core, 2)
}

func TestProductionOntologyStateRefreshRecordsNoopAndStaleCounts(t *testing.T) {
	for _, tc := range []struct {
		name      string
		prepare   func(*productionAgentExecutionOntologySink, schedulerapp.ScheduledTask) error
		wantNoop  int
		wantStale int
	}{
		{
			name: "noop",
			prepare: func(sink *productionAgentExecutionOntologySink, task schedulerapp.ScheduledTask) error {
				return sink.ProjectScheduledTask(context.Background(), task)
			},
			wantNoop: 4,
		},
		{
			name: "stale",
			prepare: func(sink *productionAgentExecutionOntologySink, task schedulerapp.ScheduledTask) error {
				newer := task
				newer.UpdatedAt = task.UpdatedAt.Add(time.Hour)
				return sink.ProjectScheduledTask(context.Background(), newer)
			},
			wantStale: 4,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			core := newOntologyEventLogTestCore(t, "production-ontology-state-refresh-"+tc.name)
			runner, err := newProductionOntologyProjectionRunner(ctx, core)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, 8, 21, 1, 0, 0, 0, time.UTC)
			task := ontologyStateRefreshTask(now)
			task.CreatedAt = now.Add(-2 * time.Hour)
			sink := newProductionAgentExecutionOntologyTestSink(core, runner, func() time.Time { return now })
			if err := tc.prepare(sink, task); err != nil {
				t.Fatal(err)
			}
			refresher := newProductionOntologyStateRefresher(core, &recordingOntologyStateRefreshRepository{tasks: []schedulerapp.ScheduledTask{task}}, sink, func() time.Time { return now })
			report, err := refresher.Refresh(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if report.NoopCount != tc.wantNoop || report.StaleCount != tc.wantStale || report.ChangedCount != 0 || report.SnapshotOnlyCount != 0 {
				t.Fatalf("refresh = %+v", report)
			}
		})
	}
}

func TestProductionOntologyStateRefreshRepairsEqualVersionLegacyState(t *testing.T) {
	ctx := context.Background()
	core := newOntologyEventLogTestCore(t, "production-ontology-state-refresh-equal-version-repair")
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 21, 1, 0, 0, 0, time.UTC)
	current := ontologyStateRefreshTask(now)
	legacy := current
	legacy.Title = "Legacy semantic payload"
	sink := newProductionAgentExecutionOntologyTestSink(core, runner, func() time.Time { return now })
	if err := sink.ProjectScheduledTask(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	if err := sink.ProjectScheduledTask(ctx, current); !errors.Is(err, ontology.ErrStateProjectionConflict) {
		t.Fatalf("live equal-version projection error = %v, want conflict", err)
	}

	refresher := newProductionOntologyStateRefresher(
		core,
		&recordingOntologyStateRefreshRepository{tasks: []schedulerapp.ScheduledTask{current}},
		sink,
		func() time.Time { return now },
	)
	report, err := refresher.Refresh(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.ChangedCount != 2 || report.NoopCount != 2 || report.StaleCount != 0 {
		t.Fatalf("repair refresh = %+v", report)
	}
}

func TestProductionOntologyStateRefreshRecordsSourceAndProjectionFailures(t *testing.T) {
	for _, tc := range []struct {
		name            string
		repository      *recordingOntologyStateRefreshRepository
		breakProjection bool
	}{
		{name: "source", repository: &recordingOntologyStateRefreshRepository{err: errors.New("list current tasks failed")}},
		{name: "projection", repository: &recordingOntologyStateRefreshRepository{tasks: []schedulerapp.ScheduledTask{ontologyStateRefreshTask(time.Date(2026, 8, 21, 1, 0, 0, 0, time.UTC))}}, breakProjection: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			core := newOntologyEventLogTestCore(t, "production-ontology-state-refresh-failure-"+tc.name)
			runner, err := newProductionOntologyProjectionRunner(ctx, core)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, 8, 21, 1, 0, 0, 0, time.UTC)
			sink := newProductionAgentExecutionOntologyTestSink(core, runner, func() time.Time { return now })
			if tc.breakProjection {
				sink.state.store = nil
			}
			refresher := newProductionOntologyStateRefresher(core, tc.repository, sink, func() time.Time { return now })
			if _, err := refresher.Refresh(ctx); err == nil {
				t.Fatal("refresh unexpectedly succeeded")
			}
			assertOntologyStateRefreshManifestCount(t, ctx, core, 1)
			var status string
			if err := core.QueryRow(ctx, `SELECT status FROM ontology_refresh_manifests`).Scan(&status); err != nil {
				t.Fatal(err)
			}
			if status != string(ontology.StateRefreshFailed) {
				t.Fatalf("manifest status = %q", status)
			}
		})
	}
}

func TestProductionOntologyStateRefreshMappingSignatureTracksRuleDefinitions(t *testing.T) {
	before := []ontology.MappingRule{{ID: "rule-1", EventType: "event.created", RuleHash: "before", Enabled: true}}
	after := []ontology.MappingRule{{ID: "rule-1", EventType: "event.created", RuleHash: "after", Enabled: true}}
	first, err := productionOntologyMappingSignature(4, before)
	if err != nil {
		t.Fatal(err)
	}
	changedRule, err := productionOntologyMappingSignature(4, after)
	if err != nil {
		t.Fatal(err)
	}
	changedRevision, err := productionOntologyMappingSignature(5, before)
	if err != nil {
		t.Fatal(err)
	}
	if first == changedRule || first == changedRevision {
		t.Fatalf("mapping signatures did not change: base=%q rule=%q revision=%q", first, changedRule, changedRevision)
	}
}

func TestProductionOntologyStateRefreshMetricIsBoundedOnSuccessAndFailure(t *testing.T) {
	for _, tc := range []struct {
		name       string
		repository *recordingOntologyStateRefreshRepository
		wantStatus string
		wantErr    bool
	}{
		{name: "success", repository: &recordingOntologyStateRefreshRepository{tasks: []schedulerapp.ScheduledTask{ontologyStateRefreshTask(time.Date(2026, 8, 21, 1, 0, 0, 0, time.UTC))}}, wantStatus: "succeeded"},
		{name: "failure", repository: &recordingOntologyStateRefreshRepository{err: errors.New("secret source detail")}, wantStatus: "failed", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			core := newOntologyEventLogTestCore(t, "production-ontology-state-refresh-metric-"+tc.name)
			runner, err := newProductionOntologyProjectionRunner(ctx, core)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, 8, 21, 1, 0, 0, 0, time.UTC)
			recorder := &capturingOntologyMetricRecorder{}
			sink := newProductionAgentExecutionOntologyTestSink(core, runner, func() time.Time { return now }, recorder)
			refresher := newProductionOntologyStateRefresher(core, tc.repository, sink, func() time.Time { return now }, recorder)
			_, refreshErr := refresher.Refresh(ctx)
			if (refreshErr != nil) != tc.wantErr {
				t.Fatalf("Refresh error = %v", refreshErr)
			}
			metric, found := recorder.last("ontology.state_refresh")
			if !found {
				t.Fatal("ontology.state_refresh metric was not captured")
			}
			allowed := map[string]bool{
				"source_count": true, "changed_count": true, "snapshot_only_count": true,
				"noop_count": true, "stale_count": true, "status": true, "duration_ms": true,
			}
			if len(metric.fields) != len(allowed) {
				t.Fatalf("metric fields = %#v", metric.fields)
			}
			for key := range metric.fields {
				if !allowed[key] {
					t.Fatalf("unbounded metric field %q in %#v", key, metric.fields)
				}
			}
			if !reflect.DeepEqual(metric.fields["status"], tc.wantStatus) {
				t.Fatalf("metric status = %#v, want %q", metric.fields["status"], tc.wantStatus)
			}
		})
	}
}

func TestProductionOntologyStateRefreshAppliesDailySnapshotRetention(t *testing.T) {
	ctx := context.Background()
	core := newOntologyEventLogTestCore(t, "production-ontology-state-refresh-retention")
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 21, 1, 0, 0, 0, time.UTC)
	cutoffDate := now.AddDate(0, 0, -productionOntologyStateSnapshotRetentionDays).Format("2006-01-02")
	oldDate := now.AddDate(0, 0, -productionOntologyStateSnapshotRetentionDays-1).Format("2006-01-02")
	for _, date := range []string{oldDate, cutoffDate} {
		if _, err := core.Exec(ctx, `
INSERT INTO ontology_daily_snapshots (
	tenant_id, event_type, entity_id, snapshot_date, semantic_hash, payload_json,
	source_version, source_updated_at, projected_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			"tenant-retention", "scheduler.task.updated", "task-"+date, date,
			"hash", `{}`, 1, now.UnixNano(), now.UnixNano()); err != nil {
			t.Fatal(err)
		}
	}
	sink := newProductionAgentExecutionOntologyTestSink(core, runner, func() time.Time { return now })
	refresher := newProductionOntologyStateRefresher(core, &recordingOntologyStateRefreshRepository{}, sink, func() time.Time { return now })
	if _, err := refresher.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	var oldCount, retainedCount int
	if err := core.QueryRow(ctx, `SELECT COUNT(*) FROM ontology_daily_snapshots WHERE snapshot_date < ?`, cutoffDate).Scan(&oldCount); err != nil {
		t.Fatal(err)
	}
	if err := core.QueryRow(ctx, `SELECT COUNT(*) FROM ontology_daily_snapshots WHERE snapshot_date = ?`, cutoffDate).Scan(&retainedCount); err != nil {
		t.Fatal(err)
	}
	if oldCount != 0 || retainedCount != 1 {
		t.Fatalf("retention counts = old:%d boundary:%d", oldCount, retainedCount)
	}
}

type recordingOntologyStateRefreshRepository struct {
	tasks          []schedulerapp.ScheduledTask
	err            error
	listTasksCalls int
	listRunsCalls  int
}

func (r *recordingOntologyStateRefreshRepository) ListTasks(context.Context, bool) ([]schedulerapp.ScheduledTask, error) {
	r.listTasksCalls++
	return append([]schedulerapp.ScheduledTask(nil), r.tasks...), r.err
}

func (r *recordingOntologyStateRefreshRepository) ListRunsForTask(context.Context, string) ([]schedulerapp.ScheduledRun, error) {
	r.listRunsCalls++
	return nil, errors.New("state refresh must not list runs")
}

func ontologyStateRefreshTask(now time.Time) schedulerapp.ScheduledTask {
	return schedulerapp.ScheduledTask{
		TenantID: "tenant-state-refresh", ScheduledTaskID: "task-state-refresh", AgentTaskID: "agent-task-state-refresh",
		Title: "Refresh current ontology state", Target: schedulerapp.Target{Mode: schedulerapp.TargetModeAgentTask, AgentID: "agent-state-refresh"},
		Schedule: schedulerapp.Schedule{Type: schedulerapp.ScheduleTypeCron, Cron: "10 8 * * *", Timezone: "Asia/Shanghai"},
		Status:   schedulerapp.TaskStatusEnabled, Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
}

func assertOntologyStateRefreshManifestCount(t *testing.T, ctx context.Context, core *db.Store, want int) {
	t.Helper()
	var count int
	if err := core.QueryRow(ctx, `SELECT COUNT(*) FROM ontology_refresh_manifests`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("manifest rows = %d, want %d", count, want)
	}
}
