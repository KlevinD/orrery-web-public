package main

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/testkit"
)

func productionOntologyFileArtifactURI(path string) string {
	slashPath := filepath.ToSlash(path)
	if !strings.HasPrefix(slashPath, "/") {
		slashPath = "/" + slashPath
	}
	return (&url.URL{Scheme: "file", Path: slashPath}).String()
}

func TestRegisterProductionOntologyArtifactHealthSweeperProbesLocalArtifacts(t *testing.T) {
	ctx := context.Background()
	rt := testkit.NewInitializedTestRuntime(t, OntologyModule())
	core, err := rt.Databases.Store("core")
	if err != nil {
		t.Fatalf("core store: %v", err)
	}
	reportPath := filepath.Join(t.TempDir(), "report.json")
	if err := os.WriteFile(reportPath, []byte(`{"score":0.99}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	artifacts := ontology.NewPlatformModelRunEvaluationArtifactStore(core)
	if err := artifacts.SaveModelRunEvaluationArtifact(ctx, ontology.ModelRunEvaluationArtifact{
		TenantID:     "tenant-1",
		Engine:       "ontology_designer",
		RunID:        "run-artifact-health-production-1",
		ArtifactKind: "evaluation_report",
		ArtifactURI:  productionOntologyFileArtifactURI(reportPath),
		Metadata: map[string]string{
			"dataset_ref":    "ontology-eval-20260622",
			"policy_version": "v1",
		},
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationArtifact: %v", err)
	}

	if err := registerProductionOntologyArtifactHealthSweeper(rt, core); err != nil {
		t.Fatalf("registerProductionOntologyArtifactHealthSweeper: %v", err)
	}
	report := rt.Sweeper.SweepOnce(ctx)
	if report.Failed != 0 {
		t.Fatalf("sweeper report = %+v", report)
	}
	page, err := artifacts.ListModelRunEvaluationArtifacts(ctx, ontology.ModelRunEvaluationArtifactQuery{
		TenantID: "tenant-1",
		Engine:   "ontology_designer",
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationArtifacts: %v", err)
	}
	if len(page.Artifacts) != 1 {
		t.Fatalf("artifact page = %+v", page)
	}
	metadata := page.Artifacts[0].Metadata
	if metadata["artifact_health_status"] != "available" ||
		metadata["artifact_health_probe_mode"] != "manual_probe" ||
		metadata["artifact_health_file_size_bytes"] != "14" ||
		metadata["artifact_health_probed_at"] == "" {
		t.Fatalf("artifact health metadata = %+v", metadata)
	}
}

func TestRegisterProductionOntologyArtifactHealthSweeperUsesRuntimeConfig(t *testing.T) {
	ctx := context.Background()
	rt := newOntologyArtifactHealthSweeperConfiguredRuntime(t, ctx)
	core, err := rt.Databases.Store("core")
	if err != nil {
		t.Fatalf("core store: %v", err)
	}
	if cfg := productionOntologyArtifactHealthSweeperConfigFromRuntime(rt); cfg.Limit != 1 {
		t.Fatalf("artifact health sweeper config = %+v", cfg)
	}
	for _, runID := range []string{"run-artifact-health-config-1", "run-artifact-health-config-2"} {
		reportPath := filepath.Join(t.TempDir(), runID+".json")
		if err := os.WriteFile(reportPath, []byte(`{"score":0.99}`), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		if err := ontology.NewPlatformModelRunEvaluationArtifactStore(core).SaveModelRunEvaluationArtifact(ctx, ontology.ModelRunEvaluationArtifact{
			TenantID:     "tenant-1",
			Engine:       "ontology_designer",
			RunID:        runID,
			ArtifactKind: "evaluation_report",
			ArtifactURI:  productionOntologyFileArtifactURI(reportPath),
			Metadata: map[string]string{
				"dataset_ref":    "ontology-eval-20260622",
				"policy_version": "v1",
			},
		}); err != nil {
			t.Fatalf("SaveModelRunEvaluationArtifact: %v", err)
		}
	}

	if err := registerProductionOntologyArtifactHealthSweeper(rt, core); err != nil {
		t.Fatalf("registerProductionOntologyArtifactHealthSweeper: %v", err)
	}
	report := rt.Sweeper.SweepOnce(ctx)
	if report.Failed != 0 {
		t.Fatalf("sweeper report = %+v", report)
	}
	page, err := ontology.NewPlatformModelRunEvaluationArtifactStore(core).ListModelRunEvaluationArtifacts(ctx, ontology.ModelRunEvaluationArtifactQuery{
		TenantID: "tenant-1",
		Engine:   "ontology_designer",
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationArtifacts: %v", err)
	}
	probed := 0
	for _, artifact := range page.Artifacts {
		if artifact.Metadata["artifact_health_probe_mode"] == "manual_probe" {
			probed++
		}
	}
	if probed != 2 {
		t.Fatalf("probed artifacts = %d page = %+v", probed, page)
	}
}

func TestRegisterProductionOntologyArtifactHealthSweeperUsesScopedS3Provider(t *testing.T) {
	ctx := context.Background()
	rt := newOntologyArtifactHealthSweeperS3Runtime(t, ctx)
	core, err := rt.Databases.Store("core")
	if err != nil {
		t.Fatalf("core store: %v", err)
	}
	cfg := productionOntologyArtifactHealthSweeperConfigFromRuntime(rt)
	if !cfg.S3.Enabled ||
		cfg.S3.Region != "us-east-1" ||
		cfg.S3.EndpointURL != "http://localhost:9000" ||
		cfg.S3.ExpectedBucketOwner != "123456789012" ||
		len(cfg.S3.AllowedPrefixes) != 1 ||
		cfg.S3.AllowedPrefixes[0] != "orrery-eval/tenant-1/" {
		t.Fatalf("artifact health sweeper S3 config = %+v", cfg)
	}
	productionOntologyArtifactHealthS3ProviderBuilderForTest = func(ctx context.Context, rt *platform.Runtime, cfg productionOntologyArtifactHealthS3Config) (ontology.ModelRunEvaluationArtifactProviderProbe, error) {
		return ontology.NewModelRunEvaluationArtifactS3ProviderProbe(ontology.ModelRunEvaluationArtifactS3ProviderProbeOptions{
			Client: fakeProductionOntologyArtifactHealthS3HeadObjectClient{
				output: &s3.HeadObjectOutput{
					ContentLength: aws.Int64(4096),
					ContentType:   aws.String("application/json"),
				},
			},
			ExpectedBucketOwner: cfg.ExpectedBucketOwner,
		}), nil
	}
	t.Cleanup(func() { productionOntologyArtifactHealthS3ProviderBuilderForTest = nil })
	artifacts := ontology.NewPlatformModelRunEvaluationArtifactStore(core)
	for _, artifact := range []ontology.ModelRunEvaluationArtifact{
		{
			TenantID:     "tenant-1",
			Engine:       "ontology_designer",
			RunID:        "run-s3-production-allowed",
			ArtifactKind: "evaluation_report",
			ArtifactURI:  "s3://orrery-eval/tenant-1/run-s3-production-allowed/report.json",
			Metadata: map[string]string{
				"dataset_ref":    "ontology-eval-20260622",
				"policy_version": "v1",
			},
			RecordedAt: time.Date(2026, 6, 22, 14, 0, 0, 0, time.UTC),
		},
		{
			TenantID:     "tenant-1",
			Engine:       "ontology_designer",
			RunID:        "run-s3-production-denied",
			ArtifactKind: "evaluation_report",
			ArtifactURI:  "s3://orrery-eval/tenant-10/report.json",
			Metadata: map[string]string{
				"dataset_ref":    "ontology-eval-20260622",
				"policy_version": "v1",
			},
			RecordedAt: time.Date(2026, 6, 22, 14, 1, 0, 0, time.UTC),
		},
	} {
		if err := artifacts.SaveModelRunEvaluationArtifact(ctx, artifact); err != nil {
			t.Fatalf("SaveModelRunEvaluationArtifact: %v", err)
		}
	}

	if err := registerProductionOntologyArtifactHealthSweeper(rt, core); err != nil {
		t.Fatalf("registerProductionOntologyArtifactHealthSweeper: %v", err)
	}
	report := rt.Sweeper.SweepOnce(ctx)
	if report.Failed != 0 {
		t.Fatalf("sweeper report = %+v", report)
	}
	page, err := artifacts.ListModelRunEvaluationArtifacts(ctx, ontology.ModelRunEvaluationArtifactQuery{
		TenantID: "tenant-1",
		Engine:   "ontology_designer",
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationArtifacts: %v", err)
	}
	statusByRun := map[string]map[string]string{}
	for _, artifact := range page.Artifacts {
		statusByRun[artifact.RunID] = artifact.Metadata
	}
	allowed := statusByRun["run-s3-production-allowed"]
	if allowed["artifact_health_status"] != "available" ||
		allowed["artifact_health_object_size_bytes"] != "4096" ||
		allowed["artifact_health_s3_bucket"] != "orrery-eval" ||
		allowed["artifact_health_s3_key"] != "tenant-1/run-s3-production-allowed/report.json" {
		t.Fatalf("allowed artifact metadata = %+v", allowed)
	}
	denied := statusByRun["run-s3-production-denied"]
	if denied["artifact_health_status"] != "unavailable" ||
		denied["artifact_health_error"] == "" ||
		denied["artifact_health_s3_key"] != "" {
		t.Fatalf("denied artifact metadata = %+v", denied)
	}
}

func TestProductionOntologyArtifactHealthS3AWSConfigUsesRuntimeSecrets(t *testing.T) {
	ctx := context.Background()
	t.Setenv("ORRERY_SECRET_AWS_ACCESS_KEY_ID", "test-access-key")
	t.Setenv("ORRERY_SECRET_AWS_SECRET_ACCESS_KEY", "test-secret-key")
	rt := newOntologyArtifactHealthSweeperS3Runtime(t, ctx)

	cfg, err := productionOntologyArtifactHealthS3AWSConfig(ctx, rt, productionOntologyArtifactHealthS3Config{
		Region: "us-east-1",
	})
	if err != nil {
		t.Fatalf("productionOntologyArtifactHealthS3AWSConfig: %v", err)
	}
	creds, err := cfg.Credentials.Retrieve(ctx)
	if err != nil {
		t.Fatalf("Retrieve credentials: %v", err)
	}
	if creds.AccessKeyID != "test-access-key" ||
		creds.SecretAccessKey != "test-secret-key" ||
		creds.Source == "" {
		t.Fatalf("credentials = %+v", creds)
	}
}

func newOntologyArtifactHealthSweeperConfiguredRuntime(t *testing.T, ctx context.Context) *platform.Runtime {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orrery.yaml")
	if err := os.WriteFile(path, []byte(`
database:
  default_url: file://./stage0-test.db
ontology:
  artifact_health_sweeper:
    limit: 1
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

func newOntologyArtifactHealthSweeperS3Runtime(t *testing.T, ctx context.Context) *platform.Runtime {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orrery.yaml")
	if err := os.WriteFile(path, []byte(`
database:
  default_url: file://./stage0-test.db
ontology:
  artifact_health_sweeper:
    s3:
      enabled: true
      region: us-east-1
      endpoint_url: http://localhost:9000
      expected_bucket_owner: "123456789012"
      allowed_prefixes:
        - orrery-eval/tenant-1/
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

type fakeProductionOntologyArtifactHealthS3HeadObjectClient struct {
	output *s3.HeadObjectOutput
}

func (c fakeProductionOntologyArtifactHealthS3HeadObjectClient) HeadObject(context.Context, *s3.HeadObjectInput, ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	return c.output, nil
}
