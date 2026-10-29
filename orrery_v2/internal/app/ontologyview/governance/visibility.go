package governance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"orrery_v2/internal/app/datavisibility"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/app/ontologyscope"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

type governancePolicyState struct {
	scope         ontologyscope.AuthorizedScopeContext
	purpose       string
	kindRevisions map[datavisibility.ResourceKind]string
	revision      string
	cacheable     bool
}

type visibleProposal struct {
	proposal ontology.OntologyMetadataProposal
	decision datavisibility.ResourceDecision
}

type visibleReceipt struct {
	receipt  ontology.MetadataProposalPromotionReceipt
	decision datavisibility.ResourceDecision
}

func (s *Service) resolvePolicyState(
	ctx context.Context,
	principal identity.Principal,
	authorized ontologyscope.AuthorizedScopeContext,
	purpose string,
	kinds ...datavisibility.ResourceKind,
) (governancePolicyState, error) {
	state := governancePolicyState{
		scope: authorized, purpose: strings.TrimSpace(purpose), kindRevisions: make(map[datavisibility.ResourceKind]string, len(kinds)),
		cacheable: authorized.Snapshot().Cacheable(),
	}
	seen := map[datavisibility.ResourceKind]struct{}{}
	ordered := make([]datavisibility.ResourceKind, 0, len(kinds))
	for _, kind := range kinds {
		if _, duplicate := seen[kind]; duplicate {
			continue
		}
		seen[kind] = struct{}{}
		ordered = append(ordered, kind)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	for _, kind := range ordered {
		descriptor := s.scopeDescriptor(authorized, kind, "governance-scope-probe:"+string(kind), nil, nil)
		decision, err := s.visibility.Evaluate(ctx, datavisibility.EvaluateRequest{
			Principal: identity.NormalizePrincipal(principal), Purpose: purpose,
			Resources: []datavisibility.ResourceDescriptor{descriptor},
		})
		if err != nil {
			return governancePolicyState{}, dependencyError(err)
		}
		if _, ok := decision.Resource(descriptor.Key); !ok {
			return governancePolicyState{}, dependencyError(fmt.Errorf("scope policy probe for %s is incomplete", kind))
		}
		state.kindRevisions[kind] = strings.TrimSpace(decision.PolicyRevision)
		state.cacheable = state.cacheable && decision.Cacheable()
	}
	state.revision = combinedPolicyRevision(authorized.Snapshot().PolicyRevision, state.kindRevisions)
	return state, nil
}

func (s *Service) visibleProposals(
	ctx context.Context,
	principal identity.Principal,
	state governancePolicyState,
	proposals []ontology.OntologyMetadataProposal,
	fields []string,
) ([]visibleProposal, error) {
	if len(proposals) == 0 {
		return nil, nil
	}
	descriptors := make([]datavisibility.ResourceDescriptor, 0, len(proposals))
	selected := make([]ontology.OntologyMetadataProposal, 0, len(proposals))
	seen := map[string]struct{}{}
	for _, proposal := range proposals {
		if proposal.TenantID != state.scope.Binding().TenantID || !validID(proposal.ID) {
			return nil, dependencyError(fmt.Errorf("proposal reader returned an invalid or cross-tenant row"))
		}
		if _, duplicate := seen[proposal.ID]; duplicate {
			return nil, dependencyError(errorsNewDuplicate("proposal", proposal.ID))
		}
		seen[proposal.ID] = struct{}{}
		descriptors = append(descriptors, s.scopeDescriptor(
			state.scope, datavisibility.ResourceProposal, "proposal:"+proposal.ID, fields, proposal,
		))
		selected = append(selected, proposal)
	}
	if len(selected) == 0 {
		return nil, nil
	}
	decision, err := s.visibility.Evaluate(ctx, datavisibility.EvaluateRequest{
		Principal: identity.NormalizePrincipal(principal), Purpose: state.purpose, Resources: descriptors,
	})
	if err != nil {
		return nil, dependencyError(err)
	}
	if err := verifyKindRevision(state, datavisibility.ResourceProposal, decision); err != nil {
		return nil, err
	}
	out := make([]visibleProposal, 0, len(selected))
	for _, proposal := range selected {
		resource, ok := decision.Resource("proposal:" + proposal.ID)
		if !ok {
			return nil, dependencyError(fmt.Errorf("proposal visibility decision is incomplete"))
		}
		if resource.Hidden || resource.ExistenceDisclosure == datavisibility.ExistenceConceal {
			continue
		}
		out = append(out, visibleProposal{proposal: proposal, decision: resource})
	}
	return out, nil
}

func (s *Service) visibleReceipts(
	ctx context.Context,
	principal identity.Principal,
	state governancePolicyState,
	receipts []ontology.MetadataProposalPromotionReceipt,
	fields []string,
) ([]visibleReceipt, error) {
	if len(receipts) == 0 {
		return nil, nil
	}
	descriptors := make([]datavisibility.ResourceDescriptor, 0, len(receipts))
	selected := make([]ontology.MetadataProposalPromotionReceipt, 0, len(receipts))
	seen := map[string]struct{}{}
	for _, receipt := range receipts {
		if receipt.TenantID != state.scope.Binding().TenantID || !validID(receipt.ReceiptID) {
			return nil, dependencyError(fmt.Errorf("receipt reader returned an invalid or cross-tenant row"))
		}
		if _, duplicate := seen[receipt.ReceiptID]; duplicate {
			return nil, dependencyError(errorsNewDuplicate("receipt", receipt.ReceiptID))
		}
		seen[receipt.ReceiptID] = struct{}{}
		descriptors = append(descriptors, s.scopeDescriptor(
			state.scope, datavisibility.ResourceReceipt, "receipt:"+receipt.ReceiptID, fields, receipt,
		))
		selected = append(selected, receipt)
	}
	if len(selected) == 0 {
		return nil, nil
	}
	decision, err := s.visibility.Evaluate(ctx, datavisibility.EvaluateRequest{
		Principal: identity.NormalizePrincipal(principal), Purpose: state.purpose, Resources: descriptors,
	})
	if err != nil {
		return nil, dependencyError(err)
	}
	if err := verifyKindRevision(state, datavisibility.ResourceReceipt, decision); err != nil {
		return nil, err
	}
	out := make([]visibleReceipt, 0, len(selected))
	for _, receipt := range selected {
		resource, ok := decision.Resource("receipt:" + receipt.ReceiptID)
		if !ok {
			return nil, dependencyError(fmt.Errorf("receipt visibility decision is incomplete"))
		}
		if resource.Hidden || resource.ExistenceDisclosure == datavisibility.ExistenceConceal {
			continue
		}
		out = append(out, visibleReceipt{receipt: receipt, decision: resource})
	}
	return out, nil
}

func (s *Service) visibleEvidenceReferences(
	ctx context.Context,
	principal identity.Principal,
	state governancePolicyState,
	budget *ontologycontract.BudgetTracker,
	refs []ontology.MetadataEvidenceRef,
) ([]EvidenceReference, bool, error) {
	if len(refs) == 0 {
		return nil, true, nil
	}
	if len(refs) > governanceScanBatch {
		return nil, false, fmt.Errorf("%w: proposal evidence references exceed their bound", ontologycontract.ErrQueryBudgetExceeded)
	}
	binding := state.scope.Binding()
	descriptors := make([]datavisibility.ResourceDescriptor, 0, len(refs))
	selected := make([]ontology.MetadataEvidenceRef, 0, len(refs))
	seen := map[string]struct{}{}
	complete := true
	for _, ref := range refs {
		ref.Kind, ref.ID = strings.TrimSpace(ref.Kind), strings.TrimSpace(ref.ID)
		if !validID(ref.Kind) || !validID(ref.ID) {
			complete = false
			continue
		}
		identityKey := strings.ToLower(ref.Kind) + "\x00" + ref.ID
		if _, duplicate := seen[identityKey]; duplicate {
			continue
		}
		seen[identityKey] = struct{}{}
		key := fmt.Sprintf("evidence-ref:%03d", len(selected))
		descriptors = append(descriptors, datavisibility.ResourceDescriptor{
			Key: key, Kind: datavisibility.ResourceEvidence, TenantID: binding.TenantID, Type: ref.Kind,
			FieldPaths: append([]string(nil), evidenceReferenceFields...),
			AccessRefs: []datavisibility.AccessRef{
				{Kind: binding.SourceObject.Type, ID: binding.SourceObject.ID, TenantID: binding.TenantID},
				{Kind: ref.Kind, ID: ref.ID, TenantID: binding.TenantID},
			},
			Payload: ref,
		})
		selected = append(selected, ref)
	}
	if len(selected) == 0 {
		return nil, false, nil
	}
	if err := budget.ConsumeCandidates(len(selected)); err != nil {
		return nil, false, err
	}
	decision, err := s.visibility.Evaluate(ctx, datavisibility.EvaluateRequest{
		Principal: identity.NormalizePrincipal(principal), Purpose: state.purpose, Resources: descriptors,
	})
	if err != nil {
		return nil, false, dependencyError(err)
	}
	if err := verifyKindRevision(state, datavisibility.ResourceEvidence, decision); err != nil {
		return nil, false, err
	}
	out := make([]EvidenceReference, 0, len(selected))
	for index, ref := range selected {
		resource, ok := decision.Resource(fmt.Sprintf("evidence-ref:%03d", index))
		if !ok {
			return nil, false, dependencyError(errorsNewDuplicate("evidence decision", ref.ID))
		}
		if resource.Hidden || resource.ExistenceDisclosure == datavisibility.ExistenceConceal || !fieldsAllowed(resource, evidenceReferenceFields...) {
			complete = false
			continue
		}
		out = append(out, EvidenceReference{Kind: ref.Kind, ID: ref.ID})
	}
	return out, complete && len(out) == len(selected), nil
}

func (s *Service) visibleCurrentReceipts(
	ctx context.Context,
	principal identity.Principal,
	state governancePolicyState,
	budget *ontologycontract.BudgetTracker,
	tenantID string,
	proposals []ontology.OntologyMetadataProposal,
) (map[string][]ontology.MetadataProposalPromotionReceipt, map[string]bool, error) {
	refs := make([]ontology.MetadataProposalVersionRef, 0, len(proposals))
	known := make(map[string]bool, len(proposals))
	requested := make(map[string]struct{}, len(proposals))
	for _, proposal := range proposals {
		known[proposal.ID] = true
		requested[proposal.ID] = struct{}{}
		if proposal.Status != ontology.MetadataProposalStatusAccepted {
			continue
		}
		refs = append(refs, ontology.MetadataProposalVersionRef{
			ProposalID: proposal.ID, Revision: proposal.Revision, ContentHash: proposal.ContentHash,
		})
	}
	out := make(map[string][]ontology.MetadataProposalPromotionReceipt, len(refs))
	if len(refs) == 0 {
		return out, known, nil
	}
	if err := budget.ConsumeStoreReads(1); err != nil {
		return nil, nil, err
	}
	page, err := s.receipts.ListCurrentSuccessfulPromotionReceipts(ctx, ontology.CurrentPromotionReceiptBatchQuery{
		TenantID: tenantID, Proposals: refs, Limit: 500,
	})
	if err != nil {
		return nil, nil, err
	}
	if err := budget.ConsumeCandidates(scannedCount(len(page.Receipts), page.HasMore)); err != nil {
		return nil, nil, err
	}
	if page.HasMore {
		return nil, nil, fmt.Errorf("%w: current successful receipt join exceeds its bound", ontologycontract.ErrQueryBudgetExceeded)
	}
	rows, err := s.visibleReceipts(ctx, principal, state, page.Receipts, currentReceiptFields)
	if err != nil {
		return nil, nil, err
	}
	visible := make(map[string]visibleReceipt, len(rows))
	for _, row := range rows {
		visible[row.receipt.ReceiptID] = row
	}
	for _, receipt := range page.Receipts {
		if _, ok := requested[receipt.ProposalID]; !ok {
			return nil, nil, dependencyError(fmt.Errorf("current receipt reader returned an unrelated proposal"))
		}
		row, ok := visible[receipt.ReceiptID]
		if !ok || !fieldsAllowed(row.decision, currentReceiptFields...) {
			known[receipt.ProposalID] = false
			continue
		}
		out[receipt.ProposalID] = append(out[receipt.ProposalID], receipt)
	}
	return out, known, nil
}

func (s *Service) scopeDescriptor(
	authorized ontologyscope.AuthorizedScopeContext,
	kind datavisibility.ResourceKind,
	key string,
	fields []string,
	payload any,
) datavisibility.ResourceDescriptor {
	binding := authorized.Binding()
	return datavisibility.ResourceDescriptor{
		Key: key, Kind: kind, TenantID: binding.TenantID, Type: string(binding.Kind),
		FieldPaths: append([]string(nil), fields...),
		AccessRefs: []datavisibility.AccessRef{{
			Kind: binding.SourceObject.Type, ID: binding.SourceObject.ID, TenantID: binding.TenantID,
		}},
		Payload: payload,
	}
}

func verifyKindRevision(state governancePolicyState, kind datavisibility.ResourceKind, decision datavisibility.Decision) error {
	if strings.TrimSpace(decision.PolicyRevision) != state.kindRevisions[kind] {
		return dependencyError(fmt.Errorf("%s visibility revision changed during query", kind))
	}
	return nil
}

func combinedPolicyRevision(snapshot string, revisions map[datavisibility.ResourceKind]string) string {
	parts := []string{strings.TrimSpace(snapshot)}
	kinds := make([]string, 0, len(revisions))
	for kind := range revisions {
		kinds = append(kinds, string(kind))
	}
	sort.Strings(kinds)
	for _, kind := range kinds {
		parts = append(parts, kind+":"+strings.TrimSpace(revisions[datavisibility.ResourceKind(kind)]))
	}
	if len(parts) == 1 && parts[0] == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

func fieldsAllowed(decision datavisibility.ResourceDecision, fields ...string) bool {
	allowed := make(map[string]struct{}, len(decision.Fields.Allowed))
	for _, field := range decision.Fields.Allowed {
		allowed[field] = struct{}{}
	}
	for _, field := range fields {
		if _, ok := allowed[field]; !ok {
			return false
		}
	}
	return true
}

func allowedFields(decision datavisibility.ResourceDecision) map[string]bool {
	out := make(map[string]bool, len(decision.Fields.Allowed))
	for _, field := range decision.Fields.Allowed {
		out[field] = true
	}
	return out
}

func rawProposals(rows []visibleProposal) []ontology.OntologyMetadataProposal {
	out := make([]ontology.OntologyMetadataProposal, len(rows))
	for index := range rows {
		out[index] = rows[index].proposal
	}
	return out
}

func rowsWithKnownActionability(rows []visibleProposal) []visibleProposal {
	out := make([]visibleProposal, 0, len(rows))
	for _, row := range rows {
		if proposalActionabilityFieldsAllowed(row) {
			out = append(out, row)
		}
	}
	return out
}

func proposalActionabilityFieldsAllowed(row visibleProposal) bool {
	allowed := allowedFields(row.decision)
	if !allowed["actionable"] || !allowed["status"] {
		return false
	}
	if row.proposal.Status == ontology.MetadataProposalStatusAccepted {
		return allowed["revision"] && allowed["content_hash"]
	}
	return true
}

func errorsNewDuplicate(kind, id string) error {
	return fmt.Errorf("duplicate %s id %s in query page", kind, id)
}

func mapProposalItem(proposal ontology.OntologyMetadataProposal, decision datavisibility.ResourceDecision, actionable, actionableKnown bool) ProposalItem {
	allowed := allowedFields(decision)
	item := ProposalItem{ID: proposal.ID}
	if allowed["revision"] {
		item.Revision = int64Value(proposal.Revision)
	}
	if allowed["target_kind"] {
		item.TargetKind = proposal.TargetKind
	}
	if allowed["target_id"] {
		item.TargetID = proposal.TargetID
	}
	if allowed["metadata_key"] {
		item.MetadataKey = proposal.MetadataKey
	}
	if allowed["proposed_value"] {
		item.ProposedValue = proposal.ProposedValue
	}
	if allowed["source_kind"] {
		item.SourceKind = proposal.SourceKind
	}
	if allowed["confidence"] {
		item.Confidence = float64Value(proposal.Confidence)
	}
	if allowed["status"] {
		item.Status = proposal.Status
	}
	if allowed["actionable"] && actionableKnown {
		value := actionable
		item.Actionable = &value
	}
	if allowed["updated_at"] {
		item.UpdatedAt = visibleTime(proposal.UpdatedAt)
	}
	return item
}

func mapProposalDetail(
	proposal ontology.OntologyMetadataProposal,
	decision datavisibility.ResourceDecision,
	actionable bool,
	actionableKnown bool,
	evidenceRefs []EvidenceReference,
	reviewEvidenceRefs []EvidenceReference,
) ProposalDetail {
	allowed := allowedFields(decision)
	detail := ProposalDetail{ProposalItem: mapProposalItem(proposal, decision, actionable, actionableKnown)}
	if allowed["source"] {
		detail.Source = proposal.Source
	}
	if allowed["source_ref"] {
		detail.SourceRef = proposal.SourceRef
	}
	if allowed["source_version"] {
		detail.SourceVersion = proposal.SourceVersion
	}
	if allowed["reason"] {
		detail.Reason = proposal.Reason
	}
	if allowed["evidence_refs"] {
		detail.EvidenceRefs = append([]EvidenceReference(nil), evidenceRefs...)
	}
	if allowed["reviewed_by"] {
		detail.ReviewedBy = proposal.ReviewedBy
	}
	if allowed["reviewed_by_kind"] {
		detail.ReviewedByKind = proposal.ReviewedByKind
	}
	if allowed["reviewed_at"] {
		detail.ReviewedAt = visibleTime(proposal.ReviewedAt)
	}
	if allowed["review_reason"] {
		detail.ReviewReason = proposal.ReviewReason
	}
	if allowed["review_evidence_refs"] {
		detail.ReviewEvidenceRefs = append([]EvidenceReference(nil), reviewEvidenceRefs...)
	}
	if allowed["created_at"] {
		detail.CreatedAt = visibleTime(proposal.CreatedAt)
	}
	return detail
}

func safeAnalysisReport(proposalID string, selected visibleProposal, related []visibleProposal, complete, evidenceComplete bool) AnalysisReport {
	selectedAllowed := allowedFields(selected.decision)
	allValuesVisible := complete && selectedAllowed["proposed_value"]
	raw := make([]ontology.OntologyMetadataProposal, 0, len(related))
	for _, row := range related {
		raw = append(raw, row.proposal)
		allValuesVisible = allValuesVisible && allowedFields(row.decision)["proposed_value"]
	}
	domain := ontology.AnalyzeMetadataProposalConflicts(raw)
	out := AnalysisReport{Issues: []AnalysisIssue{}}
	for _, issue := range domain.Issues {
		if !containsString(issue.ProposalIDs, proposalID) {
			continue
		}
		switch issue.Code {
		case ontology.MetadataProposalIssueConflictingValues:
			if !allValuesVisible {
				continue
			}
		case ontology.MetadataProposalIssueLowConfidence:
			if !selectedAllowed["source_kind"] || !selectedAllowed["confidence"] {
				continue
			}
		case ontology.MetadataProposalIssueMissingEvidence, ontology.MetadataProposalIssueMissingModelRunTrace:
			if !selectedAllowed["source_kind"] || !selectedAllowed["evidence_refs"] || !evidenceComplete {
				continue
			}
		default:
			continue
		}
		out.Issues = append(out.Issues, AnalysisIssue{Code: issue.Code, Severity: issue.Severity, Message: issue.Message})
		if issue.Severity == ontology.MetadataProposalIssueBlocking {
			out.BlockingCount++
		} else if issue.Severity == ontology.MetadataProposalIssueWarning {
			out.WarningCount++
		}
	}
	return out
}

func rawProposalContains(proposals []ontology.OntologyMetadataProposal, proposalID string) bool {
	for _, proposal := range proposals {
		if proposal.ID == proposalID {
			return true
		}
	}
	return false
}

func visibleProposalContains(proposals []visibleProposal, proposalID string) bool {
	for _, proposal := range proposals {
		if proposal.proposal.ID == proposalID {
			return true
		}
	}
	return false
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
