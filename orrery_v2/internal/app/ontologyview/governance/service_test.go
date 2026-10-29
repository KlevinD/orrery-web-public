package governance

import (
	"bytes"
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/datavisibility"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/app/ontologyscope"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

type governanceTestProposalStore struct {
	items     []ontology.OntologyMetadataProposal
	readCalls int
	lastQuery ontology.MetadataProposalCursorQuery
	err       error
}

func (s *governanceTestProposalStore) GetMetadataProposalForTenant(ctx context.Context, tenantID, proposalID string) (ontology.OntologyMetadataProposal, error) {
	s.readCalls++
	if s.err != nil {
		return ontology.OntologyMetadataProposal{}, s.err
	}
	if err := ctx.Err(); err != nil {
		return ontology.OntologyMetadataProposal{}, err
	}
	for _, proposal := range s.items {
		if proposal.TenantID == tenantID && proposal.ID == proposalID {
			return proposal, nil
		}
	}
	return ontology.OntologyMetadataProposal{}, ontology.ErrNotFound
}

func (s *governanceTestProposalStore) ListMetadataProposalCursorPage(ctx context.Context, query ontology.MetadataProposalCursorQuery) (ontology.MetadataProposalCursorPage, error) {
	s.readCalls++
	s.lastQuery = query
	if s.err != nil {
		return ontology.MetadataProposalCursorPage{}, s.err
	}
	if err := ctx.Err(); err != nil {
		return ontology.MetadataProposalCursorPage{}, err
	}
	matched := make([]ontology.OntologyMetadataProposal, 0, len(s.items))
	for _, proposal := range s.items {
		if proposal.TenantID != query.TenantID || query.Status != "" && proposal.Status != query.Status ||
			query.TargetKind != "" && proposal.TargetKind != strings.ToLower(query.TargetKind) ||
			query.TargetID != "" && proposal.TargetID != strings.ToLower(query.TargetID) ||
			query.MetadataKey != "" && proposal.MetadataKey != strings.ToLower(query.MetadataKey) {
			continue
		}
		if query.Search != "" {
			haystack := strings.ToLower(strings.Join([]string{proposal.ID, proposal.TargetID, proposal.MetadataKey, proposal.ProposedValue}, "\x00"))
			if !strings.Contains(haystack, strings.ToLower(query.Search)) {
				continue
			}
		}
		if query.After.ID != "" && !(proposal.UpdatedAt.Before(query.After.UpdatedAt) || proposal.UpdatedAt.Equal(query.After.UpdatedAt) && proposal.ID > query.After.ID) {
			continue
		}
		matched = append(matched, proposal)
	}
	sort.SliceStable(matched, func(i, j int) bool {
		if matched[i].UpdatedAt.Equal(matched[j].UpdatedAt) {
			return matched[i].ID < matched[j].ID
		}
		return matched[i].UpdatedAt.After(matched[j].UpdatedAt)
	})
	hasMore := len(matched) > query.Limit
	if hasMore {
		matched = matched[:query.Limit]
	}
	return ontology.MetadataProposalCursorPage{Proposals: matched, HasMore: hasMore}, nil
}

type governanceTestReceiptStore struct {
	items          []ontology.MetadataProposalPromotionReceipt
	readCalls      int
	err            error
	currentHasMore bool
}

type governanceTestAuditStore struct {
	items     []ProposalAuditRecord
	readCalls int
	err       error
}

func (s *governanceTestAuditStore) ListProposalAuditEvents(ctx context.Context, query ProposalAuditQuery) (ProposalAuditPage, error) {
	s.readCalls++
	if s.err != nil {
		return ProposalAuditPage{}, s.err
	}
	if err := ctx.Err(); err != nil {
		return ProposalAuditPage{}, err
	}
	matched := make([]ProposalAuditRecord, 0, len(s.items))
	for _, event := range s.items {
		if event.TenantID != query.TenantID || event.ProposalID != query.ProposalID {
			continue
		}
		if query.After.AuditID != "" && !(event.OccurredAt.Before(query.After.OccurredAt) || event.OccurredAt.Equal(query.After.OccurredAt) && event.AuditID > query.After.AuditID) {
			continue
		}
		matched = append(matched, event)
	}
	sort.SliceStable(matched, func(i, j int) bool {
		if matched[i].OccurredAt.Equal(matched[j].OccurredAt) {
			return matched[i].AuditID < matched[j].AuditID
		}
		return matched[i].OccurredAt.After(matched[j].OccurredAt)
	})
	hasMore := len(matched) > query.Limit
	if hasMore {
		matched = matched[:query.Limit]
	}
	return ProposalAuditPage{Events: matched, HasMore: hasMore}, nil
}

func (s *governanceTestReceiptStore) ListPromotionReceiptCursorPage(ctx context.Context, query ontology.PromotionReceiptCursorQuery) (ontology.PromotionReceiptCursorPage, error) {
	s.readCalls++
	if s.err != nil {
		return ontology.PromotionReceiptCursorPage{}, s.err
	}
	if err := ctx.Err(); err != nil {
		return ontology.PromotionReceiptCursorPage{}, err
	}
	matched := make([]ontology.MetadataProposalPromotionReceipt, 0, len(s.items))
	for _, receipt := range s.items {
		if receipt.TenantID != query.TenantID || query.ProposalID != "" && receipt.ProposalID != query.ProposalID ||
			query.SuccessfulOnly && receipt.Status != ontology.PromotionReceiptStatusApplied && receipt.Status != ontology.PromotionReceiptStatusNoop {
			continue
		}
		if query.After.ReceiptID != "" && !(receipt.UpdatedAt.Before(query.After.UpdatedAt) || receipt.UpdatedAt.Equal(query.After.UpdatedAt) && receipt.ReceiptID > query.After.ReceiptID) {
			continue
		}
		matched = append(matched, receipt)
	}
	sort.SliceStable(matched, func(i, j int) bool {
		if matched[i].UpdatedAt.Equal(matched[j].UpdatedAt) {
			return matched[i].ReceiptID < matched[j].ReceiptID
		}
		return matched[i].UpdatedAt.After(matched[j].UpdatedAt)
	})
	hasMore := len(matched) > query.Limit
	if hasMore {
		matched = matched[:query.Limit]
	}
	return ontology.PromotionReceiptCursorPage{Receipts: matched, HasMore: hasMore}, nil
}

func (s *governanceTestReceiptStore) ListCurrentSuccessfulPromotionReceipts(ctx context.Context, query ontology.CurrentPromotionReceiptBatchQuery) (ontology.PromotionReceiptCursorPage, error) {
	s.readCalls++
	if s.err != nil {
		return ontology.PromotionReceiptCursorPage{}, s.err
	}
	if err := ctx.Err(); err != nil {
		return ontology.PromotionReceiptCursorPage{}, err
	}
	refs := map[string]ontology.MetadataProposalVersionRef{}
	for _, ref := range query.Proposals {
		refs[ref.ProposalID] = ref
	}
	matched := make([]ontology.MetadataProposalPromotionReceipt, 0, len(s.items))
	for _, receipt := range s.items {
		ref, ok := refs[receipt.ProposalID]
		if !ok || receipt.TenantID != query.TenantID || receipt.ProposalRevision != ref.Revision ||
			receipt.ProposalContentHash != ref.ContentHash || receipt.ProposalBindingStatus != ontology.PromotionReceiptProposalBindingResolved ||
			receipt.Status != ontology.PromotionReceiptStatusApplied && receipt.Status != ontology.PromotionReceiptStatusNoop {
			continue
		}
		matched = append(matched, receipt)
	}
	sort.SliceStable(matched, func(i, j int) bool {
		if matched[i].ProposalID != matched[j].ProposalID {
			return matched[i].ProposalID < matched[j].ProposalID
		}
		if matched[i].UpdatedAt.Equal(matched[j].UpdatedAt) {
			return matched[i].ReceiptID < matched[j].ReceiptID
		}
		return matched[i].UpdatedAt.After(matched[j].UpdatedAt)
	})
	return ontology.PromotionReceiptCursorPage{Receipts: matched, HasMore: s.currentHasMore}, nil
}

type governanceTestResolver struct{ binding ontologyscope.ScopeBinding }

func (r governanceTestResolver) Resolve(_ context.Context, tenantID string) (ontologyscope.ScopeBinding, error) {
	if tenantID == "tenant-2" {
		binding := r.binding
		binding.TenantID = tenantID
		binding.AuthorizationTenantID = tenantID
		binding.SourceObject.ID = tenantID
		return binding, nil
	}
	if tenantID != r.binding.TenantID {
		return ontologyscope.ScopeBinding{}, ontologyscope.ErrBindingNotFound
	}
	return r.binding, nil
}

type governanceTestAuthorizer struct{ deny bool }

func (a governanceTestAuthorizer) AuthorizeFeature(context.Context, identity.Principal, string) error {
	if a.deny {
		return ontologyscope.ErrAccessDenied
	}
	return nil
}

func (a governanceTestAuthorizer) AuthorizeObject(context.Context, identity.Principal, string, identity.ObjectRef, string) error {
	if a.deny {
		return ontologyscope.ErrAccessDenied
	}
	return nil
}

type governanceTestSnapshots struct{}

func (governanceTestSnapshots) Capture(_ context.Context, principal identity.Principal) (authorization.AuthorizationSnapshot, error) {
	return authorization.AuthorizationSnapshot{
		PrincipalKey: authorization.PrincipalCacheKey(principal), AuthorizationRevision: "auth-1", PolicyRevision: "policy-1",
	}, nil
}

type governanceTestPolicy struct {
	hidden map[string]bool
	masked map[string][]string
}

func (p *governanceTestPolicy) Evaluate(_ context.Context, req datavisibility.AdapterRequest) (datavisibility.AdapterDecision, error) {
	out := datavisibility.AdapterDecision{Coverage: datavisibility.CoverageAll, PolicyRevision: "governance-policy-1"}
	for _, resource := range req.Resources {
		hidden := p.hidden[resource.Key]
		for _, ref := range resource.AccessRefs {
			hidden = hidden || ref.ID == "denied"
		}
		masked := append([]string(nil), p.masked[resource.Key]...)
		maskedSet := map[string]struct{}{}
		for _, field := range masked {
			maskedSet[field] = struct{}{}
		}
		allowed := make([]string, 0, len(resource.FieldPaths))
		for _, field := range resource.FieldPaths {
			if _, denied := maskedSet[field]; !denied {
				allowed = append(allowed, field)
			}
		}
		disclosure := datavisibility.ExistenceVisible
		if hidden {
			disclosure, allowed, masked = datavisibility.ExistenceConceal, nil, nil
		}
		out.Resources = append(out.Resources, datavisibility.AdapterResourceDecision{
			Key: resource.Key, Hidden: hidden, AllowedFieldPaths: allowed, MaskedFieldPaths: masked, ExistenceDisclosure: disclosure,
		})
	}
	return out, nil
}

func TestServiceAuthorizesBeforeGovernanceStoreRead(t *testing.T) {
	proposals := &governanceTestProposalStore{items: []ontology.OntologyMetadataProposal{governanceProposal("proposal-1", ontology.MetadataProposalStatusProposed, 1)}}
	receipts := &governanceTestReceiptStore{}
	service := newGovernanceServiceForTest(t, proposals, receipts, &governanceTestPolicy{hidden: map[string]bool{}, masked: map[string][]string{}}, true)
	_, err := service.ListProposals(context.Background(), governancePrincipal("user-1"), ProposalListQuery{TenantID: "tenant-1"})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("denied ListProposals error = %v", err)
	}
	if proposals.readCalls != 0 || receipts.readCalls != 0 {
		t.Fatalf("stores read before scope authorization: proposals=%d receipts=%d", proposals.readCalls, receipts.readCalls)
	}
}

