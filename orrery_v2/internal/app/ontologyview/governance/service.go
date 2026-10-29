package governance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"orrery_v2/internal/app/datavisibility"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/app/ontologyscope"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

var ErrUnavailable = ontologycontract.ErrUnavailable

const governanceScanBatch = 100

var proposalListFields = []string{
	"actionable", "confidence", "content_hash", "metadata_key", "proposed_value", "revision", "source_kind",
	"status", "target_id", "target_kind", "updated_at",
}

var proposalDetailFields = []string{
	"actionable", "confidence", "content_hash", "created_at", "evidence_refs", "metadata_key", "proposed_value",
	"reason", "review_evidence_refs", "review_reason", "reviewed_at", "reviewed_by", "reviewed_by_kind",
	"revision", "source", "source_kind", "source_ref", "source_version", "status", "target_id",
	"target_kind", "updated_at",
}

var proposalAnalysisFields = []string{"confidence", "evidence_refs", "proposed_value", "source_kind"}
var proposalSummaryFields = []string{"actionable", "content_hash", "revision", "status"}
var currentReceiptFields = []string{"proposal_binding_status", "proposal_content_hash", "proposal_revision", "status"}
var latestReceiptFields = []string{"applied_at", "status"}
var evidenceReferenceFields = []string{"ref_id", "ref_kind"}
var receiptListFields = []string{
	"actor_id", "actor_kind", "applied_at", "change_count", "issue_count", "proposal_binding_status",
	"proposal_revision", "status", "updated_at",
}
var auditEventListFields = []string{"action", "actor_id", "actor_kind", "occurred_at", "result", "severity", "summary"}

type CursorSet struct {
	Encrypted *ontologycontract.CursorCodec
	Stateful  *ontologycontract.StateCursorStore
}

type Service struct {
	gate        *ontologyscope.ScopeAccessGate
	proposals   ProposalReader
	receipts    ReceiptReader
	audits      AuditReader
	visibility  VisibilityPolicy
	cursors     CursorSet
	budgets     *ontologycontract.BudgetPolicy
	concurrency *ontologycontract.ConcurrencyLimiter
	now         func() time.Time
}

func NewService(
	gate *ontologyscope.ScopeAccessGate,
	proposals ProposalReader,
	receipts ReceiptReader,
	audits AuditReader,
	visibility VisibilityPolicy,
	cursors CursorSet,
	budgets *ontologycontract.BudgetPolicy,
	concurrency *ontologycontract.ConcurrencyLimiter,
) (*Service, error) {
	if gate == nil || proposals == nil || receipts == nil || audits == nil || visibility == nil || cursors.Stateful == nil {
		return nil, ontologyscope.ErrDependencyUnavailable
	}
	var err error
	if budgets == nil {
		budgets, err = ontologycontract.NewBudgetPolicy(nil)
		if err != nil {
			return nil, err
		}
	}
	if concurrency == nil {
		concurrency, err = ontologycontract.NewConcurrencyLimiter(nil)
		if err != nil {
			return nil, err
		}
	}
	return &Service{
		gate: gate, proposals: proposals, receipts: receipts, audits: audits, visibility: visibility,
		cursors: cursors, budgets: budgets, concurrency: concurrency, now: time.Now,
	}, nil
}

