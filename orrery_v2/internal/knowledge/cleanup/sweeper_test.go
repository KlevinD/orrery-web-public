package cleanup_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"orrery_v2/internal/knowledge/cleanup"
	"orrery_v2/internal/knowledge/core"
	"orrery_v2/internal/knowledge/testkit"
	"orrery_v2/internal/knowledge/vector"
	doccore "orrery_v2/internal/platform/document/core"
)

func TestSweeperDropsDeletingBaseAndRemovesMetadata(t *testing.T) {
	ctx := context.Background()
	repo := testkit.NewFakeRepository()
	deleting := cleanupTestBase("kb-deleting", core.KnowledgeDeleting)
	active := cleanupTestBase("kb-active", core.KnowledgeActive)
	if err := repo.SaveKnowledgeBase(ctx, deleting); err != nil {
		t.Fatalf("save deleting base: %v", err)
	}
	if err := repo.SaveKnowledgeBase(ctx, active); err != nil {
		t.Fatalf("save active base: %v", err)
	}
	vectorStore := &recordingVectorStore{}
	sweeper := cleanup.NewSweeper(cleanup.SweeperDeps{
		Repository:  repo,
		VectorStore: vectorStore,
		DescriptorForBase: func(base core.KnowledgeBase) (vector.Descriptor, error) {
			return vector.Descriptor{
				KnowledgeBaseID: string(base.ID),
				Backend:         string(base.VectorBackend),
				RootPath:        "/knowledge-root",
				StorePath:       "/knowledge-root/kb_" + string(base.ID) + "/lancedb",
				TableName:       "chunks",
				Dimensions:      base.EmbeddingDimensions,
			}, nil
		},
	})

	if err := sweeper.Sweep(ctx); err != nil {
		t.Fatalf("sweep deleting base: %v", err)
	}
	if len(vectorStore.dropped) != 1 || vectorStore.dropped[0].KnowledgeBaseID != string(deleting.ID) {
		t.Fatalf("dropped descriptors mismatch: %#v", vectorStore.dropped)
	}
	if _, err := repo.GetKnowledgeBase(ctx, deleting.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("deleting base should be removed after vector drop, got %v", err)
	}
	if _, err := repo.GetKnowledgeBase(ctx, active.ID); err != nil {
		t.Fatalf("active base should remain: %v", err)
	}
}

func TestSweeperKeepsDeletingBaseWhenVectorDropFails(t *testing.T) {
	ctx := context.Background()
	repo := testkit.NewFakeRepository()
	deleting := cleanupTestBase("kb-deleting", core.KnowledgeDeleting)
	if err := repo.SaveKnowledgeBase(ctx, deleting); err != nil {
		t.Fatalf("save deleting base: %v", err)
	}
	vectorStore := &recordingVectorStore{dropErr: errors.New("drop failed")}
	sweeper := cleanup.NewSweeper(cleanup.SweeperDeps{
		Repository:  repo,
		VectorStore: vectorStore,
		DescriptorForBase: func(base core.KnowledgeBase) (vector.Descriptor, error) {
			return vector.Descriptor{KnowledgeBaseID: string(base.ID), RootPath: "/root", StorePath: "/root/kb_" + string(base.ID) + "/lancedb"}, nil
		},
	})

	if err := sweeper.Sweep(ctx); err == nil {
		t.Fatalf("expected sweep to report vector drop failure")
	}
	base, err := repo.GetKnowledgeBase(ctx, deleting.ID)
	if err != nil {
		t.Fatalf("deleting base should remain after failed drop: %v", err)
	}
	if base.Status != core.KnowledgeDeleting {
		t.Fatalf("base status should remain deleting: %#v", base)
	}
}

type recordingVectorStore struct {
	dropped []vector.Descriptor
	dropErr error
}

func (s *recordingVectorStore) DropKnowledgeBase(_ context.Context, descriptor vector.Descriptor) error {
	s.dropped = append(s.dropped, descriptor)
	return s.dropErr
}

func cleanupTestBase(id core.KnowledgeBaseID, status core.KnowledgeStatus) core.KnowledgeBase {
	return core.KnowledgeBase{
		ID:                  id,
		Name:                "Cleanup Test Knowledge",
		OwnerType:           core.OwnerOrganization,
		OwnerID:             "org-1",
		Visibility:          core.VisibilityScoped,
		VectorBackend:       core.VectorBackendLanceDB,
		EmbeddingProfileRef: "embedding",
		EmbeddingModel:      "text-embedding-3-small",
		EmbeddingDimensions: 3,
		ChunkPolicy:         doccore.ChunkPolicy{MaxRunes: 2400, OverlapRunes: 240, MinRunes: 400},
		Status:              status,
		CreatedAt:           time.Now().UTC(),
		UpdatedAt:           time.Now().UTC(),
	}
}
