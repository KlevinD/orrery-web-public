package main

import (
	"context"
	"fmt"
	"strings"

	"orrery_v2/internal/app/knowledgeadmin"
	"orrery_v2/internal/app/knowledgeontology"
	knowledgecore "orrery_v2/internal/knowledge/core"
	"orrery_v2/internal/knowledge/provider"
	knowledgestore "orrery_v2/internal/knowledge/store"
)

type productionKnowledgeWorkspace struct {
	query    *knowledgeadmin.QueryService
	repo     knowledgestore.Repository
	registry provider.Registry
	bridge   *knowledgeontology.PlatformStore
}

func (s productionKnowledgeWorkspace) GetSource(ctx context.Context, viewer knowledgeadmin.ViewerRef, knowledgeBaseID, documentID string, revision int) (provider.SemanticSource, error) {
	base, doc, revisionProvider, _, err := s.resolve(ctx, viewer, knowledgeBaseID, documentID)
	if err != nil {
		return provider.SemanticSource{}, err
	}
	if scoped, ok := revisionProvider.(provider.ScopedSemanticSourceProvider); ok {
		ctx = provider.WithRequestMetadata(ctx, provider.RequestMetadata{RequestID: viewer.RequestID, Actor: "viewer:" + viewer.ViewerID})
		return scoped.GetSemanticSourceForDocument(ctx, base, doc, revision)
	}
	if revision < 1 {
		revisions, err := revisionProvider.ListDocumentRevisions(ctx, provider.DocumentRevisionsRequest{DocumentID: doc.ProviderDocumentID})
		if err != nil {
			return provider.SemanticSource{}, err
		}
		for _, item := range revisions {
			if item.Revision > revision {
				revision = item.Revision
			}
		}
	}
	return revisionProvider.GetSemanticSource(ctx, provider.SemanticSourceRequest{DocumentID: doc.ProviderDocumentID, Revision: revision})
}

func (s productionKnowledgeWorkspace) UpdateSource(ctx context.Context, viewer knowledgeadmin.ViewerRef, knowledgeBaseID, documentID string, expectedRevision int, content string) (provider.DocumentRevision, error) {
	if !knowledgeWorkspaceCanWrite(viewer) {
		return provider.DocumentRevision{}, knowledgeadmin.ErrPermissionDenied
	}
	base, doc, revisionProvider, _, err := s.resolve(ctx, viewer, knowledgeBaseID, documentID)
	if err != nil {
		return provider.DocumentRevision{}, err
	}
	if !knowledgeadmin.NewAccessPolicy(s.repo).CanWrite(ctx, viewer.AccessSubject(), base) {
		return provider.DocumentRevision{}, knowledgeadmin.ErrPermissionDenied
	}
	return revisionProvider.UpdateDocumentContent(ctx, provider.UpdateDocumentContentRequest{
		DocumentID: doc.ProviderDocumentID, ExpectedRevision: expectedRevision,
		Content: content, ContentType: "text/markdown", CreatedBy: viewer.ViewerID,
	})
}

func (s productionKnowledgeWorkspace) ListRevisions(ctx context.Context, viewer knowledgeadmin.ViewerRef, knowledgeBaseID, documentID string) ([]provider.DocumentRevision, error) {
	_, doc, revisionProvider, _, err := s.resolve(ctx, viewer, knowledgeBaseID, documentID)
	if err != nil {
		return nil, err
	}
	return revisionProvider.ListDocumentRevisions(ctx, provider.DocumentRevisionsRequest{DocumentID: doc.ProviderDocumentID})
}

func (s productionKnowledgeWorkspace) PublishRevision(ctx context.Context, viewer knowledgeadmin.ViewerRef, knowledgeBaseID, documentID string, revision int) (provider.DocumentRevision, error) {
	if !knowledgeWorkspaceCanWrite(viewer) {
		return provider.DocumentRevision{}, knowledgeadmin.ErrPermissionDenied
	}
	base, doc, revisionProvider, _, err := s.resolve(ctx, viewer, knowledgeBaseID, documentID)
	if err != nil {
		return provider.DocumentRevision{}, err
	}
	if !knowledgeadmin.NewAccessPolicy(s.repo).CanWrite(ctx, viewer.AccessSubject(), base) {
		return provider.DocumentRevision{}, knowledgeadmin.ErrPermissionDenied
	}
	return revisionProvider.PublishDocumentRevision(ctx, provider.PublishDocumentRevisionRequest{DocumentID: doc.ProviderDocumentID, Revision: revision, PublishedBy: viewer.ViewerID})
}

