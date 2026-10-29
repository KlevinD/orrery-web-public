package contextgraph

import (
	"context"
	"fmt"
	"strings"

	"orrery_v2/internal/platform/db"
)

// SnapshotKnowledgeDocument loads the entity dictionary and only the fact
// predicates and existing relations needed to project one knowledge document.
func (s *PlatformStore) SnapshotKnowledgeDocument(ctx context.Context, tenantID, documentID string) ([]Object, []Link, []Fact, []Evidence, error) {
	if !s.ready() || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(documentID) == "" {
		return nil, nil, nil, nil, fmt.Errorf("graph store, tenant and document are required")
	}
	tenantID, documentID = strings.TrimSpace(tenantID), strings.TrimSpace(documentID)
	var objects []Object
	var links []Link
	var facts []Fact
	var evidence []Evidence
	read := func(store *PlatformStore) error {
		var err error
		objects, err = store.Objects(ctx, tenantID)
		if err != nil {
			return err
		}
		facts, err = store.listSelectedFacts(ctx, tenantID, "LOWER(subject_kind) = 'knowledge_document' AND LOWER(TRIM(predicate)) IN ('provider_document_id', 'knowledge_base_id')")
		if err != nil {
			return err
		}
		links, evidence, err = store.KnowledgeDocumentRelations(ctx, tenantID, documentID)
		return err
	}
	var err error
	if s.tx != nil {
		err = read(s)
	} else {
		err = s.store.WithReadTx(ctx, func(tx db.Tx) error { return read(NewPlatformStoreTx(tx)) })
	}
	return objects, links, facts, evidence, err
}

func (s *PlatformStore) KnowledgeDocumentRelations(ctx context.Context, tenantID, documentID string) ([]Link, []Evidence, error) {
	if !s.ready() || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(documentID) == "" {
		return nil, nil, fmt.Errorf("graph store, tenant and document are required")
	}
	tenantID, documentID = strings.TrimSpace(tenantID), strings.TrimSpace(documentID)
	links, err := s.listSelectedLinks(ctx, tenantID, "source_stream_id = ?", []any{"knowledge_revision:" + documentID})
	if err != nil {
		return nil, nil, err
	}
	evidence, err := s.listSelectedEvidence(ctx, tenantID, "source_kind = ? AND source_id >= ? AND source_id < ?", []any{"knowledge_revision", documentID + "@", documentID + "A"})
	return links, evidence, err
}
