package filestore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"orrery_v2/internal/platform/logging"
	"orrery_v2/internal/platform/secret"
)

type s3Client interface {
	PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	DeleteObject(context.Context, *s3.DeleteObjectInput, ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
	ListObjectsV2(context.Context, *s3.ListObjectsV2Input, ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
}

// S3 uses the official AWS SDK directly; location controls URL presentation
// while the SDK owns authentication, signing and transport.
type S3 struct {
	client      s3Client
	location    S3Config
	clock       Clock
	idGenerator ErrorIDGenerator
	logger      logging.Logger
}

func NewS3(ctx context.Context, cfg S3Config, secrets secret.Provider, logger logging.Logger) (*S3, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if cfg.Bucket == "" || strings.ContainsAny(cfg.Bucket, "/\\:") || LooksLikeS3Hostname(cfg.Bucket) {
		return nil, fmt.Errorf("a bucket name is required, rather than an S3 URL or hostname")
	}
	if cfg.Region == "" {
		cfg.Region = "us-west-2"
	}
	if cfg.EndpointURL != "" {
		endpoint, err := url.Parse(cfg.EndpointURL)
		if err != nil || endpoint.Host == "" || endpoint.Scheme != "http" && endpoint.Scheme != "https" {
			return nil, fmt.Errorf("invalid S3 endpoint")
		}
	}
	options := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(cfg.Region)}
	if secrets != nil {
		access, accessErr := secrets.Get(ctx, "aws_access_key_id")
		password, passwordErr := secrets.Get(ctx, "aws_secret_access_key")
		if accessErr == nil && passwordErr == nil {
			options = append(options, awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(access.Plaintext(), password.Plaintext(), "")))
		}
	}
	configuration, err := awsconfig.LoadDefaultConfig(ctx, options...)
	if err != nil {
		return nil, fmt.Errorf("configure S3 transport: %w", err)
	}
	client := s3.NewFromConfig(configuration, func(options *s3.Options) {
		if cfg.EndpointURL != "" {
			options.BaseEndpoint, options.UsePathStyle = aws.String(cfg.EndpointURL), true
		}
	})
	return &S3{client: client, location: cfg, clock: defaultClock(cfg.Clock), idGenerator: defaultIDGenerator(cfg.IDGenerator), logger: logger}, nil
}

func LooksLikeS3Hostname(bucket string) bool {
	return strings.HasSuffix(strings.ToLower(bucket), ".amazonaws.com") || strings.HasSuffix(strings.ToLower(bucket), ".amazonaws.com.cn")
}

func (s *S3) CdnDomain() string { return s.location.CDNDomain }

func (s *S3) objectURL(key string) string {
	address := s.location
	base := ""
	switch {
	case address.CDNDomain != "":
		base = "https://" + address.CDNDomain
	case address.EndpointURL != "":
		base = strings.TrimRight(address.EndpointURL, "/") + "/" + address.Bucket
	case strings.ContainsRune(address.Bucket, '.'):
		base = "https://s3." + address.Region + ".amazonaws.com/" + address.Bucket
	default:
		base = "https://" + address.Bucket + ".s3." + address.Region + ".amazonaws.com"
	}
	parsed, err := url.Parse(base)
	if err != nil {
		return base + "/" + key
	}
	parsed.Path += "/" + key
	return parsed.String()
}

func (s *S3) KeyFromURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	address := s.location
	bases := []string{s.objectURL("")}
	if address.EndpointURL != "" {
		bases = append(bases, strings.TrimRight(address.EndpointURL, "/")+"/"+address.Bucket+"/")
	}
	if address.CDNDomain != "" {
		bases = append(bases, "https://"+address.CDNDomain+"/")
	}
	if address.Region != "" {
		bases = append(bases, "https://"+address.Bucket+".s3."+address.Region+".amazonaws.com/", "https://s3."+address.Region+".amazonaws.com/"+address.Bucket+"/")
	}
	for _, rawBase := range bases {
		base, err := url.Parse(rawBase)
		if err == nil && parsed.Scheme == base.Scheme && parsed.Host == base.Host {
			if key, found := strings.CutPrefix(parsed.Path, base.Path); found {
				return key
			}
		}
	}
	return lastURLComponent(raw, parsed)
}

func (s *S3) Upload(ctx context.Context, key string, body []byte, contentType, filename string) (string, error) {
	return s.UploadFromReader(ctx, key, bytes.NewReader(body), contentType, filename)
}