func (s *Service) ListProposals(ctx context.Context, principal identity.Principal, query ProposalListQuery) (ontologycontract.CursorPage[ProposalItem], error) {
	query, limit, err := normalizeProposalListQuery(query)
	if err != nil {
		return ontologycontract.CursorPage[ProposalItem]{}, err
	}
	authorized, err := s.authorize(ctx, principal, query.TenantID)
	if err != nil {
		return ontologycontract.CursorPage[ProposalItem]{}, err
	}
	state, err := s.resolvePolicyState(ctx, principal, authorized, "ontology.governance.proposals.read", datavisibility.ResourceProposal, datavisibility.ResourceReceipt)
	if err != nil {
		return ontologycontract.CursorPage[ProposalItem]{}, err
	}
	queryHash, err := proposalListQueryHash(query, limit)
	if err != nil {
		return ontologycontract.CursorPage[ProposalItem]{}, err
	}
	binding := governanceCursorBinding(state, queryHash)
	seek, err := s.resolveProposalCursor(query.Cursor, binding, state.cacheable)
	if err != nil {
		return ontologycontract.CursorPage[ProposalItem]{}, err
	}
	bounded, budget, cancel, err := s.budgets.Start(ctx, ontologycontract.QueryStandard)
	if err != nil {
		return ontologycontract.CursorPage[ProposalItem]{}, err
	}
	defer cancel()

	type positionedProposal struct {
		proposal        ontology.OntologyMetadataProposal
		decision        datavisibility.ResourceDecision
		actionable      bool
		actionableKnown bool
	}
	visible := make([]positionedProposal, 0, limit+1)
	for len(visible) <= limit {
		if err := budget.ConsumeStoreReads(1); err != nil {
			return ontologycontract.CursorPage[ProposalItem]{}, err
		}
		page, err := s.proposals.ListMetadataProposalCursorPage(bounded, ontology.MetadataProposalCursorQuery{
			TenantID: query.TenantID, Limit: governanceScanBatch, After: seek,
		})
		if err != nil {
			return ontologycontract.CursorPage[ProposalItem]{}, queryError(bounded, budget, err)
		}
		if err := budget.ConsumeCandidates(scannedCount(len(page.Proposals), page.HasMore)); err != nil {
			return ontologycontract.CursorPage[ProposalItem]{}, err
		}
		if len(page.Proposals) == 0 {
			if page.HasMore {
				return ontologycontract.CursorPage[ProposalItem]{}, dependencyError(errors.New("proposal reader returned an empty truncated page"))
			}
			break
		}
		rows, err := s.visibleProposals(bounded, principal, state, page.Proposals, proposalListFields)
		if err != nil {
			return ontologycontract.CursorPage[ProposalItem]{}, queryError(bounded, budget, err)
		}
		rows = visibleProposalsMatchingQuery(rows, query)
		receiptCandidates := rowsWithKnownActionability(rows)
		current, receiptKnown, err := s.visibleCurrentReceipts(bounded, principal, state, budget, query.TenantID, rawProposals(receiptCandidates))
		if err != nil {
			return ontologycontract.CursorPage[ProposalItem]{}, queryError(bounded, budget, err)
		}
		for _, row := range rows {
			known := proposalActionabilityFieldsAllowed(row) && receiptKnown[row.proposal.ID]
			actionable := ontology.IsMetadataProposalActionable(row.proposal, current[row.proposal.ID])
			if query.Queue == QueueActionable && (!known || !actionable) {
				continue
			}
			visible = append(visible, positionedProposal{proposal: row.proposal, decision: row.decision, actionable: actionable, actionableKnown: known})
			if len(visible) > limit {
				break
			}
		}
		if len(visible) > limit || !page.HasMore {
			break
		}
		last := page.Proposals[len(page.Proposals)-1]
		seek = ontology.MetadataProposalSeek{UpdatedAt: last.UpdatedAt, ID: last.ID}
	}

	hasMore := len(visible) > limit
	if hasMore {
		visible = visible[:limit]
	}
	items := make([]ProposalItem, len(visible))
	for index, row := range visible {
		items[index] = mapProposalItem(row.proposal, row.decision, row.actionable, row.actionableKnown)
	}
	if err := budget.ConsumeResults(len(items)); err != nil {
		return ontologycontract.CursorPage[ProposalItem]{}, err
	}
	next := ""
	if hasMore {
		last := visible[len(visible)-1].proposal
		next, err = s.issueProposalCursor(binding, state.cacheable, ontology.MetadataProposalSeek{UpdatedAt: last.UpdatedAt, ID: last.ID})
		if err != nil {
			return ontologycontract.CursorPage[ProposalItem]{}, err
		}
	}
	return ontologycontract.NewCursorPage(items, limit, hasMore, next)
}

