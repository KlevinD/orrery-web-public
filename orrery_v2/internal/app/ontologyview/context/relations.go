package contextview

import (
	"context"
	"strings"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/datavisibility"
	"orrery_v2/internal/app/identity"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

type positionedRelation struct {
	raw  contextgraph.Relation
	item RelationItem
}

func (s *Service) ListRelations(ctx context.Context, principal identity.Principal, query RelationListQuery) (ontologycontract.CursorPage[RelationItem], error) {
	query, limit, direction, err := normalizeRelationListQuery(query)
	if err != nil {
		return ontologycontract.CursorPage[RelationItem]{}, err
	}
	authorized, err := s.authorize(ctx, principal, query.TenantID)
	if err != nil {
		return ontologycontract.CursorPage[RelationItem]{}, err
	}
	bounded, budget, cancel, err := s.budgets.Start(ctx, ontologycontract.QueryStandard)
	if err != nil {
		return ontologycontract.CursorPage[RelationItem]{}, err
	}
	defer cancel()
	visibility, err := s.resolveVisibility(bounded, principal, authorized, budget, datavisibility.ResourceObject, datavisibility.ResourceLink)
	if err != nil {
		return ontologycontract.CursorPage[RelationItem]{}, err
	}
	tenantID := visibility.scope.Binding().TenantID
	parent, err := s.getVisibleParent(bounded, principal, tenantID, query.ObjectID, visibility, budget)
	if err != nil {
		return ontologycontract.CursorPage[RelationItem]{}, err
	}
	queryHash, err := ontologycontract.CanonicalQueryHash(struct {
		Kind      string `json:"kind"`
		ObjectID  string `json:"object_id"`
		Direction string `json:"direction"`
		LinkType  string `json:"link_type"`
		Limit     int    `json:"limit"`
	}{"relations", query.ObjectID, string(direction), query.LinkType, limit})
	if err != nil {
		return ontologycontract.CursorPage[RelationItem]{}, err
	}
	binding := contextCursorBinding(visibility, queryHash)
	after, err := s.resolveSeekCursor(query.Cursor, binding, visibility.cacheable)
	if err != nil {
		return ontologycontract.CursorPage[RelationItem]{}, err
	}
	visible := make([]positionedRelation, 0, limit+1)
	scanBatches := 0
	for len(visible) <= limit {
		if scanBatches >= contextCandidateScanBatches {
			return ontologycontract.CursorPage[RelationItem]{}, ontologycontract.ErrQueryBudgetExceeded
		}
		if err := budget.ConsumeStoreReads(1); err != nil {
			return ontologycontract.CursorPage[RelationItem]{}, err
		}
		scanBatches++
		page, err := s.graph.ListVisibleRelations(bounded, contextgraph.RelationQuery{
			TenantID: tenantID, Visibility: visibility.constraint, ObjectID: query.ObjectID,
			Direction: direction, Limit: contextScanBatch, After: after,
		})
		if err != nil {
			return ontologycontract.CursorPage[RelationItem]{}, queryError(bounded, budget, err)
		}
		if err := budget.ConsumeCandidates(len(page.Items)); err != nil {
			return ontologycontract.CursorPage[RelationItem]{}, err
		}
		mapped, err := s.visibleRelations(bounded, principal, visibility, parent, page.Items)
		if err != nil {
			return ontologycontract.CursorPage[RelationItem]{}, err
		}
		mappedByID := make(map[string]RelationItem, len(mapped))
		for _, item := range mapped {
			if query.LinkType == "" || item.LinkType == query.LinkType {
				mappedByID[item.ID] = item
			}
		}
		for _, raw := range page.Items {
			item, ok := mappedByID[raw.Link.ID]
			if !ok {
				continue
			}
			visible = append(visible, positionedRelation{raw: raw, item: item})
			if len(visible) > limit {
				break
			}
		}
		if len(visible) > limit || !page.HasMore {
			break
		}
		if len(page.Items) == 0 {
			return ontologycontract.CursorPage[RelationItem]{}, dependencyError(contextgraph.ErrQueryInvalid)
		}
		last := page.Items[len(page.Items)-1].Link
		after = contextgraph.ObjectSeek{UpdatedAt: last.UpdatedAt, ID: last.ID}
	}
	hasMore := len(visible) > limit
	if hasMore {
		visible = visible[:limit]
	}
	items := make([]RelationItem, len(visible))
	for index := range visible {
		items[index] = visible[index].item
	}
	if err := s.populateRelationLabels(bounded, tenantID, items, budget); err != nil {
		return ontologycontract.CursorPage[RelationItem]{}, err
	}
	if err := budget.ConsumeResults(len(items)); err != nil {
		return ontologycontract.CursorPage[RelationItem]{}, err
	}
	nextCursor := ""
	if hasMore {
		last := visible[len(visible)-1].raw.Link
		nextSeek := contextgraph.ObjectSeek{UpdatedAt: last.UpdatedAt, ID: last.ID}
		nextCursor, err = s.issueSeekCursor(binding, visibility.cacheable, nextSeek)
		if err != nil {
			return ontologycontract.CursorPage[RelationItem]{}, err
		}
	}
	return ontologycontract.NewCursorPage(items, limit, hasMore, nextCursor)
}

func normalizeRelationListQuery(query RelationListQuery) (RelationListQuery, int, contextgraph.RelationDirection, error) {
	query.TenantID = strings.TrimSpace(query.TenantID)
	query.ObjectID = strings.TrimSpace(query.ObjectID)
	query.Direction = strings.ToLower(strings.TrimSpace(query.Direction))
	if query.Direction == "" {
		query.Direction = string(contextgraph.RelationBoth)
	}
	query.LinkType = strings.ToLower(strings.TrimSpace(query.LinkType))
	query.Cursor = strings.TrimSpace(query.Cursor)
	limit, err := ontologycontract.NormalizeLimit(query.Limit, ontologycontract.DefaultChildListLimit, ontologycontract.MaxPageLimit)
	direction := contextgraph.RelationDirection(query.Direction)
	if err != nil || !validID(query.TenantID) || !validID(query.ObjectID) || !validOptionalID(query.LinkType) || !validRelationDirection(direction) {
		return RelationListQuery{}, 0, "", ontologycontract.ErrInvalidRequest
	}
	return query, limit, direction, nil
}

func validRelationDirection(direction contextgraph.RelationDirection) bool {
	switch direction {
	case contextgraph.RelationIncoming, contextgraph.RelationOutgoing, contextgraph.RelationBoth:
		return true
	default:
		return false
	}
}
