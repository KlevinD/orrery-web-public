package filestore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	platformerrors "orrery_v2/internal/platform/errors"
)

func (s *Local) Put(ctx context.Context, name, contentType string, reader io.Reader) (ArtifactRef, error) {
	if err := ctx.Err(); err != nil {
		return ArtifactRef{}, err
	}
	if reader == nil {
		return ArtifactRef{}, fmt.Errorf("artifact reader is required")
	}
	content, err := io.ReadAll(contextReader{ctx, reader})
	if err != nil {
		return ArtifactRef{}, fmt.Errorf("read artifact: %w", err)
	}
	sum := sha256.Sum256(content)
	id, err := s.nextArtifactID()
	if err != nil {
		return ArtifactRef{}, err
	}
	ref := ArtifactRef{
		ID:          id,
		Name:        sanitizeName(name),
		ContentType: contentType,
		Size:        int64(len(content)),
		Checksum:    hex.EncodeToString(sum[:]),
		CreatedAt:   s.clock.Now().UTC(),
	}
	ref.URL, err = s.Upload(ctx, ref.ID, content, contentType, name)
	if err != nil {
		return ArtifactRef{}, err
	}
	return ref, nil
}

func (s *Local) Get(ctx context.Context, id string) (Artifact, error) {
	if err := ctx.Err(); err != nil {
		return Artifact{}, err
	}
	name, err := localObjectName(id)
	if err != nil {
		return Artifact{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	root, err := os.OpenRoot(s.uploadDir)
	if err != nil {
		return Artifact{}, err
	}
	defer root.Close()
	reader, err := root.Open(name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Artifact{}, notFound(id)
		}
		return Artifact{}, err
	}
	defer reader.Close()
	info, err := reader.Stat()
	if err != nil {
		return Artifact{}, err
	}
	if !info.Mode().IsRegular() {
		return Artifact{}, notFound(id)
	}
	content, err := io.ReadAll(contextReader{ctx, reader})
	if err != nil {
		return Artifact{}, fmt.Errorf("read artifact: %w", err)
	}
	sum := sha256.Sum256(content)
	meta := loadLocalMetadata(root, name)
	return Artifact{
		Ref: ArtifactRef{
			ID:          id,
			Name:        sanitizeName(meta.Filename),
			ContentType: meta.ContentType,
			Size:        int64(len(content)),
			Checksum:    hex.EncodeToString(sum[:]),
			URL:         s.publicURL(id),
		},
		Content: content,
	}, nil
}

func (s *Local) URL(ctx context.Context, id string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if id == "" {
		return "", notFound(id)
	}
	reader, err := s.GetReader(ctx, id)
	if err != nil {
		if os.IsNotExist(err) {
			return "", notFound(id)
		}
		return "", err
	}
	if err := reader.Close(); err != nil {
		return "", err
	}
	return s.publicURL(id), nil
}

func (s *Local) publicURL(id string) string {
	if s.baseURL != "" {
		return s.baseURL + "/artifacts/" + id
	}
	return "artifact://" + id
}

func (s *Local) nextArtifactID() (string, error) {
	return newArtifactID(s.idGenerator, func(id string) bool {
		_, err := os.Stat(filepath.Join(s.uploadDir, id))
		return os.IsNotExist(err)
	})
}

func newArtifactID(generator ErrorIDGenerator, available func(string) bool) (string, error) {
	for attempt := 0; attempt < 5; attempt++ {
		id, err := generator.NewIDContext()
		if err != nil {
			return "", fmt.Errorf("generate artifact id: %w", err)
		}
		if available == nil || available(id) {
			return id, nil
		}
	}
	return "", fmt.Errorf("generate artifact id: no available id after retries")
}

func notFound(id string) error {
	return platformerrors.New(platformerrors.CodeNotFound, "artifact not found").WithField("artifact_id").WithMetadata("artifact_id", id)
}

func sanitizeName(name string) string {
	name = strings.TrimSpace(filepath.Base(name))
	if name == "." || name == string(filepath.Separator) || name == "" {
		return "artifact"
	}
	return name
}
