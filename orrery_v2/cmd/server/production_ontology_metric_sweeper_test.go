package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/testkit"
)

func TestRegisterProductionOntologyMetricRecordingSweeperCollectsRecentRecordings(t *testing.T) {
	ctx := context.Background()
	rt := testkit.NewInitializedTestRuntime(t, OntologyModule())
	core, err := rt.Databases.Store("core")
	if err != nil {
		t.Fatalf("core store: %v", err)
	}
	recordedAt := rt.Clock.Now().UTC().Add(-time.Minute)
	recordings := ontology.NewPlatformModelRunEvaluationTrendMetricRecordingStore(core)
	if err := recordings.SaveModelRunEvaluationTrendMetricRecording(ctx, ontology.ModelRunEvaluationTrendMetricRecording{
		MetricSample: ontology.ModelRunEvaluationTrendMetricSample{
			Name: "orrery_ontology_model_run_evaluation_runs_total",
			Labels: map[string]string{
				"tenant_id": "tenant-1",
				"engine":    "ontology_designer",
			},
			Value: 1,
		},
		RecordedAt: recordedAt,
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationTrendMetricRecording: %v", err)
	}

	if err := registerProductionOntologyMetricRecordingSweeper(rt, core); err != nil {
		t.Fatalf("registerProductionOntologyMetricRecordingSweeper: %v", err)
	}
	report := rt.Sweeper.SweepOnce(ctx)
	if report.Failed != 0 {
		t.Fatalf("sweeper report = %+v", report)
	}
	events := rt.Metrics.Drain(10)
	if len(events) != 1 ||
		events[0].Name != "orrery_ontology_model_run_evaluation_runs_total" ||
		events[0].Fields["tenant_id"] != "tenant-1" ||
		events[0].Fields["value"] != float64(1) {
		t.Fatalf("events = %+v", events)
	}
}

func TestRegisterProductionOntologyMetricRecordingSweeperUsesRuntimeConfig(t *testing.T) {
	ctx := context.Background()
	rt := newOntologyMetricSweeperConfiguredRuntime(t, ctx)
	core, err := rt.Databases.Store("core")
	if err != nil {
		t.Fatalf("core store: %v", err)
	}
	recordings := ontology.NewPlatformModelRunEvaluationTrendMetricRecordingStore(core)
	if err := recordings.SaveModelRunEvaluationTrendMetricRecording(ctx, ontology.ModelRunEvaluationTrendMetricRecording{
		MetricSample: ontology.ModelRunEvaluationTrendMetricSample{
			Name: "orrery_ontology_model_run_evaluation_runs_total",
			Labels: map[string]string{
				"tenant_id": "tenant-1",
				"engine":    "ontology_designer",
			},
			Value: 1,
		},
		RecordedAt: rt.Clock.Now().UTC().Add(-20 * time.Minute),
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationTrendMetricRecording: %v", err)
	}

	if err := registerProductionOntologyMetricRecordingSweeper(rt, core); err != nil {
		t.Fatalf("registerProductionOntologyMetricRecordingSweeper: %v", err)
	}
	report := rt.Sweeper.SweepOnce(ctx)
	if report.Failed != 0 {
		t.Fatalf("sweeper report = %+v", report)
	}
	if events := rt.Metrics.Drain(10); len(events) != 1 {
		t.Fatalf("events = %+v", events)
	}
}

func newOntologyMetricSweeperConfiguredRuntime(t *testing.T, ctx context.Context) *platform.Runtime {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orrery.yaml")
	if err := os.WriteFile(path, []byte(`
database:
  default_url: file://./stage0-test.db
ontology:
  metric_recording_sweeper:
    lookback: 30m
    limit: 5
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg := testkit.NewTestConfigFromYAML(t, path)
	rt, err := platform.OpenWithModules(ctx, cfg, OntologyModule())
	if err != nil {
		t.Fatalf("open runtime: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	if err := rt.Initialize(ctx); err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}
	return rt
}
