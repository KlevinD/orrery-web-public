package filestore

import (
	"context"
	"errors"
	"io"
	"mime"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/idgen"
)

type ownedS3Client struct {
	objects      map[string][]byte
	lastWrite    *s3.PutObjectInput
	pages        []*s3.ListObjectsV2Output
	listingCount int
	removed      []string
	failure      error
}

func (c *ownedS3Client) PutObject(_ context.Context, request *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	c.lastWrite = request
	if c.failure != nil {
		return nil, c.failure
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	c.objects[aws.ToString(request.Key)] = body
	return &s3.PutObjectOutput{}, nil
}
func (c *ownedS3Client) GetObject(_ context.Context, request *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	if c.failure != nil {
		return nil, c.failure
	}
	body, found := c.objects[aws.ToString(request.Key)]
	if !found {
		return nil, errors.New("object absent")
	}
	return &s3.GetObjectOutput{Body: io.NopCloser(strings.NewReader(string(body)))}, nil
}
func (c *ownedS3Client) DeleteObject(_ context.Context, request *s3.DeleteObjectInput, _ ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	c.removed = append(c.removed, aws.ToString(request.Key))
	if c.failure != nil {
		return nil, c.failure
	}
	delete(c.objects, aws.ToString(request.Key))
	return &s3.DeleteObjectOutput{}, nil
}
func (c *ownedS3Client) ListObjectsV2(_ context.Context, _ *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	if c.failure != nil {
		return nil, c.failure
	}
	if c.listingCount >= len(c.pages) {
		return &s3.ListObjectsV2Output{}, nil
	}
	page := c.pages[c.listingCount]
	c.listingCount++
	return page, nil
}

func ownedS3(cfg S3Config) (*S3, *ownedS3Client) {
	client := &ownedS3Client{objects: make(map[string][]byte)}
	return &S3{client: client, location: cfg, clock: defaultClock(cfg.Clock), idGenerator: defaultIDGenerator(cfg.IDGenerator)}, client
}

func TestOwnedS3AddressCompatibility(t *testing.T) {
	for _, entry := range []struct {
		cfg      S3Config
		expected string
	}{
		{S3Config{Bucket: "assets", Region: "eu-west-1"}, "https://assets.s3.eu-west-1.amazonaws.com/nested/report.txt"},
		{S3Config{Bucket: "assets.files", Region: "eu-west-1"}, "https://s3.eu-west-1.amazonaws.com/assets.files/nested/report.txt"},
		{S3Config{Bucket: "assets", EndpointURL: "http://127.0.0.1:9000/prefix/"}, "http://127.0.0.1:9000/prefix/assets/nested/report.txt"},
		{S3Config{Bucket: "assets", CDNDomain: "cdn.example.test"}, "https://cdn.example.test/nested/report.txt"},
	} {
		store, _ := ownedS3(entry.cfg)
		t.Run(entry.expected, func(t *testing.T) {
			location, err := store.URL(context.Background(), "nested/report.txt")
			if err != nil || location != entry.expected || store.KeyFromURL(location) != "nested/report.txt" {
				t.Fatalf("address: %s %v", location, err)
			}
			if key := store.KeyFromURL("https://unknown.example.test/elsewhere/report.txt"); key != "report.txt" {
				t.Fatalf("legacy basename: %s", key)
			}
		})
	}
}

func TestOwnedS3ArtifactAndUploadHeaders(t *testing.T) {
	now := time.Date(2026, 10, 15, 7, 0, 0, 0, time.UTC)
	store, client := ownedS3(S3Config{Bucket: "assets", Region: "eu-west-1", Clock: clock.NewFake(now), IDGenerator: idgen.NewDeterministic("s3-owned")})
	ctx := context.Background()
	ref, err := store.Put(ctx, "report.pdf", "application/pdf", strings.NewReader("pdf-content"))
	if err != nil {
		t.Fatal(err)
	}
	if ref.ID != "s3-owned-000001" || ref.Size != 11 || !ref.CreatedAt.Equal(now) || ref.Checksum == "" {
		t.Fatalf("ref: %+v", ref)
	}
	artifact, err := store.Get(ctx, ref.ID)
	if err != nil || string(artifact.Content) != "pdf-content" || artifact.Ref.Checksum != ref.Checksum {
		t.Fatalf("round trip: %+v %v", artifact, err)
	}
	if client.lastWrite.StorageClass != types.StorageClassIntelligentTiering || aws.ToString(client.lastWrite.Bucket) != "assets" {
		t.Fatalf("SDK request: %+v", client.lastWrite)
	}
	mode, params, err := mime.ParseMediaType(aws.ToString(client.lastWrite.ContentDisposition))
	if err != nil || mode != "inline" || params["filename"] != "report.pdf" {
		t.Fatalf("headers: %s %v %v", mode, params, err)
	}
	store.location.EndpointURL = "http://127.0.0.1:9000"
	if _, err := store.Upload(ctx, "second", []byte("x"), "text/plain", "name\r\nInjected: yes.txt"); err != nil {
		t.Fatal(err)
	}
	if client.lastWrite.StorageClass != types.StorageClassStandard || strings.ContainsAny(aws.ToString(client.lastWrite.ContentDisposition), "\r\n") {
		t.Fatal("custom endpoint or disposition invariant failed")
	}
}

func TestOwnedS3FailuresRemainObservable(t *testing.T) {
	store, client := ownedS3(S3Config{Bucket: "assets"})
	sentinel := errors.New("backend not available")
	client.failure = sentinel
	ctx := context.Background()
	if err := store.DeleteKeys(ctx, []string{"first", "second"}); !errors.Is(err, sentinel) || len(client.removed) != 2 {
		t.Fatalf("batch: %v %v", err, client.removed)
	}
	if _, err := store.Upload(ctx, "x", []byte("x"), "", ""); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	if _, err := store.GetReader(ctx, "x"); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	if err := store.DeletePrefix(ctx, "run/"); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := store.Upload(cancelled, "x", nil, "", ""); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := store.Delete(cancelled, "x"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := store.GetReader(cancelled, "x"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestOwnedS3PaginationStaysWithinRequestedScope(t *testing.T) {
	store, client := ownedS3(S3Config{Bucket: "assets"})
	client.pages = []*s3.ListObjectsV2Output{
		{Contents: []types.Object{{Key: aws.String("run/first")}}, IsTruncated: aws.Bool(true), NextContinuationToken: aws.String("page-two")},
		{Contents: []types.Object{{Key: aws.String("run/second")}, {Key: aws.String("other/private")}}},
	}
	if err := store.DeletePrefix(context.Background(), "run/"); err == nil {
		t.Fatal("out-of-scope listing was accepted")
	}
	if strings.Join(client.removed, ",") != "run/first,run/second" {
		t.Fatalf("deleted unrelated objects: %v", client.removed)
	}
	client.listingCount = 0
	client.pages = []*s3.ListObjectsV2Output{{IsTruncated: aws.Bool(true)}}
	if err := store.DeletePrefix(context.Background(), "run/"); err == nil {
		t.Fatal("missing continuation token was accepted")
	}
	client.listingCount = 0
	client.pages = []*s3.ListObjectsV2Output{{IsTruncated: aws.Bool(true), NextContinuationToken: aws.String("same")}, {IsTruncated: aws.Bool(true), NextContinuationToken: aws.String("same")}}
	if err := store.DeletePrefix(context.Background(), "run/"); err == nil {
		t.Fatal("repeated continuation token was accepted")
	}
	for _, prefix := range []string{"/", "../", "run/../private"} {
		if err := store.DeletePrefix(context.Background(), prefix); err == nil {
			t.Fatalf("unsafe prefix %q", prefix)
		}
	}
}

func TestOwnedS3InitializationRejectsInvalidLocation(t *testing.T) {
	for _, cfg := range []S3Config{{}, {Bucket: "assets.s3.eu-west-1.amazonaws.com"}, {Bucket: "assets", EndpointURL: "file:///tmp"}} {
		if store, err := NewS3(context.Background(), cfg, nil, nil); err == nil || store != nil {
			t.Fatalf("configuration accepted: %+v", cfg)
		}
	}
}
