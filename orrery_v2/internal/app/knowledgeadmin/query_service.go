package knowledgeadmin

import (
	"context"

	"orrery_v2/internal/knowledge/core"
	"orrery_v2/internal/knowledge/store"
)

type QueryService struct {
	repo   store.Repository
	policy *AccessPolicy
}

func NewQueryService(repo store.Repository) *QueryService {
	return &QueryService{repo: repo, policy: NewAccessPolicy(repo)}
}

func (s *QueryService) ListKnowledgeBases(ctx context.Context, viewer ViewerRef, query ListKnowledgeBasesQuery) (KnowledgeBasePageDTO, error) {
	if err := requirePermission(viewer, PermissionKnowledgeRead); err != nil {
		return KnowledgeBasePageDTO{}, err
	}
	if s == nil || s.repo == nil {
		return KnowledgeBasePageDTO{}, ErrServiceDependency
	}
	page, err := s.repo.ListKnowledgeBases(ctx, core.KnowledgeBaseQuery{OwnerType: core.OwnerType(query.OwnerType), OwnerID: query.OwnerID})
	if err != nil {
		return KnowledgeBasePageDTO{}, err
	}
	items := make([]KnowledgeBaseDTO, 0, len(page.Items))
	for _, base := range page.Items {
		if !s.canReadBase(ctx, viewer, base) {
			continue
		}
		items = append(items, mapBase(base))
	}
	return KnowledgeBasePageDTO{Items: items, Total: len(items)}, nil
}

func (s *QueryService) GetKnowledgeBase(ctx context.Context, viewer ViewerRef, id string) (KnowledgeBaseDTO, error) {
	if err := requirePermission(viewer, PermissionKnowledgeRead); err != nil {
		return KnowledgeBaseDTO{}, err
	}
	if s == nil || s.repo == nil {
		return KnowledgeBaseDTO{}, ErrServiceDependency
	}
	base, err := s.repo.GetKnowledgeBase(ctx, core.KnowledgeBaseID(id))
	if err != nil {
		return KnowledgeBaseDTO{}, err
	}
	if !s.canReadBase(ctx, viewer, base) {
		return KnowledgeBaseDTO{}, core.ErrNotFound
	}
	return mapBase(base), nil
}

func (s *QueryService) ListDocuments(ctx context.Context, viewer ViewerRef, knowledgeBaseID string) (KnowledgeDocumentPageDTO, error) {
	if err := requirePermission(viewer, PermissionKnowledgeRead); err != nil {
		return KnowledgeDocumentPageDTO{}, err
	}
	if s == nil || s.repo == nil {
		return KnowledgeDocumentPageDTO{}, ErrServiceDependency
	}
	if err := s.requireBaseRead(ctx, viewer, core.KnowledgeBaseID(knowledgeBaseID)); err != nil {
		return KnowledgeDocumentPageDTO{}, err
	}
	page, err := s.repo.ListDocuments(ctx, core.DocumentQuery{KnowledgeBaseID: core.KnowledgeBaseID(knowledgeBaseID)})
	if err != nil {
		return KnowledgeDocumentPageDTO{}, err
	}
	items := make([]KnowledgeDocumentDTO, 0, len(page.Items))
	for _, doc := range page.Items {
		items = append(items, mapDocument(doc))
	}
	return KnowledgeDocumentPageDTO{Items: items, Total: page.Total}, nil
}

func (s *QueryService) GetDocument(ctx context.Context, viewer ViewerRef, knowledgeBaseID string, documentID string) (KnowledgeDocumentDTO, error) {
	if err := requirePermission(viewer, PermissionKnowledgeRead); err != nil {
		return KnowledgeDocumentDTO{}, err
	}
	if s == nil || s.repo == nil {
		return KnowledgeDocumentDTO{}, ErrServiceDependency
	}
	if err := s.requireBaseRead(ctx, viewer, core.KnowledgeBaseID(knowledgeBaseID)); err != nil {
		return KnowledgeDocumentDTO{}, err
	}
	doc, err := s.repo.GetDocument(ctx, core.KnowledgeDocumentID(documentID))
	if err != nil {
		return KnowledgeDocumentDTO{}, err
	}
	if doc.KnowledgeBaseID != core.KnowledgeBaseID(knowledgeBaseID) {
		return KnowledgeDocumentDTO{}, core.ErrNotFound
	}
	return mapDocument(doc), nil
}

