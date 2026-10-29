package projection

import (
	"context"
	"errors"
	"strings"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/datavisibility"
	"orrery_v2/internal/app/identity"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

var replayHistoryFields = []string{"attempted_at", "error", "status"}

func (s *Service) ListReplayHistory(ctx context.Context, principal identity.Principal, query ReplayHistoryListQuery) (ontologycontract.CursorPage[ReplayHistoryItem], error) {
	query, limit, err := normalizeReplayHistoryListQuery(query)
	if err != nil {
		return ontologycontract.CursorPage[ReplayHistoryItem]{}, err
	}
	authorized, err := s.authorize(ctx, principal, query.TenantID)
	if err != nil {
		return ontologycontract.CursorPage[ReplayHistoryItem]{}, err
	}
	bounded, budget, cancel, err := s.budgets.Start(ctx, ontologycontract.QueryStandard)
	if err != nil {
		return ontologycontract.CursorPage[ReplayHistoryItem]{}, err
	}
	defer cancel()
	state, err := s.resolveVisibility(bounded, principal, authorized, budget)
	if err != nil {
		return ontologycontract.CursorPage[ReplayHistoryItem]{}, err
	}
	queryHash, err := replayHistoryQueryHash(query, limit)
	if err != nil {
		return ontologycontract.CursorPage[ReplayHistoryItem]{}, err
	}
	binding := projectionCursorBinding(state, queryHash)
	seek, err := s.resolveReplayHistoryCursor(query.Cursor, binding, state.cacheable)
	if err != nil {
		return ontologycontract.CursorPage[ReplayHistoryItem]{}, err
	}
	if err := budget.ConsumeStoreReads(1); err != nil {
		return ontologycontract.CursorPage[ReplayHistoryItem]{}, err
	}
	audit, err := s.audits.GetVisibleProjectionAudit(bounded, query.TenantID, query.AuditID, state.constraint)
	if err != nil {
		return ontologycontract.CursorPage[ReplayHistoryItem]{}, exactQueryError(bounded, budget, err)
	}
	if err := budget.ConsumeCandidates(1); err != nil {
		return ontologycontract.CursorPage[ReplayHistoryItem]{}, err
	}
	parent, err := s.visibleAudits(bounded, principal, state, []contextgraph.ProjectionAudit{audit}, nil)
	if err != nil {
		return ontologycontract.CursorPage[ReplayHistoryItem]{}, queryError(bounded, budget, err)
	}
	if len(parent) != 1 {
		return ontologycontract.CursorPage[ReplayHistoryItem]{}, ErrUnavailable
	}

	visible := make([]visibleReplayHistory, 0, limit+1)
	scanBatches := 0
	lastScanned := contextgraph.ProjectionReplayHistorySeek{}
	for len(visible) <= limit {
		if scanBatches >= projectionCandidateScanBatches {
			return ontologycontract.CursorPage[ReplayHistoryItem]{}, ontologycontract.ErrQueryBudgetExceeded
		}
		if err := budget.ConsumeStoreReads(1); err != nil {
			return ontologycontract.CursorPage[ReplayHistoryItem]{}, err
		}
		scanBatches++
		page, err := s.audits.ListVisibleProjectionReplayHistory(bounded, contextgraph.ProjectionReplayHistoryCursorQuery{
			TenantID: query.TenantID, AuditID: query.AuditID, Visibility: state.constraint,
			Limit: projectionScanBatch, After: seek,
		})
		if err != nil {
			return ontologycontract.CursorPage[ReplayHistoryItem]{}, queryError(bounded, budget, err)
		}
		if err := budget.ConsumeCandidates(scannedCount(len(page.Items), page.HasMore)); err != nil {
			return ontologycontract.CursorPage[ReplayHistoryItem]{}, err
		}
		if len(page.Items) == 0 {
			if page.HasMore {
				return ontologycontract.CursorPage[ReplayHistoryItem]{}, dependencyError(errors.New("projection replay history reader returned an empty truncated page"))
			}
			break
		}
		last := page.Items[len(page.Items)-1]
		lastScanned = contextgraph.ProjectionReplayHistorySeek{AttemptedAt: last.AttemptedAt, ID: last.ID}
		rows, err := s.visibleReplayHistoryRows(bounded, principal, state, query.AuditID, page.Items, replayHistoryFields)
		if err != nil {
			return ontologycontract.CursorPage[ReplayHistoryItem]{}, queryError(bounded, budget, err)
		}
		visible = append(visible, rows...)
		if len(visible) > limit || !page.HasMore {
			break
		}
		seek = lastScanned
	}

	hasMore := len(visible) > limit
	if hasMore {
		visible = visible[:limit]
	}
	items := make([]ReplayHistoryItem, len(visible))
	for index, row := range visible {
		items[index] = mapReplayHistoryItem(row.history, row.decision)
	}
	if err := budget.ConsumeResults(len(items)); err != nil {
		return ontologycontract.CursorPage[ReplayHistoryItem]{}, err
	}
	next := ""
	if hasMore {
		last := visible[len(visible)-1].history
		nextSeek := contextgraph.ProjectionReplayHistorySeek{AttemptedAt: last.AttemptedAt, ID: last.ID}
		next, err = s.issueReplayHistoryCursor(binding, state.cacheable, nextSeek)
		if err != nil {
			return ontologycontract.CursorPage[ReplayHistoryItem]{}, err
		}
	}
	return ontologycontract.NewCursorPage(items, limit, hasMore, next)
}

func normalizeReplayHistoryListQuery(query ReplayHistoryListQuery) (ReplayHistoryListQuery, int, error) {
	tenantID, auditID, err := normalizeExactRequest(query.TenantID, query.AuditID)
	if err != nil {
		return ReplayHistoryListQuery{}, 0, err
	}
	query.TenantID, query.AuditID = tenantID, auditID
	query.Cursor = strings.TrimSpace(query.Cursor)
	limit, err := ontologycontract.NormalizeLimit(query.Limit, ontologycontract.DefaultChildListLimit, ontologycontract.MaxPageLimit)
	if err != nil || len(query.Cursor) > 4096 {
		return ReplayHistoryListQuery{}, 0, ontologycontract.ErrInvalidRequest
	}
	return query, limit, nil
}

func mapReplayHistoryItem(history contextgraph.ProjectionAuditReplayHistory, decision datavisibility.ResourceDecision) ReplayHistoryItem {
	allowed := allowedFields(decision)
	item := ReplayHistoryItem{ID: history.ID}
	if allowed["status"] {
		item.Status = history.Status
	}
	if allowed["error"] {
		item.Error = safeReplayHistoryError(history.Error)
	}
	if allowed["attempted_at"] {
		item.AttemptedAt = visibleTime(history.AttemptedAt)
	}
	return item
}

func safeReplayHistoryError(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		return ""
	case "dependency_failure", "state_conflict", "unavailable", "lease_expired", "replay_failed":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "replay_failed"
	}
}
