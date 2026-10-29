package contextgraph

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"orrery_v2/internal/platform/db"
)

// Bound history materialization independently of the number of returned resources.
// Exceeding this budget fails closed; it never falls back to a tenant snapshot.
const maxTemporalSubgraphVersions = 6000

type temporalSubgraphReader struct {
	store                 *PlatformStore
	tenant                string
	businessAsOf, knownAt time.Time
	remainingVersions     int
}

func (s *PlatformStore) SnapshotSubgraphAsOf(ctx context.Context, tenantID string, subjectRefs []Ref, maxDepth int, limits CurrentSubgraphLimits, businessAsOf, knownAt time.Time) ([]Object, []Link, []Fact, []Evidence, error) {
	if !s.ready() {
		return nil, nil, nil, nil, fmt.Errorf("context graph platform store is required")
	}
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" || len(subjectRefs) == 0 || maxDepth < 0 || maxDepth > 2 || !limits.valid() || businessAsOf.IsZero() || knownAt.IsZero() {
		return nil, nil, nil, nil, fmt.Errorf("%w: tenant, subjects, depth, limits, and temporal coordinates are required", ErrQueryInvalid)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	read := func(store *PlatformStore) ([]Object, []Link, []Fact, []Evidence, error) {
		reader := temporalSubgraphReader{store: store, tenant: tenantID, businessAsOf: businessAsOf.UTC(), knownAt: knownAt.UTC(), remainingVersions: maxTemporalSubgraphVersions}
		return reader.snapshot(ctx, subjectRefs, maxDepth, limits)
	}
	if s.tx != nil {
		return read(s)
	}
	var objects []Object
	var links []Link
	var facts []Fact
	var evidence []Evidence
	err := s.store.WithTx(ctx, func(tx db.Tx) error {
		var err error
		objects, links, facts, evidence, err = read(NewPlatformStoreTx(tx))
		return err
	})
	return objects, links, facts, evidence, err
}

func (r *temporalSubgraphReader) snapshot(ctx context.Context, subjects []Ref, depth int, limits CurrentSubgraphLimits) ([]Object, []Link, []Fact, []Evidence, error) {
	selected := make(map[string]bool)
	for _, ref := range subjects {
		if id := strings.TrimSpace(ref.ID); id != "" {
			selected[id] = true
		}
	}
	if len(selected) == 0 {
		return nil, nil, nil, nil, ErrQueryInvalid
	}
	if len(selected) > limits.Objects {
		return nil, nil, nil, nil, ErrQueryBudgetExceeded
	}
	for range depth {
		links, err := r.links(ctx, sortedSubgraphIDs(selected), limits.Links)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		before := len(selected)
		for _, link := range links {
			selected[link.FromObjectID], selected[link.ToObjectID] = true, true
			if len(selected) > limits.Objects {
				return nil, nil, nil, nil, ErrQueryBudgetExceeded
			}
		}
		if len(selected) == before {
			break
		}
	}
	ids := sortedSubgraphIDs(selected)
	objects, err := r.store.listCurrentSubgraphObjects(ctx, r.tenant, ids, limits.Objects)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	versions, err := r.versions(ctx, temporalResourceObject, ids)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	objects, _, _, _, err = temporalSnapshot(objects, nil, nil, nil, versions, r.businessAsOf, r.knownAt)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	resolvedObjects := objects[:0]
	for _, object := range objects {
		if object.AccessState == AccessStateResolved {
			resolvedObjects = append(resolvedObjects, object)
		}
	}
	objects = resolvedObjects
	links, err := r.links(ctx, ids, limits.Links)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	inducedLinks := links[:0]
	for _, link := range links {
		if selected[link.FromObjectID] && selected[link.ToObjectID] {
			inducedLinks = append(inducedLinks, link)
		}
	}
	links = inducedLinks
	facts, err := r.facts(ctx, ids, limits.Facts)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	evidenceIDs := make(map[string]bool)
	addEvidence := func(refs []Ref) {
		for _, ref := range refs {
			if id := strings.TrimSpace(ref.ID); id != "" {
				evidenceIDs[id] = true
			}
		}
	}
	for _, object := range objects {
		addEvidence(object.EvidenceRefs)
	}
	for _, link := range links {
		addEvidence(link.EvidenceRefs)
	}
	for _, fact := range facts {
		addEvidence(fact.EvidenceRefs)
	}
	if len(evidenceIDs) > limits.Evidence {
		return nil, nil, nil, nil, ErrQueryBudgetExceeded
	}
	evidence, err := r.store.listCurrentSubgraphEvidence(ctx, r.tenant, sortedSubgraphIDs(evidenceIDs), limits.Evidence)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	knownEvidence := evidence[:0]
	for _, item := range evidence {
		if item.ObservedAt.IsZero() || !item.ObservedAt.After(r.knownAt) {
			knownEvidence = append(knownEvidence, item)
		}
	}
	return objects, links, facts, knownEvidence, nil
}