func (s *QueryService) ListBindings(ctx context.Context, viewer ViewerRef, knowledgeBaseID string) (KnowledgeBindingPageDTO, error) {
	if err := requirePermission(viewer, PermissionKnowledgeRead); err != nil {
		return KnowledgeBindingPageDTO{}, err
	}
	if s == nil || s.repo == nil {
		return KnowledgeBindingPageDTO{}, ErrServiceDependency
	}
	if err := s.requireBaseRead(ctx, viewer, core.KnowledgeBaseID(knowledgeBaseID)); err != nil {
		return KnowledgeBindingPageDTO{}, err
	}
	bindings, err := s.repo.ListBindings(ctx, core.BindingQuery{KnowledgeBaseID: core.KnowledgeBaseID(knowledgeBaseID)})
	if err != nil {
		return KnowledgeBindingPageDTO{}, err
	}
	items := make([]KnowledgeBindingDTO, 0, len(bindings))
	for _, binding := range bindings {
		items = append(items, mapBinding(binding))
	}
	return KnowledgeBindingPageDTO{Items: items, Total: len(items)}, nil
}

func (s *QueryService) ListJobs(ctx context.Context, viewer ViewerRef, knowledgeBaseID string) (KnowledgeJobPageDTO, error) {
	if err := requirePermission(viewer, PermissionKnowledgeRead); err != nil {
		return KnowledgeJobPageDTO{}, err
	}
	if s == nil || s.repo == nil {
		return KnowledgeJobPageDTO{}, ErrServiceDependency
	}
	if err := s.requireBaseRead(ctx, viewer, core.KnowledgeBaseID(knowledgeBaseID)); err != nil {
		return KnowledgeJobPageDTO{}, err
	}
	jobs, err := s.repo.ListIngestJobs(ctx, core.JobQuery{KnowledgeBaseID: core.KnowledgeBaseID(knowledgeBaseID)})
	if err != nil {
		return KnowledgeJobPageDTO{}, err
	}
	items := make([]KnowledgeJobDTO, 0, len(jobs))
	for _, job := range jobs {
		items = append(items, mapJob(job))
	}
	return KnowledgeJobPageDTO{Items: items, Total: len(items)}, nil
}

func (s *QueryService) GetKnowledgeJob(ctx context.Context, viewer ViewerRef, id string) (KnowledgeJobDTO, error) {
	if err := requirePermission(viewer, PermissionKnowledgeRead); err != nil {
		return KnowledgeJobDTO{}, err
	}
	if s == nil || s.repo == nil {
		return KnowledgeJobDTO{}, ErrServiceDependency
	}
	job, err := s.repo.GetIngestJob(ctx, core.KnowledgeJobID(id))
	if err != nil {
		return KnowledgeJobDTO{}, err
	}
	if !s.canReadBaseID(ctx, viewer, job.KnowledgeBaseID) {
		return KnowledgeJobDTO{}, core.ErrNotFound
	}
	return mapJob(job), nil
}

func (s *QueryService) requireBaseRead(ctx context.Context, viewer ViewerRef, id core.KnowledgeBaseID) error {
	if s.canReadBaseID(ctx, viewer, id) {
		return nil
	}
	return core.ErrNotFound
}

func (s *QueryService) canReadBaseID(ctx context.Context, viewer ViewerRef, id core.KnowledgeBaseID) bool {
	base, err := s.repo.GetKnowledgeBase(ctx, id)
	if err != nil {
		return false
	}
	return s.canReadBase(ctx, viewer, base)
}

func (s *QueryService) canReadBase(ctx context.Context, viewer ViewerRef, base core.KnowledgeBase) bool {
	if s == nil || s.policy == nil {
		return false
	}
	return s.policy.CanRead(ctx, viewer.AccessSubject(), base)
}