func visibleProposalsMatchingQuery(rows []visibleProposal, query ProposalListQuery) []visibleProposal {
	if query.Status == "" && query.TargetKind == "" && query.Search == "" {
		return rows
	}
	out := make([]visibleProposal, 0, len(rows))
	needle := strings.ToLower(query.Search)
	for _, row := range rows {
		allowed := allowedFields(row.decision)
		if query.Status != "" && (!allowed["status"] || row.proposal.Status != query.Status) {
			continue
		}
		if query.TargetKind != "" && (!allowed["target_kind"] || strings.ToLower(row.proposal.TargetKind) != query.TargetKind) {
			continue
		}
		if needle != "" && !proposalMatchesVisibleSearch(row, allowed, needle) {
			continue
		}
		out = append(out, row)
	}
	return out
}

func proposalMatchesVisibleSearch(row visibleProposal, allowed map[string]bool, needle string) bool {
	return strings.Contains(strings.ToLower(row.proposal.ID), needle) ||
		allowed["target_id"] && strings.Contains(strings.ToLower(row.proposal.TargetID), needle) ||
		allowed["metadata_key"] && strings.Contains(strings.ToLower(row.proposal.MetadataKey), needle) ||
		allowed["proposed_value"] && strings.Contains(strings.ToLower(row.proposal.ProposedValue), needle)
}

func (s *Service) GetProposal(ctx context.Context, principal identity.Principal, tenantID, proposalID string) (ProposalDetail, error) {
	tenantID, proposalID, err := normalizeExactRequest(tenantID, proposalID)
	if err != nil {
		return ProposalDetail{}, err
	}
	authorized, err := s.authorize(ctx, principal, tenantID)
	if err != nil {
		return ProposalDetail{}, err
	}
	state, err := s.resolvePolicyState(ctx, principal, authorized, "ontology.governance.proposals.read", datavisibility.ResourceProposal, datavisibility.ResourceReceipt, datavisibility.ResourceEvidence)
	if err != nil {
		return ProposalDetail{}, err
	}
	bounded, budget, cancel, err := s.budgets.Start(ctx, ontologycontract.QueryStandard)
	if err != nil {
		return ProposalDetail{}, err
	}
	defer cancel()
	if err := budget.ConsumeStoreReads(1); err != nil {
		return ProposalDetail{}, err
	}
	proposal, err := s.proposals.GetMetadataProposalForTenant(bounded, tenantID, proposalID)
	if err != nil {
		return ProposalDetail{}, exactQueryError(bounded, budget, err)
	}
	if err := budget.ConsumeCandidates(1); err != nil {
		return ProposalDetail{}, err
	}
	rows, err := s.visibleProposals(bounded, principal, state, []ontology.OntologyMetadataProposal{proposal}, proposalDetailFields)
	if err != nil {
		return ProposalDetail{}, queryError(bounded, budget, err)
	}
	if len(rows) != 1 {
		return ProposalDetail{}, ErrUnavailable
	}
	actionableKnown := proposalActionabilityFieldsAllowed(rows[0])
	current := map[string][]ontology.MetadataProposalPromotionReceipt{}
	if actionableKnown {
		var receiptKnown map[string]bool
		current, receiptKnown, err = s.visibleCurrentReceipts(bounded, principal, state, budget, tenantID, []ontology.OntologyMetadataProposal{proposal})
		if err != nil {
			return ProposalDetail{}, queryError(bounded, budget, err)
		}
		actionableKnown = receiptKnown[proposal.ID]
	}
	allowed := allowedFields(rows[0].decision)
	var evidenceRefs []EvidenceReference
	if allowed["evidence_refs"] {
		evidenceRefs, _, err = s.visibleEvidenceReferences(bounded, principal, state, budget, proposal.EvidenceRefs)
		if err != nil {
			return ProposalDetail{}, queryError(bounded, budget, err)
		}
	}
	var reviewEvidenceRefs []EvidenceReference
	if allowed["review_evidence_refs"] {
		reviewEvidenceRefs, _, err = s.visibleEvidenceReferences(bounded, principal, state, budget, proposal.ReviewEvidenceRefs)
		if err != nil {
			return ProposalDetail{}, queryError(bounded, budget, err)
		}
	}
	actionable := ontology.IsMetadataProposalActionable(proposal, current[proposal.ID])
	if err := budget.ConsumeResults(1); err != nil {
		return ProposalDetail{}, err
	}
	return mapProposalDetail(proposal, rows[0].decision, actionable, actionableKnown, evidenceRefs, reviewEvidenceRefs), nil
}

