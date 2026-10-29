package contextview

import (
	"context"
	"strings"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/datavisibility"
	"orrery_v2/internal/app/identity"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

type positionedFact struct {
	raw  contextgraph.Fact
	item FactItem
}

func (s *Service) ListFacts(ctx context.Context, principal identity.Principal, query ChildQuery) (ontologycontract.CursorPage[FactItem], error) {
	query, limit, err := normalizeChildQuery(query)
	if err != nil {
		return ontologycontract.CursorPage[FactItem]{}, err
	}
	authorized, err := s.authorize(ctx, principal, query.TenantID)
	if err != nil {
		return ontologycontract.CursorPage[FactItem]{}, err
	}
	bounded, budget, cancel, err := s.budgets.Start(ctx, ontologycontract.QueryStandard)
	if err != nil {
		return ontologycontract.CursorPage[FactItem]{}, err
	}
	defer cancel()
	visibility, err := s.resolveVisibility(bounded, principal, authorized, budget, datavisibility.ResourceObject, datavisibility.ResourceFact)
	if err != nil {
		return ontologycontract.CursorPage[FactItem]{}, err
	}
	tenantID := visibility.scope.Binding().TenantID
	parent, err := s.getVisibleParent(bounded, principal, tenantID, query.ObjectID, visibility, budget)
	if err != nil {
		return ontologycontract.CursorPage[FactItem]{}, err
	}
	queryHash, err := ontologycontract.CanonicalQueryHash(struct {
		Kind     string `json:"kind"`
		ObjectID string `json:"object_id"`
		Limit    int    `json:"limit"`
	}{"facts", query.ObjectID, limit})
	if err != nil {
		return ontologycontract.CursorPage[FactItem]{}, err
	}
	binding := contextCursorBinding(visibility, queryHash)
	after, err := s.resolveSeekCursor(query.Cursor, binding, visibility.cacheable)
	if err != nil {
		return ontologycontract.CursorPage[FactItem]{}, err
	}
	visible := make([]positionedFact, 0, limit+1)
	scanBatches := 0
	for len(visible) <= limit {
		if scanBatches >= contextCandidateScanBatches {
			return ontologycontract.CursorPage[FactItem]{}, ontologycontract.ErrQueryBudgetExceeded
		}
		if err := budget.ConsumeStoreReads(1); err != nil {
			return ontologycontract.CursorPage[FactItem]{}, err
		}
		scanBatches++
		page, err := s.graph.ListVisibleFacts(bounded, contextgraph.FactQuery{
			TenantID: tenantID, Visibility: visibility.constraint, ObjectType: parent.ObjectType,
			ObjectID: parent.ID, Limit: contextScanBatch, After: after,
		})
		if err != nil {
			return ontologycontract.CursorPage[FactItem]{}, queryError(bounded, budget, err)
		}
		if err := budget.ConsumeCandidates(len(page.Items)); err != nil {
			return ontologycontract.CursorPage[FactItem]{}, err
		}
		mapped, err := s.visibleFacts(bounded, principal, visibility, parent, page.Items)
		if err != nil {
			return ontologycontract.CursorPage[FactItem]{}, err
		}
		mappedByID := make(map[string]FactItem, len(mapped))
		for _, item := range mapped {
			mappedByID[item.ID] = item
		}
		for _, raw := range page.Items {
			item, ok := mappedByID[raw.ID]
			if !ok {
				continue
			}
			visible = append(visible, positionedFact{raw: raw, item: item})
			if len(visible) > limit {
				break
			}
		}
		if len(visible) > limit || !page.HasMore {
			break
		}
		if len(page.Items) == 0 {
			return ontologycontract.CursorPage[FactItem]{}, dependencyError(contextgraph.ErrQueryInvalid)
		}
		last := page.Items[len(page.Items)-1]
		after = contextgraph.ObjectSeek{UpdatedAt: last.UpdatedAt, ID: last.ID}
	}
	hasMore := len(visible) > limit
	if hasMore {
		visible = visible[:limit]
	}
	items := make([]FactItem, len(visible))
	for index := range visible {
		items[index] = visible[index].item
	}
	if err := budget.ConsumeResults(len(items)); err != nil {
		return ontologycontract.CursorPage[FactItem]{}, err
	}
	nextCursor := ""
	if hasMore {
		last := visible[len(visible)-1].raw
		nextSeek := contextgraph.ObjectSeek{UpdatedAt: last.UpdatedAt, ID: last.ID}
		nextCursor, err = s.issueSeekCursor(binding, visibility.cacheable, nextSeek)
		if err != nil {
			return ontologycontract.CursorPage[FactItem]{}, err
		}
	}
	return ontologycontract.NewCursorPage(items, limit, hasMore, nextCursor)
}

func normalizeChildQuery(query ChildQuery) (ChildQuery, int, error) {
	query.TenantID = strings.TrimSpace(query.TenantID)
	query.ObjectID = strings.TrimSpace(query.ObjectID)
	query.Cursor = strings.TrimSpace(query.Cursor)
	limit, err := ontologycontract.NormalizeLimit(query.Limit, ontologycontract.DefaultChildListLimit, ontologycontract.MaxPageLimit)
	if err != nil || !validID(query.TenantID) || !validID(query.ObjectID) {
		return ChildQuery{}, 0, ontologycontract.ErrInvalidRequest
	}
	return query, limit, nil
}
