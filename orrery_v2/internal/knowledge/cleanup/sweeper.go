package cleanup

import (
	"context"
	"errors"
	"fmt"

	"orrery_v2/internal/knowledge/core"
	"orrery_v2/internal/knowledge/vector"
)

type Repository interface {
	ListKnowledgeBases(ctx context.Context, query core.KnowledgeBaseQuery) (core.KnowledgeBasePage, error)
	DeleteKnowledgeBaseMetadata(ctx context.Context, id core.KnowledgeBaseID) error
}

type VectorStore interface {
	DropKnowledgeBase(ctx context.Context, descriptor vector.Descriptor) error
}

type DescriptorForBase func(core.KnowledgeBase) (vector.Descriptor, error)

type SweeperDeps struct {
	Repository        Repository
	VectorStore       VectorStore
	DescriptorForBase DescriptorForBase
	Limit             int
}

type Sweeper struct {
	repository        Repository
	vectorStore       VectorStore
	descriptorForBase DescriptorForBase
	limit             int
}

func NewSweeper(deps SweeperDeps) *Sweeper {
	return &Sweeper{
		repository:        deps.Repository,
		vectorStore:       deps.VectorStore,
		descriptorForBase: deps.DescriptorForBase,
		limit:             deps.Limit,
	}
}

func (s *Sweeper) Sweep(ctx context.Context) error {
	if s == nil || s.repository == nil || s.vectorStore == nil || s.descriptorForBase == nil {
		return fmt.Errorf("%w: knowledge cleanup dependencies are required", core.ErrDependencyUnavailable)
	}
	page, err := s.repository.ListKnowledgeBases(ctx, core.KnowledgeBaseQuery{
		Status: core.KnowledgeDeleting,
		Limit:  s.limit,
	})
	if err != nil {
		return err
	}
	var errs []error
	for _, base := range page.Items {
		if err := s.sweepBase(ctx, base); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *Sweeper) sweepBase(ctx context.Context, base core.KnowledgeBase) error {
	descriptor, err := s.descriptorForBase(base)
	if err != nil {
		return err
	}
	if err := s.vectorStore.DropKnowledgeBase(ctx, descriptor); err != nil {
		return fmt.Errorf("%w: drop vector store for %s: %v", core.ErrDeleteFailed, base.ID, err)
	}
	return s.repository.DeleteKnowledgeBaseMetadata(ctx, base.ID)
}