func (s *Service) AnalyzeProposal(ctx context.Context, principal identity.Principal, tenantID, proposalID string) (AnalysisReport, error) {
	tenantID, proposalID, err := normalizeExactRequest(tenantID, proposalID)
	if err != nil {
		return AnalysisReport{}, err
	}
	authorized, err := s.authorize(ctx, principal, tenantID)
	if err != nil {
		return AnalysisReport{}, err
	}
	state, err := s.resolvePolicyState(ctx, principal, authorized, "ontology.governance.proposals.analysis", datavisibility.ResourceProposal, datavisibility.ResourceEvidence)
	if err != nil {
		return AnalysisReport{}, err
	}
	bounded, budget, cancel, err := s.budgets.Start(ctx, ontologycontract.QueryStandard)
	if err != nil {
		return AnalysisReport{}, err
	}
	defer cancel()
	if err := budget.ConsumeStoreReads(1); err != nil {
		return AnalysisReport{}, err
	}
	proposal, err := s.proposals.GetMetadataProposalForTenant(bounded, tenantID, proposalID)
	if err != nil {
		return AnalysisReport{}, exactQueryError(bounded, budget, err)
	}
	if err := budget.ConsumeCandidates(1); err != nil {
		return AnalysisReport{}, err
	}
	selected, err := s.visibleProposals(bounded, principal, state, []ontology.OntologyMetadataProposal{proposal}, proposalAnalysisFields)
	if err != nil {
		return AnalysisReport{}, queryError(bounded, budget, err)
	}
	if len(selected) != 1 {
		return AnalysisReport{}, ErrUnavailable
	}
	if proposal.Status != ontology.MetadataProposalStatusProposed {
		return AnalysisReport{Issues: []AnalysisIssue{}}, nil
	}
	if err := budget.ConsumeStoreReads(1); err != nil {
		return AnalysisReport{}, err
	}
	page, err := s.proposals.ListMetadataProposalCursorPage(bounded, ontology.MetadataProposalCursorQuery{
		TenantID: tenantID, Status: ontology.MetadataProposalStatusProposed, TargetKind: proposal.TargetKind,
		TargetID: proposal.TargetID, MetadataKey: proposal.MetadataKey, Limit: governanceScanBatch,
	})
	if err != nil {
		return AnalysisReport{}, queryError(bounded, budget, err)
	}
	if err := budget.ConsumeCandidates(scannedCount(len(page.Proposals), page.HasMore)); err != nil {
		return AnalysisReport{}, err
	}
	if page.HasMore {
		return AnalysisReport{}, fmt.Errorf("%w: proposal analysis target group exceeds its bound", ontologycontract.ErrQueryBudgetExceeded)
	}
	related, err := s.visibleProposals(bounded, principal, state, page.Proposals, proposalAnalysisFields)
	if err != nil {
		return AnalysisReport{}, queryError(bounded, budget, err)
	}
	if !rawProposalContains(page.Proposals, proposalID) || !visibleProposalContains(related, proposalID) {
		return AnalysisReport{}, dependencyError(errors.New("proposal analysis query did not preserve the selected proposal"))
	}
	evidenceComplete := false
	if allowedFields(selected[0].decision)["evidence_refs"] {
		_, evidenceComplete, err = s.visibleEvidenceReferences(bounded, principal, state, budget, proposal.EvidenceRefs)
		if err != nil {
			return AnalysisReport{}, queryError(bounded, budget, err)
		}
	}
	report := safeAnalysisReport(proposalID, selected[0], related, len(related) == len(page.Proposals), evidenceComplete)
	if err := budget.ConsumeResults(len(report.Issues)); err != nil {
		return AnalysisReport{}, err
	}
	return report, nil
}