func TestServiceListsActionableProposalsAfterPolicyAndReceiptJoin(t *testing.T) {
	proposals := &governanceTestProposalStore{items: []ontology.OntologyMetadataProposal{
		governanceProposal("hidden-newest", ontology.MetadataProposalStatusProposed, 50),
		governanceProposal("proposed-visible", ontology.MetadataProposalStatusProposed, 40),
		governanceProposal("accepted-hidden-receipt", ontology.MetadataProposalStatusAccepted, 30),
		governanceProposal("accepted-applied", ontology.MetadataProposalStatusAccepted, 20),
		governanceProposal("proposed-middle", ontology.MetadataProposalStatusProposed, 15),
		governanceProposal("proposed-last", ontology.MetadataProposalStatusProposed, 10),
	}}
	receipts := &governanceTestReceiptStore{items: []ontology.MetadataProposalPromotionReceipt{
		governanceReceipt("receipt-hidden", proposals.items[2], ontology.PromotionReceiptStatusApplied, 35),
		governanceReceipt("receipt-visible", proposals.items[3], ontology.PromotionReceiptStatusNoop, 25),
	}}
	policy := &governanceTestPolicy{
		hidden: map[string]bool{"proposal:hidden-newest": true, "receipt:receipt-hidden": true},
		masked: map[string][]string{"proposal:proposed-visible": {"proposed_value"}},
	}
	service := newGovernanceServiceForTest(t, proposals, receipts, policy, false)
	first, err := service.ListProposals(context.Background(), governancePrincipal("user-1"), ProposalListQuery{TenantID: "tenant-1", Limit: 2})
	if err != nil {
		t.Fatalf("ListProposals first: %v", err)
	}
	if len(first.Items) != 2 || first.Items[0].ID != "proposed-visible" || first.Items[0].ProposedValue != "" ||
		first.Items[1].ID != "proposed-middle" || !first.Page.HasMore || first.Page.NextCursor == "" {
		t.Fatalf("first actionable page = %+v", first)
	}
	second, err := service.ListProposals(context.Background(), governancePrincipal("user-1"), ProposalListQuery{
		TenantID: "tenant-1", Limit: 2, Cursor: first.Page.NextCursor,
	})
	if err != nil || len(second.Items) != 1 || second.Items[0].ID != "proposed-last" || second.Page.HasMore {
		t.Fatalf("second actionable page = %+v, err=%v", second, err)
	}
	all, err := service.ListProposals(context.Background(), governancePrincipal("user-1"), ProposalListQuery{TenantID: "tenant-1", Queue: QueueAll, Limit: 10})
	if err != nil {
		t.Fatalf("ListProposals all: %v", err)
	}
	foundUnknown := false
	for _, item := range all.Items {
		if item.ID == "accepted-hidden-receipt" && item.Actionable != nil {
			t.Fatalf("hidden current receipt exposed actionability: %+v", item)
		}
		foundUnknown = foundUnknown || item.ID == "accepted-hidden-receipt"
	}
	if !foundUnknown {
		t.Fatalf("all queue omitted proposal with unknown actionability: %+v", all)
	}
	_, err = service.ListProposals(context.Background(), governancePrincipal("user-2"), ProposalListQuery{
		TenantID: "tenant-1", Limit: 2, Cursor: first.Page.NextCursor,
	})
	if !errors.Is(err, ontologycontract.ErrCursorStale) {
		t.Fatalf("cross-principal cursor error = %v", err)
	}
}

