package ontology

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/metrics"
	"orrery_v2/internal/platform/migration"
)

func TestBuildModelRunEvaluationTrendMetricSamples(t *testing.T) {
	snapshot := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	page := ModelRunEvaluationTrendPage{
		Buckets: []ModelRunEvaluationTrendBucket{{
			TenantID:     "tenant-1",
			Engine:       "ontology_designer",
			Model:        "gpt-5",
			ModelVersion: "2026-06-21",
			RunTotal:     3,
			StatusCounts: map[ModelRunEvaluationGateStatus]int{
				ModelRunEvaluationGatePassed:  2,
				ModelRunEvaluationGateBlocked: 1,
			},
			ReasonCounts: map[ModelRunEvaluationReasonCode]int{
				ModelRunEvaluationReasonBlockingIssues: 1,
			},
			WarningCounts: map[ModelRunEvaluationReasonCode]int{
				ModelRunEvaluationReasonMetricThresholdFailed: 2,
			},
			MetricThresholdResultCounts: map[string]map[ModelRunEvaluationThresholdResultStatus]int{
				"accuracy-release-gate": {
					ModelRunEvaluationThresholdResultFailed: 2,
				},
			},
			ProposalTotal:    5,
			BlockingTotal:    1,
			WarningTotal:     2,
			GateWarningTotal: 2,
		}},
		SnapshotRecordedTo: snapshot,
	}

	samples := BuildModelRunEvaluationTrendMetricSamples(page)

	if len(samples) == 0 {
		t.Fatalf("expected metric samples")
	}
	assertTrendMetricSample(t, samples, "orrery_ontology_model_run_evaluation_runs_total", 3, map[string]string{
		"tenant_id":            "tenant-1",
		"engine":               "ontology_designer",
		"model":                "gpt-5",
		"model_version":        "2026-06-21",
		"snapshot_recorded_to": snapshot.Format(time.RFC3339Nano),
	})
	assertTrendMetricSample(t, samples, "orrery_ontology_model_run_evaluation_status_total", 2, map[string]string{
		"status": "passed",
	})
	assertTrendMetricSample(t, samples, "orrery_ontology_model_run_evaluation_reason_total", 1, map[string]string{
		"reason": "blocking_issues",
	})
	assertTrendMetricSample(t, samples, "orrery_ontology_model_run_evaluation_warning_total", 2, map[string]string{
		"warning": "metric_threshold_failed",
	})
	assertTrendMetricSample(t, samples, "orrery_ontology_model_run_evaluation_threshold_result_total", 2, map[string]string{
		"rule_id":          "accuracy-release-gate",
		"threshold_status": "failed",
	})
	assertTrendMetricSample(t, samples, "orrery_ontology_model_run_evaluation_proposals_total", 5, nil)
	assertTrendMetricSample(t, samples, "orrery_ontology_model_run_evaluation_analysis_blocking_total", 1, nil)
	assertTrendMetricSample(t, samples, "orrery_ontology_model_run_evaluation_analysis_warning_total", 2, nil)
	assertTrendMetricSample(t, samples, "orrery_ontology_model_run_evaluation_gate_warning_total", 2, nil)
}

func TestRecordModelRunEvaluationTrendMetricSamples(t *testing.T) {
	sink := metrics.NewBoundedSink(10)
	samples := []ModelRunEvaluationTrendMetricSample{{
		Name: "orrery_ontology_model_run_evaluation_runs_total",
		Labels: map[string]string{
			"tenant_id": "tenant-1",
			"engine":    "ontology_designer",
		},
		Value: 3,
	}}

	recorded := RecordModelRunEvaluationTrendMetricSamples(context.Background(), sink, samples)

	if recorded != 1 {
		t.Fatalf("recorded = %d", recorded)
	}
	events := sink.Drain(10)
	if len(events) != 1 ||
		events[0].Name != "orrery_ontology_model_run_evaluation_runs_total" ||
		events[0].Fields["tenant_id"] != "tenant-1" ||
		events[0].Fields["value"] != float64(3) {
		t.Fatalf("events = %+v", events)
	}
}

