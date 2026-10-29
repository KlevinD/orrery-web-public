package knowledgeontology

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/platform/db"
)

type PlatformStore struct {
	store              *db.Store
	withdrawSource     SourceWithdrawalHandler
	sourceCatalog      SourceCatalog
	catalogMu          sync.Mutex
	catalogInitialized map[string]bool
}

func NewPlatformStore(store *db.Store) *PlatformStore {
	return &PlatformStore{store: store}
}

func (s *PlatformStore) SaveBinding(ctx context.Context, binding Binding) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("knowledge ontology store is required")
	}
	binding = normalizeBinding(binding)
	if binding.TenantID == "" || binding.KnowledgeBaseID == "" || binding.FolderID == "" {
		return fmt.Errorf("tenant, knowledge base, and folder are required")
	}
	_, err := s.store.Exec(ctx, `
INSERT INTO knowledge_ontology_bindings (
	tenant_id, knowledge_base_id, folder_id,
	source_access_kind, source_access_id, source_access_tenant_id,
	enabled, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(tenant_id, knowledge_base_id) DO UPDATE SET
	folder_id = excluded.folder_id,
	source_access_kind = excluded.source_access_kind,
	source_access_id = excluded.source_access_id,
	source_access_tenant_id = excluded.source_access_tenant_id,
	enabled = excluded.enabled,
	updated_at = excluded.updated_at`,
		binding.TenantID, binding.KnowledgeBaseID, binding.FolderID,
		binding.SourceAccessRef.Kind, binding.SourceAccessRef.ID, binding.SourceAccessRef.TenantID,
		boolInt(binding.Enabled), binding.UpdatedAt.UnixNano(),
	)
	return err
}