func (s *S3) UploadFromReader(ctx context.Context, key string, body io.Reader, contentType, filename string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if key == "" {
		return "", fmt.Errorf("empty key")
	}
	if body == nil || s.client == nil {
		return "", fmt.Errorf("S3 client and reader are required")
	}
	class := types.StorageClassIntelligentTiering
	if s.location.EndpointURL != "" {
		class = types.StorageClassStandard
	}
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Key: aws.String(key), Bucket: aws.String(s.location.Bucket), Body: body,
		ContentType: aws.String(contentType), ContentDisposition: aws.String(downloadDisposition(contentType, filename)),
		CacheControl: aws.String("max-age=432000,public"), StorageClass: class,
	})
	if err != nil {
		return "", fmt.Errorf("write S3 artifact %q: %w", key, err)
	}
	return s.objectURL(key), nil
}

func (s *S3) GetReader(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if key == "" {
		return nil, fmt.Errorf("empty key")
	}
	if s.client == nil {
		return nil, fmt.Errorf("S3 client is required")
	}
	result, err := s.client.GetObject(ctx, &s3.GetObjectInput{Key: aws.String(key), Bucket: aws.String(s.location.Bucket)})
	if err != nil {
		return nil, fmt.Errorf("read S3 artifact %q: %w", key, err)
	}
	if result == nil || result.Body == nil {
		return nil, fmt.Errorf("S3 artifact body is missing")
	}
	return result.Body, nil
}

func (s *S3) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if key == "" {
		return nil
	}
	if s.client == nil {
		return fmt.Errorf("S3 client is required")
	}
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Key: aws.String(key), Bucket: aws.String(s.location.Bucket)})
	if err == nil {
		return nil
	}
	if s.logger != nil {
		s.logger.Error(ctx, "artifact deletion failed", logging.String("key", key), logging.String("error", err.Error()))
	}
	return fmt.Errorf("s3 DeleteObject for artifact %q: %w", key, err)
}

func (s *S3) DeleteKeys(ctx context.Context, keys []string) error {
	return deleteObjectKeys(ctx, keys, s.Delete)
}

func (s *S3) DeletePrefix(ctx context.Context, prefix string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(prefix) == "" {
		return nil
	}
	value, err := normalizeObjectPrefix(prefix)
	if err != nil {
		return err
	}
	if s.client == nil {
		return fmt.Errorf("S3 client is required")
	}
	var continuation *string
	visited := make(map[string]bool)
	var failures []error
	for {
		page, err := s.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(s.location.Bucket), Prefix: aws.String(value), ContinuationToken: continuation})
		if err != nil {
			return errors.Join(append(failures, err)...)
		}
		if page == nil {
			return errors.Join(append(failures, fmt.Errorf("S3 returned no listing"))...)
		}
		for _, object := range page.Contents {
			key := aws.ToString(object.Key)
			if key == "" {
				continue
			}
			if !strings.HasPrefix(key, value) {
				failures = append(failures, fmt.Errorf("S3 listing returned a key outside prefix %q", value))
				continue
			}
			if err := s.Delete(ctx, key); err != nil {
				failures = append(failures, err)
			}
		}
		if !aws.ToBool(page.IsTruncated) {
			return errors.Join(failures...)
		}
		next := aws.ToString(page.NextContinuationToken)
		if next == "" || visited[next] {
			return errors.Join(append(failures, fmt.Errorf("S3 listing did not advance"))...)
		}
		visited[next] = true
		continuation = page.NextContinuationToken
	}
}

func (s *S3) Put(ctx context.Context, name, contentType string, reader io.Reader) (ArtifactRef, error) {
	if err := ctx.Err(); err != nil {
		return ArtifactRef{}, err
	}
	if reader == nil {
		return ArtifactRef{}, fmt.Errorf("artifact reader is required")
	}
	body, err := io.ReadAll(contextReader{ctx, reader})
	if err != nil {
		return ArtifactRef{}, err
	}
	key, err := newArtifactID(s.idGenerator, nil)
	if err != nil {
		return ArtifactRef{}, err
	}
	location, err := s.Upload(ctx, key, body, contentType, name)
	if err != nil {
		return ArtifactRef{}, err
	}
	digest := sha256.Sum256(body)
	return ArtifactRef{ID: key, Name: sanitizeName(name), ContentType: contentType, Size: int64(len(body)),
		Checksum: hex.EncodeToString(digest[:]), CreatedAt: s.clock.Now().UTC(), URL: location}, nil
}

func (s *S3) Get(ctx context.Context, key string) (Artifact, error) {
	reader, err := s.GetReader(ctx, key)
	if err != nil {
		return Artifact{}, err
	}
	defer reader.Close()
	body, err := io.ReadAll(contextReader{ctx, reader})
	if err != nil {
		return Artifact{}, err
	}
	digest := sha256.Sum256(body)
	return Artifact{Content: body, Ref: ArtifactRef{ID: key, Size: int64(len(body)), Checksum: hex.EncodeToString(digest[:]), URL: s.objectURL(key)}}, nil
}

func (s *S3) URL(ctx context.Context, key string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if key == "" {
		return "", fmt.Errorf("empty key")
	}
	return s.objectURL(key), nil
}