func TestRecordModelRunEvaluationTrendPageMetrics(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryModelRunEvaluationTrendMetricRecordingStore()
	recordedAt := time.Date(2026, 6, 22, 11, 0, 0, 0, time.UTC)
	page := ModelRunEvaluationTrendPage{
		Buckets: []ModelRunEvaluationTrendBucket{{
			TenantID:     "tenant-1",
			Engine:       "ontology_designer",
			Model:        "gpt-5",
			ModelVersion: "2026-06-21",
			RunTotal:     2,
			StatusCounts: map[ModelRunEvaluationGateStatus]int{ModelRunEvaluationGatePassed: 2},
		}},
		SnapshotRecordedTo: time.Date(2026, 6, 22, 0, 0, 0, 0, time.UTC),
	}

	recorded, err := RecordModelRunEvaluationTrendPageMetrics(ctx, store, page, recordedAt)
	if err != nil {
		t.Fatalf("RecordModelRunEvaluationTrendPageMetrics: %v", err)
	}
	if recorded == 0 {
		t.Fatalf("expected recorded metrics")
	}
	recordings, err := store.ListModelRunEvaluationTrendMetricRecordings(ctx, ModelRunEvaluationTrendMetricRecordingQuery{
		TenantID:   "tenant-1",
		MetricName: "orrery_ontology_model_run_evaluation_runs_total",
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationTrendMetricRecordings: %v", err)
	}
	if recordings.Page.Total != 1 ||
		recordings.Recordings[0].MetricSample.Value != 2 ||
		!recordings.Recordings[0].RecordedAt.Equal(recordedAt) ||
		recordings.Recordings[0].MetricSample.Labels["snapshot_recorded_to"] != page.SnapshotRecordedTo.Format(time.RFC3339Nano) {
		t.Fatalf("recordings = %+v", recordings)
	}
}

func TestRecordModelRunEvaluationTrendPageMetricsKeepsThresholdRulesDistinct(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryModelRunEvaluationTrendMetricRecordingStore()
	recordedAt := time.Date(2026, 6, 22, 12, 30, 0, 0, time.UTC)
	page := ModelRunEvaluationTrendPage{
		Buckets: []ModelRunEvaluationTrendBucket{{
			TenantID:     "tenant-1",
			Engine:       "ontology_designer",
			Model:        "gpt-5",
			ModelVersion: "2026-06-21",
			MetricThresholdResultCounts: map[string]map[ModelRunEvaluationThresholdResultStatus]int{
				"accuracy-release-gate": {
					ModelRunEvaluationThresholdResultFailed: 1,
				},
				"drift-warning": {
					ModelRunEvaluationThresholdResultFailed: 1,
				},
			},
		}},
		SnapshotRecordedTo: recordedAt,
	}

	if _, err := RecordModelRunEvaluationTrendPageMetrics(ctx, store, page, recordedAt); err != nil {
		t.Fatalf("RecordModelRunEvaluationTrendPageMetrics: %v", err)
	}
	recordings, err := store.ListModelRunEvaluationTrendMetricRecordings(ctx, ModelRunEvaluationTrendMetricRecordingQuery{
		TenantID:   "tenant-1",
		MetricName: "orrery_ontology_model_run_evaluation_threshold_result_total",
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationTrendMetricRecordings: %v", err)
	}
	if recordings.Page.Total != 2 {
		t.Fatalf("recordings = %+v", recordings)
	}
	seen := map[string]bool{}
	for _, recording := range recordings.Recordings {
		seen[recording.MetricSample.Labels["rule_id"]] = true
		if recording.MetricSample.Labels["threshold_status"] != "failed" {
			t.Fatalf("recording = %+v", recording)
		}
	}
	if !seen["accuracy-release-gate"] || !seen["drift-warning"] {
		t.Fatalf("seen rule recordings = %+v", seen)
	}

	filtered, err := store.ListModelRunEvaluationTrendMetricRecordings(ctx, ModelRunEvaluationTrendMetricRecordingQuery{
		TenantID:   "tenant-1",
		MetricName: "orrery_ontology_model_run_evaluation_threshold_result_total",
		LabelKey:   "rule_id",
		LabelValue: "accuracy-release-gate",
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationTrendMetricRecordings filtered: %v", err)
	}
	if filtered.Page.Total != 1 || filtered.Recordings[0].MetricSample.Labels["rule_id"] != "accuracy-release-gate" {
		t.Fatalf("filtered recordings = %+v", filtered)
	}
}

func TestRecordModelRunEvaluationTrendMetrics(t *testing.T) {
	ctx := context.Background()
	ledger := NewMemoryModelRunLedger()
	artifacts := NewMemoryModelRunEvaluationArtifactStore()
	policies := NewMemoryModelRunEvaluationPolicyProfileStore()
	recordings := NewMemoryModelRunEvaluationTrendMetricRecordingStore()
	recordedAt := time.Date(2026, 6, 22, 11, 30, 0, 0, time.UTC)
	if err := ledger.SaveModelRun(ctx, OntologyModelRun{
		TenantID:      "tenant-1",
		Engine:        "ontology_designer",
		Model:         "gpt-5",
		ModelVersion:  "2026-06-21",
		RunID:         "run-1",
		InputHash:     "sha256:input",
		PromptHash:    "sha256:prompt",
		Status:        ModelRunStatusSucceeded,
		ProposalCount: 1,
		RecordedAt:    recordedAt.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("SaveModelRun: %v", err)
	}

	recorded, page, err := RecordModelRunEvaluationTrendMetrics(ctx, ledger, artifacts, policies, recordings, ModelRunEvaluationTrendQuery{
		TenantID:     "tenant-1",
		Engine:       "ontology_designer",
		RecordedFrom: recordedAt.Add(-2 * time.Hour),
		RecordedTo:   recordedAt.Add(time.Hour),
		Limit:        10,
	}, recordedAt)
	if err != nil {
		t.Fatalf("RecordModelRunEvaluationTrendMetrics: %v", err)
	}
	if page.Page.Total != 1 || recorded == 0 {
		t.Fatalf("recorded=%d page=%+v", recorded, page)
	}
	saved, err := recordings.ListModelRunEvaluationTrendMetricRecordings(ctx, ModelRunEvaluationTrendMetricRecordingQuery{
		TenantID:   "tenant-1",
		MetricName: "orrery_ontology_model_run_evaluation_runs_total",
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationTrendMetricRecordings: %v", err)
	}
	if saved.Page.Total != 1 ||
		saved.Recordings[0].MetricSample.Value != 1 ||
		!saved.Recordings[0].RecordedAt.Equal(recordedAt) {
		t.Fatalf("saved recordings = %+v", saved)
	}
}

func TestCollectModelRunEvaluationTrendMetricRecordings(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryModelRunEvaluationTrendMetricRecordingStore()
	sink := metrics.NewBoundedSink(10)
	recordedAt := time.Date(2026, 6, 22, 13, 0, 0, 0, time.UTC)
	for _, ruleID := range []string{"accuracy-release-gate", "drift-warning"} {
		if err := store.SaveModelRunEvaluationTrendMetricRecording(ctx, ModelRunEvaluationTrendMetricRecording{
			MetricSample: ModelRunEvaluationTrendMetricSample{
				Name: "orrery_ontology_model_run_evaluation_threshold_result_total",
				Labels: map[string]string{
					"tenant_id":        "tenant-1",
					"engine":           "ontology_designer",
					"rule_id":          ruleID,
					"threshold_status": "failed",
					"model":            "gpt-5",
					"model_version":    "2026-06-21",
				},
				Value: 1,
			},
			RecordedAt: recordedAt,
		}); err != nil {
			t.Fatalf("SaveModelRunEvaluationTrendMetricRecording %s: %v", ruleID, err)
		}
	}

	collected, page, err := CollectModelRunEvaluationTrendMetricRecordings(ctx, store, sink, ModelRunEvaluationTrendMetricRecordingQuery{
		TenantID:   "tenant-1",
		MetricName: "orrery_ontology_model_run_evaluation_threshold_result_total",
		LabelKey:   "rule_id",
		LabelValue: "accuracy-release-gate",
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("CollectModelRunEvaluationTrendMetricRecordings: %v", err)
	}
	if collected != 1 || page.Page.Total != 1 {
		t.Fatalf("collected=%d page=%+v", collected, page)
	}
	events := sink.Drain(10)
	if len(events) != 1 ||
		events[0].Name != "orrery_ontology_model_run_evaluation_threshold_result_total" ||
		events[0].Fields["rule_id"] != "accuracy-release-gate" ||
		events[0].Fields["threshold_status"] != "failed" ||
		events[0].Fields["value"] != float64(1) {
		t.Fatalf("events = %+v", events)
	}

	collected, page, err = CollectModelRunEvaluationTrendMetricRecordings(ctx, store, nil, ModelRunEvaluationTrendMetricRecordingQuery{
		TenantID:   "tenant-1",
		MetricName: "orrery_ontology_model_run_evaluation_threshold_result_total",
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("CollectModelRunEvaluationTrendMetricRecordings nil sink: %v", err)
	}
	if collected != 0 || page.Page.Total != 2 {
		t.Fatalf("nil sink collected=%d page=%+v", collected, page)
	}
}

func TestCollectAllModelRunEvaluationTrendMetricRecordingsPaginatesRecordingStore(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryModelRunEvaluationTrendMetricRecordingStore()
	sink := metrics.NewBoundedSink(10)
	recordedAt := time.Date(2026, 6, 22, 14, 0, 0, 0, time.UTC)
	for i, ruleID := range []string{"accuracy-release-gate", "drift-warning", "latency-release-gate"} {
		if err := store.SaveModelRunEvaluationTrendMetricRecording(ctx, ModelRunEvaluationTrendMetricRecording{
			MetricSample: ModelRunEvaluationTrendMetricSample{
				Name: "orrery_ontology_model_run_evaluation_threshold_result_total",
				Labels: map[string]string{
					"tenant_id":        "tenant-1",
					"engine":           "ontology_designer",
					"rule_id":          ruleID,
					"threshold_status": "failed",
					"model":            "gpt-5",
					"model_version":    "2026-06-21",
				},
				Value: 1,
			},
			RecordedAt: recordedAt.Add(time.Duration(i) * time.Minute),
		}); err != nil {
			t.Fatalf("SaveModelRunEvaluationTrendMetricRecording %s: %v", ruleID, err)
		}
	}

	report, err := CollectAllModelRunEvaluationTrendMetricRecordings(ctx, store, sink, ModelRunEvaluationTrendMetricRecordingQuery{
		TenantID:   "tenant-1",
		MetricName: "orrery_ontology_model_run_evaluation_threshold_result_total",
		Limit:      2,
	})
	if err != nil {
		t.Fatalf("CollectAllModelRunEvaluationTrendMetricRecordings: %v", err)
	}
	if report.Collected != 3 || report.Scanned != 3 || report.Pages != 2 {
		t.Fatalf("report = %+v", report)
	}
	if events := sink.Drain(10); len(events) != 3 {
		t.Fatalf("events = %+v", events)
	}
}

func TestModelRunEvaluationTrendMetricRecordingSweeperCollectsRecordingPages(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryModelRunEvaluationTrendMetricRecordingStore()
	sink := metrics.NewBoundedSink(10)
	recordedAt := time.Date(2026, 6, 22, 15, 0, 0, 0, time.UTC)
	for i, ruleID := range []string{"accuracy-release-gate", "drift-warning", "latency-release-gate"} {
		if err := store.SaveModelRunEvaluationTrendMetricRecording(ctx, ModelRunEvaluationTrendMetricRecording{
			MetricSample: ModelRunEvaluationTrendMetricSample{
				Name: "orrery_ontology_model_run_evaluation_threshold_result_total",
				Labels: map[string]string{
					"tenant_id":        "tenant-1",
					"engine":           "ontology_designer",
					"rule_id":          ruleID,
					"threshold_status": "failed",
					"model":            "gpt-5",
					"model_version":    "2026-06-21",
				},
				Value: 1,
			},
			RecordedAt: recordedAt.Add(time.Duration(i) * time.Minute),
		}); err != nil {
			t.Fatalf("SaveModelRunEvaluationTrendMetricRecording %s: %v", ruleID, err)
		}
	}
	sweeper := NewModelRunEvaluationTrendMetricRecordingSweeper(ModelRunEvaluationTrendMetricRecordingSweeperOptions{
		Store: store,
		Sink:  sink,
		Query: ModelRunEvaluationTrendMetricRecordingQuery{
			TenantID:   "tenant-1",
			MetricName: "orrery_ontology_model_run_evaluation_threshold_result_total",
			Limit:      2,
		},
	})

	if sweeper.Name() != "ontology.model_run_evaluation_metric_recording.collector" {
		t.Fatalf("sweeper name = %q", sweeper.Name())
	}
	if err := sweeper.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if report := sweeper.LastReport(); report.Collected != 3 || report.Scanned != 3 || report.Pages != 2 {
		t.Fatalf("last report = %+v", report)
	}
	if events := sink.Drain(10); len(events) != 3 {
		t.Fatalf("events = %+v", events)
	}
}

func TestModelRunEvaluationTrendMetricRecordingSweeperUsesRollingLookbackWindow(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryModelRunEvaluationTrendMetricRecordingStore()
	sink := metrics.NewBoundedSink(10)
	now := time.Date(2026, 6, 22, 15, 0, 0, 0, time.UTC)
	for _, recordedAt := range []time.Time{
		now.Add(-2 * time.Hour),
		now.Add(-45 * time.Minute),
		now.Add(-15 * time.Minute),
	} {
		if err := store.SaveModelRunEvaluationTrendMetricRecording(ctx, ModelRunEvaluationTrendMetricRecording{
			MetricSample: ModelRunEvaluationTrendMetricSample{
				Name: "orrery_ontology_model_run_evaluation_runs_total",
				Labels: map[string]string{
					"tenant_id": "tenant-1",
					"engine":    "ontology_designer",
				},
				Value: 1,
			},
			RecordedAt: recordedAt,
		}); err != nil {
			t.Fatalf("SaveModelRunEvaluationTrendMetricRecording %s: %v", recordedAt, err)
		}
	}
	sweeper := NewModelRunEvaluationTrendMetricRecordingSweeper(ModelRunEvaluationTrendMetricRecordingSweeperOptions{
		Store:    store,
		Sink:     sink,
		Clock:    clock.NewFake(now),
		Lookback: time.Hour,
		Query: ModelRunEvaluationTrendMetricRecordingQuery{
			TenantID:   "tenant-1",
			MetricName: "orrery_ontology_model_run_evaluation_runs_total",
			Limit:      10,
		},
	})

	if err := sweeper.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if report := sweeper.LastReport(); report.Collected != 2 || report.Scanned != 2 || report.Pages != 1 {
		t.Fatalf("last report = %+v", report)
	}
	events := sink.Drain(10)
	if len(events) != 2 {
		t.Fatalf("events = %+v", events)
	}
}

func TestMemoryModelRunEvaluationTrendMetricRecordingStoreSaveAndList(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryModelRunEvaluationTrendMetricRecordingStore()
	recordedAt := time.Date(2026, 6, 22, 10, 0, 0, 0, time.UTC)

	if err := store.SaveModelRunEvaluationTrendMetricRecording(ctx, ModelRunEvaluationTrendMetricRecording{
		MetricSample: ModelRunEvaluationTrendMetricSample{
			Name: "orrery_ontology_model_run_evaluation_runs_total",
			Labels: map[string]string{
				"tenant_id":            "tenant-1",
				"engine":               "ontology_designer",
				"model":                "gpt-5",
				"model_version":        "2026-06-21",
				"snapshot_recorded_to": "2026-06-22T00:00:00Z",
			},
			Value: 3,
		},
		RecordedAt: recordedAt,
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationTrendMetricRecording: %v", err)
	}
	if err := store.SaveModelRunEvaluationTrendMetricRecording(ctx, ModelRunEvaluationTrendMetricRecording{
		MetricSample: ModelRunEvaluationTrendMetricSample{
			Name:   "orrery_ontology_model_run_evaluation_runs_total",
			Labels: map[string]string{"tenant_id": "tenant-2"},
			Value:  1,
		},
		RecordedAt: recordedAt,
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationTrendMetricRecording tenant-2: %v", err)
	}

	page, err := store.ListModelRunEvaluationTrendMetricRecordings(ctx, ModelRunEvaluationTrendMetricRecordingQuery{
		TenantID:   "tenant-1",
		MetricName: "orrery_ontology_model_run_evaluation_runs_total",
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationTrendMetricRecordings: %v", err)
	}
	if page.Page.Total != 1 || len(page.Recordings) != 1 {
		t.Fatalf("page = %+v", page)
	}
	recording := page.Recordings[0]
	if recording.ID == "" ||
		recording.MetricSample.Name != "orrery_ontology_model_run_evaluation_runs_total" ||
		recording.TenantID != "tenant-1" ||
		recording.MetricSample.Labels["model_version"] != "2026-06-21" ||
		recording.MetricSample.Value != 3 ||
		!recording.RecordedAt.Equal(recordedAt) {
		t.Fatalf("recording = %+v", recording)
	}
}

func TestPlatformModelRunEvaluationTrendMetricRecordingStoreSaveAndList(t *testing.T) {
	ctx := context.Background()
	core := newMigratedMetricRecordingCore(t, "platform-ontology-metric-recording")
	store := NewPlatformModelRunEvaluationTrendMetricRecordingStore(core)
	recordedAt := time.Date(2026, 6, 22, 10, 30, 0, 0, time.UTC)

	if err := store.SaveModelRunEvaluationTrendMetricRecording(ctx, ModelRunEvaluationTrendMetricRecording{
		MetricSample: ModelRunEvaluationTrendMetricSample{
			Name: "orrery_ontology_model_run_evaluation_gate_warning_total",
			Labels: map[string]string{
				"tenant_id":            "tenant-1",
				"engine":               "ontology_designer",
				"model":                "gpt-5",
				"model_version":        "2026-06-21",
				"warning":              "metric_threshold_failed",
				"snapshot_recorded_to": "2026-06-22T00:00:00Z",
			},
			Value: 2,
		},
		RecordedAt: recordedAt,
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationTrendMetricRecording: %v", err)
	}

	page, err := store.ListModelRunEvaluationTrendMetricRecordings(ctx, ModelRunEvaluationTrendMetricRecordingQuery{
		TenantID:   "tenant-1",
		MetricName: "orrery_ontology_model_run_evaluation_gate_warning_total",
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationTrendMetricRecordings: %v", err)
	}
	if page.Page.Total != 1 || len(page.Recordings) != 1 {
		t.Fatalf("page = %+v", page)
	}
	recording := page.Recordings[0]
	if recording.TenantID != "tenant-1" ||
		recording.Engine != "ontology_designer" ||
		recording.MetricSample.Labels["warning"] != "metric_threshold_failed" ||
		recording.MetricSample.Value != 2 ||
		!recording.RecordedAt.Equal(recordedAt) {
		t.Fatalf("recording = %+v", recording)
	}

	if err := store.SaveModelRunEvaluationTrendMetricRecording(ctx, ModelRunEvaluationTrendMetricRecording{
		MetricSample: ModelRunEvaluationTrendMetricSample{
			Name: "orrery_ontology_model_run_evaluation_gate_warning_total",
			Labels: map[string]string{
				"tenant_id": "tenant-1",
				"engine":    "ontology_designer",
				"warning":   "missing_required_artifact",
			},
			Value: 1,
		},
		RecordedAt: recordedAt,
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationTrendMetricRecording second warning: %v", err)
	}
	filtered, err := store.ListModelRunEvaluationTrendMetricRecordings(ctx, ModelRunEvaluationTrendMetricRecordingQuery{
		TenantID:   "tenant-1",
		MetricName: "orrery_ontology_model_run_evaluation_gate_warning_total",
		LabelKey:   "warning",
		LabelValue: "metric_threshold_failed",
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationTrendMetricRecordings filtered: %v", err)
	}
	if filtered.Page.Total != 1 || filtered.Recordings[0].MetricSample.Labels["warning"] != "metric_threshold_failed" {
		t.Fatalf("filtered recordings = %+v", filtered)
	}
}

func TestModelRunEvaluationTrendMetricRecordingStoreFiltersRecordedWindow(t *testing.T) {
	ctx := context.Background()
	for name, store := range map[string]ModelRunEvaluationTrendMetricRecordingStore{
		"memory":   NewMemoryModelRunEvaluationTrendMetricRecordingStore(),
		"platform": NewPlatformModelRunEvaluationTrendMetricRecordingStore(newMigratedMetricRecordingCore(t, "ontology-metric-recording-window")),
	} {
		t.Run(name, func(t *testing.T) {
			for _, recordedAt := range []time.Time{
				time.Date(2026, 6, 22, 8, 0, 0, 0, time.UTC),
				time.Date(2026, 6, 22, 9, 0, 0, 0, time.UTC),
				time.Date(2026, 6, 22, 10, 0, 0, 0, time.UTC),
				time.Date(2026, 6, 22, 11, 0, 0, 0, time.UTC),
			} {
				if err := store.SaveModelRunEvaluationTrendMetricRecording(ctx, ModelRunEvaluationTrendMetricRecording{
					MetricSample: ModelRunEvaluationTrendMetricSample{
						Name: "orrery_ontology_model_run_evaluation_runs_total",
						Labels: map[string]string{
							"tenant_id": "tenant-1",
							"engine":    "ontology_designer",
						},
						Value: 1,
					},
					RecordedAt: recordedAt,
				}); err != nil {
					t.Fatalf("SaveModelRunEvaluationTrendMetricRecording %s: %v", recordedAt, err)
				}
			}
			page, err := store.ListModelRunEvaluationTrendMetricRecordings(ctx, ModelRunEvaluationTrendMetricRecordingQuery{
				TenantID:     "tenant-1",
				MetricName:   "orrery_ontology_model_run_evaluation_runs_total",
				RecordedFrom: time.Date(2026, 6, 22, 9, 0, 0, 0, time.UTC),
				RecordedTo:   time.Date(2026, 6, 22, 11, 0, 0, 0, time.UTC),
				Limit:        10,
			})
			if err != nil {
				t.Fatalf("ListModelRunEvaluationTrendMetricRecordings: %v", err)
			}
			if page.Page.Total != 2 || len(page.Recordings) != 2 {
				t.Fatalf("page = %+v", page)
			}
			for _, recording := range page.Recordings {
				if recording.RecordedAt.Before(time.Date(2026, 6, 22, 9, 0, 0, 0, time.UTC)) ||
					!recording.RecordedAt.Before(time.Date(2026, 6, 22, 11, 0, 0, 0, time.UTC)) {
					t.Fatalf("recording outside window = %+v", recording)
				}
			}
		})
	}
}

func newMigratedMetricRecordingCore(t *testing.T, name string) *db.Store {
	t.Helper()
	ctx := context.Background()
	databases, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: name},
	}})
	if err != nil {
		t.Fatalf("open database registry: %v", err)
	}
	t.Cleanup(func() { _ = databases.Close() })
	coordinator := migration.NewCoordinator(databases)
	if err := RegisterMigrations(coordinator); err != nil {
		t.Fatalf("RegisterMigrations: %v", err)
	}
	if err := coordinator.UpRole(ctx, db.RoleCore); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	core, err := databases.Store(db.RoleCore)
	if err != nil {
		t.Fatalf("core database: %v", err)
	}
	return core
}

func assertTrendMetricSample(t *testing.T, samples []ModelRunEvaluationTrendMetricSample, name string, value float64, labels map[string]string) {
	t.Helper()
	for _, sample := range samples {
		if sample.Name != name || sample.Value != value {
			continue
		}
		matched := true
		for key, want := range labels {
			if sample.Labels[key] != want {
				matched = false
				break
			}
		}
		if matched {
			return
		}
	}
	t.Fatalf("metric sample %s value %v labels %+v not found in %+v", name, value, labels, samples)
}