func TestServiceFiltersProposalsOnlyAfterFieldPolicy(t *testing.T) {
	secret := governanceProposal("secret", ontology.MetadataProposalStatusProposed, 20)
	secret.TargetID = "secret-target"
	secret.MetadataKey = "secret-key"
	secret.ProposedValue = "classified-value"
	visible := governanceProposal("visible", ontology.MetadataProposalStatusProposed, 10)
	proposals := &governanceTestProposalStore{items: []ontology.OntologyMetadataProposal{secret, visible}}
	policy := &governanceTestPolicy{hidden: map[string]bool{}, masked: map[string][]string{
		"proposal:secret": {"metadata_key", "proposed_value", "status", "target_id", "target_kind"},
	}}
	service := newGovernanceServiceForTest(t, proposals, &governanceTestReceiptStore{}, policy, false)
	principal := governancePrincipal("user-1")
	for _, search := range []string{"secret-target", "secret-key", "classified-value"} {
		page, err := service.ListProposals(context.Background(), principal, ProposalListQuery{TenantID: "tenant-1", Queue: QueueAll, Search: search})
		if err != nil || len(page.Items) != 0 {
			t.Fatalf("masked search %q page=%+v err=%v", search, page, err)
		}
	}
	page, err := service.ListProposals(context.Background(), principal, ProposalListQuery{TenantID: "tenant-1", Queue: QueueAll, Status: ontology.MetadataProposalStatusProposed})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != "visible" {
		t.Fatalf("masked status page=%+v err=%v", page, err)
	}
	page, err = service.ListProposals(context.Background(), principal, ProposalListQuery{TenantID: "tenant-1", Queue: QueueAll, TargetKind: "object_type"})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != "visible" {
		t.Fatalf("masked target kind page=%+v err=%v", page, err)
	}
	if query := proposals.lastQuery; query.Status != "" || query.TargetKind != "" || query.Search != "" {
		t.Fatalf("sensitive proposal filters reached candidate store: %+v", query)
	}
}

