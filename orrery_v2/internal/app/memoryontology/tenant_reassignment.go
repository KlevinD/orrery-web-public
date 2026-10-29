package memoryontology

import (
	"context"
	"fmt"
	"strings"

	"orrery_v2/internal/platform/db"
)

// ReassignTenant merges legacy organization extraction state into the
// canonical tenant while preserving newer canonical rows on key conflicts.
func (s *PlatformStore) ReassignTenant(ctx context.Context, fromTenantID, toTenantID string) (int, error) {
	if s == nil || s.store == nil {
		return 0, fmt.Errorf("memory ontology platform store is required")
	}
	fromTenantID, toTenantID = strings.TrimSpace(fromTenantID), strings.TrimSpace(toTenantID)
	if fromTenantID == "" || toTenantID == "" {
		return 0, fmt.Errorf("memory ontology source and destination tenant ids are required")
	}
	if fromTenantID == toTenantID {
		return 0, nil
	}
	migrated := 0
	err := s.store.WithTx(ctx, func(tx db.Tx) error {
		for _, table := range []string{"memory_ontology_runs", "memory_ontology_mentions"} {
			var count int
			if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE tenant_id = ?", fromTenantID).Scan(&count); err != nil {
				return fmt.Errorf("count %s legacy rows: %w", table, err)
			}
			migrated += count
		}
		if migrated == 0 {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO memory_ontology_runs (
	tenant_id, memory_id, content_hash, extractor_version, status, attempts,
	next_retry_at, last_error, model_id, started_at, completed_at, updated_at
)
SELECT ?, memory_id, content_hash, extractor_version, status, attempts,
	next_retry_at, last_error, model_id, started_at, completed_at, updated_at
FROM memory_ontology_runs
WHERE tenant_id = ?
ON CONFLICT(tenant_id, memory_id, content_hash, extractor_version) DO NOTHING`, toTenantID, fromTenantID); err != nil {
			return fmt.Errorf("copy memory ontology runs: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO memory_ontology_mentions (
	tenant_id, memory_id, content_hash, extractor_version, mention_index,
	surface, object_type, object_hint, confidence, anchor, resolution,
	resolved_object_id, link_type, evidence_id, link_id, created_at
)
SELECT ?, memory_id, content_hash, extractor_version, mention_index,
	surface, object_type, object_hint, confidence, anchor, resolution,
	resolved_object_id, link_type, evidence_id, link_id, created_at
FROM memory_ontology_mentions
WHERE tenant_id = ?
ON CONFLICT(tenant_id, memory_id, content_hash, extractor_version, mention_index) DO NOTHING`, toTenantID, fromTenantID); err != nil {
			return fmt.Errorf("copy memory ontology mentions: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM memory_ontology_mentions WHERE tenant_id = ?", fromTenantID); err != nil {
			return fmt.Errorf("delete legacy memory ontology mentions: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM memory_ontology_runs WHERE tenant_id = ?", fromTenantID); err != nil {
			return fmt.Errorf("delete legacy memory ontology runs: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("reassign memory ontology tenant %s to %s: %w", fromTenantID, toTenantID, err)
	}
	return migrated, nil
}
