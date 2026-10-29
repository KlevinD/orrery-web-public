package contextgraph

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const objectQueryColumns = `id, tenant_id, object_type, canonical_key, label, properties_json, sensitivity,
	confidence, status, provenance_json, evidence_refs_json,
	owner_access_kind, owner_access_id, owner_access_tenant_id, access_state,
	source_stream_id, source_version, temporal_json, updated_at`

const linkQueryColumns = `id, tenant_id, link_type, from_object_id, to_object_id, properties_json,
	confidence, status, provenance_json, evidence_refs_json,
	source_access_kind, source_access_id, source_access_tenant_id, access_state,
	source_stream_id, source_version, temporal_json, updated_at`

const factQueryColumns = `id, tenant_id, subject_kind, subject_id, predicate, value_json, properties_json,
	confidence, status, provenance_json, evidence_refs_json,
	source_access_kind, source_access_id, source_access_tenant_id, access_state,
	source_stream_id, source_version, temporal_json, updated_at`

const evidenceQueryColumns = `id, tenant_id, source_kind, source_id, summary, confidence,
	source_access_kind, source_access_id, source_access_tenant_id, access_state, observed_at`

const projectionAuditQueryColumns = `id, tenant_id, source_event_id, source_event_type, status, reason_code, reason, details_json,
	source_access_kind, source_access_id, source_access_tenant_id, access_state, observed_at`

const projectionReplayHistoryQueryColumns = `id, audit_id, tenant_id, source_event_id, source_event_type, status, error, details_json,
	source_access_kind, source_access_id, source_access_tenant_id, access_state, attempted_at`

type queryRowScanner interface {
	Scan(...any) error
}

func (s *PlatformStore) ListContextTenantIDs(ctx context.Context, limit int) (ContextTenantPage, error) {
	if !s.ready() {
		return ContextTenantPage{}, fmt.Errorf("context graph platform store is required")
	}
	if limit <= 0 || limit > MaxQueryPageSize {
		return ContextTenantPage{}, ErrQueryInvalid
	}
	rows, err := s.query(ctx, `
SELECT tenant_id
FROM (
	SELECT tenant_id FROM context_graph_objects WHERE access_state = 'resolved'
	UNION
	SELECT tenant_id FROM context_graph_links WHERE access_state = 'resolved'
	UNION
	SELECT tenant_id FROM context_graph_facts WHERE access_state = 'resolved'
	UNION
	SELECT tenant_id FROM context_graph_evidence WHERE access_state = 'resolved'
) AS context_tenants
ORDER BY tenant_id ASC
LIMIT ?`, limit+1)
	if err != nil {
		return ContextTenantPage{}, fmt.Errorf("list context graph tenants: %w", err)
	}
	defer rows.Close()
	tenantIDs := make([]string, 0, limit+1)
	for rows.Next() {
		var tenantID string
		if err := rows.Scan(&tenantID); err != nil {
			return ContextTenantPage{}, fmt.Errorf("scan context graph tenant: %w", err)
		}
		tenantID = strings.TrimSpace(tenantID)
		if !validTenantID(tenantID) {
			return ContextTenantPage{}, fmt.Errorf("%w: invalid context graph tenant", ErrQueryInvalid)
		}
		tenantIDs = append(tenantIDs, tenantID)
	}
	if err := rows.Err(); err != nil {
		return ContextTenantPage{}, fmt.Errorf("list context graph tenants: %w", err)
	}
	hasMore := len(tenantIDs) > limit
	if hasMore {
		tenantIDs = tenantIDs[:limit]
	}
	return ContextTenantPage{TenantIDs: tenantIDs, HasMore: hasMore}, nil
}

// ListContextScopeTenantIDs returns graph tenants backed by a canonical
// ontology scope source. It keeps company-level reads from probing unrelated
// and historical graph tenants one catalog at a time.
func (s *PlatformStore) ListContextScopeTenantIDs(ctx context.Context, limit int) (ContextTenantPage, error) {
	if !s.ready() {
		return ContextTenantPage{}, fmt.Errorf("context graph platform store is required")
	}
	if limit <= 0 || limit > MaxQueryPageSize {
		return ContextTenantPage{}, ErrQueryInvalid
	}
	rows, err := s.query(ctx, `
WITH context_tenants AS (
	SELECT tenant_id FROM context_graph_objects WHERE access_state = 'resolved'
	UNION
	SELECT tenant_id FROM context_graph_links WHERE access_state = 'resolved'
	UNION
	SELECT tenant_id FROM context_graph_facts WHERE access_state = 'resolved'
	UNION
	SELECT tenant_id FROM context_graph_evidence WHERE access_state = 'resolved'
)
SELECT tenant_id
FROM context_tenants
WHERE tenant_id = 'system'
	OR EXISTS (
		SELECT 1 FROM ontology_scope_bindings AS binding
		WHERE binding.tenant_id = context_tenants.tenant_id AND binding.state = 'active'
	)
	OR (
		tenant_id NOT LIKE 'project:%'
		AND tenant_id NOT LIKE 'mission:%'
		AND tenant_id NOT LIKE 'resource:%'
		AND tenant_id NOT LIKE 'tool:%'
		AND EXISTS (
			SELECT 1 FROM platform_records AS organization
			WHERE organization.table_name = 'operations_organizations'
				AND organization.record_key = context_tenants.tenant_id
		)
	)
	OR (
		tenant_id LIKE 'project:%/%'
		AND EXISTS (
			SELECT 1 FROM platform_records AS project
			WHERE project.table_name = 'projects'
				AND project.record_key = SUBSTR(context_tenants.tenant_id, 9)
		)
	)
	OR (
		tenant_id LIKE 'mission:%/%'
		AND EXISTS (
			SELECT 1 FROM platform_records AS mission
			WHERE mission.table_name = 'mission_control_projection'
				AND (
					mission.record_key = SUBSTR(context_tenants.tenant_id, 9)
					OR (context_tenants.tenant_id LIKE 'mission:global/%'
						AND mission.record_key = SUBSTR(context_tenants.tenant_id, 16))
				)
		)
	)
	OR (
		(tenant_id LIKE 'resource:%' AND EXISTS (
			SELECT 1 FROM platform_records AS resource
			WHERE resource.table_name = 'resource_tool_definitions'
				AND resource.record_key = SUBSTR(context_tenants.tenant_id, 10)
		))
		OR (tenant_id LIKE 'tool:%' AND EXISTS (
			SELECT 1 FROM platform_records AS tool
			WHERE tool.table_name = 'resource_tool_definitions'
				AND tool.record_key = SUBSTR(context_tenants.tenant_id, 6)
		))
	)
ORDER BY tenant_id ASC
LIMIT ?`, limit+1)
	if err != nil {
		return ContextTenantPage{}, fmt.Errorf("list context graph scope tenants: %w", err)
	}
	defer rows.Close()
	tenantIDs := make([]string, 0, limit+1)
	for rows.Next() {
		var tenantID string
		if err := rows.Scan(&tenantID); err != nil {
			return ContextTenantPage{}, fmt.Errorf("scan context graph scope tenant: %w", err)
		}
		tenantID = strings.TrimSpace(tenantID)
		if !validTenantID(tenantID) {
			return ContextTenantPage{}, fmt.Errorf("%w: invalid context graph scope tenant", ErrQueryInvalid)
		}
		tenantIDs = append(tenantIDs, tenantID)
	}
	if err := rows.Err(); err != nil {
		return ContextTenantPage{}, fmt.Errorf("list context graph scope tenants: %w", err)
	}
	hasMore := len(tenantIDs) > limit
	if hasMore {
		tenantIDs = tenantIDs[:limit]
	}
	return ContextTenantPage{TenantIDs: tenantIDs, HasMore: hasMore}, nil
}

func (s *PlatformStore) ListContextEntityAccessRefs(ctx context.Context, query AccessRefQuery) (AccessRefPage, error) {
	if !s.ready() {
		return AccessRefPage{}, fmt.Errorf("context graph platform store is required")
	}
	query.TenantID = strings.TrimSpace(query.TenantID)
	query.After = normalizeAccessRefSeek(query.After)
	if !validTenantID(query.TenantID) || query.Limit <= 0 || query.Limit > MaxQueryPageSize || !validAccessRefSeek(query.After) {
		return AccessRefPage{}, ErrQueryInvalid
	}
	// Bound each indexed source before the final union. Any value in the first
	// N union results must also occur in the first N values of one source.
	branchLimit := query.Limit + 1
	rows, err := s.query(ctx, `
WITH object_refs AS (
	SELECT DISTINCT owner_access_kind AS access_kind, owner_access_id AS access_id, owner_access_tenant_id AS access_tenant_id
	FROM context_graph_objects
	WHERE tenant_id = ? AND access_state = 'resolved' AND (
		owner_access_kind > ?
		OR (owner_access_kind = ? AND owner_access_id > ?)
		OR (owner_access_kind = ? AND owner_access_id = ? AND owner_access_tenant_id > ?)
	)
	ORDER BY owner_access_kind ASC, owner_access_id ASC, owner_access_tenant_id ASC
	LIMIT ?
), link_refs AS (
	SELECT DISTINCT source_access_kind AS access_kind, source_access_id AS access_id, source_access_tenant_id AS access_tenant_id
	FROM context_graph_links
	WHERE tenant_id = ? AND access_state = 'resolved' AND (
		source_access_kind > ?
		OR (source_access_kind = ? AND source_access_id > ?)
		OR (source_access_kind = ? AND source_access_id = ? AND source_access_tenant_id > ?)
	)
	ORDER BY source_access_kind ASC, source_access_id ASC, source_access_tenant_id ASC
	LIMIT ?
), fact_refs AS (
	SELECT DISTINCT source_access_kind AS access_kind, source_access_id AS access_id, source_access_tenant_id AS access_tenant_id
	FROM context_graph_facts
	WHERE tenant_id = ? AND access_state = 'resolved' AND (
		source_access_kind > ?
		OR (source_access_kind = ? AND source_access_id > ?)
		OR (source_access_kind = ? AND source_access_id = ? AND source_access_tenant_id > ?)
	)
	ORDER BY source_access_kind ASC, source_access_id ASC, source_access_tenant_id ASC
	LIMIT ?
)
SELECT access_kind, access_id, access_tenant_id
FROM (
	SELECT * FROM object_refs
	UNION
	SELECT * FROM link_refs
	UNION
	SELECT * FROM fact_refs
) AS visible_access_refs
ORDER BY access_kind ASC, access_id ASC, access_tenant_id ASC
LIMIT ?`,
		query.TenantID,
		query.After.Kind,
		query.After.Kind, query.After.ID,
		query.After.Kind, query.After.ID, query.After.TenantID,
		branchLimit,
		query.TenantID,
		query.After.Kind,
		query.After.Kind, query.After.ID,
		query.After.Kind, query.After.ID, query.After.TenantID,
		branchLimit,
		query.TenantID,
		query.After.Kind,
		query.After.Kind, query.After.ID,
		query.After.Kind, query.After.ID, query.After.TenantID,
		branchLimit,
		branchLimit,
	)
	if err != nil {
		return AccessRefPage{}, fmt.Errorf("list context graph access references: %w", err)
	}
	defer rows.Close()
	items := make([]AccessRef, 0, query.Limit+1)
	for rows.Next() {
		var ref AccessRef
		if err := rows.Scan(&ref.Kind, &ref.ID, &ref.TenantID); err != nil {
			return AccessRefPage{}, fmt.Errorf("scan context graph access reference: %w", err)
		}
		ref = NormalizeAccessRef(ref)
		if !ref.Resolved() {
			return AccessRefPage{}, fmt.Errorf("%w: resolved access reference is incomplete", ErrAccessRefConflict)
		}
		items = append(items, ref)
	}
	if err := rows.Err(); err != nil {
		return AccessRefPage{}, fmt.Errorf("list context graph access references: %w", err)
	}
	hasMore := len(items) > query.Limit
	if hasMore {
		items = items[:query.Limit]
	}
	return AccessRefPage{Items: items, HasMore: hasMore}, nil
}

