package knowledgeontology

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/knowledge/provider"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

// SourceHead survives removal of the bridge projection. Generation fences a
// republished source even when the provider reuses its revision and content.
type SourceHead struct {
	TenantID        string                 `json:"tenant_id"`
	KnowledgeBaseID string                 `json:"knowledge_base_id"`
	FolderID        string                 `json:"folder_id"`
	DocumentID      string                 `json:"document_id"`
	RootID          string                 `json:"root_id"`
	Revision        int                    `json:"revision"`
	ContentHash     string                 `json:"content_hash"`
	Access          contextgraph.AccessRef `json:"access"`
	Generation      int64                  `json:"generation"`
	Sequence        int64                  `json:"sequence"`
	Status          string                 `json:"status"`
}

type SourceWithdrawal struct {
	ID            string                    `json:"id"`
	Source        SourceHead                `json:"source"`
	EventType     string                    `json:"event_type"`
	Sequence      int64                     `json:"sequence"`
	ObservedAt    time.Time                 `json:"observed_at"`
	ProviderEvent *provider.KnowledgeChange `json:"provider_event,omitempty"`
}

// The host connects the source attestation to the instance domain command.
// Both execute inside the bridge transaction; a partial withdrawal cannot commit.
type SourceWithdrawalHandler func(context.Context, db.Tx, SourceWithdrawal) error

type SourceCatalog func(context.Context, db.Tx, Binding) ([]SourceHead, error)

func (s *PlatformStore) WithSourceLifecycle(handler SourceWithdrawalHandler, catalog ...SourceCatalog) *PlatformStore {
	s.withdrawSource = handler
	if len(catalog) > 0 {
		s.sourceCatalog = catalog[0]
	}
	return s
}

// Recover historical source ownership before inspecting provider state. This
// includes claims whose old bridge projection was removed before this protocol.
func (s *PlatformStore) recoverSourceHeads(ctx context.Context, b Binding) error {
	if s.sourceCatalog == nil {
		return nil
	}
	s.catalogMu.Lock()
	defer s.catalogMu.Unlock()
	identity, _ := json.Marshal([]any{b.TenantID, b.KnowledgeBaseID, b.FolderID, b.SourceAccessRef})
	key := stableID("source-catalog", string(identity))
	if s.catalogInitialized[key] {
		return nil
	}
	err := s.store.WithTx(ctx, func(tx db.Tx) error {
		if err := s.lifecycleFence(ctx, tx, b); err != nil {
			return err
		}
		catalog, err := s.sourceCatalog(ctx, tx, b)
		if err != nil {
			return err
		}
		var cursor string
		err = tx.QueryRowContext(ctx, `SELECT cursor FROM knowledge_ontology_cursors WHERE tenant_id=? AND knowledge_base_id=?`, b.TenantID, b.KnowledgeBaseID).Scan(&cursor)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		sequence, _ := strconv.ParseInt(cursor, 10, 64)
		for _, h := range catalog {
			if h.TenantID != b.TenantID || h.KnowledgeBaseID != b.KnowledgeBaseID || h.DocumentID == "" || h.RootID == "" || h.Access != b.SourceAccessRef {
				return ErrRevisionMismatch
			}
			current, err := readSourceHead(ctx, tx, b, h.DocumentID)
			if err != nil {
				return err
			}
			if current.Generation > 0 {
				continue
			}
			var raw string
			err = tx.QueryRowContext(ctx, `SELECT payload FROM knowledge_ontology_projections WHERE tenant_id=? AND knowledge_base_id=? AND document_id=? ORDER BY revision DESC LIMIT 1`, b.TenantID, b.KnowledgeBaseID, h.DocumentID).Scan(&raw)
			if err == nil {
				var p Projection
				if err := json.Unmarshal([]byte(raw), &p); err != nil {
					return err
				}
				if p.KnowledgeDocumentObjectID != h.RootID {
					return ErrRevisionMismatch
				}
				h.Revision, h.ContentHash = p.Revision, p.ContentHash
			} else if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			h.FolderID, h.Status, h.Generation, h.Sequence = b.FolderID, "published", max(1, h.Generation), sequence
			if err := saveSourceHead(ctx, tx, h); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		if s.catalogInitialized == nil {
			s.catalogInitialized = map[string]bool{}
		}
		s.catalogInitialized[key] = true
	}
	return err
}

func (s *PlatformStore) liveSourceDocumentIDs(ctx context.Context, b Binding) ([]string, error) {
	rows, err := s.store.Query(ctx, `SELECT body_json FROM knowledge_ontology_source_heads WHERE tenant_id=? AND knowledge_base_id=? ORDER BY document_id`, b.TenantID, b.KnowledgeBaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var raw string
		var h SourceHead
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &h); err != nil {
			return nil, err
		}
		if h.Status == "published" {
			result = append(result, h.DocumentID)
		}
	}
	return result, rows.Err()
}

func sourceLifecycleMigration() migration.Migration {
	return migration.Migration{Version: "0243_ontology_source_lifecycle", Description: "ordered source generations and immutable withdrawal attestations", Statements: []string{
		`CREATE TABLE IF NOT EXISTS knowledge_ontology_source_heads (tenant_id TEXT NOT NULL,knowledge_base_id TEXT NOT NULL,document_id TEXT NOT NULL,body_json TEXT NOT NULL,PRIMARY KEY(tenant_id,knowledge_base_id,document_id))`,
		`CREATE TABLE IF NOT EXISTS knowledge_ontology_source_events (tenant_id TEXT NOT NULL,id TEXT NOT NULL,knowledge_base_id TEXT NOT NULL,document_id TEXT NOT NULL,body_json TEXT NOT NULL,PRIMARY KEY(tenant_id,id))`,
		`CREATE INDEX IF NOT EXISTS idx_knowledge_ontology_source_events_document ON knowledge_ontology_source_events(tenant_id,knowledge_base_id,document_id,id)`,
	}}
}

func readSourceHead(ctx context.Context, query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, b Binding, document string) (SourceHead, error) {
	var raw string
	err := query.QueryRowContext(ctx, `SELECT body_json FROM knowledge_ontology_source_heads WHERE tenant_id=? AND knowledge_base_id=? AND document_id=?`, b.TenantID, b.KnowledgeBaseID, document).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return SourceHead{}, nil
	}
	var head SourceHead
	if err == nil {
		err = json.Unmarshal([]byte(raw), &head)
	}
	return head, err
}

