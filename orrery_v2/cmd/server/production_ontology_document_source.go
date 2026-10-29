package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/knowledgeinstances"
	"orrery_v2/internal/app/knowledgeontology"
	"orrery_v2/internal/app/ontologyscope"
	knowledgecore "orrery_v2/internal/knowledge/core"
	"orrery_v2/internal/knowledge/provider"
	knowledgestore "orrery_v2/internal/knowledge/store"
	"orrery_v2/internal/platform/db"
)

type ontologyDocumentSource interface {
	Load(context.Context, identity.Principal, knowledgeinstances.Watch) (knowledgeinstances.Source, error)
	Fence(context.Context, db.Tx, knowledgeinstances.Source) error
}

type productionOntologyDocumentSource struct {
	api       *productionOntologyInferenceAPI
	core      *db.Store
	repo      knowledgestore.Repository
	providers provider.Registry
}

func (s *productionOntologyDocumentSource) Load(ctx context.Context, p identity.Principal, w knowledgeinstances.Watch) (knowledgeinstances.Source, error) {
	var result knowledgeinstances.Source
	// Check visibility before reading any provider content or local source IDs.
	object, err := s.api.context.GetObject(ctx, p, w.TenantID, w.RootID)
	if err != nil {
		return result, err
	}
	if object.ObjectType != "knowledge_document" {
		return result, knowledgeinstances.ErrInvalid
	}
	kbID, _ := object.Properties["knowledge_base_id"].(string)
	docID, _ := object.Properties["provider_document_id"].(string)
	if kbID == "" || docID == "" {
		return result, knowledgeinstances.ErrInvalid
	}
	bridge := knowledgeontology.NewPlatformStore(s.core)
	binding, err := bridge.GetBinding(ctx, w.TenantID, kbID)
	if err != nil {
		return result, err
	}
	if !binding.Enabled || binding.SourceAccessRef.Kind == "" {
		return result, ontologyscope.ErrAccessDenied
	}
	var owned int
	if err := s.core.QueryRow(ctx, `SELECT COUNT(*) FROM context_graph_objects WHERE tenant_id=? AND id=? AND object_type='knowledge_document' AND owner_access_kind=? AND owner_access_id=? AND owner_access_tenant_id=? AND access_state='resolved'`, w.TenantID, w.RootID, binding.SourceAccessRef.Kind, binding.SourceAccessRef.ID, binding.SourceAccessRef.TenantID).Scan(&owned); err != nil {
		return result, err
	}
	if owned != 1 {
		return result, ontologyscope.ErrAccessDenied
	}
	projection, err := bridge.GetLatestProjection(ctx, w.TenantID, docID)
	if err != nil {
		return result, err
	}
	if projection.KnowledgeBaseID != kbID || projection.DocumentID != docID || projection.Revision < 1 || projection.KnowledgeDocumentObjectID != w.RootID {
		return result, knowledgeinstances.ErrConflict
	}
	base, err := s.repo.GetKnowledgeBase(ctx, knowledgecore.KnowledgeBaseID(kbID))
	if err != nil {
		return result, err
	}
	doc, err := s.repo.GetDocument(ctx, knowledgecore.KnowledgeDocumentID(w.RootID))
	if err != nil {
		return result, err
	}
	if base.Status != knowledgecore.KnowledgeActive || string(doc.KnowledgeBaseID) != kbID || doc.ProviderDocumentID != docID || base.ProviderResourceID != binding.FolderID || doc.Status != knowledgecore.DocumentIndexed {
		return result, knowledgeinstances.ErrConflict
	}
	revisions, ok := s.providers.ForBase(base).(provider.RevisionProvider)
	if !ok {
		return result, fmt.Errorf("published document revision provider is unavailable")
	}
	revision, source, err := loadOntologyDocumentRevision(ctx, revisions, base, doc, projection.Revision)
	if err != nil {
		return result, err
	}
	if revision.DocumentID != docID || revision.Revision != projection.Revision || revision.FolderID != binding.FolderID || revision.PublicationStatus != "published" ||
		revision.ContentHash != projection.ContentHash || source.DocumentID != docID || source.Revision != projection.Revision || source.ContentHash != revision.ContentHash || source.ContentType != "text/markdown" {
		return result, knowledgeinstances.ErrConflict
	}
	digest := sha256.Sum256([]byte(source.Content))
	if strings.TrimPrefix(source.ContentHash, "sha256:") != hex.EncodeToString(digest[:]) {
		return result, fmt.Errorf("%w: source bytes do not match revision hash", knowledgeinstances.ErrInvalid)
	}
	result = knowledgeinstances.Source{TenantID: w.TenantID, RootID: w.RootID, KnowledgeBaseID: kbID, FolderID: binding.FolderID, DocumentID: docID, Revision: projection.Revision, ContentHash: projection.ContentHash, Content: source.Content, Access: binding.SourceAccessRef}
	head, err := bridge.SourceHead(ctx, binding, docID)
	if err != nil {
		return result, err
	}
	if head.Generation > 0 {
		if head.Status != "published" || head.Revision != result.Revision || head.ContentHash != result.ContentHash || head.RootID != result.RootID || head.Access != result.Access {
			return result, knowledgeinstances.ErrConflict
		}
		result.Generation = head.Generation
	}
	return result, knowledgeinstances.ValidateSource(result)
}

