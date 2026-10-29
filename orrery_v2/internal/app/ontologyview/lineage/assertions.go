package lineage

import (
	"context"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/datavisibility"
	"orrery_v2/internal/app/identity"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

type positionedAssertion struct {
	raw  contextgraph.FactAssertion
	item AssertionItem
}

func (s *Service) ListAssertions(ctx context.Context, principal identity.Principal, query FactPageQuery) (ontologycontract.CursorPage[AssertionItem], error) {
	query, limit, err := normalizeFactPageQuery(query)
	if err != nil {
		return ontologycontract.CursorPage[AssertionItem]{}, err
	}
	authorized, err := s.authorize(ctx, principal, query.TenantID)
	if err != nil {
		return ontologycontract.CursorPage[AssertionItem]{}, err
	}
	bounded, budget, cancel, err := s.budgets.Start(ctx, ontologycontract.QueryStandard)
	if err != nil {
		return ontologycontract.CursorPage[AssertionItem]{}, err
	}
	defer cancel()
	visibility, err := s.resolveVisibility(bounded, principal, authorized, budget, datavisibility.ResourceFact, datavisibility.ResourceObject)
	if err != nil {
		return ontologycontract.CursorPage[AssertionItem]{}, err
	}
	tenantID := visibility.scope.Binding().TenantID
	detail, _, _, err := s.getVisibleFact(bounded, principal, tenantID, query.FactID, visibility, budget, nil, nil)
	if err != nil {
		return ontologycontract.CursorPage[AssertionItem]{}, err
	}
	queryHash, err := ontologycontract.CanonicalQueryHash(struct {
		Kind    string `json:"kind"`
		FactID  string `json:"fact_id"`
		Limit   int    `json:"limit"`
		Version int64  `json:"version"`
		Updated int64  `json:"updated"`
	}{"assertions", query.FactID, limit, detail.Fact.SourceVersion, detail.Fact.UpdatedAt.UTC().UnixNano()})
	if err != nil {
		return ontologycontract.CursorPage[AssertionItem]{}, err
	}
	binding := lineageCursorBinding(visibility, queryHash)
	after, err := s.resolveAssertionSeek(query.Cursor, binding, visibility.cacheable)
	if err != nil {
		return ontologycontract.CursorPage[AssertionItem]{}, err
	}
	visible := make([]positionedAssertion, 0, limit+1)
	scanBatches := 0
	for len(visible) <= limit {
		if scanBatches >= lineageCandidateScanBatches {
			return ontologycontract.CursorPage[AssertionItem]{}, ontologycontract.ErrQueryBudgetExceeded
		}
		if err := budget.ConsumeStoreReads(1); err != nil {
			return ontologycontract.CursorPage[AssertionItem]{}, err
		}
		scanBatches++
		page, err := s.graph.ListVisibleFactAssertions(bounded, contextgraph.FactAssertionQuery{
			TenantID: tenantID, FactID: query.FactID, Visibility: visibility.constraint, Limit: lineageScanBatch, After: after,
		})
		if err != nil {
			return ontologycontract.CursorPage[AssertionItem]{}, queryError(bounded, budget, err)
		}
		if err := budget.ConsumeCandidates(len(page.Items)); err != nil {
			return ontologycontract.CursorPage[AssertionItem]{}, err
		}
		mapped, err := s.visibleAssertions(bounded, principal, visibility, page.Items)
		if err != nil {
			return ontologycontract.CursorPage[AssertionItem]{}, err
		}
		mappedByID := make(map[string]AssertionItem, len(mapped))
		for _, item := range mapped {
			mappedByID[item.ID] = item
		}
		for _, raw := range page.Items {
			item, ok := mappedByID[raw.AssertionID]
			if !ok {
				continue
			}
			visible = append(visible, positionedAssertion{raw: raw, item: item})
			if len(visible) > limit {
				break
			}
		}
		if len(visible) > limit || !page.HasMore {
			break
		}
		if len(page.Items) == 0 {
			return ontologycontract.CursorPage[AssertionItem]{}, dependencyError(contextgraph.ErrQueryInvalid)
		}
		last := page.Items[len(page.Items)-1]
		after = contextgraph.FactAssertionSeek{ObservedAt: last.ObservedAt, ID: last.AssertionID}
	}
	hasMore := len(visible) > limit
	if hasMore {
		visible = visible[:limit]
	}
	items := make([]AssertionItem, len(visible))
	for index := range visible {
		items[index] = visible[index].item
	}
	if err := budget.ConsumeResults(len(items)); err != nil {
		return ontologycontract.CursorPage[AssertionItem]{}, err
	}
	nextCursor := ""
	if hasMore {
		last := visible[len(visible)-1].raw
		nextSeek := contextgraph.FactAssertionSeek{ObservedAt: last.ObservedAt, ID: last.AssertionID}
		nextCursor, err = s.issueAssertionCursor(binding, visibility.cacheable, nextSeek)
		if err != nil {
			return ontologycontract.CursorPage[AssertionItem]{}, err
		}
	}
	return ontologycontract.NewCursorPage(items, limit, hasMore, nextCursor)
}

func (s *Service) visibleAssertions(ctx context.Context, principal identity.Principal, state visibilityState, rows []contextgraph.FactAssertion) ([]AssertionItem, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	resources := make([]datavisibility.ResourceDescriptor, 0, len(rows))
	for _, row := range rows {
		if !contextgraph.NormalizeAccessRef(row.Fact.SourceAccessRef).Resolved() || !contextgraph.NormalizeAccessRef(row.Fact.SubjectAccessRef).Resolved() {
			return nil, dependencyError(contextgraph.ErrAccessRefConflict)
		}
		resources = append(resources, datavisibility.ResourceDescriptor{
			Key: "assertion:" + row.AssertionID, Kind: datavisibility.ResourceFact,
			TenantID: row.Fact.TenantID, Type: row.Fact.Predicate,
			FieldPaths: []string{"value", "status", "confidence", "observed_at"},
			AccessRefs: visibilityAccessRefs(row.Fact.SourceAccessRef, row.Fact.SubjectAccessRef), Payload: row.Fact,
		})
	}
	decision, err := s.visibility.Evaluate(ctx, datavisibility.EvaluateRequest{
		Principal: identity.NormalizePrincipal(principal), Purpose: "ontology.lineage.read", Resources: resources,
	})
	if err != nil {
		return nil, dependencyError(err)
	}
	if err := verifyVisibilityRevision(state, datavisibility.ResourceFact, decision); err != nil {
		return nil, err
	}
	out := make([]AssertionItem, 0, len(rows))
	for _, row := range rows {
		resource, ok := decision.Resource("assertion:" + row.AssertionID)
		if !ok {
			return nil, dependencyError(datavisibility.ErrIncompleteDecision)
		}
		if hiddenDecision(resource) {
			continue
		}
		masked := maskedFieldSet(resource)
		item := AssertionItem{
			ID: row.AssertionID, Value: row.Fact.Value, Status: row.Fact.Status,
			Confidence: floatValue(row.Fact.Confidence), ObservedAt: visibleTime(row.ObservedAt),
		}
		if masked["value"] {
			item.Value = nil
		}
		if masked["status"] {
			item.Status = ""
		}
		if masked["confidence"] {
			item.Confidence = nil
		}
		if masked["observed_at"] {
			item.ObservedAt = nil
		}
		out = append(out, item)
	}
	return out, nil
}
