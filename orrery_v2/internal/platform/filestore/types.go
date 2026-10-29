package filestore

import (
	"context"
	"fmt"
	"io"
	"time"

	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/config"
	"orrery_v2/internal/platform/idgen"
	"orrery_v2/internal/platform/logging"
	"orrery_v2/internal/platform/secret"
)

type ErrorIDGenerator interface {
	NewIDContext() (string, error)
}

type Clock = clock.Clock

type Store interface {
	Put(ctx context.Context, name, contentType string, reader io.Reader) (ArtifactRef, error)
	Get(ctx context.Context, id string) (Artifact, error)
	GetReader(ctx context.Context, id string) (io.ReadCloser, error)
	Upload(ctx context.Context, key string, data []byte, contentType string, filename string) (string, error)
	Delete(ctx context.Context, id string) error
	DeleteKeys(ctx context.Context, ids []string) error
	DeletePrefix(ctx context.Context, prefix string) error
	URL(ctx context.Context, id string) (string, error)
	KeyFromURL(rawURL string) string
	CdnDomain() string
}

type Dependencies struct {
	Secrets     secret.Provider
	Logger      logging.Logger
	Clock       clock.Clock
	IDGenerator ErrorIDGenerator
}

func OpenFromConfig(ctx context.Context, cfg config.FileStoreConfig, deps Dependencies) (Store, error) {
	switch cfg.Backend {
	case "", "local":
		store, err := OpenLocalStorage(LocalConfig{
			Root:        cfg.Root,
			BaseURL:     cfg.BaseURL,
			Logger:      deps.Logger,
			Clock:       deps.Clock,
			IDGenerator: deps.IDGenerator,
		})
		if err != nil {
			return nil, err
		}
		return store, nil
	case "s3":
		store, err := NewS3(ctx, S3Config{
			Bucket:      cfg.S3.Bucket,
			Region:      cfg.S3.Region,
			EndpointURL: cfg.S3.EndpointURL,
			CDNDomain:   cfg.S3.CDNDomain,
			Clock:       deps.Clock,
			IDGenerator: deps.IDGenerator,
		}, deps.Secrets, deps.Logger)
		if err != nil {
			return nil, err
		}
		return store, nil
	default:
		return nil, fmt.Errorf("unsupported filestore backend %q", cfg.Backend)
	}
}

type LocalConfig struct {
	Root        string
	BaseURL     string
	Logger      logging.Logger
	Clock       clock.Clock
	IDGenerator ErrorIDGenerator
}

type S3Config struct {
	Bucket      string
	Region      string
	EndpointURL string
	CDNDomain   string
	Clock       clock.Clock
	IDGenerator ErrorIDGenerator
}

func defaultClock(c clock.Clock) clock.Clock {
	if c != nil {
		return c
	}
	return clock.Real{}
}

func defaultIDGenerator(generator ErrorIDGenerator) ErrorIDGenerator {
	if generator != nil {
		return generator
	}
	return idgen.NewRandom("artifact")
}

type ArtifactRef struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	ContentType string    `json:"content_type"`
	Size        int64     `json:"size"`
	Checksum    string    `json:"checksum"`
	CreatedAt   time.Time `json:"created_at"`
	URL         string    `json:"url,omitempty"`
	LocalPath   string    `json:"-"`
}

type Artifact struct {
	Ref     ArtifactRef
	Content []byte
}