func (s *PlatformStore) ListProjectionAccessRefs(ctx context.Context, query AccessRefQuery) (AccessRefPage, error) {
	if !s.ready() {
		return AccessRefPage{}, fmt.Errorf("context graph platform store is required")
	}
	query.TenantID = strings.TrimSpace(query.TenantID)
	query.After = normalizeAccessRefSeek(query.After)
	if !validTenantID(query.TenantID) || query.Limit <= 0 || query.Limit > MaxQueryPageSize || !validAccessRefSeek(query.After) {
		return AccessRefPage{}, ErrQueryInvalid
	}
	rows, err := s.query(ctx, `
SELECT DISTINCT source_access_kind, source_access_id, source_access_tenant_id
FROM context_graph_projection_audits
WHERE tenant_id = ? AND access_state = 'resolved' AND (
	source_access_kind > ?
	OR (source_access_kind = ? AND source_access_id > ?)
	OR (source_access_kind = ? AND source_access_id = ? AND source_access_tenant_id > ?)
)
ORDER BY source_access_kind ASC, source_access_id ASC, source_access_tenant_id ASC
LIMIT ?`,
		query.TenantID,
		query.After.Kind,
		query.After.Kind, query.After.ID,
		query.After.Kind, query.After.ID, query.After.TenantID,
		query.Limit+1,
	)
	if err != nil {
		return AccessRefPage{}, fmt.Errorf("list projection access references: %w", err)
	}
	defer rows.Close()
	items := make([]AccessRef, 0, query.Limit+1)
	for rows.Next() {
		var ref AccessRef
		if err := rows.Scan(&ref.Kind, &ref.ID, &ref.TenantID); err != nil {
			return AccessRefPage{}, fmt.Errorf("scan projection access reference: %w", err)
		}
		ref = NormalizeAccessRef(ref)
		if !ref.Resolved() {
			return AccessRefPage{}, fmt.Errorf("%w: resolved projection access reference is incomplete", ErrAccessRefConflict)
		}
		items = append(items, ref)
	}
	if err := rows.Err(); err != nil {
		return AccessRefPage{}, fmt.Errorf("list projection access references: %w", err)
	}
	hasMore := len(items) > query.Limit
	if hasMore {
		items = items[:query.Limit]
	}
	return AccessRefPage{Items: items, HasMore: hasMore}, nil
}

func (s *PlatformStore) CountVisibleContext(ctx context.Context, tenantID string, constraint VisibilityConstraint) (VisibleContextCounts, error) {
	if !s.ready() {
		return VisibleContextCounts{}, fmt.Errorf("context graph platform store is required")
	}
	tenantID = strings.TrimSpace(tenantID)
	constraint, err := normalizeVisibilityConstraint(constraint)
	if !validTenantID(tenantID) || err != nil || constraint.candidateScan {
		return VisibleContextCounts{}, ErrQueryInvalid
	}
	result := VisibleContextCounts{}
	type counter func(context.Context, string, VisibilityConstraint) (int64, *time.Time, *time.Time, error)
	queries := []struct {
		count  counter
		assign func(int64)
	}{
		{func(ctx context.Context, tenantID string, constraint VisibilityConstraint) (int64, *time.Time, *time.Time, error) {
			return s.countVisibleRows(ctx, tenantID, constraint, "context_graph_objects", "owner_access_kind", "owner_access_id", "owner_access_tenant_id", "updated_at")
		}, func(value int64) { result.Objects = value }},
		{s.countVisibleLinks, func(value int64) { result.Links = value }},
		{s.countVisibleFacts, func(value int64) { result.Facts = value }},
	}
	for _, query := range queries {
		count, oldest, newest, err := query.count(ctx, tenantID, constraint)
		if err != nil {
			return VisibleContextCounts{}, err
		}
		query.assign(count)
		mergeTimeBounds(&result, oldest, newest)
	}
	return result, nil
}

func (s *PlatformStore) ReadContextOverviewGroups(ctx context.Context, tenantID string) ([]ContextOverviewGroup, error) {
	if !s.ready() {
		return nil, fmt.Errorf("context graph platform store is required")
	}
	tenantID = strings.TrimSpace(tenantID)
	if !validTenantID(tenantID) {
		return nil, ErrQueryInvalid
	}
	specs := []struct {
		kind      ContextOverviewGroupKind
		label     string
		statement string
	}{
		{ContextOverviewObject, "object", `
SELECT object_type, COUNT(*), MIN(updated_at), MAX(updated_at),
	owner_access_kind, owner_access_id, owner_access_tenant_id,
	'', '', '', '', '', ''
FROM context_graph_objects
WHERE tenant_id = ? AND access_state = 'resolved'
GROUP BY object_type, owner_access_kind, owner_access_id, owner_access_tenant_id
ORDER BY object_type, owner_access_kind, owner_access_id, owner_access_tenant_id
LIMIT ?`},
		{ContextOverviewFact, "fact", `
SELECT '', COUNT(*), MIN(f.updated_at), MAX(f.updated_at),
	f.source_access_kind, f.source_access_id, f.source_access_tenant_id,
	o.owner_access_kind, o.owner_access_id, o.owner_access_tenant_id,
	'', '', ''
FROM context_graph_facts f
JOIN context_graph_objects o
	ON o.tenant_id = f.tenant_id AND o.id = f.subject_id AND o.object_type = f.subject_kind
WHERE f.tenant_id = ? AND f.access_state = 'resolved' AND o.access_state = 'resolved'
GROUP BY f.source_access_kind, f.source_access_id, f.source_access_tenant_id,
	o.owner_access_kind, o.owner_access_id, o.owner_access_tenant_id
ORDER BY f.source_access_kind, f.source_access_id, f.source_access_tenant_id,
	o.owner_access_kind, o.owner_access_id, o.owner_access_tenant_id
LIMIT ?`},
		{ContextOverviewLink, "relation", `
SELECT '', COUNT(*), MIN(l.updated_at), MAX(l.updated_at),
	l.source_access_kind, l.source_access_id, l.source_access_tenant_id,
	f.owner_access_kind, f.owner_access_id, f.owner_access_tenant_id,
	t.owner_access_kind, t.owner_access_id, t.owner_access_tenant_id
FROM context_graph_links l
JOIN context_graph_objects f ON f.tenant_id = l.tenant_id AND f.id = l.from_object_id
JOIN context_graph_objects t ON t.tenant_id = l.tenant_id AND t.id = l.to_object_id
WHERE l.tenant_id = ? AND l.access_state = 'resolved'
	AND f.access_state = 'resolved' AND t.access_state = 'resolved'
GROUP BY l.source_access_kind, l.source_access_id, l.source_access_tenant_id,
	f.owner_access_kind, f.owner_access_id, f.owner_access_tenant_id,
	t.owner_access_kind, t.owner_access_id, t.owner_access_tenant_id
ORDER BY l.source_access_kind, l.source_access_id, l.source_access_tenant_id,
	f.owner_access_kind, f.owner_access_id, f.owner_access_tenant_id,
	t.owner_access_kind, t.owner_access_id, t.owner_access_tenant_id
LIMIT ?`},
		{ContextOverviewEvidence, "evidence", `
SELECT '', COUNT(*), MIN(observed_at), MAX(observed_at),
	source_access_kind, source_access_id, source_access_tenant_id,
	'', '', '', '', '', ''
FROM context_graph_evidence
WHERE tenant_id = ? AND access_state = 'resolved'
GROUP BY source_access_kind, source_access_id, source_access_tenant_id
ORDER BY source_access_kind, source_access_id, source_access_tenant_id
LIMIT ?`},
	}
	groupsByKind := make([][]ContextOverviewGroup, len(specs))
	read := func(readCtx context.Context, index int) error {
		spec := specs[index]
		items, err := s.readContextOverviewGroupRows(readCtx, tenantID, spec.kind, spec.label, spec.statement, MaxContextOverviewGroups)
		if err == nil {
			groupsByKind[index] = items
		}
		return err
	}

	// A database transaction owns one connection and must be read serially.
	// Store-backed overview queries are independent and can share the bounded
	// database pool, cutting the enterprise aggregation critical path.
	if s.tx != nil {
		for index := range specs {
			if err := read(ctx, index); err != nil {
				return nil, err
			}
		}
	} else {
		readCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		var wg sync.WaitGroup
		var firstErr error
		var firstErrOnce sync.Once
		for index := range specs {
			wg.Add(1)
			go func(index int) {
				defer wg.Done()
				if err := read(readCtx, index); err != nil {
					firstErrOnce.Do(func() {
						firstErr = err
						cancel()
					})
				}
			}(index)
		}
		wg.Wait()
		if firstErr != nil {
			return nil, firstErr
		}
	}

	groups := make([]ContextOverviewGroup, 0)
	for _, items := range groupsByKind {
		if len(groups)+len(items) > MaxContextOverviewGroups {
			return nil, fmt.Errorf("%w: context overview group limit exceeded", ErrQueryInvalid)
		}
		groups = append(groups, items...)
	}
	return groups, nil
}