func (s *PlatformStore) SourceHead(ctx context.Context, b Binding, document string) (SourceHead, error) {
	var head SourceHead
	err := s.store.WithTx(ctx, func(tx db.Tx) error { var err error; head, err = readSourceHead(ctx, tx, b, document); return err })
	return head, err
}

func (s *PlatformStore) lifecycleFence(ctx context.Context, tx db.Tx, b Binding) error {
	if s.store.Dialect() == db.DialectPostgres {
		// Match the first lock in continuous inference's input fence. Source
		// commits take graph -> bridge -> instance locks in that same order.
		if _, err := tx.ExecContext(ctx, `LOCK TABLE context_graph_objects IN SHARE ROW EXCLUSIVE MODE`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `LOCK TABLE knowledge_ontology_bindings,knowledge_ontology_projections IN SHARE ROW EXCLUSIVE MODE`); err != nil {
			return err
		}
	}
	var folder, kind, id, tenant string
	var enabled bool
	err := tx.QueryRowContext(ctx, `SELECT folder_id,source_access_kind,source_access_id,source_access_tenant_id,enabled FROM knowledge_ontology_bindings WHERE tenant_id=? AND knowledge_base_id=?`, b.TenantID, b.KnowledgeBaseID).Scan(&folder, &kind, &id, &tenant, &enabled)
	if err != nil {
		return err
	}
	if !enabled || folder != b.FolderID || (contextgraph.AccessRef{Kind: kind, ID: id, TenantID: tenant}) != b.SourceAccessRef || kind == "" {
		return ErrRevisionMismatch
	}
	return nil
}

