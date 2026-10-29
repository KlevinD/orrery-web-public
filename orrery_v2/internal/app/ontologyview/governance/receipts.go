package governance

import (
	"context"
	"errors"

	"orrery_v2/internal/app/datavisibility"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontology"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

func (s *Service) ListReceipts(ctx context.Context, principal identity.Principal, query ReceiptListQuery) (ontologycontract.CursorPage[ReceiptItem], error) {
	query, limit, err := normalizeReceiptListQuery(query)
	if err != nil {
		return ontologycontract.CursorPage[ReceiptItem]{}, err
	}
	authorized, err := s.authorize(ctx, principal, query.TenantID)
	if err != nil {
		return ontologycontract.CursorPage[ReceiptItem]{}, err
	}
	state, err := s.resolvePolicyState(ctx, principal, authorized, "ontology.governance.receipts.read", datavisibility.ResourceProposal, datavisibility.ResourceReceipt)
	if err != nil {
		return ontologycontract.CursorPage[ReceiptItem]{}, err
	}
	queryHash, err := receiptListQueryHash(query, limit)
	if err != nil {
		return ontologycontract.CursorPage[ReceiptItem]{}, err
	}
	binding := governanceCursorBinding(state, queryHash)
	seek, err := s.resolveReceiptCursor(query.Cursor, binding, state.cacheable)
	if err != nil {
		return ontologycontract.CursorPage[ReceiptItem]{}, err
	}
	bounded, budget, cancel, err := s.budgets.Start(ctx, ontologycontract.QueryStandard)
	if err != nil {
		return ontologycontract.CursorPage[ReceiptItem]{}, err
	}
	defer cancel()
	if _, err := s.requireVisibleProposal(bounded, principal, state, budget, query.TenantID, query.ProposalID); err != nil {
		return ontologycontract.CursorPage[ReceiptItem]{}, err
	}

	visible := make([]visibleReceipt, 0, limit+1)
	for len(visible) <= limit {
		if err := budget.ConsumeStoreReads(1); err != nil {
			return ontologycontract.CursorPage[ReceiptItem]{}, err
		}
		page, err := s.receipts.ListPromotionReceiptCursorPage(bounded, ontology.PromotionReceiptCursorQuery{
			TenantID: query.TenantID, ProposalID: query.ProposalID, Limit: governanceScanBatch, After: seek,
		})
		if err != nil {
			return ontologycontract.CursorPage[ReceiptItem]{}, queryError(bounded, budget, err)
		}
		if err := budget.ConsumeCandidates(scannedCount(len(page.Receipts), page.HasMore)); err != nil {
			return ontologycontract.CursorPage[ReceiptItem]{}, err
		}
		if len(page.Receipts) == 0 {
			if page.HasMore {
				return ontologycontract.CursorPage[ReceiptItem]{}, dependencyError(errors.New("receipt reader returned an empty truncated page"))
			}
			break
		}
		for _, receipt := range page.Receipts {
			if receipt.TenantID != query.TenantID || receipt.ProposalID != query.ProposalID {
				return ontologycontract.CursorPage[ReceiptItem]{}, dependencyError(errors.New("receipt reader returned a cross-scope row"))
			}
		}
		rows, err := s.visibleReceipts(bounded, principal, state, page.Receipts, receiptListFields)
		if err != nil {
			return ontologycontract.CursorPage[ReceiptItem]{}, queryError(bounded, budget, err)
		}
		visible = append(visible, rows...)
		if len(visible) > limit || !page.HasMore {
			break
		}
		last := page.Receipts[len(page.Receipts)-1]
		seek = ontology.PromotionReceiptSeek{UpdatedAt: last.UpdatedAt, ReceiptID: last.ReceiptID}
	}

	hasMore := len(visible) > limit
	if hasMore {
		visible = visible[:limit]
	}
	items := make([]ReceiptItem, len(visible))
	for index, row := range visible {
		items[index] = mapReceiptItem(row.receipt, row.decision)
	}
	if err := budget.ConsumeResults(len(items)); err != nil {
		return ontologycontract.CursorPage[ReceiptItem]{}, err
	}
	next := ""
	if hasMore {
		last := visible[len(visible)-1].receipt
		next, err = s.issueReceiptCursor(binding, state.cacheable, ontology.PromotionReceiptSeek{UpdatedAt: last.UpdatedAt, ReceiptID: last.ReceiptID})
		if err != nil {
			return ontologycontract.CursorPage[ReceiptItem]{}, err
		}
	}
	return ontologycontract.NewCursorPage(items, limit, hasMore, next)
}

func (s *Service) requireVisibleProposal(
	ctx context.Context,
	principal identity.Principal,
	state governancePolicyState,
	budget *ontologycontract.BudgetTracker,
	tenantID, proposalID string,
) (ontology.OntologyMetadataProposal, error) {
	if err := budget.ConsumeStoreReads(1); err != nil {
		return ontology.OntologyMetadataProposal{}, err
	}
	proposal, err := s.proposals.GetMetadataProposalForTenant(ctx, tenantID, proposalID)
	if err != nil {
		return ontology.OntologyMetadataProposal{}, exactQueryError(ctx, budget, err)
	}
	if err := budget.ConsumeCandidates(1); err != nil {
		return ontology.OntologyMetadataProposal{}, err
	}
	rows, err := s.visibleProposals(ctx, principal, state, []ontology.OntologyMetadataProposal{proposal}, nil)
	if err != nil {
		return ontology.OntologyMetadataProposal{}, queryError(ctx, budget, err)
	}
	if len(rows) != 1 {
		return ontology.OntologyMetadataProposal{}, ErrUnavailable
	}
	return proposal, nil
}

func normalizeReceiptListQuery(query ReceiptListQuery) (ReceiptListQuery, int, error) {
	tenantID, proposalID, err := normalizeExactRequest(query.TenantID, query.ProposalID)
	if err != nil {
		return ReceiptListQuery{}, 0, err
	}
	query.TenantID, query.ProposalID = tenantID, proposalID
	query.Cursor = normalizeCursorToken(query.Cursor)
	limit, err := ontologycontract.NormalizeLimit(query.Limit, ontologycontract.DefaultChildListLimit, ontologycontract.MaxPageLimit)
	if err != nil || len(query.Cursor) > 4096 {
		return ReceiptListQuery{}, 0, ontologycontract.ErrInvalidRequest
	}
	return query, limit, nil
}

func mapReceiptItem(receipt ontology.MetadataProposalPromotionReceipt, decision datavisibility.ResourceDecision) ReceiptItem {
	allowed := allowedFields(decision)
	item := ReceiptItem{ReceiptID: receipt.ReceiptID}
	if allowed["status"] {
		item.Status = receipt.Status
	}
	if allowed["proposal_binding_status"] {
		item.ProposalBindingStatus = receipt.ProposalBindingStatus
	}
	if allowed["proposal_revision"] {
		item.ProposalRevision = int64Value(receipt.ProposalRevision)
	}
	if allowed["actor_id"] {
		item.ActorID = receipt.ActorID
	}
	if allowed["actor_kind"] {
		item.ActorKind = receipt.ActorKind
	}
	if allowed["change_count"] {
		item.ChangeCount = intValue(len(receipt.Changes))
	}
	if allowed["issue_count"] {
		item.IssueCount = intValue(len(receipt.Issues))
	}
	if allowed["applied_at"] {
		item.AppliedAt = visibleTime(receipt.AppliedAt)
	}
	if allowed["updated_at"] {
		item.UpdatedAt = visibleTime(receipt.UpdatedAt)
	}
	return item
}