func TestServiceOmitsActionabilityWhenJoinFieldsAreMasked(t *testing.T) {
	proposal := governanceProposal("proposal-accepted", ontology.MetadataProposalStatusAccepted, 10)
	proposals := &governanceTestProposalStore{items: []ontology.OntologyMetadataProposal{proposal}}
	policy := &governanceTestPolicy{
		hidden: map[string]bool{}, masked: map[string][]string{"proposal:proposal-accepted": {"content_hash"}},
	}
	service := newGovernanceServiceForTest(t, proposals, &governanceTestReceiptStore{}, policy, false)
	all, err := service.ListProposals(context.Background(), governancePrincipal("user-1"), ProposalListQuery{TenantID: "tenant-1", Queue: QueueAll})
	if err != nil || len(all.Items) != 1 || all.Items[0].Actionable != nil {
		t.Fatalf("masked all queue = %+v, err=%v", all, err)
	}
	actionable, err := service.ListProposals(context.Background(), governancePrincipal("user-1"), ProposalListQuery{TenantID: "tenant-1", Queue: QueueActionable})
	if err != nil || len(actionable.Items) != 0 || actionable.Page.HasMore {
		t.Fatalf("masked actionable queue = %+v, err=%v", actionable, err)
	}
}

func TestServiceConcealsExactProposalAndMapsOnlyAllowedDetailFields(t *testing.T) {
	proposal := governanceProposal("proposal-detail", ontology.MetadataProposalStatusProposed, 10)
	proposal.ProposedValue = "secret-value"
	proposal.EvidenceRefs = []ontology.MetadataEvidenceRef{
		{Kind: "document", ID: "doc-1", Metadata: map[string]string{"secret": "not-for-dto"}},
		{Kind: "document", ID: "doc-hidden"},
	}
	proposals := &governanceTestProposalStore{items: []ontology.OntologyMetadataProposal{proposal}}
	policy := &governanceTestPolicy{
		hidden: map[string]bool{"evidence-ref:001": true},
		masked: map[string][]string{"proposal:proposal-detail": {"proposed_value", "source_ref"}},
	}
	service := newGovernanceServiceForTest(t, proposals, &governanceTestReceiptStore{}, policy, false)
	detail, err := service.GetProposal(context.Background(), governancePrincipal("user-1"), "tenant-1", proposal.ID)
	if err != nil {
		t.Fatalf("GetProposal: %v", err)
	}
	if detail.ID != proposal.ID || detail.ProposedValue != "" || detail.SourceRef != "" || len(detail.EvidenceRefs) != 1 || detail.EvidenceRefs[0].ID != "doc-1" {
		t.Fatalf("masked proposal detail = %+v", detail)
	}
	if _, err := service.GetProposal(context.Background(), governancePrincipal("user-1"), "tenant-2", proposal.ID); !errors.Is(err, ontologycontract.ErrUnavailable) {
		t.Fatalf("wrong-tenant exact error = %v", err)
	}
	policy.hidden["proposal:proposal-detail"] = true
	if _, err := service.GetProposal(context.Background(), governancePrincipal("user-1"), "tenant-1", proposal.ID); !errors.Is(err, ontologycontract.ErrUnavailable) {
		t.Fatalf("hidden exact proposal error = %v", err)
	}
	if _, err := service.GetProposal(context.Background(), governancePrincipal("user-1"), "tenant-1", "missing"); !errors.Is(err, ontologycontract.ErrUnavailable) {
		t.Fatalf("missing exact proposal error = %v", err)
	}
}