func (s *Service) Summary(ctx context.Context, principal identity.Principal, tenantID string) (ontologycontract.Summary[SummaryData], error) {
	tenantID = strings.TrimSpace(tenantID)
	if !validID(tenantID) {
		return ontologycontract.Summary[SummaryData]{}, ontologycontract.ErrInvalidRequest
	}
	authorized, err := s.authorize(ctx, principal, tenantID)
	if err != nil {
		return ontologycontract.Summary[SummaryData]{}, err
	}
	release, err := s.concurrency.Acquire(ctx, ontologycontract.ConcurrencySummary, authorized.Snapshot().PrincipalKey)
	if err != nil {
		return ontologycontract.Summary[SummaryData]{}, err
	}
	defer release()
	state, err := s.resolvePolicyState(ctx, principal, authorized, "ontology.governance.summary", datavisibility.ResourceProposal, datavisibility.ResourceReceipt)
	if err != nil {
		return ontologycontract.Summary[SummaryData]{}, err
	}
	bounded, budget, cancel, err := s.budgets.Start(ctx, ontologycontract.QueryStandard)
	if err != nil {
		return ontologycontract.Summary[SummaryData]{}, err
	}
	defer cancel()

	actionable, visibleProposalIDs, complete, err := s.countActionable(bounded, principal, state, budget, tenantID)
	if err != nil {
		return ontologycontract.Summary[SummaryData]{}, err
	}
	data := SummaryData{ActionableProposals: ontologycontract.UnavailableCount(), LatestReceipt: LatestReceiptMetric{Status: ontologycontract.MetricUnavailable}}
	if complete {
		data.ActionableProposals = ontologycontract.ExactCount(actionable)
		latest, latestComplete, err := s.latestVisibleReceipt(bounded, principal, state, budget, tenantID, visibleProposalIDs)
		if err != nil {
			return ontologycontract.Summary[SummaryData]{}, err
		}
		if latestComplete {
			data.LatestReceipt.Status = ontologycontract.MetricExact
			data.LatestReceipt.Receipt = latest
		}
	}
	meta := governanceSummaryMeta(s.now(), authorized, data)
	return ontologycontract.Summary[SummaryData]{Data: data, Meta: meta}, nil
}

func (s *Service) countActionable(
	ctx context.Context,
	principal identity.Principal,
	state governancePolicyState,
	budget *ontologycontract.BudgetTracker,
	tenantID string,
) (int64, map[string]struct{}, bool, error) {
	var count int64
	visibleIDs := map[string]struct{}{}
	seek := ontology.MetadataProposalSeek{}
	for {
		if err := budget.ConsumeStoreReads(1); err != nil {
			if errors.Is(err, ontologycontract.ErrQueryBudgetExceeded) {
				return 0, nil, false, nil
			}
			return 0, nil, false, err
		}
		page, err := s.proposals.ListMetadataProposalCursorPage(ctx, ontology.MetadataProposalCursorQuery{TenantID: tenantID, Limit: governanceScanBatch, After: seek})
		if err != nil {
			return 0, nil, false, queryError(ctx, budget, err)
		}
		if err := budget.ConsumeCandidates(scannedCount(len(page.Proposals), page.HasMore)); err != nil {
			if errors.Is(err, ontologycontract.ErrQueryBudgetExceeded) {
				return 0, nil, false, nil
			}
			return 0, nil, false, err
		}
		if len(page.Proposals) == 0 {
			if page.HasMore {
				return 0, nil, false, dependencyError(errors.New("proposal reader returned an empty truncated page"))
			}
			return count, visibleIDs, true, nil
		}
		rows, err := s.visibleProposals(ctx, principal, state, page.Proposals, proposalSummaryFields)
		if err != nil {
			return 0, nil, false, queryError(ctx, budget, err)
		}
		eligible := make([]visibleProposal, 0, len(rows))
		for _, row := range rows {
			visibleIDs[row.proposal.ID] = struct{}{}
			if !fieldsAllowed(row.decision, proposalSummaryFields...) {
				return 0, nil, false, nil
			}
			eligible = append(eligible, row)
		}
		current, receiptKnown, err := s.visibleCurrentReceipts(ctx, principal, state, budget, tenantID, rawProposals(eligible))
		if err != nil {
			if errors.Is(err, ontologycontract.ErrQueryBudgetExceeded) {
				return 0, nil, false, nil
			}
			return 0, nil, false, queryError(ctx, budget, err)
		}
		for _, row := range eligible {
			if !receiptKnown[row.proposal.ID] {
				return 0, nil, false, nil
			}
			if ontology.IsMetadataProposalActionable(row.proposal, current[row.proposal.ID]) {
				count++
			}
		}
		if !page.HasMore {
			return count, visibleIDs, true, nil
		}
		last := page.Proposals[len(page.Proposals)-1]
		seek = ontology.MetadataProposalSeek{UpdatedAt: last.UpdatedAt, ID: last.ID}
	}
}