func (s *PlatformStore) readContextOverviewGroupRows(ctx context.Context, tenantID string, kind ContextOverviewGroupKind, label, statement string, limit int) ([]ContextOverviewGroup, error) {
	rows, err := s.query(ctx, statement, tenantID, limit+1)
	if err != nil {
		return nil, fmt.Errorf("read context overview %s groups: %w", label, err)
	}
	defer rows.Close()
	items := make([]ContextOverviewGroup, 0)
	for rows.Next() {
		var item ContextOverviewGroup
		var oldestRaw, newestRaw sql.NullInt64
		refs := make([]AccessRef, 3)
		if err := rows.Scan(
			&item.ObjectType, &item.Count, &oldestRaw, &newestRaw,
			&refs[0].Kind, &refs[0].ID, &refs[0].TenantID,
			&refs[1].Kind, &refs[1].ID, &refs[1].TenantID,
			&refs[2].Kind, &refs[2].ID, &refs[2].TenantID,
		); err != nil {
			return nil, fmt.Errorf("scan context overview %s group: %w", label, err)
		}
		item.Kind = kind
		item.OldestAt = nullableGraphTime(oldestRaw)
		item.NewestAt = nullableGraphTime(newestRaw)
		item.AccessRefs, err = normalizeContextOverviewAccessRefs(refs)
		if err != nil || item.Count < 1 {
			return nil, fmt.Errorf("%w: invalid context overview %s group", ErrQueryInvalid, label)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read context overview %s groups: %w", label, err)
	}
	if len(items) > limit {
		return nil, fmt.Errorf("%w: context overview group limit exceeded", ErrQueryInvalid)
	}
	return items, nil
}

func normalizeContextOverviewAccessRefs(values []AccessRef) ([]AccessRef, error) {
	refs := make([]AccessRef, 0, len(values))
	seen := make(map[AccessRef]bool, len(values))
	for _, value := range values {
		value = NormalizeAccessRef(value)
		if value == (AccessRef{}) {
			continue
		}
		if !value.Resolved() {
			return nil, ErrAccessRefConflict
		}
		if !seen[value] {
			seen[value] = true
			refs = append(refs, value)
		}
	}
	if len(refs) == 0 {
		return nil, ErrAccessRefConflict
	}
	return refs, nil
}

func (s *PlatformStore) countVisibleLinks(ctx context.Context, tenantID string, constraint VisibilityConstraint) (int64, *time.Time, *time.Time, error) {
	linkPredicate, linkArgs := visibilityPredicate("l.source_access_kind", "l.source_access_id", "l.source_access_tenant_id", constraint)
	fromPredicate, fromArgs := visibilityPredicate("f.owner_access_kind", "f.owner_access_id", "f.owner_access_tenant_id", constraint)
	toPredicate, toArgs := visibilityPredicate("t.owner_access_kind", "t.owner_access_id", "t.owner_access_tenant_id", constraint)
	args := []any{tenantID}
	args = append(args, linkArgs...)
	args = append(args, fromArgs...)
	args = append(args, toArgs...)
	return s.scanVisibleCount(ctx, `
SELECT COUNT(*), MIN(l.updated_at), MAX(l.updated_at)
FROM context_graph_links l
JOIN context_graph_objects f ON f.tenant_id = l.tenant_id AND f.id = l.from_object_id
JOIN context_graph_objects t ON t.tenant_id = l.tenant_id AND t.id = l.to_object_id
WHERE l.tenant_id = ? AND l.access_state = 'resolved'
	AND f.access_state = 'resolved' AND t.access_state = 'resolved'
	AND `+linkPredicate+` AND `+fromPredicate+` AND `+toPredicate, args, "context_graph_links")
}

func (s *PlatformStore) countVisibleFacts(ctx context.Context, tenantID string, constraint VisibilityConstraint) (int64, *time.Time, *time.Time, error) {
	factPredicate, factArgs := visibilityPredicate("f.source_access_kind", "f.source_access_id", "f.source_access_tenant_id", constraint)
	subjectPredicate, subjectArgs := visibilityPredicate("o.owner_access_kind", "o.owner_access_id", "o.owner_access_tenant_id", constraint)
	args := []any{tenantID}
	args = append(args, factArgs...)
	args = append(args, subjectArgs...)
	return s.scanVisibleCount(ctx, `
SELECT COUNT(*), MIN(f.updated_at), MAX(f.updated_at)
FROM context_graph_facts f
JOIN context_graph_objects o ON o.tenant_id = f.tenant_id AND o.id = f.subject_id AND o.object_type = f.subject_kind
WHERE f.tenant_id = ? AND f.access_state = 'resolved' AND o.access_state = 'resolved'
	AND `+factPredicate+` AND `+subjectPredicate, args, "context_graph_facts")
}

func (s *PlatformStore) countVisibleRows(ctx context.Context, tenantID string, constraint VisibilityConstraint, table, kindColumn, idColumn, tenantColumn, timeColumn string) (int64, *time.Time, *time.Time, error) {
	predicate, args := visibilityPredicate(kindColumn, idColumn, tenantColumn, constraint)
	queryArgs := []any{tenantID}
	queryArgs = append(queryArgs, args...)
	statement := fmt.Sprintf(`SELECT COUNT(*), MIN(%s), MAX(%s) FROM %s WHERE tenant_id = ? AND access_state = 'resolved' AND %s`, timeColumn, timeColumn, table, predicate)
	return s.scanVisibleCount(ctx, statement, queryArgs, table)
}

func (s *PlatformStore) scanVisibleCount(ctx context.Context, statement string, args []any, label string) (int64, *time.Time, *time.Time, error) {
	var count int64
	var oldestRaw, newestRaw sql.NullInt64
	if err := s.queryRow(ctx, statement, args...).Scan(&count, &oldestRaw, &newestRaw); err != nil {
		return 0, nil, nil, fmt.Errorf("count visible %s: %w", label, err)
	}
	return count, nullableGraphTime(oldestRaw), nullableGraphTime(newestRaw), nil
}

func (s *PlatformStore) ListVisibleObjects(ctx context.Context, query ObjectQuery) (ObjectPage, error) {
	if !s.ready() {
		return ObjectPage{}, fmt.Errorf("context graph platform store is required")
	}
	query, err := normalizeObjectQuery(query)
	if err != nil {
		return ObjectPage{}, err
	}
	predicate, visibilityArgs := visibilityPredicate("owner_access_kind", "owner_access_id", "owner_access_tenant_id", query.Visibility)
	conditions := []string{"tenant_id = ?", "access_state = 'resolved'", predicate}
	args := []any{query.TenantID}
	args = append(args, visibilityArgs...)
	if query.ObjectType != "" {
		conditions = append(conditions, "object_type = ?")
		args = append(args, query.ObjectType)
	}
	if query.Status != "" {
		conditions = append(conditions, "status = ?")
		args = append(args, query.Status)
	}
	if query.SourceKind != "" {
		conditions = append(conditions, "owner_access_kind = ?")
		args = append(args, query.SourceKind)
	}
	if query.Search != "" {
		conditions = append(conditions, `(LOWER(id) LIKE ? ESCAPE '!' OR LOWER(label) LIKE ? ESCAPE '!' OR LOWER(canonical_key) LIKE ? ESCAPE '!')`)
		pattern := "%" + escapeLike(strings.ToLower(query.Search)) + "%"
		args = append(args, pattern, pattern, pattern)
	}
	if query.After.ID != "" {
		conditions = append(conditions, "(updated_at < ? OR (updated_at = ? AND id > ?))")
		seek := query.After.UpdatedAt.UTC().UnixNano()
		args = append(args, seek, seek, query.After.ID)
	}
	args = append(args, query.Limit+1)
	rows, err := s.query(ctx, `SELECT `+objectQueryColumns+` FROM context_graph_objects WHERE `+strings.Join(conditions, " AND ")+` ORDER BY updated_at DESC, id ASC LIMIT ?`, args...)
	if err != nil {
		return ObjectPage{}, fmt.Errorf("list visible context graph objects: %w", err)
	}
	defer rows.Close()
	items := make([]Object, 0, query.Limit+1)
	for rows.Next() {
		item, err := scanQueryObject(rows)
		if err != nil {
			return ObjectPage{}, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return ObjectPage{}, fmt.Errorf("list visible context graph objects: %w", err)
	}
	hasMore := len(items) > query.Limit
	if hasMore {
		items = items[:query.Limit]
	}
	return ObjectPage{Items: items, HasMore: hasMore}, nil
}

func (s *PlatformStore) GetVisibleObject(ctx context.Context, tenantID, objectID string, constraint VisibilityConstraint) (Object, error) {
	if !s.ready() {
		return Object{}, fmt.Errorf("context graph platform store is required")
	}
	tenantID, objectID = strings.TrimSpace(tenantID), strings.TrimSpace(objectID)
	constraint, err := normalizeVisibilityConstraint(constraint)
	if !validTenantID(tenantID) || !validQueryID(objectID) || err != nil {
		return Object{}, ErrQueryInvalid
	}
	predicate, visibilityArgs := visibilityPredicate("owner_access_kind", "owner_access_id", "owner_access_tenant_id", constraint)
	args := []any{tenantID, objectID}
	args = append(args, visibilityArgs...)
	item, err := scanQueryObject(s.queryRow(ctx, `SELECT `+objectQueryColumns+` FROM context_graph_objects WHERE tenant_id = ? AND id = ? AND access_state = 'resolved' AND `+predicate, args...))
	if err == sql.ErrNoRows {
		return Object{}, ErrNotFound
	}
	if err != nil {
		return Object{}, err
	}
	return item, nil
}

func (s *PlatformStore) GetVisibleFact(ctx context.Context, tenantID, factID string, constraint VisibilityConstraint) (FactDetail, error) {
	if !s.ready() {
		return FactDetail{}, fmt.Errorf("context graph platform store is required")
	}
	tenantID, factID = strings.TrimSpace(tenantID), strings.TrimSpace(factID)
	constraint, err := normalizeVisibilityConstraint(constraint)
	if !validTenantID(tenantID) || !validQueryID(factID) || err != nil {
		return FactDetail{}, ErrQueryInvalid
	}
	factPredicate, factArgs := visibilityPredicate("f.source_access_kind", "f.source_access_id", "f.source_access_tenant_id", constraint)
	subjectPredicate, subjectArgs := visibilityPredicate("o.owner_access_kind", "o.owner_access_id", "o.owner_access_tenant_id", constraint)
	args := []any{tenantID, factID}
	args = append(args, factArgs...)
	args = append(args, subjectArgs...)
	var fact factQueryScan
	var subject objectQueryScan
	scanArgs := append(fact.args(), subject.args()...)
	err = s.queryRow(ctx, `SELECT `+qualifiedQueryColumns("f", factQueryColumns)+`, `+qualifiedQueryColumns("o", objectQueryColumns)+`
FROM context_graph_facts f
JOIN context_graph_objects o ON o.tenant_id = f.tenant_id AND o.id = f.subject_id AND o.object_type = f.subject_kind
WHERE f.tenant_id = ? AND f.id = ? AND f.access_state = 'resolved' AND o.access_state = 'resolved'
	AND `+factPredicate+` AND `+subjectPredicate, args...).Scan(scanArgs...)
	if errors.Is(err, sql.ErrNoRows) {
		return FactDetail{}, ErrNotFound
	}
	if err != nil {
		return FactDetail{}, fmt.Errorf("get visible context graph fact: %w", err)
	}
	return FactDetail{Fact: fact.value(), Subject: subject.value()}, nil
}

func (s *PlatformStore) ListVisibleLineageFacts(ctx context.Context, query VisibleFactQuery) (FactPage, error) {
	if !s.ready() {
		return FactPage{}, fmt.Errorf("context graph platform store is required")
	}
	query, err := normalizeVisibleFactQuery(query)
	if err != nil {
		return FactPage{}, err
	}
	factPredicate, factArgs := visibilityPredicate("f.source_access_kind", "f.source_access_id", "f.source_access_tenant_id", query.Visibility)
	subjectPredicate, subjectArgs := visibilityPredicate("o.owner_access_kind", "o.owner_access_id", "o.owner_access_tenant_id", query.Visibility)
	conditions := []string{"f.tenant_id = ?", "f.access_state = 'resolved'", "o.access_state = 'resolved'", factPredicate, subjectPredicate}
	args := []any{query.TenantID}
	args = append(args, factArgs...)
	args = append(args, subjectArgs...)
	if query.After.ID != "" {
		conditions = append(conditions, "(f.updated_at < ? OR (f.updated_at = ? AND f.id > ?))")
		seek := query.After.UpdatedAt.UTC().UnixNano()
		args = append(args, seek, seek, query.After.ID)
	}
	args = append(args, query.Limit+1)
	rows, err := s.query(ctx, `SELECT `+qualifiedQueryColumns("f", factQueryColumns)+`, o.owner_access_kind, o.owner_access_id, o.owner_access_tenant_id
FROM context_graph_facts f
JOIN context_graph_objects o ON o.tenant_id = f.tenant_id AND o.id = f.subject_id AND o.object_type = f.subject_kind
WHERE `+strings.Join(conditions, " AND ")+` ORDER BY f.updated_at DESC, f.id ASC LIMIT ?`, args...)
	if err != nil {
		return FactPage{}, fmt.Errorf("list visible lineage facts: %w", err)
	}
	defer rows.Close()
	items := make([]Fact, 0, query.Limit+1)
	for rows.Next() {
		item, err := scanQueryLineageFact(rows)
		if err != nil {
			return FactPage{}, fmt.Errorf("scan visible lineage fact: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return FactPage{}, fmt.Errorf("list visible lineage facts: %w", err)
	}
	hasMore := len(items) > query.Limit
	if hasMore {
		items = items[:query.Limit]
	}
	return FactPage{Items: items, HasMore: hasMore}, nil
}

func (s *PlatformStore) ListVisibleFactAssertions(ctx context.Context, query FactAssertionQuery) (FactAssertionPage, error) {
	if !s.ready() {
		return FactAssertionPage{}, fmt.Errorf("context graph platform store is required")
	}
	query, err := normalizeFactAssertionQuery(query)
	if err != nil {
		return FactAssertionPage{}, err
	}
	assertionPredicate, assertionArgs := visibilityPredicate("a.source_access_kind", "a.source_access_id", "a.source_access_tenant_id", query.Visibility)
	currentPredicate, currentArgs := visibilityPredicate("f.source_access_kind", "f.source_access_id", "f.source_access_tenant_id", query.Visibility)
	currentSubjectPredicate, currentSubjectArgs := visibilityPredicate("co.owner_access_kind", "co.owner_access_id", "co.owner_access_tenant_id", query.Visibility)
	assertionSubjectPredicate, assertionSubjectArgs := visibilityPredicate("ao.owner_access_kind", "ao.owner_access_id", "ao.owner_access_tenant_id", query.Visibility)
	conditions := []string{
		"a.tenant_id = ?", "a.fact_id = ?", "a.access_state = 'resolved'", "f.access_state = 'resolved'",
		"co.access_state = 'resolved'", "ao.access_state = 'resolved'", assertionPredicate, currentPredicate,
		currentSubjectPredicate, assertionSubjectPredicate,
	}
	args := []any{query.TenantID, query.FactID}
	args = append(args, assertionArgs...)
	args = append(args, currentArgs...)
	args = append(args, currentSubjectArgs...)
	args = append(args, assertionSubjectArgs...)
	if query.After.ID != "" {
		conditions = append(conditions, "(a.observed_at < ? OR (a.observed_at = ? AND a.assertion_id < ?))")
		seek := query.After.ObservedAt.UTC().UnixNano()
		args = append(args, seek, seek, query.After.ID)
	}
	args = append(args, query.Limit+1)
	rows, err := s.query(ctx, `SELECT a.assertion_id, `+assertionFactQueryColumns("a")+`, ao.owner_access_kind, ao.owner_access_id, ao.owner_access_tenant_id, a.observed_at
FROM context_graph_fact_assertions a
JOIN context_graph_facts f ON f.tenant_id = a.tenant_id AND f.id = a.fact_id
JOIN context_graph_objects co ON co.tenant_id = f.tenant_id AND co.id = f.subject_id AND co.object_type = f.subject_kind
JOIN context_graph_objects ao ON ao.tenant_id = a.tenant_id AND ao.id = a.subject_id AND ao.object_type = a.subject_kind
WHERE `+strings.Join(conditions, " AND ")+` ORDER BY a.observed_at DESC, a.assertion_id DESC LIMIT ?`, args...)
	if err != nil {
		return FactAssertionPage{}, fmt.Errorf("list visible fact assertions: %w", err)
	}
	defer rows.Close()
	items := make([]FactAssertion, 0, query.Limit+1)
	for rows.Next() {
		item, err := scanFactAssertion(rows)
		if err != nil {
			return FactAssertionPage{}, fmt.Errorf("scan visible fact assertion: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return FactAssertionPage{}, fmt.Errorf("list visible fact assertions: %w", err)
	}
	hasMore := len(items) > query.Limit
	if hasMore {
		items = items[:query.Limit]
	}
	return FactAssertionPage{Items: items, HasMore: hasMore}, nil
}

func (s *PlatformStore) ListVisibleFacts(ctx context.Context, query FactQuery) (FactPage, error) {
	if !s.ready() {
		return FactPage{}, fmt.Errorf("context graph platform store is required")
	}
	query, err := normalizeFactQuery(query)
	if err != nil {
		return FactPage{}, err
	}
	factPredicate, factArgs := visibilityPredicate("f.source_access_kind", "f.source_access_id", "f.source_access_tenant_id", query.Visibility)
	subjectPredicate, subjectArgs := visibilityPredicate("o.owner_access_kind", "o.owner_access_id", "o.owner_access_tenant_id", query.Visibility)
	conditions := []string{
		"f.tenant_id = ?", "f.subject_kind = ?", "f.subject_id = ?", "f.access_state = 'resolved'", "o.access_state = 'resolved'",
		factPredicate, subjectPredicate,
	}
	args := []any{query.TenantID, query.ObjectType, query.ObjectID}
	args = append(args, factArgs...)
	args = append(args, subjectArgs...)
	if query.After.ID != "" {
		conditions = append(conditions, "(f.updated_at < ? OR (f.updated_at = ? AND f.id > ?))")
		seek := query.After.UpdatedAt.UTC().UnixNano()
		args = append(args, seek, seek, query.After.ID)
	}
	args = append(args, query.Limit+1)
	rows, err := s.query(ctx, `SELECT `+qualifiedQueryColumns("f", factQueryColumns)+`
FROM context_graph_facts f
JOIN context_graph_objects o ON o.tenant_id = f.tenant_id AND o.id = f.subject_id AND o.object_type = f.subject_kind
WHERE `+strings.Join(conditions, " AND ")+` ORDER BY f.updated_at DESC, f.id ASC LIMIT ?`, args...)
	if err != nil {
		return FactPage{}, fmt.Errorf("list visible context graph facts: %w", err)
	}
	defer rows.Close()
	items := make([]Fact, 0, query.Limit+1)
	for rows.Next() {
		item, err := scanQueryFact(rows)
		if err != nil {
			return FactPage{}, fmt.Errorf("scan visible context graph fact: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return FactPage{}, fmt.Errorf("list visible context graph facts: %w", err)
	}
	hasMore := len(items) > query.Limit
	if hasMore {
		items = items[:query.Limit]
	}
	return FactPage{Items: items, HasMore: hasMore}, nil
}

func (s *PlatformStore) ListVisibleRelations(ctx context.Context, query RelationQuery) (RelationPage, error) {
	if !s.ready() {
		return RelationPage{}, fmt.Errorf("context graph platform store is required")
	}
	query, err := normalizeRelationQuery(query)
	if err != nil {
		return RelationPage{}, err
	}
	linkPredicate, linkArgs := visibilityPredicate("l.source_access_kind", "l.source_access_id", "l.source_access_tenant_id", query.Visibility)
	fromPredicate, fromArgs := visibilityPredicate("f.owner_access_kind", "f.owner_access_id", "f.owner_access_tenant_id", query.Visibility)
	toPredicate, toArgs := visibilityPredicate("t.owner_access_kind", "t.owner_access_id", "t.owner_access_tenant_id", query.Visibility)
	conditions := []string{
		"l.tenant_id = ?", "l.access_state = 'resolved'", "f.access_state = 'resolved'", "t.access_state = 'resolved'",
		linkPredicate, fromPredicate, toPredicate,
	}
	args := []any{query.TenantID}
	args = append(args, linkArgs...)
	args = append(args, fromArgs...)
	args = append(args, toArgs...)
	switch query.Direction {
	case RelationIncoming:
		conditions = append(conditions, "l.to_object_id = ?")
		args = append(args, query.ObjectID)
	case RelationOutgoing:
		conditions = append(conditions, "l.from_object_id = ?")
		args = append(args, query.ObjectID)
	default:
		conditions = append(conditions, "(l.from_object_id = ? OR l.to_object_id = ?)")
		args = append(args, query.ObjectID, query.ObjectID)
	}
	if query.LinkType != "" {
		conditions = append(conditions, "l.link_type = ?")
		args = append(args, query.LinkType)
	}
	if query.After.ID != "" {
		conditions = append(conditions, "(l.updated_at < ? OR (l.updated_at = ? AND l.id > ?))")
		seek := query.After.UpdatedAt.UTC().UnixNano()
		args = append(args, seek, seek, query.After.ID)
	}
	args = append(args, query.Limit+1)
	rows, err := s.query(ctx, relationSelectSQL()+` WHERE `+strings.Join(conditions, " AND ")+` ORDER BY l.updated_at DESC, l.id ASC LIMIT ?`, args...)
	if err != nil {
		return RelationPage{}, fmt.Errorf("list visible context graph relations: %w", err)
	}
	defer rows.Close()
	items := make([]Relation, 0, query.Limit+1)
	for rows.Next() {
		link, from, to, err := scanQueryRelationRow(rows)
		if err != nil {
			return RelationPage{}, fmt.Errorf("scan visible context graph relation: %w", err)
		}
		direction, other := RelationIncoming, from
		if link.FromObjectID == query.ObjectID {
			direction, other = RelationOutgoing, to
		}
		items = append(items, Relation{Link: link, OtherObject: other, Direction: direction})
	}
	if err := rows.Err(); err != nil {
		return RelationPage{}, fmt.Errorf("list visible context graph relations: %w", err)
	}
	hasMore := len(items) > query.Limit
	if hasMore {
		items = items[:query.Limit]
	}
	return RelationPage{Items: items, HasMore: hasMore}, nil
}

func (s *PlatformStore) ListVisibleEvidence(ctx context.Context, query EvidenceQuery) (EvidencePage, error) {
	if !s.ready() {
		return EvidencePage{}, fmt.Errorf("context graph platform store is required")
	}
	query, err := normalizeEvidenceQuery(query)
	if err != nil {
		return EvidencePage{}, err
	}
	predicate, visibilityArgs := visibilityPredicate("source_access_kind", "source_access_id", "source_access_tenant_id", query.Visibility)
	conditions := []string{"tenant_id = ?", "access_state = 'resolved'", predicate}
	args := []any{query.TenantID}
	args = append(args, visibilityArgs...)
	selectors := make([]string, 0, len(query.SourceRefs)+2)
	if query.SourceKind != "" {
		selectors = append(selectors, "(source_kind = ? AND source_id = ?)")
		args = append(args, query.SourceKind, query.SourceID)
	}
	for _, ref := range query.SourceRefs {
		selectors = append(selectors, "(source_kind = ? AND source_id = ?)")
		args = append(args, ref.Kind, ref.ID)
	}
	if len(query.EvidenceIDs) > 0 {
		selectors = append(selectors, "id IN ("+queryPlaceholders(len(query.EvidenceIDs))+")")
		for _, id := range query.EvidenceIDs {
			args = append(args, id)
		}
	}
	conditions = append(conditions, "("+strings.Join(selectors, " OR ")+")")
	if query.After.ID != "" {
		conditions = append(conditions, "(observed_at < ? OR (observed_at = ? AND id > ?))")
		seek := query.After.UpdatedAt.UTC().UnixNano()
		args = append(args, seek, seek, query.After.ID)
	}
	args = append(args, query.Limit+1)
	rows, err := s.query(ctx, `SELECT `+evidenceQueryColumns+` FROM context_graph_evidence WHERE `+strings.Join(conditions, " AND ")+` ORDER BY observed_at DESC, id ASC LIMIT ?`, args...)
	if err != nil {
		return EvidencePage{}, fmt.Errorf("list visible context graph evidence: %w", err)
	}
	defer rows.Close()
	items := make([]Evidence, 0, query.Limit+1)
	for rows.Next() {
		item, err := scanQueryEvidence(rows)
		if err != nil {
			return EvidencePage{}, fmt.Errorf("scan visible context graph evidence: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return EvidencePage{}, fmt.Errorf("list visible context graph evidence: %w", err)
	}
	hasMore := len(items) > query.Limit
	if hasMore {
		items = items[:query.Limit]
	}
	return EvidencePage{Items: items, HasMore: hasMore}, nil
}

func (s *PlatformStore) GetVisibleProjectionAudit(ctx context.Context, tenantID, auditID string, constraint VisibilityConstraint) (ProjectionAudit, error) {
	if !s.ready() {
		return ProjectionAudit{}, fmt.Errorf("context graph platform store is required")
	}
	tenantID = strings.TrimSpace(tenantID)
	auditID = strings.TrimSpace(auditID)
	constraint, err := normalizeVisibilityConstraint(constraint)
	if !validTenantID(tenantID) || !validQueryID(auditID) || err != nil {
		return ProjectionAudit{}, ErrQueryInvalid
	}
	predicate, visibilityArgs := visibilityPredicate("source_access_kind", "source_access_id", "source_access_tenant_id", constraint)
	args := []any{tenantID, auditID}
	args = append(args, visibilityArgs...)
	audit, err := scanQueryProjectionAudit(s.queryRow(ctx, `SELECT `+projectionAuditQueryColumns+`
FROM context_graph_projection_audits
WHERE tenant_id = ? AND id = ? AND access_state = 'resolved' AND `+predicate, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return ProjectionAudit{}, ErrNotFound
	}
	if err != nil {
		return ProjectionAudit{}, fmt.Errorf("get visible projection audit: %w", err)
	}
	return audit, nil
}

func (s *PlatformStore) ListVisibleProjectionAudits(ctx context.Context, query ProjectionAuditCursorQuery) (ProjectionAuditCursorPage, error) {
	if !s.ready() {
		return ProjectionAuditCursorPage{}, fmt.Errorf("context graph platform store is required")
	}
	query, err := normalizeProjectionAuditCursorQuery(query)
	if err != nil {
		return ProjectionAuditCursorPage{}, err
	}
	predicate, visibilityArgs := visibilityPredicate("source_access_kind", "source_access_id", "source_access_tenant_id", query.Visibility)
	conditions := []string{"tenant_id = ?", "access_state = 'resolved'", predicate}
	args := []any{query.TenantID}
	args = append(args, visibilityArgs...)
	if query.Status != "" {
		conditions = append(conditions, "status = ?")
		args = append(args, query.Status)
	}
	if query.ReasonCode != "" {
		conditions = append(conditions, "reason_code = ?")
		args = append(args, query.ReasonCode)
	}
	if query.After.ID != "" {
		conditions = append(conditions, "(observed_at < ? OR (observed_at = ? AND id > ?))")
		seek := query.After.ObservedAt.UTC().UnixNano()
		args = append(args, seek, seek, query.After.ID)
	}
	args = append(args, query.Limit+1)
	rows, err := s.query(ctx, `SELECT `+projectionAuditQueryColumns+`
FROM context_graph_projection_audits
WHERE `+strings.Join(conditions, " AND ")+`
ORDER BY observed_at DESC, id ASC
LIMIT ?`, args...)
	if err != nil {
		return ProjectionAuditCursorPage{}, fmt.Errorf("list visible projection audits: %w", err)
	}
	defer rows.Close()
	items := make([]ProjectionAudit, 0, query.Limit+1)
	for rows.Next() {
		item, err := scanQueryProjectionAudit(rows)
		if err != nil {
			return ProjectionAuditCursorPage{}, fmt.Errorf("scan visible projection audit: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return ProjectionAuditCursorPage{}, fmt.Errorf("list visible projection audits: %w", err)
	}
	hasMore := len(items) > query.Limit
	if hasMore {
		items = items[:query.Limit]
	}
	return ProjectionAuditCursorPage{Items: items, HasMore: hasMore}, nil
}

func (s *PlatformStore) ListVisibleProjectionReplayHistory(ctx context.Context, query ProjectionReplayHistoryCursorQuery) (ProjectionReplayHistoryCursorPage, error) {
	if !s.ready() {
		return ProjectionReplayHistoryCursorPage{}, fmt.Errorf("context graph platform store is required")
	}
	query, err := normalizeProjectionReplayHistoryCursorQuery(query)
	if err != nil {
		return ProjectionReplayHistoryCursorPage{}, err
	}
	predicate, visibilityArgs := visibilityPredicate("source_access_kind", "source_access_id", "source_access_tenant_id", query.Visibility)
	conditions := []string{"tenant_id = ?", "audit_id = ?", "access_state = 'resolved'", predicate}
	args := []any{query.TenantID, query.AuditID}
	args = append(args, visibilityArgs...)
	if query.After.ID != "" {
		conditions = append(conditions, "(attempted_at < ? OR (attempted_at = ? AND id < ?))")
		seek := query.After.AttemptedAt.UTC().UnixNano()
		args = append(args, seek, seek, query.After.ID)
	}
	args = append(args, query.Limit+1)
	rows, err := s.query(ctx, `SELECT `+projectionReplayHistoryQueryColumns+`
FROM context_graph_projection_audit_replay_history
WHERE `+strings.Join(conditions, " AND ")+`
ORDER BY attempted_at DESC, id DESC
LIMIT ?`, args...)
	if err != nil {
		return ProjectionReplayHistoryCursorPage{}, fmt.Errorf("list visible projection replay history: %w", err)
	}
	defer rows.Close()
	items := make([]ProjectionAuditReplayHistory, 0, query.Limit+1)
	for rows.Next() {
		item, err := scanQueryProjectionReplayHistory(rows)
		if err != nil {
			return ProjectionReplayHistoryCursorPage{}, fmt.Errorf("scan visible projection replay history: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return ProjectionReplayHistoryCursorPage{}, fmt.Errorf("list visible projection replay history: %w", err)
	}
	hasMore := len(items) > query.Limit
	if hasMore {
		items = items[:query.Limit]
	}
	return ProjectionReplayHistoryCursorPage{Items: items, HasMore: hasMore}, nil
}

func (s *PlatformStore) ListVisibleProjectionTrace(ctx context.Context, query ProjectionTraceQuery) (ProjectionTracePage, error) {
	if !s.ready() {
		return ProjectionTracePage{}, fmt.Errorf("context graph platform store is required")
	}
	query, err := normalizeProjectionTraceQuery(query)
	if err != nil {
		return ProjectionTracePage{}, err
	}
	predicate, visibilityArgs := visibilityPredicate("source_access_kind", "source_access_id", "source_access_tenant_id", query.Visibility)
	conditions := []string{"tenant_id = ?", "source_event_type = ?", "source_event_id = ?", "access_state = 'resolved'", predicate}
	args := []any{query.TenantID, query.SourceEventType, query.SourceEventID}
	args = append(args, visibilityArgs...)
	if query.After.ID != "" {
		conditions = append(conditions, "(observed_at < ? OR (observed_at = ? AND id < ?))")
		seek := query.After.ObservedAt.UTC().UnixNano()
		args = append(args, seek, seek, query.After.ID)
	}
	args = append(args, query.Limit+1)
	rows, err := s.query(ctx, `SELECT id, tenant_id, source_event_id, source_event_type, status, reason_code, reason, details_json,
	 source_access_kind, source_access_id, source_access_tenant_id, access_state, observed_at
FROM context_graph_projection_audits
WHERE `+strings.Join(conditions, " AND ")+` ORDER BY observed_at DESC, id DESC LIMIT ?`, args...)
	if err != nil {
		return ProjectionTracePage{}, fmt.Errorf("list visible projection trace: %w", err)
	}
	defer rows.Close()
	items := make([]ProjectionAudit, 0, query.Limit+1)
	for rows.Next() {
		item, err := scanQueryProjectionAudit(rows)
		if err != nil {
			return ProjectionTracePage{}, fmt.Errorf("scan visible projection trace: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return ProjectionTracePage{}, fmt.Errorf("list visible projection trace: %w", err)
	}
	hasMore := len(items) > query.Limit
	if hasMore {
		items = items[:query.Limit]
	}
	return ProjectionTracePage{Items: items, HasMore: hasMore}, nil
}

func (s *PlatformStore) BuildVisibleNeighborhood(ctx context.Context, query NeighborhoodQuery) (Neighborhood, error) {
	if !s.ready() {
		return Neighborhood{}, fmt.Errorf("context graph platform store is required")
	}
	query, err := normalizeNeighborhoodQuery(query)
	if err != nil {
		return Neighborhood{}, err
	}
	root, err := s.GetVisibleObject(ctx, query.TenantID, query.RootID, query.Visibility)
	if err != nil {
		return Neighborhood{}, err
	}
	objects := map[string]Object{root.ID: root}
	links := map[string]Link{}
	frontier := []string{root.ID}
	truncated := false
	for level := 0; level < query.Depth && len(frontier) > 0; level++ {
		sort.Strings(frontier)
		candidates := make([]neighborhoodEdge, 0)
		for start := 0; start < len(frontier); start += maxNeighborhoodFrontierChunk {
			end := start + maxNeighborhoodFrontierChunk
			if end > len(frontier) {
				end = len(frontier)
			}
			remaining := query.MaxEdges - len(links)
			if remaining <= 0 {
				truncated = true
				break
			}
			linkTypes := query.LinkTypes
			if query.LinkType != "" {
				linkTypes = []string{query.LinkType}
			}
			rows, hasMore, err := s.listVisibleNeighborhoodEdges(ctx, query.TenantID, query.Visibility, frontier[start:end], linkTypes, remaining)
			if err != nil {
				return Neighborhood{}, err
			}
			candidates = append(candidates, rows...)
			truncated = truncated || hasMore
		}
		sort.Slice(candidates, func(i, j int) bool {
			if candidates[i].link.UpdatedAt.Equal(candidates[j].link.UpdatedAt) {
				return candidates[i].link.ID < candidates[j].link.ID
			}
			return candidates[i].link.UpdatedAt.After(candidates[j].link.UpdatedAt)
		})
		nextSet := map[string]struct{}{}
		for _, candidate := range candidates {
			if _, exists := links[candidate.link.ID]; exists {
				continue
			}
			if len(links) >= query.MaxEdges {
				truncated = true
				break
			}
			missing := make([]Object, 0, 2)
			if _, exists := objects[candidate.from.ID]; !exists {
				missing = append(missing, candidate.from)
			}
			if _, exists := objects[candidate.to.ID]; !exists && candidate.to.ID != candidate.from.ID {
				missing = append(missing, candidate.to)
			}
			if len(objects)+len(missing) > query.MaxNodes {
				truncated = true
				continue
			}
			links[candidate.link.ID] = candidate.link
			for _, object := range missing {
				objects[object.ID] = object
				nextSet[object.ID] = struct{}{}
			}
		}
		frontier = frontier[:0]
		for id := range nextSet {
			frontier = append(frontier, id)
		}
	}
	objectItems := make([]Object, 0, len(objects))
	for _, object := range objects {
		objectItems = append(objectItems, object)
	}
	sort.Slice(objectItems, func(i, j int) bool {
		if objectItems[i].ID == query.RootID && objectItems[j].ID == query.RootID {
			return false
		}
		if objectItems[i].ID == query.RootID {
			return true
		}
		if objectItems[j].ID == query.RootID {
			return false
		}
		return objectItems[i].ID < objectItems[j].ID
	})
	linkItems := make([]Link, 0, len(links))
	for _, link := range links {
		linkItems = append(linkItems, link)
	}
	sort.Slice(linkItems, func(i, j int) bool {
		if linkItems[i].UpdatedAt.Equal(linkItems[j].UpdatedAt) {
			return linkItems[i].ID < linkItems[j].ID
		}
		return linkItems[i].UpdatedAt.After(linkItems[j].UpdatedAt)
	})
	return Neighborhood{RootID: root.ID, Objects: objectItems, Links: linkItems, Truncated: truncated}, nil
}

const maxNeighborhoodFrontierChunk = 100

type neighborhoodEdge struct {
	link Link
	from Object
	to   Object
}

func (s *PlatformStore) listVisibleNeighborhoodEdges(ctx context.Context, tenantID string, constraint VisibilityConstraint, frontier []string, linkTypes []string, limit int) ([]neighborhoodEdge, bool, error) {
	linkPredicate, linkArgs := visibilityPredicate("l.source_access_kind", "l.source_access_id", "l.source_access_tenant_id", constraint)
	fromPredicate, fromArgs := visibilityPredicate("f.owner_access_kind", "f.owner_access_id", "f.owner_access_tenant_id", constraint)
	toPredicate, toArgs := visibilityPredicate("t.owner_access_kind", "t.owner_access_id", "t.owner_access_tenant_id", constraint)
	conditions := []string{
		"l.tenant_id = ?", "l.access_state = 'resolved'", "f.access_state = 'resolved'", "t.access_state = 'resolved'",
		linkPredicate, fromPredicate, toPredicate,
		"(l.from_object_id IN (" + queryPlaceholders(len(frontier)) + ") OR l.to_object_id IN (" + queryPlaceholders(len(frontier)) + "))",
	}
	args := []any{tenantID}
	args = append(args, linkArgs...)
	args = append(args, fromArgs...)
	args = append(args, toArgs...)
	for _, id := range frontier {
		args = append(args, id)
	}
	for _, id := range frontier {
		args = append(args, id)
	}
	if len(linkTypes) > 0 {
		conditions = append(conditions, "l.link_type IN ("+queryPlaceholders(len(linkTypes))+")")
		for _, linkType := range linkTypes {
			args = append(args, linkType)
		}
	}
	args = append(args, limit+1)
	rows, err := s.query(ctx, relationSelectSQL()+` WHERE `+strings.Join(conditions, " AND ")+` ORDER BY l.updated_at DESC, l.id ASC LIMIT ?`, args...)
	if err != nil {
		return nil, false, fmt.Errorf("list visible context graph neighborhood edges: %w", err)
	}
	defer rows.Close()
	items := make([]neighborhoodEdge, 0, limit+1)
	for rows.Next() {
		link, from, to, err := scanQueryRelationRow(rows)
		if err != nil {
			return nil, false, fmt.Errorf("scan visible context graph neighborhood edge: %w", err)
		}
		items = append(items, neighborhoodEdge{link: link, from: from, to: to})
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("list visible context graph neighborhood edges: %w", err)
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	return items, hasMore, nil
}

func scanQueryObject(row queryRowScanner) (Object, error) {
	var target objectQueryScan
	if err := row.Scan(target.args()...); err != nil {
		return Object{}, err
	}
	return target.value(), nil
}

type objectQueryScan struct {
	item                                                           Object
	propertiesJSON, provenanceJSON, evidenceRefsJSON, temporalJSON string
	updatedAt                                                      int64
}

func (s *objectQueryScan) args() []any {
	return []any{
		&s.item.ID, &s.item.TenantID, &s.item.ObjectType, &s.item.CanonicalKey, &s.item.Label,
		&s.propertiesJSON, &s.item.Sensitivity, &s.item.Confidence, &s.item.Status,
		&s.provenanceJSON, &s.evidenceRefsJSON,
		&s.item.OwnerAccessRef.Kind, &s.item.OwnerAccessRef.ID, &s.item.OwnerAccessRef.TenantID, &s.item.AccessState,
		&s.item.SourceStreamID, &s.item.SourceVersion, &s.temporalJSON, &s.updatedAt,
	}
}

func (s *objectQueryScan) value() Object {
	decodeGraphJSON(s.propertiesJSON, &s.item.Properties)
	decodeGraphJSON(s.provenanceJSON, &s.item.Provenance)
	decodeGraphJSON(s.evidenceRefsJSON, &s.item.EvidenceRefs)
	decodeGraphJSON(s.temporalJSON, &s.item.Temporal)
	s.item.UpdatedAt = unixNanoToTime(s.updatedAt)
	return s.item
}

type linkQueryScan struct {
	item                                                           Link
	propertiesJSON, provenanceJSON, evidenceRefsJSON, temporalJSON string
	updatedAt                                                      int64
}

func (s *linkQueryScan) args() []any {
	return []any{
		&s.item.ID, &s.item.TenantID, &s.item.LinkType, &s.item.FromObjectID, &s.item.ToObjectID,
		&s.propertiesJSON, &s.item.Confidence, &s.item.Status, &s.provenanceJSON, &s.evidenceRefsJSON,
		&s.item.SourceAccessRef.Kind, &s.item.SourceAccessRef.ID, &s.item.SourceAccessRef.TenantID, &s.item.AccessState,
		&s.item.SourceStreamID, &s.item.SourceVersion, &s.temporalJSON, &s.updatedAt,
	}
}

func (s *linkQueryScan) value() Link {
	decodeGraphJSON(s.propertiesJSON, &s.item.Properties)
	decodeGraphJSON(s.provenanceJSON, &s.item.Provenance)
	decodeGraphJSON(s.evidenceRefsJSON, &s.item.EvidenceRefs)
	decodeGraphJSON(s.temporalJSON, &s.item.Temporal)
	s.item.UpdatedAt = unixNanoToTime(s.updatedAt)
	return s.item
}

type factQueryScan struct {
	item                                                                      Fact
	valueJSON, propertiesJSON, provenanceJSON, evidenceRefsJSON, temporalJSON string
	updatedAt                                                                 int64
}

func (s *factQueryScan) args() []any {
	return []any{
		&s.item.ID, &s.item.TenantID, &s.item.Subject.Kind, &s.item.Subject.ID, &s.item.Predicate,
		&s.valueJSON, &s.propertiesJSON, &s.item.Confidence, &s.item.Status, &s.provenanceJSON, &s.evidenceRefsJSON,
		&s.item.SourceAccessRef.Kind, &s.item.SourceAccessRef.ID, &s.item.SourceAccessRef.TenantID, &s.item.AccessState,
		&s.item.SourceStreamID, &s.item.SourceVersion, &s.temporalJSON, &s.updatedAt,
	}
}

func (s *factQueryScan) value() Fact {
	decodeGraphJSON(s.valueJSON, &s.item.Value)
	decodeGraphJSON(s.propertiesJSON, &s.item.Properties)
	decodeGraphJSON(s.provenanceJSON, &s.item.Provenance)
	decodeGraphJSON(s.evidenceRefsJSON, &s.item.EvidenceRefs)
	decodeGraphJSON(s.temporalJSON, &s.item.Temporal)
	s.item.UpdatedAt = unixNanoToTime(s.updatedAt)
	return s.item
}

func scanQueryFact(row queryRowScanner) (Fact, error) {
	var target factQueryScan
	if err := row.Scan(target.args()...); err != nil {
		return Fact{}, err
	}
	return target.value(), nil
}

func scanQueryLineageFact(row queryRowScanner) (Fact, error) {
	var target factQueryScan
	var subjectAccessRef AccessRef
	args := append(target.args(), &subjectAccessRef.Kind, &subjectAccessRef.ID, &subjectAccessRef.TenantID)
	if err := row.Scan(args...); err != nil {
		return Fact{}, err
	}
	item := target.value()
	item.SubjectAccessRef = NormalizeAccessRef(subjectAccessRef)
	return item, nil
}

func scanQueryRelationRow(row queryRowScanner) (Link, Object, Object, error) {
	var link linkQueryScan
	var from, to objectQueryScan
	args := append(link.args(), from.args()...)
	args = append(args, to.args()...)
	if err := row.Scan(args...); err != nil {
		return Link{}, Object{}, Object{}, err
	}
	return link.value(), from.value(), to.value(), nil
}

func scanQueryEvidence(row queryRowScanner) (Evidence, error) {
	var item Evidence
	var observedAt int64
	if err := row.Scan(
		&item.ID, &item.TenantID, &item.SourceRef.Kind, &item.SourceRef.ID, &item.Summary, &item.Confidence,
		&item.SourceAccessRef.Kind, &item.SourceAccessRef.ID, &item.SourceAccessRef.TenantID, &item.AccessState, &observedAt,
	); err != nil {
		return Evidence{}, err
	}
	item.ObservedAt = unixNanoToTime(observedAt)
	return item, nil
}

func assertionFactQueryColumns(alias string) string {
	return strings.Join([]string{
		alias + ".fact_id", alias + ".tenant_id", alias + ".subject_kind", alias + ".subject_id", alias + ".predicate",
		alias + ".value_json", alias + ".properties_json", alias + ".confidence", alias + ".status", alias + ".provenance_json",
		alias + ".evidence_refs_json", alias + ".source_access_kind", alias + ".source_access_id", alias + ".source_access_tenant_id",
		alias + ".access_state", alias + ".source_stream_id", alias + ".source_version", alias + ".temporal_json", alias + ".observed_at",
	}, ", ")
}

func scanFactAssertion(row queryRowScanner) (FactAssertion, error) {
	var item FactAssertion
	var fact factQueryScan
	var subjectAccessRef AccessRef
	var observedAt int64
	args := []any{&item.AssertionID}
	args = append(args, fact.args()...)
	args = append(args, &subjectAccessRef.Kind, &subjectAccessRef.ID, &subjectAccessRef.TenantID, &observedAt)
	if err := row.Scan(args...); err != nil {
		return FactAssertion{}, err
	}
	item.Fact = fact.value()
	item.Fact.SubjectAccessRef = NormalizeAccessRef(subjectAccessRef)
	item.ObservedAt = unixNanoToTime(observedAt)
	return item, nil
}

func scanQueryProjectionAudit(row queryRowScanner) (ProjectionAudit, error) {
	var item ProjectionAudit
	var detailsJSON string
	var observedAt int64
	if err := row.Scan(
		&item.ID, &item.TenantID, &item.SourceEventID, &item.SourceEventType, &item.Status,
		&item.ReasonCode, &item.Reason, &detailsJSON,
		&item.SourceAccessRef.Kind, &item.SourceAccessRef.ID, &item.SourceAccessRef.TenantID, &item.AccessState, &observedAt,
	); err != nil {
		return ProjectionAudit{}, err
	}
	decodeGraphJSON(detailsJSON, &item.Details)
	item.ObservedAt = unixNanoToTime(observedAt)
	return item, nil
}

func scanQueryProjectionReplayHistory(row queryRowScanner) (ProjectionAuditReplayHistory, error) {
	var item ProjectionAuditReplayHistory
	var detailsJSON string
	var attemptedAt int64
	if err := row.Scan(
		&item.ID, &item.AuditID, &item.TenantID, &item.SourceEventID, &item.SourceEventType, &item.Status,
		&item.Error, &detailsJSON,
		&item.SourceAccessRef.Kind, &item.SourceAccessRef.ID, &item.SourceAccessRef.TenantID, &item.AccessState, &attemptedAt,
	); err != nil {
		return ProjectionAuditReplayHistory{}, err
	}
	decodeGraphJSON(detailsJSON, &item.Details)
	item.AttemptedAt = unixNanoToTime(attemptedAt)
	return item, nil
}

func relationSelectSQL() string {
	return `SELECT ` + qualifiedQueryColumns("l", linkQueryColumns) + `, ` + qualifiedQueryColumns("f", objectQueryColumns) + `, ` + qualifiedQueryColumns("t", objectQueryColumns) + `
FROM context_graph_links l
JOIN context_graph_objects f ON f.tenant_id = l.tenant_id AND f.id = l.from_object_id
JOIN context_graph_objects t ON t.tenant_id = l.tenant_id AND t.id = l.to_object_id`
}

func qualifiedQueryColumns(alias, columns string) string {
	parts := strings.Split(columns, ",")
	for index, column := range parts {
		parts[index] = alias + "." + strings.TrimSpace(column)
	}
	return strings.Join(parts, ", ")
}

func queryPlaceholders(count int) string {
	if count <= 0 {
		return "NULL"
	}
	values := make([]string, count)
	for index := range values {
		values[index] = "?"
	}
	return strings.Join(values, ",")
}

func normalizeVisibilityConstraint(constraint VisibilityConstraint) (VisibilityConstraint, error) {
	if constraint.candidateScan {
		if len(constraint.AllowedAccessRefs) != 0 {
			return VisibilityConstraint{}, ErrQueryInvalid
		}
		return constraint, nil
	}
	if len(constraint.AllowedAccessRefs) > MaxVisibilityAccessRefs {
		return VisibilityConstraint{}, ErrQueryInvalid
	}
	seen := map[AccessRef]struct{}{}
	refs := make([]AccessRef, 0, len(constraint.AllowedAccessRefs))
	for _, ref := range constraint.AllowedAccessRefs {
		ref = NormalizeAccessRef(ref)
		if !ref.Resolved() {
			return VisibilityConstraint{}, ErrQueryInvalid
		}
		if _, exists := seen[ref]; exists {
			continue
		}
		seen[ref] = struct{}{}
		refs = append(refs, ref)
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Kind != refs[j].Kind {
			return refs[i].Kind < refs[j].Kind
		}
		if refs[i].ID != refs[j].ID {
			return refs[i].ID < refs[j].ID
		}
		return refs[i].TenantID < refs[j].TenantID
	})
	constraint.AllowedAccessRefs = refs
	return constraint, nil
}

func visibilityPredicate(kindColumn, idColumn, tenantColumn string, constraint VisibilityConstraint) (string, []any) {
	if constraint.candidateScan {
		return "1 = 1", nil
	}
	if len(constraint.AllowedAccessRefs) == 0 {
		return "1 = 0", nil
	}
	parts := make([]string, 0, len(constraint.AllowedAccessRefs))
	args := make([]any, 0, len(constraint.AllowedAccessRefs)*3)
	for _, ref := range constraint.AllowedAccessRefs {
		parts = append(parts, fmt.Sprintf("(%s = ? AND %s = ? AND %s = ?)", kindColumn, idColumn, tenantColumn))
		args = append(args, ref.Kind, ref.ID, ref.TenantID)
	}
	return "(" + strings.Join(parts, " OR ") + ")", args
}

func normalizeObjectQuery(query ObjectQuery) (ObjectQuery, error) {
	query.TenantID = strings.TrimSpace(query.TenantID)
	query.ObjectType = strings.ToLower(strings.TrimSpace(query.ObjectType))
	query.Status = strings.ToLower(strings.TrimSpace(query.Status))
	query.SourceKind = strings.ToLower(strings.TrimSpace(query.SourceKind))
	query.Search = strings.TrimSpace(query.Search)
	query.After.ID = strings.TrimSpace(query.After.ID)
	constraint, err := normalizeVisibilityConstraint(query.Visibility)
	query.Visibility = constraint
	if err != nil || !validTenantID(query.TenantID) || !validOptionalQueryID(query.ObjectType) || !validOptionalQueryID(query.Status) || !validOptionalQueryID(query.SourceKind) || !validSearchText(query.Search) || query.Limit <= 0 || query.Limit > MaxQueryPageSize || !validObjectSeek(query.After) {
		return ObjectQuery{}, ErrQueryInvalid
	}
	return query, nil
}

func normalizeFactQuery(query FactQuery) (FactQuery, error) {
	query.TenantID = strings.TrimSpace(query.TenantID)
	query.ObjectType = strings.ToLower(strings.TrimSpace(query.ObjectType))
	query.ObjectID = strings.TrimSpace(query.ObjectID)
	query.After.ID = strings.TrimSpace(query.After.ID)
	constraint, err := normalizeVisibilityConstraint(query.Visibility)
	query.Visibility = constraint
	if err != nil || !validTenantID(query.TenantID) || !validQueryID(query.ObjectType) || !validQueryID(query.ObjectID) || query.Limit <= 0 || query.Limit > MaxQueryPageSize || !validObjectSeek(query.After) {
		return FactQuery{}, ErrQueryInvalid
	}
	return query, nil
}

func normalizeVisibleFactQuery(query VisibleFactQuery) (VisibleFactQuery, error) {
	query.TenantID = strings.TrimSpace(query.TenantID)
	query.After.ID = strings.TrimSpace(query.After.ID)
	constraint, err := normalizeVisibilityConstraint(query.Visibility)
	query.Visibility = constraint
	if err != nil || !validTenantID(query.TenantID) || query.Limit <= 0 || query.Limit > MaxQueryPageSize || !validObjectSeek(query.After) {
		return VisibleFactQuery{}, ErrQueryInvalid
	}
	return query, nil
}

func normalizeFactAssertionQuery(query FactAssertionQuery) (FactAssertionQuery, error) {
	query.TenantID = strings.TrimSpace(query.TenantID)
	query.FactID = strings.TrimSpace(query.FactID)
	query.After.ID = strings.TrimSpace(query.After.ID)
	constraint, err := normalizeVisibilityConstraint(query.Visibility)
	query.Visibility = constraint
	if err != nil || !validTenantID(query.TenantID) || !validQueryID(query.FactID) || query.Limit <= 0 || query.Limit > MaxQueryPageSize || !validFactAssertionSeek(query.After) {
		return FactAssertionQuery{}, ErrQueryInvalid
	}
	return query, nil
}

func normalizeRelationQuery(query RelationQuery) (RelationQuery, error) {
	query.TenantID = strings.TrimSpace(query.TenantID)
	query.ObjectID = strings.TrimSpace(query.ObjectID)
	query.Direction = RelationDirection(strings.ToLower(strings.TrimSpace(string(query.Direction))))
	if query.Direction == "" {
		query.Direction = RelationBoth
	}
	query.LinkType = strings.ToLower(strings.TrimSpace(query.LinkType))
	query.After.ID = strings.TrimSpace(query.After.ID)
	constraint, err := normalizeVisibilityConstraint(query.Visibility)
	query.Visibility = constraint
	if err != nil || !validTenantID(query.TenantID) || !validQueryID(query.ObjectID) || !validRelationDirection(query.Direction) || !validOptionalQueryID(query.LinkType) || query.Limit <= 0 || query.Limit > MaxQueryPageSize || !validObjectSeek(query.After) {
		return RelationQuery{}, ErrQueryInvalid
	}
	return query, nil
}

func normalizeEvidenceQuery(query EvidenceQuery) (EvidenceQuery, error) {
	query.TenantID = strings.TrimSpace(query.TenantID)
	query.SourceKind = strings.ToLower(strings.TrimSpace(query.SourceKind))
	query.SourceID = strings.TrimSpace(query.SourceID)
	query.After.ID = strings.TrimSpace(query.After.ID)
	constraint, err := normalizeVisibilityConstraint(query.Visibility)
	query.Visibility = constraint
	if err != nil || !validTenantID(query.TenantID) || (query.SourceKind == "") != (query.SourceID == "") || !validOptionalQueryID(query.SourceKind) || !validOptionalQueryID(query.SourceID) || query.Limit <= 0 || query.Limit > MaxQueryPageSize || !validObjectSeek(query.After) || len(query.SourceRefs) > MaxQueryPageSize || len(query.EvidenceIDs) > MaxQueryPageSize {
		return EvidenceQuery{}, ErrQueryInvalid
	}
	seenRefs := map[Ref]struct{}{}
	refs := make([]Ref, 0, len(query.SourceRefs))
	for _, ref := range query.SourceRefs {
		ref.Kind = strings.ToLower(strings.TrimSpace(ref.Kind))
		ref.ID = strings.TrimSpace(ref.ID)
		if !validQueryID(ref.Kind) || !validQueryID(ref.ID) {
			return EvidenceQuery{}, ErrQueryInvalid
		}
		if _, exists := seenRefs[ref]; exists {
			continue
		}
		seenRefs[ref] = struct{}{}
		refs = append(refs, ref)
	}
	sort.Slice(refs, func(i, j int) bool {
		return refs[i].Kind < refs[j].Kind || refs[i].Kind == refs[j].Kind && refs[i].ID < refs[j].ID
	})
	query.SourceRefs = refs
	seenIDs := map[string]struct{}{}
	ids := make([]string, 0, len(query.EvidenceIDs))
	for _, id := range query.EvidenceIDs {
		id = strings.TrimSpace(id)
		if !validQueryID(id) {
			return EvidenceQuery{}, ErrQueryInvalid
		}
		if _, exists := seenIDs[id]; exists {
			continue
		}
		seenIDs[id] = struct{}{}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	query.EvidenceIDs = ids
	if query.SourceKind == "" && len(query.SourceRefs) == 0 && len(query.EvidenceIDs) == 0 {
		return EvidenceQuery{}, ErrQueryInvalid
	}
	return query, nil
}

func normalizeProjectionAuditCursorQuery(query ProjectionAuditCursorQuery) (ProjectionAuditCursorQuery, error) {
	query.TenantID = strings.TrimSpace(query.TenantID)
	query.Status = strings.ToLower(strings.TrimSpace(query.Status))
	query.ReasonCode = strings.ToLower(strings.TrimSpace(query.ReasonCode))
	query.After.ID = strings.TrimSpace(query.After.ID)
	constraint, err := normalizeVisibilityConstraint(query.Visibility)
	query.Visibility = constraint
	if err != nil || !validTenantID(query.TenantID) || !validOptionalQueryID(query.Status) || !validOptionalQueryID(query.ReasonCode) || query.Limit <= 0 || query.Limit > MaxQueryPageSize || !validProjectionAuditSeek(query.After) {
		return ProjectionAuditCursorQuery{}, ErrQueryInvalid
	}
	return query, nil
}

func normalizeProjectionReplayHistoryCursorQuery(query ProjectionReplayHistoryCursorQuery) (ProjectionReplayHistoryCursorQuery, error) {
	query.TenantID = strings.TrimSpace(query.TenantID)
	query.AuditID = strings.TrimSpace(query.AuditID)
	query.After.ID = strings.TrimSpace(query.After.ID)
	constraint, err := normalizeVisibilityConstraint(query.Visibility)
	query.Visibility = constraint
	if err != nil || !validTenantID(query.TenantID) || !validQueryID(query.AuditID) || query.Limit <= 0 || query.Limit > MaxQueryPageSize || !validProjectionReplayHistorySeek(query.After) {
		return ProjectionReplayHistoryCursorQuery{}, ErrQueryInvalid
	}
	return query, nil
}

func normalizeProjectionTraceQuery(query ProjectionTraceQuery) (ProjectionTraceQuery, error) {
	query.TenantID = strings.TrimSpace(query.TenantID)
	query.SourceEventID = strings.TrimSpace(query.SourceEventID)
	query.SourceEventType = strings.TrimSpace(query.SourceEventType)
	query.After.ID = strings.TrimSpace(query.After.ID)
	constraint, err := normalizeVisibilityConstraint(query.Visibility)
	query.Visibility = constraint
	if err != nil || !validTenantID(query.TenantID) || !validQueryID(query.SourceEventID) || !validQueryID(query.SourceEventType) || query.Limit <= 0 || query.Limit > MaxQueryPageSize || !validProjectionAuditSeek(query.After) {
		return ProjectionTraceQuery{}, ErrQueryInvalid
	}
	return query, nil
}

func normalizeNeighborhoodQuery(query NeighborhoodQuery) (NeighborhoodQuery, error) {
	query.TenantID = strings.TrimSpace(query.TenantID)
	query.RootID = strings.TrimSpace(query.RootID)
	query.LinkType = strings.ToLower(strings.TrimSpace(query.LinkType))
	query.LinkTypes = normalizeNeighborhoodLinkTypes(query.LinkTypes)
	constraint, err := normalizeVisibilityConstraint(query.Visibility)
	query.Visibility = constraint
	if err != nil || !validTenantID(query.TenantID) || !validQueryID(query.RootID) || !validOptionalQueryID(query.LinkType) || len(query.LinkTypes) > MaxNeighborhoodLinkTypes || query.LinkType != "" && len(query.LinkTypes) > 0 || !validNeighborhoodLinkTypes(query.LinkTypes) || query.Depth <= 0 || query.Depth > 2 || query.MaxNodes <= 0 || query.MaxNodes > MaxNeighborhoodCandidateNodes || query.MaxEdges <= 0 || query.MaxEdges > MaxNeighborhoodCandidateEdges {
		return NeighborhoodQuery{}, ErrQueryInvalid
	}
	return query, nil
}

func normalizeNeighborhoodLinkTypes(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func validNeighborhoodLinkTypes(values []string) bool {
	for _, value := range values {
		if !validQueryID(value) {
			return false
		}
	}
	return true
}

func validRelationDirection(direction RelationDirection) bool {
	switch direction {
	case RelationIncoming, RelationOutgoing, RelationBoth:
		return true
	default:
		return false
	}
}

func normalizeAccessRefSeek(seek AccessRefSeek) AccessRefSeek {
	seek.Kind = strings.ToLower(strings.TrimSpace(seek.Kind))
	seek.ID = strings.TrimSpace(seek.ID)
	seek.TenantID = strings.TrimSpace(seek.TenantID)
	return seek
}

func validAccessRefSeek(seek AccessRefSeek) bool {
	if seek == (AccessRefSeek{}) {
		return true
	}
	return validQueryID(seek.Kind) && validQueryID(seek.ID) && validTenantID(seek.TenantID)
}

func validObjectSeek(seek ObjectSeek) bool {
	if seek.ID == "" {
		return seek.UpdatedAt.IsZero()
	}
	return validQueryID(seek.ID) && !seek.UpdatedAt.IsZero()
}

func validFactAssertionSeek(seek FactAssertionSeek) bool {
	if seek.ID == "" {
		return seek.ObservedAt.IsZero()
	}
	return validQueryID(seek.ID) && !seek.ObservedAt.IsZero()
}

func validProjectionAuditSeek(seek ProjectionAuditSeek) bool {
	if seek.ID == "" {
		return seek.ObservedAt.IsZero()
	}
	return validQueryID(seek.ID) && !seek.ObservedAt.IsZero()
}

func validProjectionReplayHistorySeek(seek ProjectionReplayHistorySeek) bool {
	if seek.ID == "" {
		return seek.AttemptedAt.IsZero()
	}
	return validQueryID(seek.ID) && !seek.AttemptedAt.IsZero()
}

func validTenantID(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= 256
}

func validQueryID(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= 256
}

func validOptionalQueryID(value string) bool {
	return value == "" || validQueryID(value)
}

func validSearchText(value string) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) <= 128
}

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, "!", "!!")
	value = strings.ReplaceAll(value, "%", "!%")
	return strings.ReplaceAll(value, "_", "!_")
}

func nullableGraphTime(value sql.NullInt64) *time.Time {
	if !value.Valid {
		return nil
	}
	result := unixNanoToTime(value.Int64)
	return &result
}

func mergeTimeBounds(counts *VisibleContextCounts, oldest, newest *time.Time) {
	if counts == nil {
		return
	}
	if oldest != nil && (counts.OldestAt == nil || oldest.Before(*counts.OldestAt)) {
		value := oldest.UTC()
		counts.OldestAt = &value
	}
	if newest != nil && (counts.NewestAt == nil || newest.After(*counts.NewestAt)) {
		value := newest.UTC()
		counts.NewestAt = &value
	}
}
