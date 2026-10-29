package projection

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"unicode/utf8"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/datavisibility"
	"orrery_v2/internal/app/identity"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

var auditListFields = []string{"observed_at", "reason_code", "source_event_type", "status"}
var auditDetailFields = []string{"details", "observed_at", "reason", "reason_code", "source_event_id", "source_event_type", "status"}

func (s *Service) ListAudits(ctx context.Context, principal identity.Principal, query AuditListQuery) (ontologycontract.CursorPage[AuditItem], error) {
	query, limit, err := normalizeAuditListQuery(query)
	if err != nil {
		return ontologycontract.CursorPage[AuditItem]{}, err
	}
	authorized, err := s.authorize(ctx, principal, query.TenantID)
	if err != nil {
		return ontologycontract.CursorPage[AuditItem]{}, err
	}
	bounded, budget, cancel, err := s.budgets.Start(ctx, ontologycontract.QueryStandard)
	if err != nil {
		return ontologycontract.CursorPage[AuditItem]{}, err
	}
	defer cancel()
	state, err := s.resolveVisibility(bounded, principal, authorized, budget)
	if err != nil {
		return ontologycontract.CursorPage[AuditItem]{}, err
	}
	queryHash, err := auditListQueryHash(query, limit)
	if err != nil {
		return ontologycontract.CursorPage[AuditItem]{}, err
	}
	binding := projectionCursorBinding(state, queryHash)
	seek, err := s.resolveAuditCursor(query.Cursor, binding, state.cacheable)
	if err != nil {
		return ontologycontract.CursorPage[AuditItem]{}, err
	}

	visible := make([]visibleAudit, 0, limit+1)
	scanBatches := 0
	lastScanned := contextgraph.ProjectionAuditSeek{}
	for len(visible) <= limit {
		if scanBatches >= projectionCandidateScanBatches {
			return ontologycontract.CursorPage[AuditItem]{}, ontologycontract.ErrQueryBudgetExceeded
		}
		if err := budget.ConsumeStoreReads(1); err != nil {
			return ontologycontract.CursorPage[AuditItem]{}, err
		}
		scanBatches++
		page, err := s.audits.ListVisibleProjectionAudits(bounded, contextgraph.ProjectionAuditCursorQuery{
			TenantID: query.TenantID, Visibility: state.constraint, Limit: projectionScanBatch, After: seek,
		})
		if err != nil {
			return ontologycontract.CursorPage[AuditItem]{}, queryError(bounded, budget, err)
		}
		if err := budget.ConsumeCandidates(scannedCount(len(page.Items), page.HasMore)); err != nil {
			return ontologycontract.CursorPage[AuditItem]{}, err
		}
		if len(page.Items) == 0 {
			if page.HasMore {
				return ontologycontract.CursorPage[AuditItem]{}, dependencyError(errors.New("projection audit reader returned an empty truncated page"))
			}
			break
		}
		last := page.Items[len(page.Items)-1]
		lastScanned = contextgraph.ProjectionAuditSeek{ObservedAt: last.ObservedAt, ID: last.ID}
		rows, err := s.visibleAudits(bounded, principal, state, page.Items, auditListFields)
		if err != nil {
			return ontologycontract.CursorPage[AuditItem]{}, queryError(bounded, budget, err)
		}
		for _, row := range rows {
			allowed := allowedFields(row.decision)
			if query.Status != "" && (!allowed["status"] || row.audit.Status != query.Status) ||
				query.ReasonCode != "" && (!allowed["reason_code"] || row.audit.ReasonCode != query.ReasonCode) {
				continue
			}
			visible = append(visible, row)
			if len(visible) > limit {
				break
			}
		}
		if len(visible) > limit || !page.HasMore {
			break
		}
		seek = lastScanned
	}

	hasMore := len(visible) > limit
	if hasMore {
		visible = visible[:limit]
	}
	items := make([]AuditItem, len(visible))
	for index, row := range visible {
		items[index] = mapAuditItem(row.audit, row.decision)
	}
	if err := budget.ConsumeResults(len(items)); err != nil {
		return ontologycontract.CursorPage[AuditItem]{}, err
	}
	next := ""
	if hasMore {
		last := visible[len(visible)-1].audit
		nextSeek := contextgraph.ProjectionAuditSeek{ObservedAt: last.ObservedAt, ID: last.ID}
		next, err = s.issueAuditCursor(binding, state.cacheable, nextSeek)
		if err != nil {
			return ontologycontract.CursorPage[AuditItem]{}, err
		}
	}
	return ontologycontract.NewCursorPage(items, limit, hasMore, next)
}