func saveSourceHead(ctx context.Context, tx db.Tx, h SourceHead) error {
	raw, err := json.Marshal(h)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO knowledge_ontology_source_heads(tenant_id,knowledge_base_id,document_id,body_json) VALUES(?,?,?,?) ON CONFLICT(tenant_id,knowledge_base_id,document_id) DO UPDATE SET body_json=excluded.body_json`, h.TenantID, h.KnowledgeBaseID, h.DocumentID, string(raw))
	return err
}

// applyPublishedProjection serializes publication with source withdrawal and
// document commits. Empty cursor is an authoritative, fenced reconciliation.
func (s *PlatformStore) applyPublishedProjection(ctx context.Context, b Binding, p Projection, cursor string, expected *SourceHead, watermark int64, applier ProjectionApplier) error {
	return s.store.WithTx(ctx, func(tx db.Tx) error {
		if err := s.lifecycleFence(ctx, tx, b); err != nil {
			return err
		}
		h, err := readSourceHead(ctx, tx, b, p.DocumentID)
		if err != nil {
			return err
		}
		sequence, _ := strconv.ParseInt(cursor, 10, 64)
		if sequence > 0 && h.Sequence >= sequence {
			return upsertCursor(ctx, tx, b, cursor, time.Now().UTC())
		}
		if expected != nil && h != *expected {
			return ErrRevisionMismatch
		}
		if sequence == 0 && expected == nil && h.Status == "withdrawn" {
			return ErrRevisionMismatch
		}
		if p.Revision < h.Revision && h.Status == "published" {
			h.Sequence = max(h.Sequence, sequence, watermark)
			if err := saveSourceHead(ctx, tx, h); err != nil {
				return err
			}
			return upsertCursor(ctx, tx, b, cursor, time.Now().UTC())
		}
		generation := h.Generation
		if generation == 0 || h.Status != "published" || h.Revision != p.Revision || h.ContentHash != p.ContentHash {
			generation++
		}
		h = SourceHead{TenantID: b.TenantID, KnowledgeBaseID: b.KnowledgeBaseID, FolderID: b.FolderID, DocumentID: p.DocumentID, RootID: p.KnowledgeDocumentObjectID, Revision: p.Revision, ContentHash: p.ContentHash, Access: b.SourceAccessRef, Generation: generation, Sequence: max(h.Sequence, sequence, watermark), Status: "published"}
		if applier != nil {
			a, ok := applier.(*GraphApplier)
			if !ok {
				return fmt.Errorf("transactional source lifecycle requires the graph applier")
			}
			p.ExistingLinks, p.ExistingEvidence, err = sourceGraphRows(ctx, tx, b.TenantID, p.DocumentID)
			if err != nil {
				return err
			}
			validator := a.validator
			if _, ok := validator.(*OntologyLinkValidator); ok {
				validator = NewOntologyLinkValidator(ontology.NewPlatformRegistryTx(tx), contextgraph.NewPlatformStoreTx(tx))
			}
			if err := NewGraphApplier(contextgraph.NewPlatformStoreTx(tx), validator).ApplyProjection(ctx, b, p); err != nil {
				return err
			}
		}
		if err := saveSourceHead(ctx, tx, h); err != nil {
			return err
		}
		return saveProjectionTx(ctx, tx, b, p, cursor, time.Now().UTC())
	})
}

// applyTombstone accepts only the trusted provider feed, never a source read
// failure. Old events cannot delete a newer locally attested publication.
func (s *PlatformStore) applyTombstone(ctx context.Context, b Binding, c provider.KnowledgeChange) (bool, error) {
	if c.FolderID != b.FolderID || c.DocumentID == "" || c.Sequence < 1 || !isDocumentTombstoneEvent(c.EventType) {
		return false, ErrRevisionMismatch
	}
	return s.withdrawDocument(ctx, b, c.DocumentID, c.EventType, c.Sequence, nil, c)
}

func (s *PlatformStore) withdrawDocument(ctx context.Context, b Binding, document, eventType string, sequence int64, expected *SourceHead, providerEvent ...provider.KnowledgeChange) (bool, error) {
	applied := false
	err := s.store.WithTx(ctx, func(tx db.Tx) error {
		if err := s.lifecycleFence(ctx, tx, b); err != nil {
			return err
		}
		h, err := readSourceHead(ctx, tx, b, document)
		if err != nil {
			return err
		}
		if expected != nil && h != *expected {
			return nil
		}
		if sequence > 0 && sequence <= h.Sequence {
			return nil
		}
		if h.Generation == 0 {
			h = SourceHead{TenantID: b.TenantID, KnowledgeBaseID: b.KnowledgeBaseID, FolderID: b.FolderID, DocumentID: document, Access: b.SourceAccessRef, Generation: 1, Status: "published"}
			var raw string
			err := tx.QueryRowContext(ctx, `SELECT payload FROM knowledge_ontology_projections WHERE tenant_id=? AND knowledge_base_id=? AND document_id=? ORDER BY revision DESC LIMIT 1`, b.TenantID, b.KnowledgeBaseID, document).Scan(&raw)
			if err == nil {
				var p Projection
				if err := json.Unmarshal([]byte(raw), &p); err != nil {
					return err
				}
				h.RootID, h.Revision, h.ContentHash = p.KnowledgeDocumentObjectID, p.Revision, p.ContentHash
			} else if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		if h.Status == "withdrawn" {
			h.Sequence = max(h.Sequence, sequence)
			return saveSourceHead(ctx, tx, h)
		}
		if len(providerEvent) > 0 {
			c := providerEvent[0]
			if (c.Revision > 0 && c.Revision < h.Revision) || (c.Revision == h.Revision && c.ContentHash != "" && c.ContentHash != h.ContentHash) {
				h.Sequence = max(h.Sequence, sequence)
				if err := saveSourceHead(ctx, tx, h); err != nil {
					return err
				}
				return upsertCursor(ctx, tx, b, strconv.FormatInt(sequence, 10), time.Now().UTC())
			}
		}
		eventType = strings.ToLower(strings.TrimSpace(eventType))
		identity, _ := json.Marshal([]any{b.TenantID, b.KnowledgeBaseID, document, h.Generation, sequence, eventType})
		event := SourceWithdrawal{ID: stableID("knowledge-source-withdrawal", string(identity)), Source: h, EventType: eventType, Sequence: sequence, ObservedAt: time.Now().UTC()}
		if len(providerEvent) > 0 {
			event.ProviderEvent = &providerEvent[0]
		}
		raw, err := json.Marshal(event)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO knowledge_ontology_source_events(tenant_id,id,knowledge_base_id,document_id,body_json) VALUES(?,?,?,?,?)`, b.TenantID, event.ID, b.KnowledgeBaseID, document, string(raw)); err != nil {
			return err
		}
		if err := s.withdrawSource(ctx, tx, event); err != nil {
			return err
		}
		links, evidence, err := sourceGraphRows(ctx, tx, b.TenantID, document)
		if err != nil {
			return err
		}
		if err := NewGraphApplier(contextgraph.NewPlatformStoreTx(tx), nil).DeleteDocumentProjection(ctx, b, document, links, evidence); err != nil {
			return err
		}
		for _, table := range []string{"knowledge_ontology_mentions", "knowledge_ontology_claims", "knowledge_ontology_projections"} {
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE tenant_id=? AND knowledge_base_id=? AND document_id=?`, b.TenantID, b.KnowledgeBaseID, document); err != nil {
				return err
			}
		}
		h.Status, h.Sequence = "withdrawn", max(h.Sequence, sequence)
		if err := saveSourceHead(ctx, tx, h); err != nil {
			return err
		}
		if sequence > 0 {
			if err := upsertCursor(ctx, tx, b, strconv.FormatInt(sequence, 10), event.ObservedAt); err != nil {
				return err
			}
		}
		applied = true
		return nil
	})
	return applied, err
}

// Only the bridge-owned stream and exact source reference can be removed.
func sourceGraphRows(ctx context.Context, tx db.Tx, tenant, document string) ([]contextgraph.Link, []contextgraph.Evidence, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM context_graph_links WHERE tenant_id=? AND source_stream_id=?`, tenant, "knowledge_revision:"+document)
	if err != nil {
		return nil, nil, err
	}
	var links []contextgraph.Link
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, nil, err
		}
		links = append(links, contextgraph.Link{ID: id, TenantID: tenant, SourceStreamID: "knowledge_revision:" + document})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT id,source_kind,source_id FROM context_graph_evidence WHERE tenant_id=? AND source_kind='knowledge_revision'`, tenant)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var evidence []contextgraph.Evidence
	for rows.Next() {
		item := contextgraph.Evidence{TenantID: tenant}
		if err := rows.Scan(&item.ID, &item.SourceRef.Kind, &item.SourceRef.ID); err != nil {
			return nil, nil, err
		}
		if knowledgeEvidenceBelongsToDocument(item, tenant, document) {
			evidence = append(evidence, item)
		}
	}
	return links, evidence, rows.Err()
}

func (e SourceWithdrawal) Fence(ctx context.Context, tx db.Tx) error {
	var raw string
	if err := tx.QueryRowContext(ctx, `SELECT body_json FROM knowledge_ontology_source_events WHERE tenant_id=? AND id=?`, e.Source.TenantID, e.ID).Scan(&raw); err != nil {
		return err
	}
	expected, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if raw != string(expected) {
		return ErrRevisionMismatch
	}
	h, err := readSourceHead(ctx, tx, Binding{TenantID: e.Source.TenantID, KnowledgeBaseID: e.Source.KnowledgeBaseID}, e.Source.DocumentID)
	if err != nil {
		return err
	}
	// A legacy source may not have a head yet; the bridge holds its insertion fence.
	if h.Generation != 0 && h != e.Source {
		return ErrRevisionMismatch
	}
	return nil
}