// ReconcileOwnedBinding makes the knowledge-base owner the only ontology
// tenant for the base. Cross-tenant read access remains represented by
// knowledge bindings and must not create duplicate ontology projections.
func (s *PlatformStore) ReconcileOwnedBinding(ctx context.Context, binding Binding) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("knowledge ontology store is required")
	}
	binding = normalizeBinding(binding)
	if binding.TenantID == "" || binding.KnowledgeBaseID == "" || binding.FolderID == "" {
		return fmt.Errorf("tenant, knowledge base, and folder are required")
	}
	return s.store.WithTx(ctx, func(tx db.Tx) error {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO knowledge_ontology_bindings (
	tenant_id, knowledge_base_id, folder_id,
	source_access_kind, source_access_id, source_access_tenant_id,
	enabled, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(tenant_id, knowledge_base_id) DO UPDATE SET
	folder_id = excluded.folder_id,
	source_access_kind = excluded.source_access_kind,
	source_access_id = excluded.source_access_id,
	source_access_tenant_id = excluded.source_access_tenant_id,
	enabled = excluded.enabled,
	updated_at = excluded.updated_at`,
			binding.TenantID, binding.KnowledgeBaseID, binding.FolderID,
			binding.SourceAccessRef.Kind, binding.SourceAccessRef.ID, binding.SourceAccessRef.TenantID,
			boolInt(binding.Enabled), binding.UpdatedAt.UnixNano(),
		); err != nil {
			return err
		}
		for _, table := range []string{
			"knowledge_ontology_mentions",
			"knowledge_ontology_claims",
			"knowledge_ontology_dictionary_versions",
		} {
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE knowledge_base_id = ? AND tenant_id <> ?`, binding.KnowledgeBaseID, binding.TenantID); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM knowledge_ontology_bindings WHERE knowledge_base_id = ? AND tenant_id <> ?`, binding.KnowledgeBaseID, binding.TenantID)
		return err
	})
}

func (s *PlatformStore) GetBinding(ctx context.Context, tenantID, knowledgeBaseID string) (Binding, error) {
	if s == nil || s.store == nil {
		return Binding{}, fmt.Errorf("knowledge ontology store is required")
	}
	return scanBinding(s.store.QueryRow(ctx, `
SELECT tenant_id, knowledge_base_id, folder_id,
	source_access_kind, source_access_id, source_access_tenant_id,
	enabled, updated_at
FROM knowledge_ontology_bindings
WHERE tenant_id = ? AND knowledge_base_id = ?`, strings.TrimSpace(tenantID), strings.TrimSpace(knowledgeBaseID)))
}

func (s *PlatformStore) ListBindings(ctx context.Context, tenantID string) ([]Binding, error) {
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("knowledge ontology store is required")
	}
	rows, err := s.store.Query(ctx, `
SELECT tenant_id, knowledge_base_id, folder_id,
	source_access_kind, source_access_id, source_access_tenant_id,
	enabled, updated_at
FROM knowledge_ontology_bindings
WHERE tenant_id = ? ORDER BY knowledge_base_id`, strings.TrimSpace(tenantID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	bindings := []Binding{}
	for rows.Next() {
		binding, err := scanBinding(rows)
		if err != nil {
			return nil, err
		}
		bindings = append(bindings, binding)
	}
	return bindings, rows.Err()
}

func (s *PlatformStore) ListEnabledBindings(ctx context.Context) ([]Binding, error) {
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("knowledge ontology store is required")
	}
	rows, err := s.store.Query(ctx, `
SELECT tenant_id, knowledge_base_id, folder_id,
	source_access_kind, source_access_id, source_access_tenant_id,
	enabled, updated_at
FROM knowledge_ontology_bindings
WHERE enabled = 1 ORDER BY tenant_id, knowledge_base_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	bindings := []Binding{}
	for rows.Next() {
		binding, err := scanBinding(rows)
		if err != nil {
			return nil, err
		}
		bindings = append(bindings, binding)
	}
	return bindings, rows.Err()
}

func (s *PlatformStore) Cursor(ctx context.Context, binding Binding) (string, error) {
	if s == nil || s.store == nil {
		return "", fmt.Errorf("knowledge ontology store is required")
	}
	var cursor string
	err := s.store.QueryRow(ctx, `
SELECT cursor FROM knowledge_ontology_cursors
WHERE tenant_id = ? AND knowledge_base_id = ?`, binding.TenantID, binding.KnowledgeBaseID).Scan(&cursor)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return cursor, err
}

func (s *PlatformStore) AdvanceCursor(ctx context.Context, binding Binding, cursor string) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("knowledge ontology store is required")
	}
	return s.store.WithTx(ctx, func(tx db.Tx) error {
		if s.withdrawSource != nil {
			if err := s.lifecycleFence(ctx, tx, binding); err != nil {
				return err
			}
		}
		return upsertCursor(ctx, tx, binding, cursor, time.Now().UTC())
	})
}

func (s *PlatformStore) OntologyDictionaryVersion(ctx context.Context, binding Binding) (string, error) {
	if s == nil || s.store == nil {
		return "", fmt.Errorf("knowledge ontology store is required")
	}
	var version string
	err := s.store.QueryRow(ctx, `
SELECT dictionary_version FROM knowledge_ontology_dictionary_versions
WHERE tenant_id = ? AND knowledge_base_id = ?`, binding.TenantID, binding.KnowledgeBaseID).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return version, err
}

func (s *PlatformStore) SaveOntologyDictionaryVersion(ctx context.Context, binding Binding, version string) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("knowledge ontology store is required")
	}
	_, err := s.store.Exec(ctx, `
INSERT INTO knowledge_ontology_dictionary_versions (tenant_id, knowledge_base_id, dictionary_version, updated_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(tenant_id, knowledge_base_id) DO UPDATE SET
	dictionary_version = excluded.dictionary_version,
	updated_at = excluded.updated_at`, binding.TenantID, binding.KnowledgeBaseID, version, time.Now().UTC().UnixNano())
	return err
}

func (s *PlatformStore) SaveProjectionAndCursor(ctx context.Context, binding Binding, projection Projection, cursor string) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("knowledge ontology store is required")
	}
	if projection.TenantID != binding.TenantID || projection.KnowledgeBaseID != binding.KnowledgeBaseID || projection.DocumentID == "" || projection.Revision < 1 {
		return fmt.Errorf("projection does not match binding")
	}
	if s.withdrawSource != nil {
		return s.applyPublishedProjection(ctx, binding, projection, cursor, nil, 0, nil)
	}
	now := time.Now().UTC()
	return s.store.WithTx(ctx, func(tx db.Tx) error {
		return saveProjectionTx(ctx, tx, binding, projection, cursor, now)
	})
}

