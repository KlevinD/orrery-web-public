package contextgraph

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"orrery_v2/internal/platform/db"
)

const currentSubgraphQueryBatchSize = 200

func (s *PlatformStore) SnapshotCurrentSubgraph(ctx context.Context, tenantID string, subjectRefs []Ref, maxDepth int, limits CurrentSubgraphLimits) ([]Object, []Link, []Fact, []Evidence, error) {
	if !s.ready() {
		return nil, nil, nil, nil, fmt.Errorf("context graph platform store is required")
	}
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" || len(subjectRefs) == 0 || maxDepth < 0 || maxDepth > 2 || !limits.valid() {
		return nil, nil, nil, nil, fmt.Errorf("%w: tenant, subjects, depth, and subgraph limits are required", ErrQueryInvalid)
	}
	if s.tx == nil {
		var objects []Object
		var links []Link
		var facts []Fact
		var evidence []Evidence
		err := s.store.WithTx(ctx, func(tx db.Tx) error {
			var err error
			objects, links, facts, evidence, err = NewPlatformStoreTx(tx).snapshotCurrentSubgraph(ctx, tenantID, subjectRefs, maxDepth, limits)
			return err
		})
		return objects, links, facts, evidence, err
	}
	return s.snapshotCurrentSubgraph(ctx, tenantID, subjectRefs, maxDepth, limits)
}

func (s *PlatformStore) snapshotCurrentSubgraph(ctx context.Context, tenantID string, subjectRefs []Ref, maxDepth int, limits CurrentSubgraphLimits) ([]Object, []Link, []Fact, []Evidence, error) {
	selected := make(map[string]bool, len(subjectRefs))
	for _, ref := range subjectRefs {
		if id := strings.TrimSpace(ref.ID); id != "" {
			selected[id] = true
		}
	}
	if len(selected) > limits.Objects {
		return nil, nil, nil, nil, fmt.Errorf("%w: current subgraph objects exceed %d", ErrQueryBudgetExceeded, limits.Objects)
	}
	for depth := 0; depth < maxDepth; depth++ {
		before := len(selected)
		if err := s.expandCurrentSubgraphIDs(ctx, tenantID, selected, limits); err != nil {
			return nil, nil, nil, nil, err
		}
		if len(selected) == before {
			break
		}
	}
	ids := sortedSubgraphIDs(selected)
	objects, err := s.listCurrentSubgraphObjects(ctx, tenantID, ids, limits.Objects)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	links, err := s.listCurrentSubgraphLinks(ctx, tenantID, ids, selected, limits.Links)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	facts, err := s.listCurrentSubgraphFacts(ctx, tenantID, ids, limits.Facts)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	evidenceIDs := make(map[string]bool)
	for _, object := range objects {
		for _, ref := range object.EvidenceRefs {
			evidenceIDs[strings.TrimSpace(ref.ID)] = true
		}
	}
	for _, link := range links {
		for _, ref := range link.EvidenceRefs {
			evidenceIDs[strings.TrimSpace(ref.ID)] = true
		}
	}
	for _, fact := range facts {
		for _, ref := range fact.EvidenceRefs {
			evidenceIDs[strings.TrimSpace(ref.ID)] = true
		}
	}
	delete(evidenceIDs, "")
	if len(evidenceIDs) > limits.Evidence {
		return nil, nil, nil, nil, fmt.Errorf("%w: current subgraph evidence exceed %d", ErrQueryBudgetExceeded, limits.Evidence)
	}
	evidence, err := s.listCurrentSubgraphEvidence(ctx, tenantID, sortedSubgraphIDs(evidenceIDs), limits.Evidence)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return objects, links, facts, evidence, nil
}