func TestServiceAnalysisSuppressesConflictUnlessEveryRelatedValueIsVisible(t *testing.T) {
	selected := governanceProposal("proposal-a", ontology.MetadataProposalStatusProposed, 20)
	selected.SourceKind = ontology.MetadataProposalSourceModelInference
	selected.Confidence = 0.5
	selected.EvidenceRefs = nil
	other := governanceProposal("proposal-b", ontology.MetadataProposalStatusProposed, 10)
	other.ProposedValue = "another-value"
	proposals := &governanceTestProposalStore{items: []ontology.OntologyMetadataProposal{selected, other}}
	policy := &governanceTestPolicy{hidden: map[string]bool{"proposal:proposal-b": true}, masked: map[string][]string{}}
	service := newGovernanceServiceForTest(t, proposals, &governanceTestReceiptStore{}, policy, false)
	report, err := service.AnalyzeProposal(context.Background(), governancePrincipal("user-1"), "tenant-1", selected.ID)
	if err != nil {
		t.Fatalf("AnalyzeProposal hidden conflict: %v", err)
	}
	if hasAnalysisIssue(report, ontology.MetadataProposalIssueConflictingValues) || !hasAnalysisIssue(report, ontology.MetadataProposalIssueLowConfidence) || !hasAnalysisIssue(report, ontology.MetadataProposalIssueMissingEvidence) {
		t.Fatalf("hidden-conflict analysis = %+v", report)
	}
	delete(policy.hidden, "proposal:proposal-b")
	policy.masked["proposal:proposal-b"] = []string{"proposed_value"}
	report, err = service.AnalyzeProposal(context.Background(), governancePrincipal("user-1"), "tenant-1", selected.ID)
	if err != nil || hasAnalysisIssue(report, ontology.MetadataProposalIssueConflictingValues) {
		t.Fatalf("masked-conflict analysis = %+v, err=%v", report, err)
	}
	delete(policy.masked, "proposal:proposal-b")
	report, err = service.AnalyzeProposal(context.Background(), governancePrincipal("user-1"), "tenant-1", selected.ID)
	if err != nil || !hasAnalysisIssue(report, ontology.MetadataProposalIssueConflictingValues) {
		t.Fatalf("visible-conflict analysis = %+v, err=%v", report, err)
	}
}