func (s productionKnowledgeWorkspace) GetOntologyProjection(ctx context.Context, viewer knowledgeadmin.ViewerRef, knowledgeBaseID, documentID string, revision int) (knowledgeontology.Projection, error) {
	_, doc, _, tenantID, err := s.resolve(ctx, viewer, knowledgeBaseID, documentID)
	if err != nil {
		return knowledgeontology.Projection{}, err
	}
	if revision > 0 {
		return s.bridge.GetProjection(ctx, tenantID, doc.ProviderDocumentID, revision)
	}
	return s.bridge.GetLatestProjection(ctx, tenantID, doc.ProviderDocumentID)
}

func (s productionKnowledgeWorkspace) ListDocumentRelations(ctx context.Context, viewer knowledgeadmin.ViewerRef, knowledgeBaseID, documentID, direction string, limit int) (knowledgeadmin.KnowledgeDocumentRelationPageDTO, error) {
	base, doc, _, tenantID, err := s.resolve(ctx, viewer, knowledgeBaseID, documentID)
	if err != nil {
		return knowledgeadmin.KnowledgeDocumentRelationPageDTO{}, err
	}
	relations, err := s.bridge.ListDocumentRelations(ctx, tenantID, string(base.ID), string(doc.ID), knowledgeontology.DocumentRelationDirection(direction), limit)
	if err != nil {
		return knowledgeadmin.KnowledgeDocumentRelationPageDTO{}, err
	}
	items := make([]knowledgeadmin.KnowledgeDocumentRelationDTO, 0, len(relations))
	for _, relation := range relations {
		other, err := s.query.GetDocument(ctx, viewer, knowledgeBaseID, relation.OtherDocumentID)
		if err != nil || other.KnowledgeBaseID != knowledgeBaseID {
			// A relation may point at an object that is no longer a document in
			// this base. Do not expose the object or its relation metadata.
			continue
		}
		items = append(items, knowledgeadmin.KnowledgeDocumentRelationDTO{
			Direction:        string(relation.Direction),
			LinkType:         relation.LinkType,
			SourceDocumentID: relation.SourceDocumentID,
			Revision:         relation.Revision,
			Anchor:           relation.Anchor,
			Document:         other,
		})
	}
	return knowledgeadmin.KnowledgeDocumentRelationPageDTO{Items: items}, nil
}

func (s productionKnowledgeWorkspace) resolve(ctx context.Context, viewer knowledgeadmin.ViewerRef, knowledgeBaseID, documentID string) (knowledgecore.KnowledgeBase, knowledgecore.KnowledgeDocument, provider.RevisionProvider, string, error) {
	if s.query == nil || s.repo == nil || s.registry == nil || s.bridge == nil {
		return knowledgecore.KnowledgeBase{}, knowledgecore.KnowledgeDocument{}, nil, "", knowledgeadmin.ErrServiceDependency
	}
	dto, err := s.query.GetDocument(ctx, viewer, knowledgeBaseID, documentID)
	if err != nil {
		return knowledgecore.KnowledgeBase{}, knowledgecore.KnowledgeDocument{}, nil, "", err
	}
	doc, err := s.repo.GetDocument(ctx, knowledgecore.KnowledgeDocumentID(dto.ID))
	if err != nil {
		return knowledgecore.KnowledgeBase{}, knowledgecore.KnowledgeDocument{}, nil, "", err
	}
	base, err := s.repo.GetKnowledgeBase(ctx, doc.KnowledgeBaseID)
	if err != nil {
		return knowledgecore.KnowledgeBase{}, knowledgecore.KnowledgeDocument{}, nil, "", err
	}
	revisionProvider, ok := s.registry.ForBase(base).(provider.RevisionProvider)
	if !ok || revisionProvider == nil || strings.TrimSpace(doc.ProviderDocumentID) == "" {
		return knowledgecore.KnowledgeBase{}, knowledgecore.KnowledgeDocument{}, nil, "", fmt.Errorf("%w: PageIndex revision provider is required", knowledgeadmin.ErrServiceDependency)
	}
	tenantID := strings.TrimSpace(base.OwnerID)
	if tenantID == "" {
		tenantID = "default"
	}
	return base, doc, revisionProvider, tenantID, nil
}

func knowledgeWorkspaceCanWrite(viewer knowledgeadmin.ViewerRef) bool {
	for _, permission := range viewer.PermissionScope {
		if permission == knowledgeadmin.PermissionKnowledgeWrite || permission == knowledgeadmin.PermissionKnowledgeManage {
			return true
		}
	}
	return false
}