func (r *temporalSubgraphReader) links(ctx context.Context, ids []string, limit int) ([]Link, error) {
	current := make(map[string]Link)
	for _, batch := range currentSubgraphBatches(ids) {
		args := append(currentSubgraphArgs(r.tenant, batch, batch), limit+1)
		rows, err := r.store.query(ctx, `SELECT `+linkQueryColumns+`, l.temporal_json
FROM context_graph_links l
WHERE l.tenant_id = ? AND l.access_state = 'resolved'
AND (l.from_object_id IN (`+currentSubgraphPlaceholders(len(batch))+`) OR l.to_object_id IN (`+currentSubgraphPlaceholders(len(batch))+`))
ORDER BY l.id LIMIT ?`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var scan linkQueryScan
			var temporalJSON string
			if err := rows.Scan(append(scan.args(), &temporalJSON)...); err != nil {
				rows.Close()
				return nil, err
			}
			link := scan.value()
			decodeGraphJSON(temporalJSON, &link.Temporal)
			current[link.ID] = link
			if len(current) > limit {
				rows.Close()
				return nil, ErrQueryBudgetExceeded
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	resourceIDs, err := r.relatedResourceIDs(ctx, temporalResourceLink, ids, limit)
	if err != nil {
		return nil, err
	}
	for id := range current {
		resourceIDs[id] = true
	}
	if len(resourceIDs) > limit {
		return nil, ErrQueryBudgetExceeded
	}
	versions, err := r.versions(ctx, temporalResourceLink, sortedSubgraphIDs(resourceIDs))
	if err != nil {
		return nil, err
	}
	base := make([]Link, 0, len(current))
	for _, link := range current {
		base = append(base, link)
	}
	_, links, _, _, err := temporalSnapshot(nil, base, nil, nil, versions, r.businessAsOf, r.knownAt)
	if err != nil {
		return nil, err
	}
	selected := make(map[string]bool, len(ids))
	for _, id := range ids {
		selected[id] = true
	}
	out := links[:0]
	for _, link := range links {
		if link.AccessState == AccessStateResolved && (selected[link.FromObjectID] || selected[link.ToObjectID]) {
			out = append(out, link)
		}
	}
	return out, nil
}

func (r *temporalSubgraphReader) facts(ctx context.Context, ids []string, limit int) ([]Fact, error) {
	base, err := r.store.listCurrentSubgraphFacts(ctx, r.tenant, ids, limit)
	if err != nil {
		return nil, err
	}
	resourceIDs, err := r.relatedResourceIDs(ctx, temporalResourceFact, ids, limit)
	if err != nil {
		return nil, err
	}
	for _, fact := range base {
		resourceIDs[fact.ID] = true
	}
	if len(resourceIDs) > limit {
		return nil, ErrQueryBudgetExceeded
	}
	versions, err := r.versions(ctx, temporalResourceFact, sortedSubgraphIDs(resourceIDs))
	if err != nil {
		return nil, err
	}
	_, _, facts, _, err := temporalSnapshot(nil, nil, base, nil, versions, r.businessAsOf, r.knownAt)
	if err != nil {
		return nil, err
	}
	selected := make(map[string]bool, len(ids))
	for _, id := range ids {
		selected[id] = true
	}
	out := facts[:0]
	for _, fact := range facts {
		if fact.AccessState == AccessStateResolved && selected[fact.Subject.ID] {
			out = append(out, fact)
		}
	}
	return out, nil
}

func (r *temporalSubgraphReader) relatedResourceIDs(ctx context.Context, kind string, ids []string, limit int) (map[string]bool, error) {
	selected := make(map[string]bool)
	for _, batch := range currentSubgraphBatches(ids) {
		placeholders := currentSubgraphPlaceholders(len(batch))
		args := []any{r.tenant, kind}
		condition := "(payload_json -> 'subject' ->> 'id') IN (" + placeholders + ")"
		for _, id := range batch {
			args = append(args, id)
		}
		if kind == temporalResourceLink {
			condition = "((payload_json ->> 'from_object_id') IN (" + placeholders + ") OR (payload_json ->> 'to_object_id') IN (" + placeholders + "))"
			for _, id := range batch {
				args = append(args, id)
			}
		}
		args = append(args, limit+1)
		rows, err := r.store.query(ctx, `SELECT resource_id FROM context_graph_resource_versions
WHERE tenant_id = ? AND resource_kind = ? AND `+condition+`
GROUP BY resource_id ORDER BY resource_id LIMIT ?`, args...)
		if err != nil {
			return nil, fmt.Errorf("find temporal subgraph resources: %w", err)
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			selected[id] = true
			if len(selected) > limit {
				rows.Close()
				return nil, ErrQueryBudgetExceeded
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return selected, nil
}

func (r *temporalSubgraphReader) versions(ctx context.Context, kind string, ids []string) ([]temporalResourceVersion, error) {
	var out []temporalResourceVersion
	for _, batch := range currentSubgraphBatches(ids) {
		args := []any{r.tenant, kind}
		for _, id := range batch {
			args = append(args, id)
		}
		args = append(args, r.remainingVersions+1)
		rows, err := r.store.query(ctx, `SELECT resource_id, assertion_id, payload_json,
access_kind, access_id, access_tenant_id, access_state, source_stream_id, source_version, updated_at
FROM context_graph_resource_versions
WHERE tenant_id = ? AND resource_kind = ? AND resource_id IN (`+currentSubgraphPlaceholders(len(batch))+`)
ORDER BY resource_id, assertion_id LIMIT ?`, args...)
		if err != nil {
			return nil, fmt.Errorf("read temporal subgraph versions: %w", err)
		}
		for rows.Next() {
			var id, assertionID, payload, stream string
			var access AccessRef
			var state AccessState
			var sourceVersion, updatedAt int64
			if err := rows.Scan(&id, &assertionID, &payload, &access.Kind, &access.ID, &access.TenantID, &state, &stream, &sourceVersion, &updatedAt); err != nil {
				rows.Close()
				return nil, err
			}
			r.remainingVersions--
			if r.remainingVersions < 0 {
				rows.Close()
				return nil, fmt.Errorf("%w: temporal subgraph history exceeds %d versions", ErrQueryBudgetExceeded, maxTemporalSubgraphVersions)
			}
			var version temporalResourceVersion
			var payloadID, payloadTenant string
			switch kind {
			case temporalResourceObject:
				var value Object
				if err := json.Unmarshal([]byte(payload), &value); err != nil {
					rows.Close()
					return nil, err
				}
				value.OwnerAccessRef, value.AccessState, value.SourceStreamID, value.SourceVersion, value.UpdatedAt = access, state, stream, sourceVersion, unixNanoToTime(updatedAt)
				version, payloadID, payloadTenant = objectTemporalVersion(value), value.ID, value.TenantID
			case temporalResourceLink:
				var value Link
				if err := json.Unmarshal([]byte(payload), &value); err != nil {
					rows.Close()
					return nil, err
				}
				value.SourceAccessRef, value.AccessState, value.SourceStreamID, value.SourceVersion, value.UpdatedAt = access, state, stream, sourceVersion, unixNanoToTime(updatedAt)
				version, payloadID, payloadTenant = linkTemporalVersion(value), value.ID, value.TenantID
			case temporalResourceFact:
				var value Fact
				if err := json.Unmarshal([]byte(payload), &value); err != nil {
					rows.Close()
					return nil, err
				}
				value.SourceAccessRef, value.AccessState, value.SourceStreamID, value.SourceVersion, value.UpdatedAt = access, state, stream, sourceVersion, unixNanoToTime(updatedAt)
				version, payloadID, payloadTenant = factTemporalVersion(value), value.ID, value.TenantID
			}
			if payloadID != id || payloadTenant != r.tenant {
				rows.Close()
				return nil, fmt.Errorf("temporal resource identity does not match its tenant or key")
			}
			version.AssertionID = assertionID
			out = append(out, version)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AssertionID < out[j].AssertionID })
	return out, nil
}

var _ TemporalSubgraphStore = (*PlatformStore)(nil)
var _ TemporalSubgraphStore = (*MemoryStore)(nil)
