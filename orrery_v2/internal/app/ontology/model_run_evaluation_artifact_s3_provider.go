package ontology

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

type ModelRunEvaluationArtifactS3HeadObjectClient interface {
	HeadObject(ctx context.Context, params *s3.HeadObjectInput, optFns ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
}

type ModelRunEvaluationArtifactS3ProviderProbeOptions struct {
	Client              ModelRunEvaluationArtifactS3HeadObjectClient
	ExpectedBucketOwner string
}

type ModelRunEvaluationArtifactS3ProviderProbe struct {
	client              ModelRunEvaluationArtifactS3HeadObjectClient
	expectedBucketOwner string
}

func NewModelRunEvaluationArtifactS3ProviderProbe(options ModelRunEvaluationArtifactS3ProviderProbeOptions) ModelRunEvaluationArtifactS3ProviderProbe {
	return ModelRunEvaluationArtifactS3ProviderProbe{
		client:              options.Client,
		expectedBucketOwner: strings.TrimSpace(options.ExpectedBucketOwner),
	}
}

func (p ModelRunEvaluationArtifactS3ProviderProbe) ProbeModelRunEvaluationArtifact(ctx context.Context, artifact ModelRunEvaluationArtifact) (ModelRunEvaluationArtifactProviderProbeResult, error) {
	if p.client == nil {
		return ModelRunEvaluationArtifactProviderProbeResult{}, fmt.Errorf("%w: model run evaluation artifact S3 provider client is required", ErrInvalidDefinition)
	}
	bucket, key, err := modelRunEvaluationArtifactS3BucketKey(artifact)
	if err != nil {
		return ModelRunEvaluationArtifactProviderProbeResult{}, err
	}
	input := &s3.HeadObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	}
	if p.expectedBucketOwner != "" {
		input.ExpectedBucketOwner = aws.String(p.expectedBucketOwner)
	}
	out, err := p.client.HeadObject(ctx, input)
	if err != nil {
		if code, ok := modelRunEvaluationArtifactS3ErrorCode(err); ok && modelRunEvaluationArtifactS3MissingCode(code) {
			return ModelRunEvaluationArtifactProviderProbeResult{
				Available: false,
				Metadata: map[string]string{
					"artifact_health_error_code": code,
					"artifact_health_s3_bucket":  bucket,
					"artifact_health_s3_key":     key,
				},
			}, nil
		}
		return ModelRunEvaluationArtifactProviderProbeResult{}, fmt.Errorf("s3 HeadObject %s/%s: %w", bucket, key, err)
	}
	return ModelRunEvaluationArtifactProviderProbeResult{
		Available: true,
		Metadata:  modelRunEvaluationArtifactS3HeadObjectMetadata(bucket, key, out),
	}, nil
}

func modelRunEvaluationArtifactS3BucketKey(artifact ModelRunEvaluationArtifact) (string, string, error) {
	if err := ValidateModelRunEvaluationArtifactReference(&artifact); err != nil {
		return "", "", err
	}
	parsed, err := url.Parse(artifact.ArtifactURI)
	if err != nil {
		return "", "", fmt.Errorf("%w: model run evaluation artifact S3 URI is invalid", ErrInvalidDefinition)
	}
	if strings.ToLower(parsed.Scheme) != "s3" {
		return "", "", fmt.Errorf("%w: model run evaluation artifact provider %q is not s3", ErrInvalidDefinition, parsed.Scheme)
	}
	bucket := strings.TrimSpace(parsed.Host)
	key := strings.Trim(parsed.Path, "/")
	if bucket == "" || key == "" {
		return "", "", fmt.Errorf("%w: model run evaluation artifact S3 URI must include bucket and key", ErrInvalidDefinition)
	}
	return bucket, key, nil
}

func modelRunEvaluationArtifactS3HeadObjectMetadata(bucket string, key string, out *s3.HeadObjectOutput) map[string]string {
	metadata := map[string]string{
		"artifact_health_s3_bucket": bucket,
		"artifact_health_s3_key":    key,
	}
	if out == nil {
		return metadata
	}
	if out.ContentLength != nil {
		metadata["artifact_health_object_size_bytes"] = strconv.FormatInt(aws.ToInt64(out.ContentLength), 10)
	}
	if value := strings.TrimSpace(aws.ToString(out.ContentType)); value != "" {
		metadata["artifact_health_object_content_type"] = value
	}
	if value := strings.Trim(strings.TrimSpace(aws.ToString(out.ETag)), `"`); value != "" {
		metadata["artifact_health_object_etag"] = value
	}
	if out.LastModified != nil && !out.LastModified.IsZero() {
		metadata["artifact_health_object_last_modified"] = out.LastModified.UTC().Format(time.RFC3339Nano)
	}
	if value := strings.TrimSpace(aws.ToString(out.VersionId)); value != "" {
		metadata["artifact_health_object_version_id"] = value
	}
	return metadata
}

func modelRunEvaluationArtifactS3ErrorCode(err error) (string, bool) {
	var noSuchKey *types.NoSuchKey
	if errors.As(err, &noSuchKey) {
		return noSuchKey.ErrorCode(), true
	}
	var notFound *types.NotFound
	if errors.As(err, &notFound) {
		return notFound.ErrorCode(), true
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode(), true
	}
	return "", false
}

func modelRunEvaluationArtifactS3MissingCode(code string) bool {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "nosuchkey", "notfound", "404":
		return true
	default:
		return false
	}
}