func TestServiceAnalysisReturnsEmptyForDecidedProposal(t *testing.T) {
	proposal := governanceProposal("proposal-accepted", ontology.MetadataProposalStatusAccepted, 10)
	proposals := &governanceTestProposalStore{items: []ontology.OntologyMetadataProposal{proposal}}
	service := newGovernanceServiceForTest(t, proposals, &governanceTestReceiptStore{}, &governanceTestPolicy{hidden: map[string]bool{}, masked: map[string][]string{}}, false)
	report, err := service.AnalyzeProposal(context.Background(), governancePrincipal("user-1"), "tenant-1", proposal.ID)
	if err != nil || report.BlockingCount != 0 || report.WarningCount != 0 || len(report.Issues) != 0 || proposals.readCalls != 1 {
		t.Fatalf("decided proposal analysis = %+v, reads=%d, err=%v", report, proposals.readCalls, err)
	}
}

func TestServiceSummaryUsesOnlyVisibleProposalsAndReceipts(t *testing.T) {
	proposed := governanceProposal("proposal-proposed", ontology.MetadataProposalStatusProposed, 30)
	accepted := governanceProposal("proposal-accepted", ontology.MetadataProposalStatusAccepted, 20)
	hidden := governanceProposal("proposal-hidden", ontology.MetadataProposalStatusProposed, 10)
	proposals := &governanceTestProposalStore{items: []ontology.OntologyMetadataProposal{proposed, accepted, hidden}}
	receipts := &governanceTestReceiptStore{items: []ontology.MetadataProposalPromotionReceipt{
		governanceReceipt("receipt-new-hidden", accepted, ontology.PromotionReceiptStatusApplied, 50),
		governanceReceipt("receipt-visible", accepted, ontology.PromotionReceiptStatusNoop, 40),
	}}
	policy := &governanceTestPolicy{
		hidden: map[string]bool{"proposal:proposal-hidden": true, "receipt:receipt-new-hidden": true}, masked: map[string][]string{},
	}
	service := newGovernanceServiceForTest(t, proposals, receipts, policy, false)
	summary, err := service.Summary(context.Background(), governancePrincipal("user-1"), "tenant-1")
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if summary.Data.ActionableProposals.Status != ontologycontract.MetricUnavailable || summary.Data.LatestReceipt.Status != ontologycontract.MetricUnavailable ||
		summary.Meta.DataRevision == "" || summary.Meta.AuthorizationRevision == nil {
		t.Fatalf("governance summary = %+v", summary)
	}
	stable, err := service.Summary(context.Background(), governancePrincipal("user-1"), "tenant-1")
	if err != nil || stable.Meta.DataRevision != summary.Meta.DataRevision {
		t.Fatalf("stable summary revision = %q, want %q, err=%v", stable.Meta.DataRevision, summary.Meta.DataRevision, err)
	}
	receipts.currentHasMore = true
	summary, err = service.Summary(context.Background(), governancePrincipal("user-1"), "tenant-1")
	if err != nil || summary.Data.ActionableProposals.Status != ontologycontract.MetricUnavailable || summary.Data.LatestReceipt.Status != ontologycontract.MetricUnavailable {
		t.Fatalf("truncated summary = %+v, err=%v", summary, err)
	}
}

func TestServiceListsPolicyFilteredReceiptHistoryWithBoundCursor(t *testing.T) {
	proposal := governanceProposal("proposal-receipts", ontology.MetadataProposalStatusAccepted, 60)
	proposals := &governanceTestProposalStore{items: []ontology.OntologyMetadataProposal{proposal}}
	receipts := &governanceTestReceiptStore{items: []ontology.MetadataProposalPromotionReceipt{
		governanceReceipt("receipt-hidden", proposal, ontology.PromotionReceiptStatusApplied, 50),
		governanceReceipt("receipt-visible-a", proposal, ontology.PromotionReceiptStatusNoop, 40),
		governanceReceipt("receipt-visible-b", proposal, ontology.PromotionReceiptStatusBlocked, 30),
	}}
	receipts.items[1].Changes = []ontology.MetadataProposalPromotionChange{{TargetID: "target-1"}}
	receipts.items[1].Issues = []ontology.MetadataProposalPromotionIssue{{Code: "warning"}}
	policy := &governanceTestPolicy{
		hidden: map[string]bool{"receipt:receipt-hidden": true},
		masked: map[string][]string{"receipt:receipt-visible-a": {"actor_id"}},
	}
	service := newGovernanceServiceForTest(t, proposals, receipts, policy, false)
	first, err := service.ListReceipts(context.Background(), governancePrincipal("user-1"), ReceiptListQuery{TenantID: "tenant-1", ProposalID: proposal.ID, Limit: 1})
	if err != nil || len(first.Items) != 1 || first.Items[0].ReceiptID != "receipt-visible-a" || first.Items[0].ActorID != "" ||
		first.Items[0].ChangeCount == nil || *first.Items[0].ChangeCount != 1 || first.Items[0].IssueCount == nil || *first.Items[0].IssueCount != 1 || !first.Page.HasMore {
		t.Fatalf("first receipt page = %+v, err=%v", first, err)
	}
	second, err := service.ListReceipts(context.Background(), governancePrincipal("user-1"), ReceiptListQuery{
		TenantID: "tenant-1", ProposalID: proposal.ID, Limit: 1, Cursor: first.Page.NextCursor,
	})
	if err != nil || len(second.Items) != 1 || second.Items[0].ReceiptID != "receipt-visible-b" || second.Page.HasMore {
		t.Fatalf("second receipt page = %+v, err=%v", second, err)
	}
	if _, err := service.ListReceipts(context.Background(), governancePrincipal("user-2"), ReceiptListQuery{
		TenantID: "tenant-1", ProposalID: proposal.ID, Limit: 1, Cursor: first.Page.NextCursor,
	}); !errors.Is(err, ontologycontract.ErrCursorStale) {
		t.Fatalf("cross-principal receipt cursor error = %v", err)
	}
}

