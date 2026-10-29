package ontology

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/platform/clock"
)

func modelRunEvaluationArtifactFileURI(path string) string {
	slashPath := filepath.ToSlash(path)
	if !strings.HasPrefix(slashPath, "/") {
		slashPath = "/" + slashPath
	}
	return (&url.URL{Scheme: "file", Path: slashPath}).String()
}

func TestMemoryModelRunEvaluationArtifactStoreSaveAndList(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryModelRunEvaluationArtifactStore()
	recordedAt := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)

	if err := store.SaveModelRunEvaluationArtifact(ctx, ModelRunEvaluationArtifact{
		TenantID:     "tenant-1",
		Engine:       "ontology_designer",
		RunID:        "run-1",
		ArtifactKind: "evaluation_report",
		ArtifactURI:  "s3://orrery-eval/run-1/report.json",
		ArtifactHash: "sha256:abc",
		Summary:      "policy and schema evaluation report",
		Metadata:     map[string]string{"dataset_ref": "ontology-eval-20260621"},
		RecordedAt:   recordedAt,
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationArtifact: %v", err)
	}
	if err := store.SaveModelRunEvaluationArtifact(ctx, ModelRunEvaluationArtifact{
		TenantID:     "tenant-2",
		Engine:       "ontology_designer",
		RunID:        "run-1",
		ArtifactKind: "evaluation_report",
		ArtifactURI:  "s3://orrery-eval/tenant-2/report.json",
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationArtifact tenant-2: %v", err)
	}

	page, err := store.ListModelRunEvaluationArtifacts(ctx, ModelRunEvaluationArtifactQuery{
		TenantID: "tenant-1",
		Engine:   "ontology_designer",
		RunID:    "run-1",
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationArtifacts: %v", err)
	}
	if page.Page.Total != 1 || len(page.Artifacts) != 1 {
		t.Fatalf("artifact page = %+v", page)
	}
	artifact := page.Artifacts[0]
	if artifact.TenantID != "tenant-1" ||
		artifact.ArtifactURI != "s3://orrery-eval/run-1/report.json" ||
		artifact.ArtifactHash != "sha256:abc" ||
		artifact.Metadata["dataset_ref"] != "ontology-eval-20260621" {
		t.Fatalf("artifact = %+v", artifact)
	}
}

func TestModelRunEvaluationArtifactValidation(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryModelRunEvaluationArtifactStore()

	err := store.SaveModelRunEvaluationArtifact(ctx, ModelRunEvaluationArtifact{
		Engine:       "ontology_designer",
		RunID:        "run-1",
		ArtifactKind: "evaluation_report",
		ArtifactURI:  "s3://orrery-eval/run-1/report.json",
	})
	if err == nil {
		t.Fatalf("expected tenant_id validation error")
	}
	err = store.SaveModelRunEvaluationArtifact(ctx, ModelRunEvaluationArtifact{
		TenantID:     "tenant-1",
		Engine:       "ontology_designer",
		ArtifactKind: "evaluation_report",
		ArtifactURI:  "s3://orrery-eval/run-1/report.json",
	})
	if err == nil {
		t.Fatalf("expected run_id validation error")
	}
	err = store.SaveModelRunEvaluationArtifact(ctx, ModelRunEvaluationArtifact{
		TenantID:     "tenant-1",
		Engine:       "ontology_designer",
		RunID:        "run-1",
		ArtifactKind: "evaluation_report",
	})
	if err == nil {
		t.Fatalf("expected artifact_uri validation error")
	}
}

func TestModelRunEvaluationArtifactRejectsUnsafeURIAndMarksValidation(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryModelRunEvaluationArtifactStore()

	err := store.SaveModelRunEvaluationArtifact(ctx, ModelRunEvaluationArtifact{
		TenantID:     "tenant-1",
		Engine:       "ontology_designer",
		RunID:        "run-1",
		ArtifactKind: "evaluation_report",
		ArtifactURI:  "http://169.254.169.254/latest/meta-data",
	})
	if err == nil {
		t.Fatalf("expected unsafe artifact_uri validation error")
	}

	if err := store.SaveModelRunEvaluationArtifact(ctx, ModelRunEvaluationArtifact{
		TenantID:     "tenant-1",
		Engine:       "ontology_designer",
		RunID:        "run-1",
		ArtifactKind: "evaluation_report",
		ArtifactURI:  "s3://orrery-eval/run-1/report.json",
		Metadata:     map[string]string{"dataset_ref": "ontology-eval-20260621"},
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationArtifact safe uri: %v", err)
	}

	page, err := store.ListModelRunEvaluationArtifacts(ctx, ModelRunEvaluationArtifactQuery{
		TenantID: "tenant-1",
		Engine:   "ontology_designer",
		RunID:    "run-1",
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationArtifacts: %v", err)
	}
	if len(page.Artifacts) != 1 {
		t.Fatalf("artifact page = %+v", page)
	}
	metadata := page.Artifacts[0].Metadata
	if metadata["artifact_ref_validation_status"] != "syntax_passed" ||
		metadata["artifact_ref_validation_mode"] != "uri_syntax" ||
		metadata["dataset_ref"] != "ontology-eval-20260621" {
		t.Fatalf("validation metadata = %+v", metadata)
	}
}

func TestModelRunEvaluationArtifactAnnotatesMetadataSchema(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryModelRunEvaluationArtifactStore()

	if err := store.SaveModelRunEvaluationArtifact(ctx, ModelRunEvaluationArtifact{
		TenantID:     "tenant-1",
		Engine:       "ontology_designer",
		RunID:        "run-1",
		ArtifactKind: "evaluation_report",
		ArtifactURI:  "s3://orrery-eval/run-1/report.json",
		Metadata: map[string]string{
			"dataset_ref":    "ontology-eval-20260621",
			"policy_version": "policy-v1",
		},
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationArtifact: %v", err)
	}

	page, err := store.ListModelRunEvaluationArtifacts(ctx, ModelRunEvaluationArtifactQuery{
		TenantID: "tenant-1",
		Engine:   "ontology_designer",
		RunID:    "run-1",
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationArtifacts: %v", err)
	}
	if len(page.Artifacts) != 1 {
		t.Fatalf("artifact page = %+v", page)
	}
	metadata := page.Artifacts[0].Metadata
	if metadata["artifact_metadata_schema_id"] != "ontology.evaluation_report.v1" ||
		metadata["artifact_metadata_schema_mode"] != "builtin_required_keys" ||
		metadata["artifact_metadata_schema_status"] != "passed" ||
		metadata["artifact_metadata_schema_required_keys"] != "dataset_ref,policy_version" ||
		metadata["artifact_metadata_schema_missing_keys"] != "" {
		t.Fatalf("schema metadata = %+v", metadata)
	}
}

func TestModelRunEvaluationArtifactMetadataSchemaWarnsOnMissingRequiredKeys(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryModelRunEvaluationArtifactStore()

	if err := store.SaveModelRunEvaluationArtifact(ctx, ModelRunEvaluationArtifact{
		TenantID:     "tenant-1",
		Engine:       "ontology_designer",
		RunID:        "run-1",
		ArtifactKind: "evaluation_report",
		ArtifactURI:  "s3://orrery-eval/run-1/report.json",
		Metadata:     map[string]string{"dataset_ref": "ontology-eval-20260621"},
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationArtifact: %v", err)
	}

	page, err := store.ListModelRunEvaluationArtifacts(ctx, ModelRunEvaluationArtifactQuery{
		TenantID: "tenant-1",
		Engine:   "ontology_designer",
		RunID:    "run-1",
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationArtifacts: %v", err)
	}
	if len(page.Artifacts) != 1 {
		t.Fatalf("artifact page = %+v", page)
	}
	metadata := page.Artifacts[0].Metadata
	if metadata["artifact_metadata_schema_id"] != "ontology.evaluation_report.v1" ||
		metadata["artifact_metadata_schema_status"] != "warning" ||
		metadata["artifact_metadata_schema_missing_keys"] != "policy_version" {
		t.Fatalf("schema metadata = %+v", metadata)
	}
}

func TestModelRunEvaluationArtifactAnnotatesProviderHealthProbe(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryModelRunEvaluationArtifactStore()

	if err := store.SaveModelRunEvaluationArtifact(ctx, ModelRunEvaluationArtifact{
		TenantID:     "tenant-1",
		Engine:       "ontology_designer",
		RunID:        "run-1",
		ArtifactKind: "evaluation_report",
		ArtifactURI:  "s3://orrery-eval/run-1/report.json",
		Metadata: map[string]string{
			"dataset_ref":    "ontology-eval-20260621",
			"policy_version": "policy-v1",
		},
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationArtifact: %v", err)
	}

	page, err := store.ListModelRunEvaluationArtifacts(ctx, ModelRunEvaluationArtifactQuery{
		TenantID: "tenant-1",
		Engine:   "ontology_designer",
		RunID:    "run-1",
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationArtifacts: %v", err)
	}
	if len(page.Artifacts) != 1 {
		t.Fatalf("artifact page = %+v", page)
	}
	metadata := page.Artifacts[0].Metadata
	if metadata["artifact_health_provider"] != "s3" ||
		metadata["artifact_health_status"] != "probe_not_run" ||
		metadata["artifact_health_probe_mode"] != "provider_metadata" ||
		metadata["artifact_health_probe_capability"] != "head_object" ||
		metadata["artifact_health_resource"] != "orrery-eval/run-1/report.json" {
		t.Fatalf("health metadata = %+v", metadata)
	}
}

func TestProbeModelRunEvaluationArtifactHealthChecksLocalFile(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryModelRunEvaluationArtifactStore()
	reportPath := filepath.Join(t.TempDir(), "report.json")
	if err := os.WriteFile(reportPath, []byte(`{"score":0.99}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	probedAt := time.Date(2026, 6, 22, 8, 30, 0, 0, time.UTC)

	artifact, err := ProbeModelRunEvaluationArtifactHealth(ModelRunEvaluationArtifact{
		TenantID:     "tenant-1",
		Engine:       "ontology_designer",
		RunID:        "run-1",
		ArtifactKind: "evaluation_report",
		ArtifactURI:  modelRunEvaluationArtifactFileURI(reportPath),
		Metadata: map[string]string{
			"dataset_ref":    "ontology-eval-20260622",
			"policy_version": "policy-v1",
		},
	}, func() time.Time { return probedAt })
	if err != nil {
		t.Fatalf("ProbeModelRunEvaluationArtifactHealth: %v", err)
	}
	metadata := artifact.Metadata
	if metadata["artifact_health_provider"] != "file" ||
		metadata["artifact_health_status"] != "available" ||
		metadata["artifact_health_probe_mode"] != "manual_probe" ||
		metadata["artifact_health_probe_capability"] != "stat" ||
		metadata["artifact_health_resource"] != reportPath ||
		metadata["artifact_health_probed_at"] != probedAt.Format(time.RFC3339Nano) ||
		metadata["artifact_health_file_size_bytes"] != "14" ||
		metadata["artifact_health_error"] != "" {
		t.Fatalf("probe metadata = %+v", metadata)
	}

	if err := store.SaveModelRunEvaluationArtifact(ctx, artifact); err != nil {
		t.Fatalf("SaveModelRunEvaluationArtifact: %v", err)
	}
	page, err := store.ListModelRunEvaluationArtifacts(ctx, ModelRunEvaluationArtifactQuery{
		TenantID: "tenant-1",
		Engine:   "ontology_designer",
		RunID:    "run-1",
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationArtifacts: %v", err)
	}
	if len(page.Artifacts) != 1 {
		t.Fatalf("artifact page = %+v", page)
	}
	if page.Artifacts[0].Metadata["artifact_health_status"] != "available" ||
		page.Artifacts[0].Metadata["artifact_health_probe_mode"] != "manual_probe" {
		t.Fatalf("persisted probe metadata = %+v", page.Artifacts[0].Metadata)
	}
}

func TestProbeModelRunEvaluationArtifactHealthMarksMissingLocalFile(t *testing.T) {
	missingPath := filepath.Join(t.TempDir(), "missing.json")
	probedAt := time.Date(2026, 6, 22, 8, 45, 0, 0, time.UTC)

	artifact, err := ProbeModelRunEvaluationArtifactHealth(ModelRunEvaluationArtifact{
		TenantID:     "tenant-1",
		Engine:       "ontology_designer",
		RunID:        "run-1",
		ArtifactKind: "evaluation_report",
		ArtifactURI:  modelRunEvaluationArtifactFileURI(missingPath),
		Metadata: map[string]string{
			"dataset_ref":    "ontology-eval-20260622",
			"policy_version": "policy-v1",
		},
	}, func() time.Time { return probedAt })
	if err != nil {
		t.Fatalf("ProbeModelRunEvaluationArtifactHealth: %v", err)
	}
	metadata := artifact.Metadata
	if metadata["artifact_health_status"] != "unavailable" ||
		metadata["artifact_health_probe_mode"] != "manual_probe" ||
		metadata["artifact_health_probed_at"] != probedAt.Format(time.RFC3339Nano) ||
		metadata["artifact_health_error"] == "" {
		t.Fatalf("missing probe metadata = %+v", metadata)
	}
}

func TestProbeModelRunEvaluationArtifactHealthUsesProviderProbe(t *testing.T) {
	probedAt := time.Date(2026, 6, 22, 9, 15, 0, 0, time.UTC)
	artifact, err := ProbeModelRunEvaluationArtifactHealthWithProviders(ModelRunEvaluationArtifact{
		TenantID:     "tenant-1",
		Engine:       "ontology_designer",
		RunID:        "run-1",
		ArtifactKind: "evaluation_report",
		ArtifactURI:  "s3://orrery-eval/run-1/report.json",
		Metadata: map[string]string{
			"dataset_ref":    "ontology-eval-20260622",
			"policy_version": "policy-v1",
		},
	}, func() time.Time { return probedAt }, map[string]ModelRunEvaluationArtifactProviderProbe{
		"s3": fakeModelRunEvaluationArtifactProviderProbe{
			result: ModelRunEvaluationArtifactProviderProbeResult{
				Available: true,
				Metadata: map[string]string{
					"artifact_health_object_size_bytes": "1024",
					"artifact_health_object_etag":       "etag-1",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("ProbeModelRunEvaluationArtifactHealthWithProviders: %v", err)
	}
	metadata := artifact.Metadata
	if metadata["artifact_health_provider"] != "s3" ||
		metadata["artifact_health_status"] != "available" ||
		metadata["artifact_health_probe_mode"] != "manual_probe" ||
		metadata["artifact_health_probe_capability"] != "head_object" ||
		metadata["artifact_health_resource"] != "orrery-eval/run-1/report.json" ||
		metadata["artifact_health_probed_at"] != probedAt.Format(time.RFC3339Nano) ||
		metadata["artifact_health_object_size_bytes"] != "1024" ||
		metadata["artifact_health_object_etag"] != "etag-1" ||
		metadata["artifact_health_error"] != "" {
		t.Fatalf("provider probe metadata = %+v", metadata)
	}
}

func TestProbeModelRunEvaluationArtifactHealthMarksUnsupportedProviderProbe(t *testing.T) {
	probedAt := time.Date(2026, 6, 22, 9, 30, 0, 0, time.UTC)
	artifact, err := ProbeModelRunEvaluationArtifactHealth(ModelRunEvaluationArtifact{
		TenantID:     "tenant-1",
		Engine:       "ontology_designer",
		RunID:        "run-1",
		ArtifactKind: "evaluation_report",
		ArtifactURI:  "s3://orrery-eval/run-1/report.json",
		Metadata: map[string]string{
			"dataset_ref":    "ontology-eval-20260622",
			"policy_version": "policy-v1",
		},
	}, func() time.Time { return probedAt })
	if err != nil {
		t.Fatalf("ProbeModelRunEvaluationArtifactHealth: %v", err)
	}
	if artifact.Metadata["artifact_health_status"] != "probe_unsupported" ||
		artifact.Metadata["artifact_health_probe_mode"] != "manual_probe" {
		t.Fatalf("unsupported provider metadata = %+v", artifact.Metadata)
	}
}

func TestScopedModelRunEvaluationArtifactProviderProbeAllowsConfiguredPrefix(t *testing.T) {
	probedAt := time.Date(2026, 6, 22, 9, 45, 0, 0, time.UTC)
	delegate := &spyModelRunEvaluationArtifactProviderProbe{
		result: ModelRunEvaluationArtifactProviderProbeResult{
			Available: true,
			Metadata: map[string]string{
				"artifact_health_object_size_bytes": "2048",
			},
		},
	}
	artifact, err := ProbeModelRunEvaluationArtifactHealthWithProviders(ModelRunEvaluationArtifact{
		TenantID:     "tenant-1",
		Engine:       "ontology_designer",
		RunID:        "run-1",
		ArtifactKind: "evaluation_report",
		ArtifactURI:  "s3://orrery-eval/tenant-1/run-1/report.json",
		Metadata: map[string]string{
			"dataset_ref":    "ontology-eval-20260622",
			"policy_version": "policy-v1",
		},
	}, func() time.Time { return probedAt }, map[string]ModelRunEvaluationArtifactProviderProbe{
		"s3": NewScopedModelRunEvaluationArtifactProviderProbe("s3", []string{"orrery-eval/tenant-1/"}, delegate),
	})
	if err != nil {
		t.Fatalf("ProbeModelRunEvaluationArtifactHealthWithProviders: %v", err)
	}
	if delegate.calls != 1 {
		t.Fatalf("delegate calls = %d, want 1", delegate.calls)
	}
	if delegate.artifacts[0].Metadata["artifact_health_resource"] != "orrery-eval/tenant-1/run-1/report.json" {
		t.Fatalf("delegated artifact metadata = %+v", delegate.artifacts[0].Metadata)
	}
	metadata := artifact.Metadata
	if metadata["artifact_health_status"] != "available" ||
		metadata["artifact_health_object_size_bytes"] != "2048" ||
		metadata["artifact_health_error"] != "" {
		t.Fatalf("scoped provider metadata = %+v", metadata)
	}
}

func TestScopedModelRunEvaluationArtifactProviderProbeRejectsResourceOutsideConfiguredPrefix(t *testing.T) {
	probedAt := time.Date(2026, 6, 22, 9, 50, 0, 0, time.UTC)
	delegate := &spyModelRunEvaluationArtifactProviderProbe{
		result: ModelRunEvaluationArtifactProviderProbeResult{Available: true},
	}
	artifact, err := ProbeModelRunEvaluationArtifactHealthWithProviders(ModelRunEvaluationArtifact{
		TenantID:     "tenant-1",
		Engine:       "ontology_designer",
		RunID:        "run-1",
		ArtifactKind: "evaluation_report",
		ArtifactURI:  "s3://orrery-eval/tenant-10/report.json",
		Metadata: map[string]string{
			"dataset_ref":    "ontology-eval-20260622",
			"policy_version": "policy-v1",
		},
	}, func() time.Time { return probedAt }, map[string]ModelRunEvaluationArtifactProviderProbe{
		"s3": NewScopedModelRunEvaluationArtifactProviderProbe("s3", []string{"orrery-eval/tenant-1"}, delegate),
	})
	if err != nil {
		t.Fatalf("ProbeModelRunEvaluationArtifactHealthWithProviders: %v", err)
	}
	if delegate.calls != 0 {
		t.Fatalf("delegate calls = %d, want 0", delegate.calls)
	}
	metadata := artifact.Metadata
	if metadata["artifact_health_status"] != "unavailable" ||
		!strings.Contains(metadata["artifact_health_error"], "not allowed for provider") ||
		metadata["artifact_health_resource"] != "orrery-eval/tenant-10/report.json" {
		t.Fatalf("rejected scoped provider metadata = %+v", metadata)
	}
}

func TestModelRunEvaluationArtifactHealthProbeSweeperProbesAndPersistsArtifacts(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryModelRunEvaluationArtifactStore()
	probedAt := time.Date(2026, 6, 22, 10, 0, 0, 0, time.UTC)
	for _, artifact := range []ModelRunEvaluationArtifact{
		{
			TenantID:     "tenant-1",
			Engine:       "ontology_designer",
			RunID:        "run-1",
			ArtifactKind: "evaluation_report",
			ArtifactURI:  "s3://orrery-eval/run-1/report.json",
			Metadata: map[string]string{
				"dataset_ref":    "ontology-eval-20260622",
				"policy_version": "policy-v1",
			},
		},
		{
			TenantID:     "tenant-1",
			Engine:       "ontology_designer",
			RunID:        "run-2",
			ArtifactKind: "evaluation_report",
			ArtifactURI:  "s3://orrery-eval/run-2/report.json",
			Metadata: map[string]string{
				"dataset_ref":    "ontology-eval-20260622",
				"policy_version": "policy-v1",
			},
		},
	} {
		if err := store.SaveModelRunEvaluationArtifact(ctx, artifact); err != nil {
			t.Fatalf("SaveModelRunEvaluationArtifact: %v", err)
		}
	}
	sweeper := NewModelRunEvaluationArtifactHealthProbeSweeper(ModelRunEvaluationArtifactHealthProbeSweeperOptions{
		Store: store,
		Clock: clock.NewFake(probedAt),
		Query: ModelRunEvaluationArtifactQuery{
			TenantID: "tenant-1",
			Engine:   "ontology_designer",
			Limit:    1,
		},
		Providers: map[string]ModelRunEvaluationArtifactProviderProbe{
			"s3": fakeModelRunEvaluationArtifactProviderProbe{
				result: ModelRunEvaluationArtifactProviderProbeResult{
					Available: true,
					Metadata: map[string]string{
						"artifact_health_object_size_bytes": "1024",
					},
				},
			},
		},
	})

	if sweeper.Name() != "ontology.model_run_evaluation_artifact_health.prober" {
		t.Fatalf("sweeper name = %q", sweeper.Name())
	}
	if err := sweeper.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if report := sweeper.LastReport(); report.Probed != 2 || report.Updated != 2 || report.Pages != 2 {
		t.Fatalf("last report = %+v", report)
	}
	page, err := store.ListModelRunEvaluationArtifacts(ctx, ModelRunEvaluationArtifactQuery{
		TenantID: "tenant-1",
		Engine:   "ontology_designer",
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationArtifacts: %v", err)
	}
	if len(page.Artifacts) != 2 {
		t.Fatalf("artifact page = %+v", page)
	}
	for _, artifact := range page.Artifacts {
		metadata := artifact.Metadata
		if metadata["artifact_health_status"] != "available" ||
			metadata["artifact_health_probe_mode"] != "manual_probe" ||
			metadata["artifact_health_probed_at"] != probedAt.Format(time.RFC3339Nano) ||
			metadata["artifact_health_object_size_bytes"] != "1024" {
			t.Fatalf("artifact %s metadata = %+v", artifact.RunID, metadata)
		}
	}
}

type fakeModelRunEvaluationArtifactProviderProbe struct {
	result ModelRunEvaluationArtifactProviderProbeResult
	err    error
}

func (p fakeModelRunEvaluationArtifactProviderProbe) ProbeModelRunEvaluationArtifact(_ context.Context, _ ModelRunEvaluationArtifact) (ModelRunEvaluationArtifactProviderProbeResult, error) {
	return p.result, p.err
}

type spyModelRunEvaluationArtifactProviderProbe struct {
	result    ModelRunEvaluationArtifactProviderProbeResult
	err       error
	calls     int
	artifacts []ModelRunEvaluationArtifact
}

func (p *spyModelRunEvaluationArtifactProviderProbe) ProbeModelRunEvaluationArtifact(_ context.Context, artifact ModelRunEvaluationArtifact) (ModelRunEvaluationArtifactProviderProbeResult, error) {
	p.calls++
	p.artifacts = append(p.artifacts, artifact)
	return p.result, p.err
}