func saveProjectionTx(ctx context.Context, tx db.Tx, binding Binding, projection Projection, cursor string, now time.Time) error {
	payload, err := json.Marshal(projection)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO knowledge_ontology_projections (
	tenant_id, knowledge_base_id, document_id, revision, content_hash, payload, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(tenant_id, document_id, revision) DO UPDATE SET
	knowledge_base_id = excluded.knowledge_base_id,
	content_hash = excluded.content_hash,
	payload = excluded.payload,
	updated_at = excluded.updated_at`,
		projection.TenantID, projection.KnowledgeBaseID, projection.DocumentID,
		projection.Revision, projection.ContentHash, string(payload), now.UnixNano(),
	); err != nil {
		return err
	}
	if err := replaceAssociations(ctx, tx, projection); err != nil {
		return err
	}
	return upsertCursor(ctx, tx, binding, cursor, now)
}

func (s *PlatformStore) DeleteDocument(ctx context.Context, binding Binding, documentID string) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("knowledge ontology store is required")
	}
	if s.withdrawSource != nil {
		return fmt.Errorf("source lifecycle requires a trusted tombstone or fenced complete inventory")
	}
	documentID = strings.TrimSpace(documentID)
	if strings.TrimSpace(binding.TenantID) == "" || strings.TrimSpace(binding.KnowledgeBaseID) == "" || documentID == "" {
		return fmt.Errorf("tenant, knowledge base, and document are required")
	}
	return s.store.WithTx(ctx, func(tx db.Tx) error {
		for _, table := range []string{"knowledge_ontology_mentions", "knowledge_ontology_claims", "knowledge_ontology_projections"} {
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE tenant_id = ? AND knowledge_base_id = ? AND document_id = ?`,
				binding.TenantID, binding.KnowledgeBaseID, documentID); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *PlatformStore) ListProjectedDocumentIDs(ctx context.Context, binding Binding) ([]string, error) {
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("knowledge ontology store is required")
	}
	rows, err := s.store.Query(ctx, `
SELECT DISTINCT document_id
FROM knowledge_ontology_projections
WHERE tenant_id = ? AND knowledge_base_id = ?
ORDER BY document_id`, binding.TenantID, binding.KnowledgeBaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	documentIDs := []string{}
	for rows.Next() {
		var documentID string
		if err := rows.Scan(&documentID); err != nil {
			return nil, err
		}
		documentIDs = append(documentIDs, documentID)
	}
	return documentIDs, rows.Err()
}