func TestServiceRequiresVisibleProposalBeforeReceiptOrAuditRead(t *testing.T) {
	proposal := governanceProposal("proposal-hidden", ontology.MetadataProposalStatusProposed, 10)
	proposals := &governanceTestProposalStore{items: []ontology.OntologyMetadataProposal{proposal}}
	receipts := &governanceTestReceiptStore{items: []ontology.MetadataProposalPromotionReceipt{governanceReceipt("receipt-1", proposal, ontology.PromotionReceiptStatusApplied, 20)}}
	audits := &governanceTestAuditStore{items: []ProposalAuditRecord{governanceAudit("audit-1", proposal.ID, 20)}}
	policy := &governanceTestPolicy{hidden: map[string]bool{"proposal:proposal-hidden": true}, masked: map[string][]string{}}
	service := newGovernanceServiceForTestWithAudit(t, proposals, receipts, audits, policy, false)
	if _, err := service.ListReceipts(context.Background(), governancePrincipal("user-1"), ReceiptListQuery{TenantID: "tenant-1", ProposalID: proposal.ID}); !errors.Is(err, ontologycontract.ErrUnavailable) {
		t.Fatalf("hidden proposal receipt error = %v", err)
	}
	if _, err := service.ListAuditEvents(context.Background(), governancePrincipal("user-1"), AuditEventListQuery{TenantID: "tenant-1", ProposalID: proposal.ID}); !errors.Is(err, ontologycontract.ErrUnavailable) {
		t.Fatalf("hidden proposal audit error = %v", err)
	}
	if receipts.readCalls != 0 || audits.readCalls != 0 {
		t.Fatalf("child stores read before proposal visibility: receipts=%d audits=%d", receipts.readCalls, audits.readCalls)
	}
}

func TestServiceListsAuditEventsWithIndependentPolicyFiltering(t *testing.T) {
	proposal := governanceProposal("proposal-audit", ontology.MetadataProposalStatusAccepted, 60)
	proposals := &governanceTestProposalStore{items: []ontology.OntologyMetadataProposal{proposal}}
	audits := &governanceTestAuditStore{items: []ProposalAuditRecord{
		governanceAudit("audit-hidden", proposal.ID, 50),
		governanceAudit("audit-visible-a", proposal.ID, 40),
		governanceAudit("audit-visible-b", proposal.ID, 30),
	}}
	audits.items[0].AccessRefs = []datavisibility.AccessRef{{Kind: "mission", ID: "denied", TenantID: "tenant-1"}}
	policy := &governanceTestPolicy{
		hidden: map[string]bool{},
		masked: map[string][]string{"audit:audit-visible-a": {"actor_id", "summary"}},
	}
	service := newGovernanceServiceForTestWithAudit(t, proposals, &governanceTestReceiptStore{}, audits, policy, false)
	first, err := service.ListAuditEvents(context.Background(), governancePrincipal("user-1"), AuditEventListQuery{TenantID: "tenant-1", ProposalID: proposal.ID, Limit: 1})
	if err != nil || len(first.Items) != 1 || first.Items[0].AuditID != "audit-visible-a" || first.Items[0].ActorID != "" || first.Items[0].Summary != "" || !first.Page.HasMore {
		t.Fatalf("first audit page = %+v, err=%v", first, err)
	}
	second, err := service.ListAuditEvents(context.Background(), governancePrincipal("user-1"), AuditEventListQuery{
		TenantID: "tenant-1", ProposalID: proposal.ID, Limit: 1, Cursor: first.Page.NextCursor,
	})
	if err != nil || len(second.Items) != 1 || second.Items[0].AuditID != "audit-visible-b" || second.Page.HasMore {
		t.Fatalf("second audit page = %+v, err=%v", second, err)
	}
}