func (s *Service) latestVisibleReceipt(
	ctx context.Context,
	principal identity.Principal,
	state governancePolicyState,
	budget *ontologycontract.BudgetTracker,
	tenantID string,
	visibleProposalIDs map[string]struct{},
) (*SuccessfulReceiptRef, bool, error) {
	if len(visibleProposalIDs) == 0 {
		return nil, true, nil
	}
	seek := ontology.PromotionReceiptSeek{}
	for {
		if err := budget.ConsumeStoreReads(1); err != nil {
			if errors.Is(err, ontologycontract.ErrQueryBudgetExceeded) {
				return nil, false, nil
			}
			return nil, false, err
		}
		page, err := s.receipts.ListPromotionReceiptCursorPage(ctx, ontology.PromotionReceiptCursorQuery{
			TenantID: tenantID, SuccessfulOnly: true, Limit: governanceScanBatch, After: seek,
		})
		if err != nil {
			return nil, false, queryError(ctx, budget, err)
		}
		if err := budget.ConsumeCandidates(scannedCount(len(page.Receipts), page.HasMore)); err != nil {
			if errors.Is(err, ontologycontract.ErrQueryBudgetExceeded) {
				return nil, false, nil
			}
			return nil, false, err
		}
		candidates := make([]ontology.MetadataProposalPromotionReceipt, 0, len(page.Receipts))
		for _, receipt := range page.Receipts {
			if _, visible := visibleProposalIDs[receipt.ProposalID]; visible {
				candidates = append(candidates, receipt)
			}
		}
		rows, err := s.visibleReceipts(ctx, principal, state, candidates, latestReceiptFields)
		if err != nil {
			return nil, false, queryError(ctx, budget, err)
		}
		for _, row := range rows {
			if !fieldsAllowed(row.decision, latestReceiptFields...) {
				continue
			}
			return &SuccessfulReceiptRef{
				ReceiptID: row.receipt.ReceiptID, Status: row.receipt.Status, AppliedAt: visibleTime(row.receipt.AppliedAt),
			}, true, nil
		}
		if !page.HasMore {
			return nil, true, nil
		}
		if len(page.Receipts) == 0 {
			return nil, false, dependencyError(errors.New("receipt reader returned an empty truncated page"))
		}
		last := page.Receipts[len(page.Receipts)-1]
		seek = ontology.PromotionReceiptSeek{UpdatedAt: last.UpdatedAt, ReceiptID: last.ReceiptID}
	}
}

func (s *Service) authorize(ctx context.Context, principal identity.Principal, tenantID string) (ontologyscope.AuthorizedScopeContext, error) {
	if s == nil || s.gate == nil {
		return ontologyscope.AuthorizedScopeContext{}, ontologyscope.ErrDependencyUnavailable
	}
	authorized, err := s.gate.AuthorizeRead(ctx, identity.NormalizePrincipal(principal), strings.TrimSpace(tenantID))
	if ontologyscope.IsNonDisclosingReadError(err) {
		return ontologyscope.AuthorizedScopeContext{}, ErrUnavailable
	}
	return authorized, err
}

