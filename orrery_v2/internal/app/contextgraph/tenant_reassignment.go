package contextgraph

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"orrery_v2/internal/platform/db"
)

// ReassignTenant merges a legacy tenant graph into its canonical tenant. The
// copy-before-delete transaction preserves existing canonical rows and makes
// the operation safe to repeat after rollback or restored data.
func (s *PlatformStore) ReassignTenant(ctx context.Context, fromTenantID, toTenantID string) (int, error) {
	fromTenantID = strings.TrimSpace(fromTenantID)
	toTenantID = strings.TrimSpace(toTenantID)
	if fromTenantID == "" || toTenantID == "" {
		return 0, fmt.Errorf("context graph source and destination tenant ids are required")
	}
	if fromTenantID == toTenantID {
		return 0, nil
	}
	if s == nil || s.store == nil || s.tx != nil {
		return 0, fmt.Errorf("context graph tenant reassignment requires a platform store")
	}

	legacyRows := 0
	err := s.store.WithTx(ctx, func(tx db.Tx) error {
		var err error
		legacyRows, err = countContextGraphTenantRows(ctx, tx, fromTenantID)
		if err != nil || legacyRows == 0 {
			return err
		}
		if err := copyContextGraphTenantRows(ctx, tx, s.store.Dialect(), fromTenantID, toTenantID); err != nil {
			return err
		}
		if err := normalizeContextGraphTenantReferences(ctx, tx, fromTenantID, toTenantID); err != nil {
			return err
		}
		if err := deleteContextGraphTenantRows(ctx, tx, fromTenantID); err != nil {
			return err
		}
		remaining, err := countContextGraphTenantRows(ctx, tx, fromTenantID)
		if err != nil {
			return err
		}
		if remaining != 0 {
			return fmt.Errorf("context graph tenant reassignment left %d legacy rows", remaining)
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("reassign context graph tenant %s to %s: %w", fromTenantID, toTenantID, err)
	}
	return legacyRows, nil
}

// DeleteTenant removes every graph row owned by a retired tenant. Callers must
// establish that the tenant is no longer a valid authorization boundary.
func (s *PlatformStore) DeleteTenant(ctx context.Context, tenantID string) (int, error) {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return 0, fmt.Errorf("context graph tenant id is required")
	}
	if s == nil || s.store == nil || s.tx != nil {
		return 0, fmt.Errorf("context graph tenant deletion requires a platform store")
	}

	deletedRows := 0
	err := s.store.WithTx(ctx, func(tx db.Tx) error {
		var err error
		deletedRows, err = countContextGraphTenantRows(ctx, tx, tenantID)
		if err != nil {
			return err
		}
		if err := deleteContextGraphTenantRows(ctx, tx, tenantID); err != nil {
			return err
		}
		remaining, err := countContextGraphTenantRows(ctx, tx, tenantID)
		if err != nil {
			return err
		}
		if remaining != 0 {
			return fmt.Errorf("context graph tenant deletion left %d rows", remaining)
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("delete context graph tenant %s: %w", tenantID, err)
	}
	return deletedRows, nil
}

func countContextGraphTenantRows(ctx context.Context, tx db.Tx, tenantID string) (int, error) {
	total := 0
	for _, table := range []string{
		"context_graph_objects", "context_graph_links", "context_graph_facts", "context_graph_fact_assertions",
		"context_graph_evidence", "context_graph_resource_versions", "context_graph_projection_audits",
		"context_graph_projection_audit_replay_history", "context_graph_access_ref_backfill_audit",
	} {
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE tenant_id = ?", tenantID).Scan(&count); err != nil {
			return 0, fmt.Errorf("count %s tenant rows: %w", table, err)
		}
		total += count
	}
	return total, nil
}

func copyContextGraphTenantRows(ctx context.Context, tx db.Tx, dialect db.Dialect, fromTenantID, toTenantID string) error {
	fromOrganizationKey := "organization:" + fromTenantID
	toOrganizationKey := "organization:" + toTenantID
	legacyJSON, _ := json.Marshal(fromTenantID)
	canonicalJSON, _ := json.Marshal(toTenantID)
	valueExpression := "CASE WHEN value_json = ? THEN ? ELSE value_json END"
	if dialect == db.DialectPostgres {
		valueExpression = "CASE WHEN CAST(value_json AS TEXT) = ? THEN CAST(? AS JSONB) ELSE value_json END"
	}
	statements := []struct {
		name  string
		query string
		args  []any
	}{
		{name: "objects", query: `
INSERT INTO context_graph_objects (
	id, tenant_id, object_type, canonical_key, label, properties_json, sensitivity, confidence, status,
	provenance_json, evidence_refs_json, source_stream_id, source_version, updated_at,
	owner_access_kind, owner_access_id, owner_access_tenant_id, access_state, temporal_json
)
SELECT CASE WHEN id = ? THEN ? ELSE id END, ?, object_type,
	CASE WHEN canonical_key = ? THEN ? ELSE canonical_key END,
	label, properties_json, sensitivity, confidence, status, provenance_json, evidence_refs_json,
	CASE WHEN source_stream_id = ? THEN ? ELSE source_stream_id END,
	source_version, updated_at, owner_access_kind,
	CASE WHEN owner_access_id = ? THEN ? ELSE owner_access_id END,
	CASE WHEN owner_access_tenant_id = ? THEN ? ELSE owner_access_tenant_id END,
	access_state, temporal_json
FROM context_graph_objects WHERE tenant_id = ?
ON CONFLICT DO NOTHING`, args: []any{fromTenantID, toTenantID, toTenantID, fromOrganizationKey, toOrganizationKey, fromTenantID, toTenantID, fromTenantID, toTenantID, fromTenantID, toTenantID, fromTenantID}},
		{name: "links", query: `
INSERT INTO context_graph_links (
	id, tenant_id, link_type, from_object_id, to_object_id, properties_json, confidence, status,
	provenance_json, evidence_refs_json, source_stream_id, source_version, updated_at,
	source_access_kind, source_access_id, source_access_tenant_id, access_state, temporal_json
)
SELECT id, ?, link_type,
	CASE WHEN from_object_id = ? THEN ? ELSE from_object_id END,
	CASE WHEN to_object_id = ? THEN ? ELSE to_object_id END,
	properties_json, confidence, status, provenance_json, evidence_refs_json,
	CASE WHEN source_stream_id = ? THEN ? ELSE source_stream_id END,
	source_version, updated_at, source_access_kind,
	CASE WHEN source_access_id = ? THEN ? ELSE source_access_id END,
	CASE WHEN source_access_tenant_id = ? THEN ? ELSE source_access_tenant_id END,
	access_state, temporal_json
FROM context_graph_links WHERE tenant_id = ?
ON CONFLICT DO NOTHING`, args: []any{toTenantID, fromTenantID, toTenantID, fromTenantID, toTenantID, fromTenantID, toTenantID, fromTenantID, toTenantID, fromTenantID, toTenantID, fromTenantID}},
		{name: "facts", query: `
INSERT INTO context_graph_facts (
	id, tenant_id, subject_kind, subject_id, predicate, value_json, properties_json, confidence, status,
	provenance_json, evidence_refs_json, source_stream_id, source_version, updated_at,
	source_access_kind, source_access_id, source_access_tenant_id, access_state, temporal_json
)
SELECT id, ?, subject_kind, CASE WHEN subject_id = ? THEN ? ELSE subject_id END, predicate, ` + valueExpression + `,
	properties_json, confidence, status, provenance_json, evidence_refs_json,
	CASE WHEN source_stream_id = ? THEN ? ELSE source_stream_id END,
	source_version, updated_at, source_access_kind,
	CASE WHEN source_access_id = ? THEN ? ELSE source_access_id END,
	CASE WHEN source_access_tenant_id = ? THEN ? ELSE source_access_tenant_id END,
	access_state, temporal_json
FROM context_graph_facts WHERE tenant_id = ?
ON CONFLICT DO NOTHING`, args: []any{toTenantID, fromTenantID, toTenantID, string(legacyJSON), string(canonicalJSON), fromTenantID, toTenantID, fromTenantID, toTenantID, fromTenantID, toTenantID, fromTenantID}},
		{name: "fact assertions", query: `
INSERT INTO context_graph_fact_assertions (
	assertion_id, tenant_id, fact_id, subject_kind, subject_id, predicate, value_json, properties_json,
	confidence, status, provenance_json, evidence_refs_json, source_stream_id, source_version, observed_at,
	source_access_kind, source_access_id, source_access_tenant_id, access_state, temporal_json
)
SELECT assertion_id, ?, fact_id, subject_kind, CASE WHEN subject_id = ? THEN ? ELSE subject_id END,
	predicate, ` + valueExpression + `, properties_json, confidence, status, provenance_json, evidence_refs_json,
	CASE WHEN source_stream_id = ? THEN ? ELSE source_stream_id END,
	source_version, observed_at, source_access_kind,
	CASE WHEN source_access_id = ? THEN ? ELSE source_access_id END,
	CASE WHEN source_access_tenant_id = ? THEN ? ELSE source_access_tenant_id END,
	access_state, temporal_json
FROM context_graph_fact_assertions WHERE tenant_id = ?
ON CONFLICT DO NOTHING`, args: []any{toTenantID, fromTenantID, toTenantID, string(legacyJSON), string(canonicalJSON), fromTenantID, toTenantID, fromTenantID, toTenantID, fromTenantID, toTenantID, fromTenantID}},
		{name: "evidence", query: `
INSERT INTO context_graph_evidence (
	id, tenant_id, source_kind, source_id, summary, confidence, observed_at,
	source_access_kind, source_access_id, source_access_tenant_id, access_state
)
SELECT id, ?, source_kind, CASE WHEN source_id = ? THEN ? ELSE source_id END,
	summary, confidence, observed_at, source_access_kind,
	CASE WHEN source_access_id = ? THEN ? ELSE source_access_id END,
	CASE WHEN source_access_tenant_id = ? THEN ? ELSE source_access_tenant_id END, access_state
FROM context_graph_evidence WHERE tenant_id = ?
ON CONFLICT DO NOTHING`, args: []any{toTenantID, fromTenantID, toTenantID, fromTenantID, toTenantID, fromTenantID, toTenantID, fromTenantID}},
		{name: "resource versions", query: `
INSERT INTO context_graph_resource_versions (
	tenant_id, resource_kind, resource_id, assertion_id, payload_json, temporal_mode, business_valid_from,
	business_valid_to, source_recorded_at, source_superseded_at, orrery_ingested_at, access_kind, access_id,
	access_tenant_id, access_state, source_stream_id, source_version, updated_at
)
SELECT ?, resource_kind, CASE WHEN resource_id = ? THEN ? ELSE resource_id END,
	assertion_id, payload_json, temporal_mode, business_valid_from, business_valid_to, source_recorded_at,
	source_superseded_at, orrery_ingested_at, access_kind,
	CASE WHEN access_id = ? THEN ? ELSE access_id END,
	CASE WHEN access_tenant_id = ? THEN ? ELSE access_tenant_id END,
	access_state, CASE WHEN source_stream_id = ? THEN ? ELSE source_stream_id END, source_version, updated_at
FROM context_graph_resource_versions WHERE tenant_id = ?
ON CONFLICT DO NOTHING`, args: []any{toTenantID, fromTenantID, toTenantID, fromTenantID, toTenantID, fromTenantID, toTenantID, fromTenantID, toTenantID, fromTenantID}},
		{name: "projection audits", query: `
INSERT INTO context_graph_projection_audits (
	id, tenant_id, source_event_id, source_event_type, status, reason_code, reason, details_json, observed_at,
	source_access_kind, source_access_id, source_access_tenant_id, access_state, revision
)
SELECT id, ?, source_event_id, source_event_type, status, reason_code, reason, details_json, observed_at,
	source_access_kind, CASE WHEN source_access_id = ? THEN ? ELSE source_access_id END,
	CASE WHEN source_access_tenant_id = ? THEN ? ELSE source_access_tenant_id END, access_state, revision
FROM context_graph_projection_audits WHERE tenant_id = ?
ON CONFLICT DO NOTHING`, args: []any{toTenantID, fromTenantID, toTenantID, fromTenantID, toTenantID, fromTenantID}},
		{name: "projection audit replay history", query: `
INSERT INTO context_graph_projection_audit_replay_history (
	id, audit_id, tenant_id, source_event_id, source_event_type, status, error, details_json, attempted_at,
	source_access_kind, source_access_id, source_access_tenant_id, access_state
)
SELECT id, audit_id, ?, source_event_id, source_event_type, status, error, details_json, attempted_at,
	source_access_kind, CASE WHEN source_access_id = ? THEN ? ELSE source_access_id END,
	CASE WHEN source_access_tenant_id = ? THEN ? ELSE source_access_tenant_id END, access_state
FROM context_graph_projection_audit_replay_history WHERE tenant_id = ?
ON CONFLICT DO NOTHING`, args: []any{toTenantID, fromTenantID, toTenantID, fromTenantID, toTenantID, fromTenantID}},
		{name: "access reference backfill audit", query: `
INSERT INTO context_graph_access_ref_backfill_audit (
	run_id, resource_kind, tenant_id, resource_id, access_kind, access_id, access_tenant_id,
	source_kind, source_id, changed_by, changed_at
)
SELECT run_id, resource_kind, ?, CASE WHEN resource_id = ? THEN ? ELSE resource_id END,
	access_kind, CASE WHEN access_id = ? THEN ? ELSE access_id END,
	CASE WHEN access_tenant_id = ? THEN ? ELSE access_tenant_id END,
	source_kind, CASE WHEN source_id = ? THEN ? ELSE source_id END, changed_by, changed_at
FROM context_graph_access_ref_backfill_audit WHERE tenant_id = ?
ON CONFLICT DO NOTHING`, args: []any{toTenantID, fromTenantID, toTenantID, fromTenantID, toTenantID, fromTenantID, toTenantID, fromTenantID, toTenantID, fromTenantID}},
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement.query, statement.args...); err != nil {
			return fmt.Errorf("copy context graph %s: %w", statement.name, err)
		}
	}
	return nil
}

func normalizeContextGraphTenantReferences(ctx context.Context, tx db.Tx, fromTenantID, toTenantID string) error {
	statements := []struct {
		name  string
		query string
		args  []any
	}{
		{name: "objects", query: `UPDATE context_graph_objects SET
		owner_access_id = CASE WHEN owner_access_id = ? THEN ? ELSE owner_access_id END,
		owner_access_tenant_id = CASE WHEN owner_access_tenant_id = ? THEN ? ELSE owner_access_tenant_id END,
		source_stream_id = CASE WHEN source_stream_id = ? THEN ? ELSE source_stream_id END
		WHERE tenant_id = ?`, args: []any{fromTenantID, toTenantID, fromTenantID, toTenantID, fromTenantID, toTenantID, toTenantID}},
		{name: "links", query: `UPDATE context_graph_links SET
		source_access_id = CASE WHEN source_access_id = ? THEN ? ELSE source_access_id END,
		source_access_tenant_id = CASE WHEN source_access_tenant_id = ? THEN ? ELSE source_access_tenant_id END,
		source_stream_id = CASE WHEN source_stream_id = ? THEN ? ELSE source_stream_id END
		WHERE tenant_id = ?`, args: []any{fromTenantID, toTenantID, fromTenantID, toTenantID, fromTenantID, toTenantID, toTenantID}},
		{name: "facts", query: `UPDATE context_graph_facts SET
		source_access_id = CASE WHEN source_access_id = ? THEN ? ELSE source_access_id END,
		source_access_tenant_id = CASE WHEN source_access_tenant_id = ? THEN ? ELSE source_access_tenant_id END,
		source_stream_id = CASE WHEN source_stream_id = ? THEN ? ELSE source_stream_id END
		WHERE tenant_id = ?`, args: []any{fromTenantID, toTenantID, fromTenantID, toTenantID, fromTenantID, toTenantID, toTenantID}},
		{name: "fact assertions", query: `UPDATE context_graph_fact_assertions SET
		source_access_id = CASE WHEN source_access_id = ? THEN ? ELSE source_access_id END,
		source_access_tenant_id = CASE WHEN source_access_tenant_id = ? THEN ? ELSE source_access_tenant_id END,
		source_stream_id = CASE WHEN source_stream_id = ? THEN ? ELSE source_stream_id END
		WHERE tenant_id = ?`, args: []any{fromTenantID, toTenantID, fromTenantID, toTenantID, fromTenantID, toTenantID, toTenantID}},
		{name: "evidence", query: `UPDATE context_graph_evidence SET
		source_access_id = CASE WHEN source_access_id = ? THEN ? ELSE source_access_id END,
		source_access_tenant_id = CASE WHEN source_access_tenant_id = ? THEN ? ELSE source_access_tenant_id END
		WHERE tenant_id = ?`, args: []any{fromTenantID, toTenantID, fromTenantID, toTenantID, toTenantID}},
		{name: "resource versions", query: `UPDATE context_graph_resource_versions SET
		access_id = CASE WHEN access_id = ? THEN ? ELSE access_id END,
		access_tenant_id = CASE WHEN access_tenant_id = ? THEN ? ELSE access_tenant_id END,
		source_stream_id = CASE WHEN source_stream_id = ? THEN ? ELSE source_stream_id END
		WHERE tenant_id = ?`, args: []any{fromTenantID, toTenantID, fromTenantID, toTenantID, fromTenantID, toTenantID, toTenantID}},
		{name: "projection audits", query: `UPDATE context_graph_projection_audits SET
		source_access_id = CASE WHEN source_access_id = ? THEN ? ELSE source_access_id END,
		source_access_tenant_id = CASE WHEN source_access_tenant_id = ? THEN ? ELSE source_access_tenant_id END
		WHERE tenant_id = ?`, args: []any{fromTenantID, toTenantID, fromTenantID, toTenantID, toTenantID}},
		{name: "projection audit replay history", query: `UPDATE context_graph_projection_audit_replay_history SET
		source_access_id = CASE WHEN source_access_id = ? THEN ? ELSE source_access_id END,
		source_access_tenant_id = CASE WHEN source_access_tenant_id = ? THEN ? ELSE source_access_tenant_id END
		WHERE tenant_id = ?`, args: []any{fromTenantID, toTenantID, fromTenantID, toTenantID, toTenantID}},
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement.query, statement.args...); err != nil {
			return fmt.Errorf("normalize context graph %s references: %w", statement.name, err)
		}
	}
	return nil
}

func deleteContextGraphTenantRows(ctx context.Context, tx db.Tx, tenantID string) error {
	for _, table := range []string{
		"context_graph_projection_audit_command_claims", "context_graph_projection_audit_commands",
		"context_graph_projection_audit_replay_history", "context_graph_projection_audits",
		"context_graph_access_ref_backfill_audit", "context_graph_resource_versions", "context_graph_fact_assertions",
		"context_graph_links", "context_graph_facts", "context_graph_evidence", "context_graph_objects",
	} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE tenant_id = ?", tenantID); err != nil {
			return fmt.Errorf("delete %s tenant rows: %w", table, err)
		}
	}
	return nil
}
