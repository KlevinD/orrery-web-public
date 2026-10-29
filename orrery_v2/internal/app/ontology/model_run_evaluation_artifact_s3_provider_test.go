package ontology

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

func TestModelRunEvaluationArtifactS3ProviderProbeUsesHeadObject(t *testing.T) {
	lastModified := time.Date(2026, 6, 22, 12, 30, 0, 0, time.UTC)
	client := &fakeModelRunEvaluationArtifactS3HeadObjectClient{
		output: &s3.HeadObjectOutput{
			ContentLength: aws.Int64(2048),
			ContentType:   aws.String("application/json"),
			ETag:          aws.String(`"etag-1"`),
			LastModified:  aws.Time(lastModified),
			VersionId:     aws.String("version-1"),
		},
	}
	probedAt := time.Date(2026, 6, 22, 12, 45, 0, 0, time.UTC)

	artifact, err := ProbeModelRunEvaluationArtifactHealthWithProviders(ModelRunEvaluationArtifact{
		TenantID:     "tenant-1",
		Engine:       "ontology_designer",
		RunID:        "run-s3-head-1",
		ArtifactKind: "evaluation_report",
		ArtifactURI:  "s3://orrery-eval/tenant-1/run-s3-head-1/report.json",
		Metadata: map[string]string{
			"dataset_ref":    "ontology-eval-20260622",
			"policy_version": "policy-v1",
		},
	}, func() time.Time { return probedAt }, map[string]ModelRunEvaluationArtifactProviderProbe{
		"s3": NewModelRunEvaluationArtifactS3ProviderProbe(ModelRunEvaluationArtifactS3ProviderProbeOptions{
			Client:              client,
			ExpectedBucketOwner: "123456789012",
		}),
	})
	if err != nil {
		t.Fatalf("ProbeModelRunEvaluationArtifactHealthWithProviders: %v", err)
	}
	if len(client.inputs) != 1 {
		t.Fatalf("HeadObject calls = %d, want 1", len(client.inputs))
	}
	input := client.inputs[0]
	if aws.ToString(input.Bucket) != "orrery-eval" ||
		aws.ToString(input.Key) != "tenant-1/run-s3-head-1/report.json" ||
		aws.ToString(input.ExpectedBucketOwner) != "123456789012" {
		t.Fatalf("HeadObject input = %+v", input)
	}
	metadata := artifact.Metadata
	if metadata["artifact_health_status"] != "available" ||
		metadata["artifact_health_probe_mode"] != "manual_probe" ||
		metadata["artifact_health_object_size_bytes"] != "2048" ||
		metadata["artifact_health_object_content_type"] != "application/json" ||
		metadata["artifact_health_object_etag"] != "etag-1" ||
		metadata["artifact_health_object_last_modified"] != lastModified.Format(time.RFC3339Nano) ||
		metadata["artifact_health_object_version_id"] != "version-1" ||
		metadata["artifact_health_s3_bucket"] != "orrery-eval" ||
		metadata["artifact_health_s3_key"] != "tenant-1/run-s3-head-1/report.json" ||
		metadata["artifact_health_error"] != "" {
		t.Fatalf("S3 health metadata = %+v", metadata)
	}
}

func TestModelRunEvaluationArtifactS3ProviderProbeMarksMissingObjectUnavailable(t *testing.T) {
	client := &fakeModelRunEvaluationArtifactS3HeadObjectClient{
		err: &types.NoSuchKey{},
	}
	probedAt := time.Date(2026, 6, 22, 13, 0, 0, 0, time.UTC)

	artifact, err := ProbeModelRunEvaluationArtifactHealthWithProviders(ModelRunEvaluationArtifact{
		TenantID:     "tenant-1",
		Engine:       "ontology_designer",
		RunID:        "run-s3-head-missing",
		ArtifactKind: "evaluation_report",
		ArtifactURI:  "s3://orrery-eval/tenant-1/missing.json",
		Metadata: map[string]string{
			"dataset_ref":    "ontology-eval-20260622",
			"policy_version": "policy-v1",
		},
	}, func() time.Time { return probedAt }, map[string]ModelRunEvaluationArtifactProviderProbe{
		"s3": NewModelRunEvaluationArtifactS3ProviderProbe(ModelRunEvaluationArtifactS3ProviderProbeOptions{
			Client: client,
		}),
	})
	if err != nil {
		t.Fatalf("ProbeModelRunEvaluationArtifactHealthWithProviders: %v", err)
	}
	if len(client.inputs) != 1 {
		t.Fatalf("HeadObject calls = %d, want 1", len(client.inputs))
	}
	metadata := artifact.Metadata
	if metadata["artifact_health_status"] != "unavailable" ||
		metadata["artifact_health_error_code"] != "NoSuchKey" ||
		metadata["artifact_health_s3_bucket"] != "orrery-eval" ||
		metadata["artifact_health_s3_key"] != "tenant-1/missing.json" ||
		metadata["artifact_health_error"] != "" {
		t.Fatalf("missing S3 health metadata = %+v", metadata)
	}
}

type fakeModelRunEvaluationArtifactS3HeadObjectClient struct {
	output *s3.HeadObjectOutput
	err    error
	inputs []*s3.HeadObjectInput
}

func (c *fakeModelRunEvaluationArtifactS3HeadObjectClient) HeadObject(_ context.Context, input *s3.HeadObjectInput, _ ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	c.inputs = append(c.inputs, input)
	return c.output, c.err
}