func normalizeProposalListQuery(query ProposalListQuery) (ProposalListQuery, int, error) {
	query.TenantID = strings.TrimSpace(query.TenantID)
	query.Queue = ProposalQueue(strings.ToLower(strings.TrimSpace(string(query.Queue))))
	if query.Queue == "" {
		query.Queue = QueueActionable
	}
	query.TargetKind = strings.ToLower(strings.TrimSpace(query.TargetKind))
	query.Search = strings.TrimSpace(query.Search)
	query.Cursor = strings.TrimSpace(query.Cursor)
	limit, err := ontologycontract.NormalizeLimit(query.Limit, ontologycontract.DefaultChildListLimit, ontologycontract.MaxPageLimit)
	if err != nil || !validID(query.TenantID) || len(query.TargetKind) > 128 || utf8.RuneCountInString(query.Search) > 128 || len(query.Cursor) > 4096 {
		return ProposalListQuery{}, 0, ontologycontract.ErrInvalidRequest
	}
	if query.Queue != QueueActionable && query.Queue != QueueAll {
		return ProposalListQuery{}, 0, ontologycontract.ErrInvalidRequest
	}
	if query.Status != "" {
		switch query.Status {
		case ontology.MetadataProposalStatusProposed, ontology.MetadataProposalStatusAccepted, ontology.MetadataProposalStatusRejected:
		default:
			return ProposalListQuery{}, 0, ontologycontract.ErrInvalidRequest
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

func validID(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= 256
}

func scannedCount(returned int, hasMore bool) int {
	if hasMore {
		return returned + 1
	}
	return returned
}

func dependencyError(err error) error {
	if errors.Is(err, ontologycontract.ErrInvalidRequest) || errors.Is(err, ontologycontract.ErrInvalidCursor) || errors.Is(err, ontologycontract.ErrCursorStale) || errors.Is(err, ontologycontract.ErrQueryBudgetExceeded) {
		return err
	}
	return fmt.Errorf("%w: %v", ontologyscope.ErrDependencyUnavailable, err)
}

func queryError(ctx context.Context, budget *ontologycontract.BudgetTracker, err error) error {
	if ctx != nil && ctx.Err() != nil {
		return budget.CheckContext(ctx)
	}
	return dependencyError(err)
}

func exactQueryError(ctx context.Context, budget *ontologycontract.BudgetTracker, err error) error {
	if errors.Is(err, ontology.ErrNotFound) || errors.Is(err, ErrUnavailable) {
		return ErrUnavailable
	}
	return queryError(ctx, budget, err)
}

func governanceSummaryMeta(at time.Time, authorized ontologyscope.AuthorizedScopeContext, data SummaryData) ontologycontract.SummaryMeta {
	hash := sha256.New()
	_, _ = fmt.Fprintf(hash, "%s:", data.ActionableProposals.Status)
	if data.ActionableProposals.Value != nil {
		_, _ = fmt.Fprintf(hash, "%d", *data.ActionableProposals.Value)
	}
	_, _ = fmt.Fprintf(hash, ":%s:", data.LatestReceipt.Status)
	if data.LatestReceipt.Receipt != nil {
		_, _ = fmt.Fprintf(hash, "%s:%s:", data.LatestReceipt.Receipt.ReceiptID, data.LatestReceipt.Receipt.Status)
		if data.LatestReceipt.Receipt.AppliedAt != nil {
			_, _ = fmt.Fprintf(hash, "%d", data.LatestReceipt.Receipt.AppliedAt.UTC().UnixNano())
		}
	}
	meta := ontologycontract.SummaryMeta{AsOf: at.UTC(), DataRevision: hex.EncodeToString(hash.Sum(nil))}
	if revision := strings.TrimSpace(authorized.Snapshot().AuthorizationRevision); revision != "" {
		meta.AuthorizationRevision = &revision
	}
	return meta
}

func visibleTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	copy := value.UTC()
	return &copy
}

func int64Value(value int64) *int64 {
	copy := value
	return &copy
}

func float64Value(value float64) *float64 {
	copy := value
	return &copy
}

func intValue(value int) *int {
	copy := value
	return &copy
}
