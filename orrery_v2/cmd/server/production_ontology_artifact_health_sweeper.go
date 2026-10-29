package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/secret"
)

const productionOntologyArtifactHealthSweeperLimit = 100

type productionOntologyArtifactHealthSweeperConfig struct {
	Limit int
	S3    productionOntologyArtifactHealthS3Config
}

type productionOntologyArtifactHealthS3Config struct {
	Enabled             bool
	Region              string
	EndpointURL         string
	ExpectedBucketOwner string
	AllowedPrefixes     []string
}

var productionOntologyArtifactHealthS3ProviderBuilderForTest func(context.Context, *platform.Runtime, productionOntologyArtifactHealthS3Config) (ontology.ModelRunEvaluationArtifactProviderProbe, error)

func registerProductionOntologyArtifactHealthSweeper(rt *platform.Runtime, core *db.Store) error {
	if rt == nil || core == nil {
		return fmt.Errorf("ontology artifact health sweeper requires runtime and core database")
	}
	cfg := productionOntologyArtifactHealthSweeperConfigFromRuntime(rt)
	providers, err := productionOntologyArtifactHealthProviders(context.Background(), rt, cfg)
	if err != nil {
		return err
	}
	return rt.RegisterSweeper("ontology.model_run_evaluation_artifact_health.prober", ontology.NewModelRunEvaluationArtifactHealthProbeSweeper(ontology.ModelRunEvaluationArtifactHealthProbeSweeperOptions{
		Store:     ontology.NewPlatformModelRunEvaluationArtifactStore(core),
		Clock:     rt.Clock,
		Providers: providers,
		Query: ontology.ModelRunEvaluationArtifactQuery{
			Limit: cfg.Limit,
		},
	}))
}

func productionOntologyArtifactHealthSweeperConfigFromRuntime(rt *platform.Runtime) productionOntologyArtifactHealthSweeperConfig {
	cfg := productionOntologyArtifactHealthSweeperConfig{Limit: productionOntologyArtifactHealthSweeperLimit}
	if rt == nil {
		return cfg
	}
	if limit, ok := rt.Config.Int("ontology.artifact_health_sweeper.limit"); ok && limit > 0 {
		cfg.Limit = limit
	}
	cfg.S3 = productionOntologyArtifactHealthS3ConfigFromRuntime(rt)
	return cfg
}

func productionOntologyArtifactHealthS3ConfigFromRuntime(rt *platform.Runtime) productionOntologyArtifactHealthS3Config {
	cfg := productionOntologyArtifactHealthS3Config{}
	if rt == nil {
		return cfg
	}
	if enabled, ok := rt.Config.Bool("ontology.artifact_health_sweeper.s3.enabled"); ok {
		cfg.Enabled = enabled
	}
	if region, ok := rt.Config.String("ontology.artifact_health_sweeper.s3.region"); ok {
		cfg.Region = strings.TrimSpace(region)
	}
	if endpoint, ok := rt.Config.String("ontology.artifact_health_sweeper.s3.endpoint_url"); ok {
		cfg.EndpointURL = strings.TrimSpace(endpoint)
	}
	if owner, ok := rt.Config.String("ontology.artifact_health_sweeper.s3.expected_bucket_owner"); ok {
		cfg.ExpectedBucketOwner = strings.TrimSpace(owner)
	}
	if prefixes, ok := stringSliceConfig(rt.Config.Lookup("ontology.artifact_health_sweeper.s3.allowed_prefixes")); ok {
		cfg.AllowedPrefixes = prefixes
	}
	return cfg
}

func productionOntologyArtifactHealthProviders(ctx context.Context, rt *platform.Runtime, cfg productionOntologyArtifactHealthSweeperConfig) (map[string]ontology.ModelRunEvaluationArtifactProviderProbe, error) {
	providers := map[string]ontology.ModelRunEvaluationArtifactProviderProbe{}
	if cfg.S3.Enabled {
		if len(cfg.S3.AllowedPrefixes) == 0 {
			return nil, fmt.Errorf("ontology artifact health S3 provider requires allowed prefixes")
		}
		probe, err := productionOntologyArtifactHealthS3Provider(ctx, rt, cfg.S3)
		if err != nil {
			return nil, err
		}
		providers["s3"] = ontology.NewScopedModelRunEvaluationArtifactProviderProbe("s3", cfg.S3.AllowedPrefixes, probe)
	}
	return providers, nil
}

func productionOntologyArtifactHealthS3Provider(ctx context.Context, rt *platform.Runtime, cfg productionOntologyArtifactHealthS3Config) (ontology.ModelRunEvaluationArtifactProviderProbe, error) {
	if productionOntologyArtifactHealthS3ProviderBuilderForTest != nil {
		return productionOntologyArtifactHealthS3ProviderBuilderForTest(ctx, rt, cfg)
	}
	awsCfg, err := productionOntologyArtifactHealthS3AWSConfig(ctx, rt, cfg)
	if err != nil {
		return nil, err
	}
	options := []func(*s3.Options){}
	if cfg.EndpointURL != "" {
		options = append(options, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(cfg.EndpointURL)
			o.UsePathStyle = true
		})
	}
	return ontology.NewModelRunEvaluationArtifactS3ProviderProbe(ontology.ModelRunEvaluationArtifactS3ProviderProbeOptions{
		Client:              s3.NewFromConfig(awsCfg, options...),
		ExpectedBucketOwner: cfg.ExpectedBucketOwner,
	}), nil
}

func productionOntologyArtifactHealthS3AWSConfig(ctx context.Context, rt *platform.Runtime, cfg productionOntologyArtifactHealthS3Config) (aws.Config, error) {
	region := firstNonBlank(cfg.Region, "us-west-2")
	opts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(region),
	}
	if rt != nil && rt.Secrets != nil {
		accessKey, accessErr := rt.Secrets.Get(ctx, "aws_access_key_id")
		secretKey, secretErr := rt.Secrets.Get(ctx, "aws_secret_access_key")
		if accessErr == nil && secretErr == nil {
			opts = append(opts, awsconfig.WithCredentialsProvider(
				credentials.NewStaticCredentialsProvider(accessKey.Plaintext(), secretKey.Plaintext(), ""),
			))
		} else if accessErr != nil && !secret.IsNotFound(accessErr) {
			return aws.Config{}, fmt.Errorf("load ontology artifact health S3 access key secret: %w", accessErr)
		} else if secretErr != nil && !secret.IsNotFound(secretErr) {
			return aws.Config{}, fmt.Errorf("load ontology artifact health S3 secret key secret: %w", secretErr)
		}
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return aws.Config{}, fmt.Errorf("load ontology artifact health S3 config: %w", err)
	}
	return awsCfg, nil
}

func stringSliceConfig(value any, ok bool) ([]string, bool) {
	if !ok || value == nil {
		return nil, false
	}
	out := []string{}
	switch v := value.(type) {
	case []any:
		for _, item := range v {
			if text := strings.TrimSpace(fmt.Sprint(item)); text != "" {
				out = append(out, text)
			}
		}
	case []string:
		for _, item := range v {
			if text := strings.TrimSpace(item); text != "" {
				out = append(out, text)
			}
		}
	case string:
		for _, item := range strings.Split(v, ",") {
			if text := strings.TrimSpace(item); text != "" {
				out = append(out, text)
			}
		}
	default:
		return nil, false
	}
	return out, true
}