func newGovernanceServiceForTest(
	t *testing.T,
	proposals *governanceTestProposalStore,
	receipts *governanceTestReceiptStore,
	policy *governanceTestPolicy,
	deny bool,
) *Service {
	return newGovernanceServiceForTestWithAudit(t, proposals, receipts, &governanceTestAuditStore{}, policy, deny)
}

func newGovernanceServiceForTestWithAudit(
	t *testing.T,
	proposals *governanceTestProposalStore,
	receipts *governanceTestReceiptStore,
	audits *governanceTestAuditStore,
	policy *governanceTestPolicy,
	deny bool,
) *Service {
	t.Helper()
	binding := ontologyscope.ScopeBinding{
		TenantID: "tenant-1", Kind: ontologyscope.KindCustom, SourceObject: identity.ObjectRef{Type: "workspace", ID: "tenant-1"},
		AuthorizationTenantID: "tenant-1", ReadAction: "workspace.view", ManageAction: "workspace.manage", BindingVersion: 1, State: ontologyscope.BindingStateActive,
	}
	gate := ontologyscope.NewScopeAccessGate(governanceTestResolver{binding: binding}, governanceTestAuthorizer{deny: deny}, governanceTestSnapshots{})
	codec, err := ontologycontract.NewCursorCodec(ontologycontract.CursorCodecOptions{
		PrimaryID: "test", Keys: []ontologycontract.CursorKey{{ID: "test", Key: bytes.Repeat([]byte{11}, 32)}},
	})
	if err != nil {
		t.Fatalf("NewCursorCodec: %v", err)
	}
	stateful, err := ontologycontract.NewStateCursorStore(ontologycontract.StateCursorOptions{MaxEntries: 100})
	if err != nil {
		t.Fatalf("NewStateCursorStore: %v", err)
	}
	visibility := datavisibility.NewService(
		datavisibility.Register(datavisibility.ResourceAudit, policy),
		datavisibility.Register(datavisibility.ResourceProposal, policy),
		datavisibility.Register(datavisibility.ResourceReceipt, policy),
		datavisibility.Register(datavisibility.ResourceEvidence, policy),
	)
	service, err := NewService(gate, proposals, receipts, audits, visibility, CursorSet{Encrypted: codec, Stateful: stateful}, nil, nil)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	service.now = func() time.Time { return time.Unix(100, 0).UTC() }
	return service
}

func governancePrincipal(id string) identity.Principal {
	return identity.NormalizePrincipal(identity.Principal{
		SubjectProvider: "oidc", Subject: id, UserID: id, ViewerKind: identity.ViewerKindUser, CoarseScopes: []string{"operations.read"},
	})
}

func governanceProposal(id string, status ontology.MetadataProposalStatus, unix int64) ontology.OntologyMetadataProposal {
	return ontology.OntologyMetadataProposal{
		ID: id, TenantID: "tenant-1", Revision: 1, ContentHash: "hash-" + id,
		TargetKind: "object_type", TargetID: "task", MetadataKey: "owner_domain", ProposedValue: "mission",
		Source: "deterministic.owner", SourceKind: ontology.MetadataProposalSourceDeterministic, Confidence: 1,
		Status: status, CreatedAt: time.Unix(unix-1, 0).UTC(), UpdatedAt: time.Unix(unix, 0).UTC(),
	}
}

func governanceReceipt(id string, proposal ontology.OntologyMetadataProposal, status ontology.PromotionReceiptStatus, unix int64) ontology.MetadataProposalPromotionReceipt {
	return ontology.MetadataProposalPromotionReceipt{
		ReceiptID: id, TenantID: proposal.TenantID, ProposalID: proposal.ID, ProposalRevision: proposal.Revision,
		ProposalContentHash: proposal.ContentHash, ProposalBindingStatus: ontology.PromotionReceiptProposalBindingResolved,
		Status: status, ActorID: "reviewer-1", ActorKind: "user", AppliedAt: time.Unix(unix, 0).UTC(), UpdatedAt: time.Unix(unix, 0).UTC(),
	}
}

func governanceAudit(id, proposalID string, unix int64) ProposalAuditRecord {
	return ProposalAuditRecord{
		AuditID: id, TenantID: "tenant-1", ProposalID: proposalID, Action: "ontology.governance.review",
		Result: "succeeded", Severity: "info", ActorID: "reviewer-1", ActorKind: "user",
		Summary: "proposal reviewed", OccurredAt: time.Unix(unix, 0).UTC(),
	}
}

func hasAnalysisIssue(report AnalysisReport, code ontology.MetadataProposalIssueCode) bool {
	for _, issue := range report.Issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}