// The acceptance boundary is the latest locally attested bridge revision.
// Provider I/O stays outside the transaction; source sync advances this head.
func (s *productionOntologyDocumentSource) Fence(ctx context.Context, tx db.Tx, source knowledgeinstances.Source) error {
	if s.core.Dialect() == db.DialectPostgres {
		if _, err := tx.ExecContext(ctx, `LOCK TABLE knowledge_ontology_bindings,knowledge_ontology_projections IN SHARE ROW EXCLUSIVE MODE`); err != nil {
			return err
		}
	}
	var kb, hash, payload string
	var revision int
	err := tx.QueryRowContext(ctx, `SELECT knowledge_base_id,revision,content_hash,payload FROM knowledge_ontology_projections WHERE tenant_id=? AND document_id=? ORDER BY revision DESC LIMIT 1`, source.TenantID, source.DocumentID).Scan(&kb, &revision, &hash, &payload)
	if err != nil || kb != source.KnowledgeBaseID || revision != source.Revision || hash != source.ContentHash {
		return knowledgeinstances.ErrConflict
	}
	var projection knowledgeontology.Projection
	if err := json.Unmarshal([]byte(payload), &projection); err != nil {
		return err
	}
	if projection.KnowledgeDocumentObjectID != source.RootID || projection.TenantID != source.TenantID || projection.KnowledgeBaseID != source.KnowledgeBaseID || projection.DocumentID != source.DocumentID || projection.Revision != source.Revision || projection.ContentHash != source.ContentHash {
		return knowledgeinstances.ErrConflict
	}
	var enabled bool
	var kind, id, tenant, folder string
	err = tx.QueryRowContext(ctx, `SELECT enabled,source_access_kind,source_access_id,source_access_tenant_id,folder_id FROM knowledge_ontology_bindings WHERE tenant_id=? AND knowledge_base_id=?`, source.TenantID, kb).Scan(&enabled, &kind, &id, &tenant, &folder)
	if err != nil || !enabled || kind != source.Access.Kind || id != source.Access.ID || tenant != source.Access.TenantID || folder != source.FolderID {
		return knowledgeinstances.ErrConflict
	}
	var headJSON string
	err = tx.QueryRowContext(ctx, `SELECT body_json FROM knowledge_ontology_source_heads WHERE tenant_id=? AND knowledge_base_id=? AND document_id=?`, source.TenantID, source.KnowledgeBaseID, source.DocumentID).Scan(&headJSON)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		var head knowledgeontology.SourceHead
		if err := json.Unmarshal([]byte(headJSON), &head); err != nil {
			return err
		}
		if head.Status != "published" || head.Generation != source.Generation || head.RootID != source.RootID || head.Revision != source.Revision || head.ContentHash != source.ContentHash || head.Access != source.Access {
			return knowledgeinstances.ErrConflict
		}
	} else if source.Generation != 0 {
		return knowledgeinstances.ErrConflict
	}
	return nil
}

// Indexed PageIndex documents expose an atomic published snapshot even when
// their provider has no editable semantic revision history yet.
func loadOntologyDocumentRevision(ctx context.Context, revisions provider.RevisionProvider, base knowledgecore.KnowledgeBase, doc knowledgecore.KnowledgeDocument, number int) (provider.DocumentRevision, provider.SemanticSource, error) {
	revision, err := revisions.GetDocumentRevision(ctx, provider.DocumentRevisionRequest{DocumentID: doc.ProviderDocumentID, Revision: number})
	if err != nil {
		if indexed, ok := revisions.(provider.IndexedDocumentSourceProvider); ok {
			snapshot, snapshotErr := indexed.GetIndexedDocumentSource(ctx, provider.IndexedDocumentSourceRequest{DocumentID: doc.ProviderDocumentID})
			if snapshotErr == nil && snapshot.Revision.Revision == number {
				return snapshot.Revision, snapshot.Source, nil
			}
		}
		return revision, provider.SemanticSource{}, err
	}
	var source provider.SemanticSource
	if scoped, ok := revisions.(provider.ScopedSemanticSourceProvider); ok {
		source, err = scoped.GetSemanticSourceForDocument(ctx, base, doc, number)
	} else {
		source, err = revisions.GetSemanticSource(ctx, provider.SemanticSourceRequest{DocumentID: doc.ProviderDocumentID, Revision: number})
	}
	return revision, source, err
}