func (s *Service) GetAudit(ctx context.Context, principal identity.Principal, tenantID, auditID string) (AuditDetail, error) {
	tenantID, auditID, err := normalizeExactRequest(tenantID, auditID)
	if err != nil {
		return AuditDetail{}, err
	}
	authorized, err := s.authorize(ctx, principal, tenantID)
	if err != nil {
		return AuditDetail{}, err
	}
	bounded, budget, cancel, err := s.budgets.Start(ctx, ontologycontract.QueryStandard)
	if err != nil {
		return AuditDetail{}, err
	}
	defer cancel()
	state, err := s.resolveVisibility(bounded, principal, authorized, budget)
	if err != nil {
		return AuditDetail{}, err
	}
	if err := budget.ConsumeStoreReads(1); err != nil {
		return AuditDetail{}, err
	}
	audit, err := s.audits.GetVisibleProjectionAudit(bounded, tenantID, auditID, state.constraint)
	if err != nil {
		return AuditDetail{}, exactQueryError(bounded, budget, err)
	}
	if err := budget.ConsumeCandidates(1); err != nil {
		return AuditDetail{}, err
	}
	rows, err := s.visibleAudits(bounded, principal, state, []contextgraph.ProjectionAudit{audit}, auditDetailFields)
	if err != nil {
		return AuditDetail{}, queryError(bounded, budget, err)
	}
	if len(rows) != 1 {
		return AuditDetail{}, ErrUnavailable
	}
	if err := budget.ConsumeResults(1); err != nil {
		return AuditDetail{}, err
	}
	return mapAuditDetail(rows[0].audit, rows[0].decision), nil
}

func normalizeAuditListQuery(query AuditListQuery) (AuditListQuery, int, error) {
	query.TenantID = strings.TrimSpace(query.TenantID)
	query.Status = strings.ToLower(strings.TrimSpace(query.Status))
	query.ReasonCode = strings.ToLower(strings.TrimSpace(query.ReasonCode))
	query.Cursor = strings.TrimSpace(query.Cursor)
	limit, err := ontologycontract.NormalizeLimit(query.Limit, ontologycontract.DefaultListLimit, ontologycontract.MaxPageLimit)
	if err != nil || !validID(query.TenantID) || !utf8.ValidString(query.ReasonCode) || len(query.ReasonCode) > 256 || len(query.Cursor) > 4096 {
		return AuditListQuery{}, 0, ontologycontract.ErrInvalidRequest
	}
	if query.Status != "" {
		switch query.Status {
		case "applied", "skipped", "replayed", "acknowledged", "failed":
		default:
			return AuditListQuery{}, 0, ontologycontract.ErrInvalidRequest
		}
	}
	return query, limit, nil
}

func normalizeExactRequest(tenantID, id string) (string, string, error) {
	tenantID, id = strings.TrimSpace(tenantID), strings.TrimSpace(id)
	if !validID(tenantID) || !validID(id) {
		return "", "", ontologycontract.ErrInvalidRequest
	}
	return tenantID, id, nil
}

func mapAuditItem(audit contextgraph.ProjectionAudit, decision datavisibility.ResourceDecision) AuditItem {
	allowed := allowedFields(decision)
	item := AuditItem{ID: audit.ID}
	if allowed["source_event_type"] {
		item.SourceEventType = audit.SourceEventType
	}
	if allowed["status"] {
		item.Status = audit.Status
	}
	if allowed["reason_code"] {
		item.ReasonCode = audit.ReasonCode
	}
	if allowed["observed_at"] {
		item.ObservedAt = visibleTime(audit.ObservedAt)
	}
	return item
}

func mapAuditDetail(audit contextgraph.ProjectionAudit, decision datavisibility.ResourceDecision) AuditDetail {
	allowed := allowedFields(decision)
	detail := AuditDetail{AuditItem: mapAuditItem(audit, decision)}
	if allowed["source_event_id"] {
		detail.SourceEventID = audit.SourceEventID
	}
	if allowed["reason"] {
		detail.Reason = audit.Reason
	}
	if allowed["details"] && len(audit.Details) > 0 {
		detail.Details = maps.Clone(audit.Details)
		delete(detail.Details, "last_replay_error")
		if value, exists := detail.Details["last_replay_error_code"]; exists {
			detail.Details["last_replay_error_code"] = safeReplayHistoryError(fmt.Sprint(value))
		}
	}
	return detail
}
