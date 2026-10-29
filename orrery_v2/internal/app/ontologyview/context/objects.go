package contextview

import (
	"context"
	"strings"
	"unicode/utf8"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/datavisibility"
	"orrery_v2/internal/app/identity"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

const contextScanBatch = contextgraph.MaxQueryPageSize
const contextCandidateScanBatches = 8

type positionedObject struct {
	raw  contextgraph.Object
	item ObjectItem
}

func (s *Service) ListObjects(ctx context.Context, principal identity.Principal, query ObjectListQuery) (ontologycontract.CursorPage[ObjectItem], error) {
	query, limit, err := normalizeObjectListQuery(query)
	if err != nil {
		return ontologycontract.CursorPage[ObjectItem]{}, err
	}
	authorized, err := s.authorize(ctx, principal, query.TenantID)
	if err != nil {
		return ontologycontract.CursorPage[ObjectItem]{}, err
	}
	bounded, budget, cancel, err := s.budgets.Start(ctx, ontologycontract.QueryStandard)
	if err != nil {
		return ontologycontract.CursorPage[ObjectItem]{}, err
	}
	defer cancel()
	visibility, err := s.resolveVisibility(bounded, principal, authorized, budget, datavisibility.ResourceObject)
	if err != nil {
		return ontologycontract.CursorPage[ObjectItem]{}, err
	}
	storeObjectType := ""
	if query.ObjectType != "" {
		filterable, err := s.resolveFilterFields(bounded, principal, visibility, datavisibility.ResourceObject, "object_type")
		if err != nil {
			return ontologycontract.CursorPage[ObjectItem]{}, err
		}
		if filterable["object_type"] {
			storeObjectType = query.ObjectType
		}
	}
	queryHash, err := ontologycontract.CanonicalQueryHash(struct {
		ObjectType string `json:"object_type"`
		Status     string `json:"status"`
		Source     string `json:"source"`
		Search     string `json:"search"`
		Limit      int    `json:"limit"`
	}{query.ObjectType, query.Status, query.Source, strings.ToLower(query.Search), limit})
	if err != nil {
		return ontologycontract.CursorPage[ObjectItem]{}, err
	}
	binding := contextCursorBinding(visibility, queryHash)
	after, err := s.resolveSeekCursor(query.Cursor, binding, visibility.cacheable)
	if err != nil {
		return ontologycontract.CursorPage[ObjectItem]{}, err
	}
	visible := make([]positionedObject, 0, limit+1)
	tenantID := visibility.scope.Binding().TenantID
	scanBatches := 0
	for len(visible) <= limit {
		if scanBatches >= contextCandidateScanBatches {
			return ontologycontract.CursorPage[ObjectItem]{}, ontologycontract.ErrQueryBudgetExceeded
		}
		if err := budget.ConsumeStoreReads(1); err != nil {
			return ontologycontract.CursorPage[ObjectItem]{}, err
		}
		scanBatches++
		page, err := s.graph.ListVisibleObjects(bounded, contextgraph.ObjectQuery{
			TenantID: tenantID, Visibility: visibility.constraint, ObjectType: storeObjectType, Limit: contextScanBatch, After: after,
		})
		if err != nil {
			return ontologycontract.CursorPage[ObjectItem]{}, queryError(bounded, budget, err)
		}
		if err := budget.ConsumeCandidates(len(page.Items)); err != nil {
			return ontologycontract.CursorPage[ObjectItem]{}, err
		}
		mapped, err := s.visibleObjectRows(bounded, principal, visibility, page.Items, false, "canonical_key", "source")
		if err != nil {
			return ontologycontract.CursorPage[ObjectItem]{}, err
		}
		mappedByID := make(map[string]ObjectItem, len(mapped))
		for _, item := range mapped {
			if objectMatchesListQuery(item, query) {
				mappedByID[item.raw.ID] = item.detail.ObjectItem
			}
		}
		for _, raw := range page.Items {
			item, ok := mappedByID[raw.ID]
			if !ok {
				continue
			}
			visible = append(visible, positionedObject{raw: raw, item: item})
			if len(visible) > limit {
				break
			}
		}
		if len(visible) > limit || !page.HasMore {
			break
		}
		if len(page.Items) == 0 {
			return ontologycontract.CursorPage[ObjectItem]{}, dependencyError(contextgraph.ErrQueryInvalid)
		}
		last := page.Items[len(page.Items)-1]
		after = contextgraph.ObjectSeek{UpdatedAt: last.UpdatedAt, ID: last.ID}
	}
	hasMore := len(visible) > limit
	if hasMore {
		visible = visible[:limit]
	}
	items := make([]ObjectItem, len(visible))
	for index := range visible {
		items[index] = visible[index].item
	}
	if err := budget.ConsumeResults(len(items)); err != nil {
		return ontologycontract.CursorPage[ObjectItem]{}, err
	}
	nextCursor := ""
	if hasMore {
		last := visible[len(visible)-1].raw
		nextSeek := contextgraph.ObjectSeek{UpdatedAt: last.UpdatedAt, ID: last.ID}
		nextCursor, err = s.issueSeekCursor(binding, visibility.cacheable, nextSeek)
		if err != nil {
			return ontologycontract.CursorPage[ObjectItem]{}, err
		}
	}
	return ontologycontract.NewCursorPage(items, limit, hasMore, nextCursor)
}

func objectMatchesListQuery(row visibleObjectRow, query ObjectListQuery) bool {
	allowed := allowedFieldSet(row.decision)
	if query.ObjectType != "" && (!allowed["object_type"] || strings.ToLower(row.raw.ObjectType) != query.ObjectType) {
		return false
	}
	if query.Status != "" && (!allowed["status"] || strings.ToLower(row.raw.Status) != query.Status) {
		return false
	}
	if query.Source != "" && (!allowed["source"] || strings.ToLower(row.raw.OwnerAccessRef.Kind) != query.Source) {
		return false
	}
	if query.Search == "" {
		return true
	}
	needle := strings.ToLower(query.Search)
	return strings.Contains(strings.ToLower(row.raw.ID), needle) ||
		allowed["label"] && strings.Contains(strings.ToLower(row.raw.Label), needle) ||
		allowed["canonical_key"] && strings.Contains(strings.ToLower(row.raw.CanonicalKey), needle)
}

func allowedFieldSet(decision datavisibility.ResourceDecision) map[string]bool {
	out := make(map[string]bool, len(decision.Fields.Allowed))
	for _, field := range decision.Fields.Allowed {
		out[field] = true
	}
	return out
}

func (s *Service) GetObject(ctx context.Context, principal identity.Principal, tenantID, objectID string) (ObjectDetail, error) {
	tenantID, objectID = strings.TrimSpace(tenantID), strings.TrimSpace(objectID)
	if !validID(tenantID) || !validID(objectID) {
		return ObjectDetail{}, ontologycontract.ErrInvalidRequest
	}
	authorized, err := s.authorize(ctx, principal, tenantID)
	if err != nil {
		return ObjectDetail{}, err
	}
	bounded, budget, cancel, err := s.budgets.Start(ctx, ontologycontract.QueryStandard)
	if err != nil {
		return ObjectDetail{}, err
	}
	defer cancel()
	visibility, err := s.resolveVisibility(bounded, principal, authorized, budget, datavisibility.ResourceObject)
	if err != nil {
		return ObjectDetail{}, err
	}
	if err := budget.ConsumeStoreReads(1); err != nil {
		return ObjectDetail{}, err
	}
	object, err := s.graph.GetVisibleObject(bounded, visibility.scope.Binding().TenantID, objectID, visibility.constraint)
	if err != nil {
		return ObjectDetail{}, queryError(bounded, budget, err)
	}
	if err := budget.ConsumeCandidates(1); err != nil {
		return ObjectDetail{}, err
	}
	items, err := s.visibleObjects(bounded, principal, visibility, []contextgraph.Object{object}, true)
	if err != nil {
		return ObjectDetail{}, err
	}
	if len(items) != 1 {
		return ObjectDetail{}, ErrUnavailable
	}
	if err := budget.ConsumeResults(1); err != nil {
		return ObjectDetail{}, err
	}
	return items[0], nil
}

func (s *Service) getVisibleParent(ctx context.Context, principal identity.Principal, tenantID, objectID string, visibility visibilityState, budget *ontologycontract.BudgetTracker) (contextgraph.Object, error) {
	if err := budget.ConsumeStoreReads(1); err != nil {
		return contextgraph.Object{}, err
	}
	object, err := s.graph.GetVisibleObject(ctx, tenantID, objectID, visibility.constraint)
	if err != nil {
		return contextgraph.Object{}, queryError(ctx, budget, err)
	}
	if err := budget.ConsumeCandidates(1); err != nil {
		return contextgraph.Object{}, err
	}
	items, err := s.visibleObjects(ctx, principal, visibility, []contextgraph.Object{object}, false)
	if err != nil {
		return contextgraph.Object{}, err
	}
	if len(items) != 1 {
		return contextgraph.Object{}, ErrUnavailable
	}
	return object, nil
}

func normalizeObjectListQuery(query ObjectListQuery) (ObjectListQuery, int, error) {
	query.TenantID = strings.TrimSpace(query.TenantID)
	query.ObjectType = strings.ToLower(strings.TrimSpace(query.ObjectType))
	query.Status = strings.ToLower(strings.TrimSpace(query.Status))
	query.Source = strings.ToLower(strings.TrimSpace(query.Source))
	query.Search = strings.TrimSpace(query.Search)
	query.Cursor = strings.TrimSpace(query.Cursor)
	limit, err := ontologycontract.NormalizeLimit(query.Limit, ontologycontract.DefaultListLimit, ontologycontract.MaxPageLimit)
	if err != nil || !validID(query.TenantID) || !validOptionalID(query.ObjectType) || !validOptionalID(query.Status) || !validOptionalID(query.Source) || !utf8.ValidString(query.Search) || utf8.RuneCountInString(query.Search) > 128 {
		return ObjectListQuery{}, 0, ontologycontract.ErrInvalidRequest
	}
	return query, limit, nil
}

func validID(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= 256
}

func validOptionalID(value string) bool {
	return value == "" || validID(value)
}
