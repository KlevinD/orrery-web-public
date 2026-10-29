package contextgraph

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/platform/db"
)

type AccessRefResourceKind string

const (
	AccessRefResourceObject        AccessRefResourceKind = "object"
	AccessRefResourceLink          AccessRefResourceKind = "link"
	AccessRefResourceFact          AccessRefResourceKind = "fact"
	AccessRefResourceFactAssertion AccessRefResourceKind = "fact_assertion"
	AccessRefResourceEvidence      AccessRefResourceKind = "evidence"
	AccessRefResourceAudit         AccessRefResourceKind = "projection_audit"
	AccessRefResourceReplayHistory AccessRefResourceKind = "projection_replay_history"
)

type AccessRefBackfillCandidate struct {
	ResourceKind AccessRefResourceKind
	TenantID     string
	ResourceID   string
	SourceKind   string
	SourceID     string
	sourceValue  string
}

type AccessRefBackfillResolver interface {
	ResolveAccessRef(context.Context, AccessRefBackfillCandidate) (AccessRef, bool, error)
}

type AccessRefBackfillResolverFunc func(context.Context, AccessRefBackfillCandidate) (AccessRef, bool, error)

func (f AccessRefBackfillResolverFunc) ResolveAccessRef(ctx context.Context, candidate AccessRefBackfillCandidate) (AccessRef, bool, error) {
	return f(ctx, candidate)
}

type AccessRefBackfillCursor struct {
	ResourceKind AccessRefResourceKind `json:"resource_kind"`
	TenantID     string                `json:"tenant_id"`
	ResourceID   string                `json:"resource_id"`
}

type AccessRefBackfillOptions struct {
	Apply     bool
	Limit     int
	After     AccessRefBackfillCursor
	RunID     string
	ChangedBy string
	Now       func() time.Time
}

type AccessRefBackfillReport struct {
	Apply      bool                    `json:"apply"`
	RunID      string                  `json:"run_id,omitempty"`
	Scanned    int                     `json:"scanned"`
	Resolved   int                     `json:"resolved"`
	Updated    int                     `json:"updated"`
	Unresolved int                     `json:"unresolved"`
	HasMore    bool                    `json:"has_more"`
	Next       AccessRefBackfillCursor `json:"next_cursor,omitempty"`
}

type accessRefBackfillSpec struct {
	kind          AccessRefResourceKind
	table         string
	idColumn      string
	accessPrefix  string
	sourceColumns string
	sourceMode    string
	sourceKindCol string
	sourceIDCol   string
}

var accessRefBackfillSpecs = []accessRefBackfillSpec{
	{kind: AccessRefResourceObject, table: "context_graph_objects", idColumn: "id", accessPrefix: "owner_access", sourceColumns: "provenance_json", sourceMode: "provenance"},
	{kind: AccessRefResourceLink, table: "context_graph_links", idColumn: "id", accessPrefix: "source_access", sourceColumns: "provenance_json", sourceMode: "provenance"},
	{kind: AccessRefResourceFact, table: "context_graph_facts", idColumn: "id", accessPrefix: "source_access", sourceColumns: "provenance_json", sourceMode: "provenance"},
	{kind: AccessRefResourceFactAssertion, table: "context_graph_fact_assertions", idColumn: "assertion_id", accessPrefix: "source_access", sourceColumns: "provenance_json", sourceMode: "provenance"},
	{kind: AccessRefResourceEvidence, table: "context_graph_evidence", idColumn: "id", accessPrefix: "source_access", sourceColumns: "source_kind, source_id", sourceMode: "columns", sourceKindCol: "source_kind", sourceIDCol: "source_id"},
	{kind: AccessRefResourceAudit, table: "context_graph_projection_audits", idColumn: "id", accessPrefix: "source_access", sourceColumns: "source_event_type, source_event_id", sourceMode: "columns", sourceKindCol: "source_event_type", sourceIDCol: "source_event_id"},
	{kind: AccessRefResourceReplayHistory, table: "context_graph_projection_audit_replay_history", idColumn: "id", accessPrefix: "source_access", sourceColumns: "source_event_type, source_event_id", sourceMode: "columns", sourceKindCol: "source_event_type", sourceIDCol: "source_event_id"},
}

