package main

import (
	"context"
	"database/sql"
	"errors"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/knowledgeinstances"
	"orrery_v2/internal/app/knowledgeontology"
	"orrery_v2/internal/app/ontologyreasoning"
	"orrery_v2/internal/app/ontologyscope"
	contextview "orrery_v2/internal/app/ontologyview/context"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
	knowledgecore "orrery_v2/internal/knowledge/core"
)

type groundedSourceReader interface {
	CheckGroundingSources(context.Context, identity.Principal, string, []knowledgeinstances.AssertionSourceQuery) (map[string]ontologyreasoning.GroundedSource, error)
}

// CheckGroundingSources verifies locally attested publication heads and current
// source visibility. No provider text is copied into a new retrieval index.
func (s *productionOntologyDocumentSource) CheckGroundingSources(ctx context.Context, p identity.Principal, tenant string, assertions []knowledgeinstances.AssertionSourceQuery) (map[string]ontologyreasoning.GroundedSource, error) {
	sources, err := (&knowledgeinstances.Store{Core: s.core}).ReadAssertionSources(ctx, tenant, assertions)
	if err != nil {
		return nil, err
	}
	out := map[string]ontologyreasoning.GroundedSource{}
	checked := map[string]bool{}
	current := map[string]string{}
	for ref, source := range sources {
		key := source.Stamp()
		if !checked[key] {
			if len(checked) >= 32 {
				return nil, ontologycontract.ErrQueryBudgetExceeded
			}
			checked[key] = true
			stamp, err := s.checkGroundingSource(ctx, p, source)
			if err != nil {
				return nil, err
			}
			current[key] = stamp
		}
		if current[key] == "" {
			continue
		}
		out[ref] = ontologyreasoning.GroundedSource{RootID: source.RootID, DocumentID: source.DocumentID, Revision: source.Revision, ContentHash: source.ContentHash, Stamp: current[key]}
	}
	return out, nil
}

func (s *productionOntologyDocumentSource) checkGroundingSource(ctx context.Context, p identity.Principal, expected knowledgeinstances.Source) (string, error) {
	object, err := s.api.context.GetObject(ctx, p, expected.TenantID, expected.RootID)
	if errors.Is(err, contextview.ErrUnavailable) || errors.Is(err, ontologyscope.ErrAccessDenied) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if object.Redacted || object.ObjectType != "knowledge_document" || object.Properties["knowledge_base_id"] != expected.KnowledgeBaseID || object.Properties["provider_document_id"] != expected.DocumentID {
		return "", nil
	}
	bridge := knowledgeontology.NewPlatformStore(s.core)
	binding, err := bridge.GetBinding(ctx, expected.TenantID, expected.KnowledgeBaseID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !binding.Enabled || binding.FolderID != expected.FolderID || binding.SourceAccessRef != expected.Access {
		return "", nil
	}
	base, err := s.repo.GetKnowledgeBase(ctx, knowledgecore.KnowledgeBaseID(expected.KnowledgeBaseID))
	if errors.Is(err, knowledgecore.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	doc, err := s.repo.GetDocument(ctx, knowledgecore.KnowledgeDocumentID(expected.RootID))
	if errors.Is(err, knowledgecore.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if base.Status != knowledgecore.KnowledgeActive || base.ProviderResourceID != expected.FolderID || string(doc.KnowledgeBaseID) != expected.KnowledgeBaseID || doc.ProviderDocumentID != expected.DocumentID || doc.Status != knowledgecore.DocumentIndexed {
		return "", nil
	}
	projection, err := bridge.GetLatestProjection(ctx, expected.TenantID, expected.DocumentID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if projection.KnowledgeBaseID != expected.KnowledgeBaseID || projection.KnowledgeDocumentObjectID != expected.RootID || projection.Revision != expected.Revision || projection.ContentHash != expected.ContentHash {
		return "", nil
	}
	head, err := bridge.SourceHead(ctx, binding, expected.DocumentID)
	if err != nil {
		return "", err
	}
	// Unattested legacy generations stay material until normal extraction has
	// bound them to a current head. Republish cannot revive an old proof.
	if head.Generation < 1 || head.Generation != expected.Generation || head.Status != "published" || head.RootID != expected.RootID || head.Revision != expected.Revision || head.ContentHash != expected.ContentHash || head.Access != expected.Access {
		return "", nil
	}
	return inferenceDigest([]any{expected.Stamp(), binding, base, doc, projection, head}), nil
}
