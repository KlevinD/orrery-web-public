package lineage

import (
	"context"
	"errors"
	"strings"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/datavisibility"
	"orrery_v2/internal/app/identity"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

type positionedProjectionTrace struct {
	raw  contextgraph.ProjectionAudit
	item ProjectionTraceItem
}

func (s *Service) ListProjectionTrace(ctx context.Context, principal identity.Principal, query FactPageQuery) (ontologycontract.CursorPage[ProjectionTraceItem], error) {
	query, limit, err := normalizeFactPageQuery(query)
	if err != nil {
		return ontologycontract.CursorPage[ProjectionTraceItem]{}, err
	}
	authorized, err := s.authorize(ctx, principal, query.TenantID)
	if err != nil {
		return ontologycontract.CursorPage[ProjectionTraceItem]{}, err
	}
	bounded, budget, cancel, err := s.budgets.Start(ctx, ontologycontract.QueryStandard)
	if err != nil {
		return ontologycontract.CursorPage[ProjectionTraceItem]{}, err
	}
	defer cancel()
	visibility, err := s.resolveVisibility(bounded, principal, authorized, budget,
		datavisibility.ResourceFact, datavisibility.ResourceObject, datavisibility.ResourceEvent, datavisibility.ResourceAudit,
	)
	if err != nil {
		return ontologycontract.CursorPage[ProjectionTraceItem]{}, err
	}
	tenantID := visibility.scope.Binding().TenantID
	detail, factDecision, _, err := s.getVisibleFact(bounded, principal, tenantID, query.FactID, visibility, budget, []string{"provenance"}, nil)
	if err != nil {
		return ontologycontract.CursorPage[ProjectionTraceItem]{}, err
	}
	if maskedFieldSet(factDecision)["provenance"] || strings.ToLower(strings.TrimSpace(detail.Fact.Provenance.Kind)) != "event" || !validID(detail.Fact.Provenance.ID) {
		return ontologycontract.CursorPage[ProjectionTraceItem]{}, ErrUnavailable
	}
	if err := budget.ConsumeStoreReads(1); err != nil {
		return ontologycontract.CursorPage[ProjectionTraceItem]{}, err
	}
	event, err := s.events.GetSafeOntologyEvent(bounded, tenantID, detail.Fact.Provenance.ID, detail.Fact.SourceAccessRef)
	if errors.Is(err, ErrSafeEventUnavailable) || errors.Is(err, contextgraph.ErrNotFound) {
		return ontologycontract.CursorPage[ProjectionTraceItem]{}, ErrUnavailable
	}
	if err != nil {
		return ontologycontract.CursorPage[ProjectionTraceItem]{}, dependencyError(err)
	}
	if event.TenantID != tenantID || event.Metadata.ID != detail.Fact.Provenance.ID || contextgraph.NormalizeAccessRef(event.SourceAccessRef) != contextgraph.NormalizeAccessRef(detail.Fact.SourceAccessRef) {
		return ontologycontract.CursorPage[ProjectionTraceItem]{}, ErrUnavailable
	}
	visibleEvent, ok, err := s.visibleEvent(bounded, principal, visibility, event)
	if err != nil {
		return ontologycontract.CursorPage[ProjectionTraceItem]{}, err
	}
	if !ok || !validID(visibleEvent.Type) {
		return ontologycontract.CursorPage[ProjectionTraceItem]{}, ErrUnavailable
	}
	queryHash, err := ontologycontract.CanonicalQueryHash(struct {
		Kind      string `json:"kind"`
		FactID    string `json:"fact_id"`
		EventID   string `json:"event_id"`
		EventType string `json:"event_type"`
		Limit     int    `json:"limit"`
		Version   int64  `json:"version"`
		Updated   int64  `json:"updated"`
	}{"projection-trace", query.FactID, visibleEvent.ID, visibleEvent.Type, limit, detail.Fact.SourceVersion, detail.Fact.UpdatedAt.UTC().UnixNano()})
	if err != nil {
		return ontologycontract.CursorPage[ProjectionTraceItem]{}, err
	}
	binding := lineageCursorBinding(visibility, queryHash)
	after, err := s.resolveProjectionSeek(query.Cursor, binding, visibility.cacheable)
	if err != nil {
		return ontologycontract.CursorPage[ProjectionTraceItem]{}, err
	}
	visible := make([]positionedProjectionTrace, 0, limit+1)
	scanBatches := 0
	for len(visible) <= limit {
		if scanBatches >= lineageCandidateScanBatches {
			return ontologycontract.CursorPage[ProjectionTraceItem]{}, ontologycontract.ErrQueryBudgetExceeded
		}
		if err := budget.ConsumeStoreReads(1); err != nil {
			return ontologycontract.CursorPage[ProjectionTraceItem]{}, err
		}
		scanBatches++
		page, err := s.graph.ListVisibleProjectionTrace(bounded, contextgraph.ProjectionTraceQuery{
			TenantID: tenantID, SourceEventID: visibleEvent.ID, SourceEventType: visibleEvent.Type,
			Visibility: visibility.constraint, Limit: lineageScanBatch, After: after,
		})
		if err != nil {
			return ontologycontract.CursorPage[ProjectionTraceItem]{}, queryError(bounded, budget, err)
		}
		if err := budget.ConsumeCandidates(len(page.Items)); err != nil {
			return ontologycontract.CursorPage[ProjectionTraceItem]{}, err
		}
		mapped, err := s.visibleProjectionTrace(bounded, principal, visibility, tenantID, detail.Fact.SourceAccessRef, visibleEvent, page.Items)
		if err != nil {
			return ontologycontract.CursorPage[ProjectionTraceItem]{}, err
		}
		mappedByID := make(map[string]ProjectionTraceItem, len(mapped))
		for _, item := range mapped {
			mappedByID[item.ID] = item
		}
		for _, raw := range page.Items {
			item, ok := mappedByID[raw.ID]
			if !ok {
				continue
			}
			visible = append(visible, positionedProjectionTrace{raw: raw, item: item})
			if len(visible) > limit {
				break
			}
		}
		if len(visible) > limit || !page.HasMore {
			break
		}
		if len(page.Items) == 0 {
			return ontologycontract.CursorPage[ProjectionTraceItem]{}, dependencyError(contextgraph.ErrQueryInvalid)
		}
		last := page.Items[len(page.Items)-1]
		after = contextgraph.ProjectionAuditSeek{ObservedAt: last.ObservedAt, ID: last.ID}
	}
	hasMore := len(visible) > limit
	if hasMore {
		visible = visible[:limit]
	}
	items := make([]ProjectionTraceItem, len(visible))
	for index := range visible {
		items[index] = visible[index].item
	}
	if err := budget.ConsumeResults(len(items)); err != nil {
		return ontologycontract.CursorPage[ProjectionTraceItem]{}, err
	}
	nextCursor := ""
	if hasMore {
		last := visible[len(visible)-1].raw
		nextSeek := contextgraph.ProjectionAuditSeek{ObservedAt: last.ObservedAt, ID: last.ID}
		nextCursor, err = s.issueProjectionCursor(binding, visibility.cacheable, nextSeek)
		if err != nil {
			return ontologycontract.CursorPage[ProjectionTraceItem]{}, err
		}
	}
	return ontologycontract.NewCursorPage(items, limit, hasMore, nextCursor)
}

func (s *Service) visibleProjectionTrace(ctx context.Context, principal identity.Principal, state visibilityState, tenantID string, expectedRef contextgraph.AccessRef, event SafeEventMetadata, rows []contextgraph.ProjectionAudit) ([]ProjectionTraceItem, error) {
	valid := make([]contextgraph.ProjectionAudit, 0, len(rows))
	resources := make([]datavisibility.ResourceDescriptor, 0, len(rows))
	expectedRef = contextgraph.NormalizeAccessRef(expectedRef)
	for _, row := range rows {
		if row.TenantID != tenantID || row.SourceEventID != event.ID || row.SourceEventType != event.Type || contextgraph.NormalizeAccessRef(row.SourceAccessRef) != expectedRef {
			continue
		}
		valid = append(valid, row)
		resources = append(resources, datavisibility.ResourceDescriptor{
			Key: "audit:" + row.ID, Kind: datavisibility.ResourceAudit, TenantID: row.TenantID,
			Type: row.Status, FieldPaths: []string{"status", "reason_code", "reason", "observed_at"},
			AccessRefs: visibilityAccessRefs(row.SourceAccessRef), Payload: row,
		})
	}
	if len(resources) == 0 {
		return nil, nil
	}
	decision, err := s.visibility.Evaluate(ctx, datavisibility.EvaluateRequest{
		Principal: identity.NormalizePrincipal(principal), Purpose: "ontology.lineage.read", Resources: resources,
	})
	if err != nil {
		return nil, dependencyError(err)
	}
	if err := verifyVisibilityRevision(state, datavisibility.ResourceAudit, decision); err != nil {
		return nil, err
	}
	out := make([]ProjectionTraceItem, 0, len(valid))
	for _, row := range valid {
		resource, ok := decision.Resource("audit:" + row.ID)
		if !ok {
			return nil, dependencyError(datavisibility.ErrIncompleteDecision)
		}
		masked := maskedFieldSet(resource)
		if hiddenDecision(resource) || masked["status"] {
			continue
		}
		item := ProjectionTraceItem{ID: row.ID, Status: row.Status, ReasonCode: row.ReasonCode, Reason: row.Reason, ObservedAt: visibleTime(row.ObservedAt)}
		if masked["reason_code"] {
			item.ReasonCode = ""
		}
		if masked["reason"] {
			item.Reason = ""
		}
		if masked["observed_at"] {
			item.ObservedAt = nil
		}
		out = append(out, item)
	}
	return out, nil
}