func replaceAssociations(ctx context.Context, tx db.Tx, projection Projection) error {
	for _, table := range []string{"knowledge_ontology_mentions", "knowledge_ontology_claims"} {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE tenant_id = ? AND document_id = ? AND revision = ?`,
			projection.TenantID, projection.DocumentID, projection.Revision); err != nil {
			return err
		}
	}
	for index, subject := range projection.Subjects {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO knowledge_ontology_mentions (
	tenant_id, knowledge_base_id, document_id, revision, mention_index,
	object_type, resolved_object_id, resolution, status, surface, anchor,
	extraction_method, confidence, observed_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			projection.TenantID, projection.KnowledgeBaseID, projection.DocumentID, projection.Revision, index,
			subject.Reference.Kind, subject.ObjectID, subject.Resolution, subject.Status, subject.Surface,
			subject.Evidence.Anchor, subject.Evidence.ExtractionMethod, subject.Confidence, subject.Evidence.ObservedAt.UnixNano(),
		); err != nil {
			return err
		}
	}
	for index, assertion := range projection.Assertions {
		confidence := 1.0
		if assertion.Status != AssertionConfirmed {
			confidence = 0
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO knowledge_ontology_claims (
	tenant_id, knowledge_base_id, document_id, revision, claim_index, link_type,
	from_object_type, from_object_id, to_object_type, to_object_id, status,
	anchor, extraction_method, confidence, observed_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			projection.TenantID, projection.KnowledgeBaseID, projection.DocumentID, projection.Revision, index, assertion.LinkType,
			assertion.From.Kind, assertion.FromObjectID, assertion.To.Kind, assertion.ToObjectID, assertion.Status,
			assertion.Evidence.Anchor, assertion.Evidence.ExtractionMethod, confidence, assertion.Evidence.ObservedAt.UnixNano(),
		); err != nil {
			return err
		}
	}
	return nil
}

// ListObjectAssociations navigates from visible ontology objects to the latest
// document revision that mentions them or makes a claim about them.
func (s *PlatformStore) ListObjectAssociations(ctx context.Context, tenantID string, refs []contextgraph.Ref, limit int) ([]KnowledgeAssociation, error) {
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("knowledge ontology store is required")
	}
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" || len(refs) == 0 {
		return []KnowledgeAssociation{}, nil
	}
	if limit <= 0 || limit > contextgraph.MaxQueryPageSize {
		return nil, fmt.Errorf("association limit must be between 1 and %d", contextgraph.MaxQueryPageSize)
	}
	kindByID := make(map[string]string, len(refs))
	ids := make([]string, 0, len(refs))
	for _, ref := range refs {
		id := strings.TrimSpace(ref.ID)
		if id == "" {
			continue
		}
		if _, exists := kindByID[id]; !exists {
			ids = append(ids, id)
		}
		kindByID[id] = strings.ToLower(strings.TrimSpace(ref.Kind))
	}
	if len(ids) == 0 {
		return []KnowledgeAssociation{}, nil
	}
	result := make([]KnowledgeAssociation, 0, limit)
	seen := make(map[string]struct{})
	for _, id := range ids {
		items, err := s.listSingleObjectAssociations(ctx, tenantID, ObjectRef{Kind: kindByID[id], ID: id}, limit)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			key := strings.Join([]string{string(item.Kind), item.LinkType, item.ObjectRef.ID, item.KnowledgeBaseID, item.DocumentID}, "\x1f")
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			result = append(result, item)
			if len(result) >= limit {
				return result, nil
			}
		}
	}
	return result, nil
}

func (s *PlatformStore) listSingleObjectAssociations(ctx context.Context, tenantID string, ref ObjectRef, limit int) ([]KnowledgeAssociation, error) {
	out := make([]KnowledgeAssociation, 0, limit)
	rows, err := s.store.Query(ctx, `
SELECT m.knowledge_base_id, m.document_id, m.revision, m.status, m.surface,
	m.anchor, m.extraction_method, m.confidence, m.object_type
FROM knowledge_ontology_mentions m
WHERE m.tenant_id = ? AND m.resolved_object_id = ? AND m.resolution = ?
	AND NOT EXISTS (
		SELECT 1 FROM knowledge_ontology_projections newer
		WHERE newer.tenant_id = m.tenant_id AND newer.document_id = m.document_id AND newer.revision > m.revision
	)
ORDER BY m.confidence DESC, m.revision DESC LIMIT ?`, tenantID, ref.ID, ResolutionResolved, limit)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		item := KnowledgeAssociation{Kind: AssociationMention, ObjectRef: ref}
		if err := rows.Scan(&item.KnowledgeBaseID, &item.DocumentID, &item.Revision, &item.Status, &item.Surface,
			&item.Anchor, &item.ExtractionMethod, &item.Confidence, &item.ObjectRef.Kind); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if len(out) >= limit {
		return out, nil
	}
	claimRows, err := s.store.Query(ctx, `
SELECT c.knowledge_base_id, c.document_id, c.revision, c.status, c.link_type, c.anchor,
	c.extraction_method, c.confidence, c.from_object_type, c.from_object_id,
	c.to_object_type, c.to_object_id
FROM knowledge_ontology_claims c
WHERE c.tenant_id = ? AND (c.from_object_id = ? OR c.to_object_id = ?)
	AND NOT EXISTS (
		SELECT 1 FROM knowledge_ontology_projections newer
		WHERE newer.tenant_id = c.tenant_id AND newer.document_id = c.document_id AND newer.revision > c.revision
	)
ORDER BY c.confidence DESC, c.revision DESC LIMIT ?`, tenantID, ref.ID, ref.ID, limit-len(out))
	if err != nil {
		return nil, err
	}
	defer claimRows.Close()
	for claimRows.Next() {
		var fromType, fromID, toType, toID string
		item := KnowledgeAssociation{ObjectRef: ref}
		if err := claimRows.Scan(&item.KnowledgeBaseID, &item.DocumentID, &item.Revision, &item.Status, &item.LinkType, &item.Anchor,
			&item.ExtractionMethod, &item.Confidence, &fromType, &fromID, &toType, &toID); err != nil {
			return nil, err
		}
		if fromID == ref.ID {
			item.Kind, item.ObjectRef.Kind = AssociationClaimSubject, fromType
		} else {
			item.Kind, item.ObjectRef.Kind = AssociationClaimObject, toType
		}
		out = append(out, item)
	}
	return out, claimRows.Err()
}

func (s *PlatformStore) GetProjection(ctx context.Context, tenantID, documentID string, revision int) (Projection, error) {
	if s == nil || s.store == nil {
		return Projection{}, fmt.Errorf("knowledge ontology store is required")
	}
	var payload string
	err := s.store.QueryRow(ctx, `
SELECT payload FROM knowledge_ontology_projections
WHERE tenant_id = ? AND document_id = ? AND revision = ?`, tenantID, documentID, revision).Scan(&payload)
	if err != nil {
		return Projection{}, err
	}
	var projection Projection
	if err := json.Unmarshal([]byte(payload), &projection); err != nil {
		return Projection{}, err
	}
	return projection, nil
}

func (s *PlatformStore) GetLatestProjection(ctx context.Context, tenantID, documentID string) (Projection, error) {
	if s == nil || s.store == nil {
		return Projection{}, fmt.Errorf("knowledge ontology store is required")
	}
	var payload string
	err := s.store.QueryRow(ctx, `
SELECT payload FROM knowledge_ontology_projections
WHERE tenant_id = ? AND document_id = ?
ORDER BY revision DESC LIMIT 1`, tenantID, documentID).Scan(&payload)
	if err != nil {
		return Projection{}, err
	}
	var projection Projection
	if err := json.Unmarshal([]byte(payload), &projection); err != nil {
		return Projection{}, err
	}
	return projection, nil
}

// ListDocumentRelations returns the latest confirmed document-to-document
// assertions for one knowledge base. The stored assertion remains directed;
// direction=both simply returns the matching edge with its relative direction
// from the requested document.
func (s *PlatformStore) ListDocumentRelations(ctx context.Context, tenantID, knowledgeBaseID, documentObjectID string, direction DocumentRelationDirection, limit int) ([]DocumentRelation, error) {
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("knowledge ontology store is required")
	}
	tenantID = strings.TrimSpace(tenantID)
	knowledgeBaseID = strings.TrimSpace(knowledgeBaseID)
	documentObjectID = strings.TrimSpace(documentObjectID)
	direction = normalizeDocumentRelationDirection(direction)
	if tenantID == "" || knowledgeBaseID == "" || documentObjectID == "" {
		return nil, fmt.Errorf("tenant, knowledge base, and document object are required")
	}
	if direction == "" {
		return nil, fmt.Errorf("invalid document relation direction")
	}
	if limit <= 0 || limit > contextgraph.MaxQueryPageSize {
		return nil, fmt.Errorf("document relation limit must be between 1 and %d", contextgraph.MaxQueryPageSize)
	}

	rows, err := s.store.Query(ctx, `
SELECT payload
FROM knowledge_ontology_projections p
WHERE p.tenant_id = ? AND p.knowledge_base_id = ?
	AND NOT EXISTS (
		SELECT 1 FROM knowledge_ontology_projections newer
		WHERE newer.tenant_id = p.tenant_id
		  AND newer.knowledge_base_id = p.knowledge_base_id
		  AND newer.document_id = p.document_id
		  AND newer.revision > p.revision
	)
ORDER BY p.document_id, p.revision DESC`, tenantID, knowledgeBaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	relations := make([]DocumentRelation, 0, limit)
	seen := make(map[string]struct{})
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var projection Projection
		if err := json.Unmarshal([]byte(payload), &projection); err != nil {
			return nil, err
		}
		for _, assertion := range projection.Assertions {
			if assertion.Status != AssertionConfirmed || assertion.LinkType != DocumentReferenceLinkType {
				continue
			}
			currentDirection, otherID := documentRelationDirection(assertion, documentObjectID)
			if currentDirection == "" || !documentRelationDirectionMatches(direction, currentDirection) || otherID == "" || otherID == documentObjectID {
				continue
			}
			key := strings.Join([]string{assertion.LinkType, assertion.FromObjectID, assertion.ToObjectID}, "\x1f")
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			relations = append(relations, DocumentRelation{
				LinkType: assertion.LinkType, Direction: currentDirection,
				OtherDocumentID: otherID, SourceDocumentID: projection.DocumentID,
				Revision: projection.Revision, Anchor: assertion.Evidence.Anchor,
			})
			if len(relations) >= limit {
				return relations, rows.Err()
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return relations, nil
}

func normalizeDocumentRelationDirection(direction DocumentRelationDirection) DocumentRelationDirection {
	switch DocumentRelationDirection(strings.ToLower(strings.TrimSpace(string(direction)))) {
	case "", DocumentRelationBoth:
		return DocumentRelationBoth
	case DocumentRelationIncoming:
		return DocumentRelationIncoming
	case DocumentRelationOutgoing:
		return DocumentRelationOutgoing
	default:
		return ""
	}
}

func documentRelationDirection(assertion AssertionProjection, documentObjectID string) (DocumentRelationDirection, string) {
	switch {
	case assertion.FromObjectID == documentObjectID:
		return DocumentRelationOutgoing, assertion.ToObjectID
	case assertion.ToObjectID == documentObjectID:
		return DocumentRelationIncoming, assertion.FromObjectID
	default:
		return "", ""
	}
}

func documentRelationDirectionMatches(requested, current DocumentRelationDirection) bool {
	return requested == DocumentRelationBoth || requested == current
}

func upsertCursor(ctx context.Context, tx db.Tx, binding Binding, cursor string, now time.Time) error {
	if cursor == "" {
		return nil
	}
	var previous string
	err := tx.QueryRowContext(ctx, `SELECT cursor FROM knowledge_ontology_cursors WHERE tenant_id=? AND knowledge_base_id=?`, binding.TenantID, binding.KnowledgeBaseID).Scan(&previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	currentNumber, currentErr := strconv.ParseInt(cursor, 10, 64)
	previousNumber, previousErr := strconv.ParseInt(previous, 10, 64)
	if currentErr == nil && previousErr == nil && currentNumber <= previousNumber {
		return nil
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO knowledge_ontology_cursors (tenant_id, knowledge_base_id, cursor, updated_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(tenant_id, knowledge_base_id) DO UPDATE SET
	cursor = excluded.cursor,
	updated_at = excluded.updated_at`, binding.TenantID, binding.KnowledgeBaseID, cursor, now.UnixNano())
	return err
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanBinding(row rowScanner) (Binding, error) {
	var binding Binding
	var enabled int
	var updatedAt int64
	err := row.Scan(
		&binding.TenantID, &binding.KnowledgeBaseID, &binding.FolderID,
		&binding.SourceAccessRef.Kind, &binding.SourceAccessRef.ID, &binding.SourceAccessRef.TenantID,
		&enabled, &updatedAt,
	)
	binding.Enabled = enabled != 0
	binding.UpdatedAt = time.Unix(0, updatedAt).UTC()
	return binding, err
}

func normalizeBinding(binding Binding) Binding {
	binding.TenantID = strings.TrimSpace(binding.TenantID)
	binding.KnowledgeBaseID = strings.TrimSpace(binding.KnowledgeBaseID)
	binding.FolderID = strings.TrimSpace(binding.FolderID)
	binding.SourceAccessRef = contextgraph.NormalizeAccessRef(binding.SourceAccessRef)
	if binding.UpdatedAt.IsZero() {
		binding.UpdatedAt = time.Now().UTC()
	}
	return binding
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