func (s *PlatformStore) expandCurrentSubgraphIDs(ctx context.Context, tenantID string, selected map[string]bool, limits CurrentSubgraphLimits) error {
	ids := sortedSubgraphIDs(selected)
	for _, batch := range currentSubgraphBatches(ids) {
		args := currentSubgraphArgs(tenantID, batch, batch)
		args = append(args, limits.Links+1)
		rows, err := s.query(ctx, `
SELECT id, from_object_id, to_object_id
FROM context_graph_links
WHERE tenant_id = ? AND access_state = 'resolved'
	AND (from_object_id IN (`+currentSubgraphPlaceholders(len(batch))+`) OR to_object_id IN (`+currentSubgraphPlaceholders(len(batch))+`))
ORDER BY id
LIMIT ?`, args...)
		if err != nil {
			return fmt.Errorf("expand current context subgraph: %w", err)
		}
		count := 0
		for rows.Next() {
			var id, fromID, toID string
			if err := rows.Scan(&id, &fromID, &toID); err != nil {
				rows.Close()
				return fmt.Errorf("scan current context subgraph link: %w", err)
			}
			count++
			if count > limits.Links {
				rows.Close()
				return fmt.Errorf("%w: current subgraph links exceed %d", ErrQueryBudgetExceeded, limits.Links)
			}
			selected[fromID] = true
			selected[toID] = true
			if len(selected) > limits.Objects {
				rows.Close()
				return fmt.Errorf("%w: current subgraph objects exceed %d", ErrQueryBudgetExceeded, limits.Objects)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("expand current context subgraph: %w", err)
		}
		rows.Close()
	}
	return nil
}

func (s *PlatformStore) listCurrentSubgraphObjects(ctx context.Context, tenantID string, ids []string, limit int) ([]Object, error) {
	out := make([]Object, 0, len(ids))
	for _, batch := range currentSubgraphBatches(ids) {
		rows, err := s.query(ctx, `
SELECT id, tenant_id, object_type, canonical_key, label, properties_json, sensitivity,
	confidence, status, provenance_json, evidence_refs_json,
	owner_access_kind, owner_access_id, owner_access_tenant_id, access_state,
	source_stream_id, source_version, temporal_json, updated_at
FROM context_graph_objects
WHERE tenant_id = ? AND access_state = 'resolved' AND id IN (`+currentSubgraphPlaceholders(len(batch))+`)
ORDER BY id
LIMIT ?`, append(currentSubgraphArgs(tenantID, batch), limit-len(out)+1)...)
		if err != nil {
			return nil, fmt.Errorf("list current context subgraph objects: %w", err)
		}
		for rows.Next() {
			var object Object
			var propertiesJSON, provenanceJSON, evidenceRefsJSON, temporalJSON string
			var updatedAt int64
			if err := rows.Scan(&object.ID, &object.TenantID, &object.ObjectType, &object.CanonicalKey, &object.Label, &propertiesJSON, &object.Sensitivity, &object.Confidence, &object.Status, &provenanceJSON, &evidenceRefsJSON, &object.OwnerAccessRef.Kind, &object.OwnerAccessRef.ID, &object.OwnerAccessRef.TenantID, &object.AccessState, &object.SourceStreamID, &object.SourceVersion, &temporalJSON, &updatedAt); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan current context subgraph object: %w", err)
			}
			decodeGraphJSON(propertiesJSON, &object.Properties)
			decodeGraphJSON(provenanceJSON, &object.Provenance)
			decodeGraphJSON(evidenceRefsJSON, &object.EvidenceRefs)
			decodeGraphJSON(temporalJSON, &object.Temporal)
			object.UpdatedAt = unixNanoToTime(updatedAt)
			out = append(out, object)
			if len(out) > limit {
				rows.Close()
				return nil, fmt.Errorf("%w: current subgraph objects exceed %d", ErrQueryBudgetExceeded, limit)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("list current context subgraph objects: %w", err)
		}
		rows.Close()
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *PlatformStore) listCurrentSubgraphLinks(ctx context.Context, tenantID string, ids []string, selected map[string]bool, limit int) ([]Link, error) {
	byID := make(map[string]Link)
	for _, batch := range currentSubgraphBatches(ids) {
		args := currentSubgraphArgs(tenantID, batch, batch)
		args = append(args, limit-len(byID)+1)
		rows, err := s.query(ctx, `
SELECT id, tenant_id, link_type, from_object_id, to_object_id, properties_json,
	confidence, status, provenance_json, evidence_refs_json,
	source_access_kind, source_access_id, source_access_tenant_id, access_state,
	source_stream_id, source_version, temporal_json, updated_at
FROM context_graph_links
WHERE tenant_id = ? AND access_state = 'resolved'
	AND from_object_id IN (`+currentSubgraphPlaceholders(len(batch))+`)
	AND to_object_id IN (`+currentSubgraphPlaceholders(len(batch))+`)
ORDER BY id
LIMIT ?`, args...)
		if err != nil {
			return nil, fmt.Errorf("list current context subgraph links: %w", err)
		}
		for rows.Next() {
			var link Link
			var propertiesJSON, provenanceJSON, evidenceRefsJSON, temporalJSON string
			var updatedAt int64
			if err := rows.Scan(&link.ID, &link.TenantID, &link.LinkType, &link.FromObjectID, &link.ToObjectID, &propertiesJSON, &link.Confidence, &link.Status, &provenanceJSON, &evidenceRefsJSON, &link.SourceAccessRef.Kind, &link.SourceAccessRef.ID, &link.SourceAccessRef.TenantID, &link.AccessState, &link.SourceStreamID, &link.SourceVersion, &temporalJSON, &updatedAt); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan current context subgraph link: %w", err)
			}
			if !selected[link.FromObjectID] || !selected[link.ToObjectID] {
				continue
			}
			decodeGraphJSON(propertiesJSON, &link.Properties)
			decodeGraphJSON(provenanceJSON, &link.Provenance)
			decodeGraphJSON(evidenceRefsJSON, &link.EvidenceRefs)
			decodeGraphJSON(temporalJSON, &link.Temporal)
			link.UpdatedAt = unixNanoToTime(updatedAt)
			byID[link.ID] = link
			if len(byID) > limit {
				rows.Close()
				return nil, fmt.Errorf("%w: current subgraph links exceed %d", ErrQueryBudgetExceeded, limit)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("list current context subgraph links: %w", err)
		}
		rows.Close()
	}
	out := make([]Link, 0, len(byID))
	for _, link := range byID {
		out = append(out, link)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *PlatformStore) listCurrentSubgraphFacts(ctx context.Context, tenantID string, ids []string, limit int) ([]Fact, error) {
	out := make([]Fact, 0)
	for _, batch := range currentSubgraphBatches(ids) {
		rows, err := s.query(ctx, `
SELECT id, tenant_id, subject_kind, subject_id, predicate, value_json, properties_json,
	confidence, status, provenance_json, evidence_refs_json,
	source_access_kind, source_access_id, source_access_tenant_id, access_state,
	source_stream_id, source_version, temporal_json, updated_at
FROM context_graph_facts
WHERE tenant_id = ? AND access_state = 'resolved' AND subject_id IN (`+currentSubgraphPlaceholders(len(batch))+`)
ORDER BY id
LIMIT ?`, append(currentSubgraphArgs(tenantID, batch), limit-len(out)+1)...)
		if err != nil {
			return nil, fmt.Errorf("list current context subgraph facts: %w", err)
		}
		for rows.Next() {
			var fact Fact
			var subjectKind, subjectID, valueJSON, propertiesJSON, provenanceJSON, evidenceRefsJSON, temporalJSON string
			var updatedAt int64
			if err := rows.Scan(&fact.ID, &fact.TenantID, &subjectKind, &subjectID, &fact.Predicate, &valueJSON, &propertiesJSON, &fact.Confidence, &fact.Status, &provenanceJSON, &evidenceRefsJSON, &fact.SourceAccessRef.Kind, &fact.SourceAccessRef.ID, &fact.SourceAccessRef.TenantID, &fact.AccessState, &fact.SourceStreamID, &fact.SourceVersion, &temporalJSON, &updatedAt); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan current context subgraph fact: %w", err)
			}
			fact.Subject = Ref{Kind: subjectKind, ID: subjectID}
			decodeGraphJSON(valueJSON, &fact.Value)
			decodeGraphJSON(propertiesJSON, &fact.Properties)
			decodeGraphJSON(provenanceJSON, &fact.Provenance)
			decodeGraphJSON(evidenceRefsJSON, &fact.EvidenceRefs)
			decodeGraphJSON(temporalJSON, &fact.Temporal)
			fact.UpdatedAt = unixNanoToTime(updatedAt)
			out = append(out, fact)
			if len(out) > limit {
				rows.Close()
				return nil, fmt.Errorf("%w: current subgraph facts exceed %d", ErrQueryBudgetExceeded, limit)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("list current context subgraph facts: %w", err)
		}
		rows.Close()
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *PlatformStore) listCurrentSubgraphEvidence(ctx context.Context, tenantID string, ids []string, limit int) ([]Evidence, error) {
	out := make([]Evidence, 0, len(ids))
	for _, batch := range currentSubgraphBatches(ids) {
		rows, err := s.query(ctx, `
SELECT id, tenant_id, source_kind, source_id, summary, confidence,
	source_access_kind, source_access_id, source_access_tenant_id, access_state, observed_at
FROM context_graph_evidence
WHERE tenant_id = ? AND access_state = 'resolved' AND id IN (`+currentSubgraphPlaceholders(len(batch))+`)
ORDER BY id
LIMIT ?`, append(currentSubgraphArgs(tenantID, batch), limit-len(out)+1)...)
		if err != nil {
			return nil, fmt.Errorf("list current context subgraph evidence: %w", err)
		}
		for rows.Next() {
			var evidence Evidence
			var sourceKind, sourceID string
			var observedAt int64
			if err := rows.Scan(&evidence.ID, &evidence.TenantID, &sourceKind, &sourceID, &evidence.Summary, &evidence.Confidence, &evidence.SourceAccessRef.Kind, &evidence.SourceAccessRef.ID, &evidence.SourceAccessRef.TenantID, &evidence.AccessState, &observedAt); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan current context subgraph evidence: %w", err)
			}
			evidence.SourceRef = Ref{Kind: sourceKind, ID: sourceID}
			evidence.ObservedAt = unixNanoToTime(observedAt)
			out = append(out, evidence)
			if len(out) > limit {
				rows.Close()
				return nil, fmt.Errorf("%w: current subgraph evidence exceed %d", ErrQueryBudgetExceeded, limit)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("list current context subgraph evidence: %w", err)
		}
		rows.Close()
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func currentSubgraphBatches(ids []string) [][]string {
	if len(ids) == 0 {
		return nil
	}
	batches := make([][]string, 0, (len(ids)+currentSubgraphQueryBatchSize-1)/currentSubgraphQueryBatchSize)
	for start := 0; start < len(ids); start += currentSubgraphQueryBatchSize {
		end := start + currentSubgraphQueryBatchSize
		if end > len(ids) {
			end = len(ids)
		}
		batches = append(batches, ids[start:end])
	}
	return batches
}

func currentSubgraphPlaceholders(count int) string {
	return strings.TrimSuffix(strings.Repeat("?,", count), ",")
}

func currentSubgraphArgs(tenantID string, batches ...[]string) []any {
	count := 1
	for _, batch := range batches {
		count += len(batch)
	}
	args := make([]any, 0, count)
	args = append(args, tenantID)
	for _, batch := range batches {
		for _, id := range batch {
			args = append(args, id)
		}
	}
	return args
}

func sortedSubgraphIDs[T ~bool](set map[string]T) []string {
	ids := make([]string, 0, len(set))
	for id := range set {
		if strings.TrimSpace(id) != "" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

var _ CurrentSubgraphStore = (*PlatformStore)(nil)