func BackfillAccessRefs(ctx context.Context, store *db.Store, resolver AccessRefBackfillResolver, opts AccessRefBackfillOptions) (AccessRefBackfillReport, error) {
	if store == nil || resolver == nil {
		return AccessRefBackfillReport{}, fmt.Errorf("context graph store and access ref resolver are required")
	}
	if err := ctx.Err(); err != nil {
		return AccessRefBackfillReport{}, err
	}
	if opts.Limit <= 0 {
		opts.Limit = 100
	}
	if opts.Limit > 500 {
		return AccessRefBackfillReport{}, fmt.Errorf("access ref backfill limit must not exceed 500")
	}
	opts.RunID = strings.TrimSpace(opts.RunID)
	opts.ChangedBy = strings.TrimSpace(opts.ChangedBy)
	if opts.Apply && (opts.RunID == "" || opts.ChangedBy == "") {
		return AccessRefBackfillReport{}, fmt.Errorf("access ref backfill run id and changed by are required in apply mode")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	candidates, hasMore, err := listUnresolvedAccessRefCandidates(ctx, store, opts.After, opts.Limit)
	if err != nil {
		return AccessRefBackfillReport{}, err
	}
	report := AccessRefBackfillReport{Apply: opts.Apply, RunID: opts.RunID, Scanned: len(candidates), HasMore: hasMore}
	type resolution struct {
		candidate AccessRefBackfillCandidate
		ref       AccessRef
	}
	resolved := make([]resolution, 0, len(candidates))
	for _, candidate := range candidates {
		ref, found, err := resolver.ResolveAccessRef(ctx, candidate)
		if err != nil {
			return AccessRefBackfillReport{}, err
		}
		ref = normalizedAccessRefOrEmpty(ref)
		if !found || !ref.Resolved() {
			report.Unresolved++
			continue
		}
		report.Resolved++
		resolved = append(resolved, resolution{candidate: candidate, ref: ref})
	}
	if len(candidates) > 0 && hasMore {
		last := candidates[len(candidates)-1]
		report.Next = AccessRefBackfillCursor{ResourceKind: last.ResourceKind, TenantID: last.TenantID, ResourceID: last.ResourceID}
	}
	if !opts.Apply || len(resolved) == 0 {
		return report, nil
	}
	changedAt := opts.Now().UTC().UnixNano()
	err = store.WithTx(ctx, func(tx db.Tx) error {
		for _, item := range resolved {
			updated, err := applyResolvedAccessRef(ctx, tx, item.candidate, item.ref, opts.RunID, opts.ChangedBy, changedAt)
			if err != nil {
				return err
			}
			if updated {
				report.Updated++
			}
		}
		return nil
	})
	if err != nil {
		return AccessRefBackfillReport{}, err
	}
	return report, nil
}

func listUnresolvedAccessRefCandidates(ctx context.Context, store *db.Store, after AccessRefBackfillCursor, limit int) ([]AccessRefBackfillCandidate, bool, error) {
	start, err := accessRefBackfillStartIndex(after.ResourceKind)
	if err != nil {
		return nil, false, err
	}
	out := make([]AccessRefBackfillCandidate, 0, limit+1)
	for index := start; index < len(accessRefBackfillSpecs) && len(out) <= limit; index++ {
		spec := accessRefBackfillSpecs[index]
		afterTenant, afterID := "", ""
		if spec.kind == after.ResourceKind {
			afterTenant = strings.TrimSpace(after.TenantID)
			afterID = strings.TrimSpace(after.ResourceID)
		}
		remaining := limit + 1 - len(out)
		items, err := listUnresolvedAccessRefCandidatesForSpec(ctx, store, spec, afterTenant, afterID, remaining)
		if err != nil {
			return nil, false, err
		}
		out = append(out, items...)
	}
	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	return out, hasMore, nil
}

func accessRefBackfillStartIndex(kind AccessRefResourceKind) (int, error) {
	if kind == "" {
		return 0, nil
	}
	for index, spec := range accessRefBackfillSpecs {
		if spec.kind == kind {
			return index, nil
		}
	}
	return 0, fmt.Errorf("unknown access ref backfill cursor resource kind %q", kind)
}

func listUnresolvedAccessRefCandidatesForSpec(ctx context.Context, store *db.Store, spec accessRefBackfillSpec, afterTenant, afterID string, limit int) ([]AccessRefBackfillCandidate, error) {
	query := `SELECT tenant_id, ` + spec.idColumn + `, ` + spec.sourceColumns + `
FROM ` + spec.table + `
WHERE access_state = 'unresolved' AND (tenant_id > ? OR (tenant_id = ? AND ` + spec.idColumn + ` > ?))
ORDER BY tenant_id, ` + spec.idColumn + `
LIMIT ?`
	rows, err := store.Query(ctx, query, afterTenant, afterTenant, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("list unresolved %s access refs: %w", spec.kind, err)
	}
	defer rows.Close()
	out := make([]AccessRefBackfillCandidate, 0, limit)
	for rows.Next() {
		candidate := AccessRefBackfillCandidate{ResourceKind: spec.kind}
		if spec.sourceMode == "provenance" {
			var provenanceJSON string
			if err := rows.Scan(&candidate.TenantID, &candidate.ResourceID, &provenanceJSON); err != nil {
				return nil, fmt.Errorf("scan unresolved %s access ref: %w", spec.kind, err)
			}
			candidate.sourceValue = provenanceJSON
			var provenance Ref
			if err := json.Unmarshal([]byte(provenanceJSON), &provenance); err == nil {
				candidate.SourceKind = strings.TrimSpace(provenance.Kind)
				candidate.SourceID = strings.TrimSpace(provenance.ID)
			}
		} else {
			if err := rows.Scan(&candidate.TenantID, &candidate.ResourceID, &candidate.SourceKind, &candidate.SourceID); err != nil {
				return nil, fmt.Errorf("scan unresolved %s access ref: %w", spec.kind, err)
			}
		}
		out = append(out, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func applyResolvedAccessRef(ctx context.Context, tx db.Tx, candidate AccessRefBackfillCandidate, ref AccessRef, runID, changedBy string, changedAt int64) (bool, error) {
	spec, ok := accessRefBackfillSpecFor(candidate.ResourceKind)
	if !ok {
		return false, fmt.Errorf("unsupported access ref resource kind %q", candidate.ResourceKind)
	}
	query := `UPDATE ` + spec.table + ` SET
	` + spec.accessPrefix + `_kind = ?,
	` + spec.accessPrefix + `_id = ?,
	` + spec.accessPrefix + `_tenant_id = ?,
	access_state = 'resolved'
WHERE tenant_id = ? AND ` + spec.idColumn + ` = ? AND access_state = 'unresolved'`
	args := []any{ref.Kind, ref.ID, ref.TenantID, candidate.TenantID, candidate.ResourceID}
	if spec.sourceMode == "provenance" {
		query += " AND provenance_json = ?"
		args = append(args, candidate.sourceValue)
	} else {
		query += " AND " + spec.sourceKindCol + " = ? AND " + spec.sourceIDCol + " = ?"
		args = append(args, candidate.SourceKind, candidate.SourceID)
	}
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("backfill %s access ref: %w", candidate.ResourceKind, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected == 0 {
		return false, nil
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO context_graph_access_ref_backfill_audit (
	run_id, resource_kind, tenant_id, resource_id,
	access_kind, access_id, access_tenant_id,
	source_kind, source_id, changed_by, changed_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		runID, candidate.ResourceKind, candidate.TenantID, candidate.ResourceID,
		ref.Kind, ref.ID, ref.TenantID,
		candidate.SourceKind, candidate.SourceID, changedBy, changedAt,
	)
	if err != nil {
		return false, fmt.Errorf("audit %s access ref backfill: %w", candidate.ResourceKind, err)
	}
	return true, nil
}

func accessRefBackfillSpecFor(kind AccessRefResourceKind) (accessRefBackfillSpec, bool) {
	for _, spec := range accessRefBackfillSpecs {
		if spec.kind == kind {
			return spec, true
		}
	}
	return accessRefBackfillSpec{}, false
}
