package platform

import (
	"context"
	"fmt"
	"io"

	"orrery_v2/internal/platform/filestore"
)

type ArtifactStore struct {
	store filestore.Store
}

func NewArtifactStore(store filestore.Store) *ArtifactStore {
	return &ArtifactStore{store: store}
}

func (s *ArtifactStore) Put(ctx context.Context, name, contentType string, reader io.Reader) (filestore.ArtifactRef, error) {
	if s == nil || s.store == nil {
		return filestore.ArtifactRef{}, fmt.Errorf("platform artifact store is not configured")
	}
	ref, err := s.store.Put(ctx, name, contentType, reader)
	if err != nil {
		return filestore.ArtifactRef{}, err
	}
	ref.LocalPath = ""
	return ref, nil
}

func (s *ArtifactStore) Get(ctx context.Context, id string) (filestore.Artifact, error) {
	if s == nil || s.store == nil {
		return filestore.Artifact{}, fmt.Errorf("platform artifact store is not configured")
	}
	artifact, err := s.store.Get(ctx, id)
	if err != nil {
		return filestore.Artifact{}, err
	}
	artifact.Ref.LocalPath = ""
	return artifact, nil
}

func (s *ArtifactStore) GetReader(ctx context.Context, id string) (io.ReadCloser, error) {
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("platform artifact store is not configured")
	}
	return s.store.GetReader(ctx, id)
}

func (s *ArtifactStore) URL(ctx context.Context, id string) (string, error) {
	if s == nil || s.store == nil {
		return "", fmt.Errorf("platform artifact store is not configured")
	}
	return s.store.URL(ctx, id)
}

func (s *ArtifactStore) Delete(ctx context.Context, id string) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("platform artifact store is not configured")
	}
	return s.store.Delete(ctx, id)
}

func (s *ArtifactStore) DeleteMany(ctx context.Context, ids []string) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("platform artifact store is not configured")
	}
	return s.store.DeleteKeys(ctx, ids)
}

func (s *ArtifactStore) DeletePrefix(ctx context.Context, prefix string) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("platform artifact store is not configured")
	}
	return s.store.DeletePrefix(ctx, prefix)
}
