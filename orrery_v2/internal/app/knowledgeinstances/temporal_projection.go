package knowledgeinstances

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/platform/db"
)

// Closing a source assertion appends a knowledge-time tombstone. It never
// edits a prior version or invents history for an old static projection.
func closeClaimHistory(ctx context.Context, tx db.Tx, tenant, id, stream string, link bool, at time.Time) error {
	kind := "fact"
	if link {
		kind = "link"
	}
	var payload string
	var access contextgraph.AccessRef
	var accessState contextgraph.AccessState
	var version int64
	err := tx.QueryRowContext(ctx, `SELECT payload_json,access_kind,access_id,access_tenant_id,access_state,source_version FROM context_graph_resource_versions WHERE tenant_id=? AND resource_kind=? AND resource_id=? AND source_stream_id=? ORDER BY source_recorded_at DESC,source_version DESC,updated_at DESC LIMIT 1`, tenant, kind, id, stream).Scan(&payload, &access.Kind, &access.ID, &access.TenantID, &accessState, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	graph := contextgraph.NewPlatformStoreTx(tx)
	if link {
		var value contextgraph.Link
		if err := json.Unmarshal([]byte(payload), &value); err != nil {
			return err
		}
		if value.ID != id || value.TenantID != tenant || value.Provenance.Kind != Domain {
			return ErrConflict
		}
		if value.Temporal.SourceSupersededAt != nil {
			return nil
		}
		value.SourceAccessRef, value.AccessState, value.SourceStreamID = access, accessState, stream
		value.Temporal.SourceSupersededAt = &at
		value.SourceVersion = max(version+1, at.UnixNano())
		value.UpdatedAt = at
		return graph.SaveLink(ctx, value)
	}
	var value contextgraph.Fact
	if err := json.Unmarshal([]byte(payload), &value); err != nil {
		return err
	}
	if value.ID != id || value.TenantID != tenant || value.Provenance.Kind != Domain {
		return ErrConflict
	}
	if value.Temporal.SourceSupersededAt != nil {
		return nil
	}
	value.SourceAccessRef, value.AccessState, value.SourceStreamID = access, accessState, stream
	value.Temporal.SourceSupersededAt = &at
	value.SourceVersion = max(version+1, at.UnixNano())
	value.UpdatedAt = at
	return graph.SaveFact(ctx, value)
}

// A clock rollover changes only the current materialized view. Immutable
// versions retain their intrinsic status and business window for past queries.
func refreshClaimClock(ctx context.Context, tx db.Tx, tenant, watch string, c Claim, at time.Time) error {
	table := "context_graph_facts"
	if c.TargetID != "" {
		table = "context_graph_links"
	}
	stream := "knowledge-document:" + tenant + ":" + watch
	status := claimStatus(c, at)
	if _, err := tx.ExecContext(ctx, `UPDATE `+table+` SET status=CASE WHEN ?='active' AND status='disputed' THEN status ELSE ? END WHERE tenant_id=? AND id=? AND source_stream_id=?`, status, status, tenant, c.ID, stream); err != nil {
		return err
	}
	if c.TargetID == "" {
		if _, err := tx.ExecContext(ctx, `UPDATE context_graph_fact_assertions SET status=CASE WHEN ?='active' AND status='disputed' THEN status ELSE ? END WHERE tenant_id=? AND fact_id=? AND source_stream_id=?`, status, status, tenant, c.ID, stream); err != nil {
			return err
		}
	}
	var err error
	c.ProjectionHash, err = projectionHash(ctx, tx, table, tenant, c.ID, stream)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE knowledge_instance_claims SET body_json=? WHERE tenant_id=? AND id=? AND watch_id=?`, body(c), tenant, c.ID, watch)
	return err
}

func sameActiveSet(p Plan, old []SourceClaim, at time.Time) bool {
	claims := replacementClaims(p, old)
	ids := func(at time.Time) []string {
		var result []string
		for _, c := range activeSourceClaimsAt(claims, at) {
			result = append(result, c.Claim.ID)
		}
		return result
	}
	return Digest(ids(p.CreatedAt)) == Digest(ids(at))
}
