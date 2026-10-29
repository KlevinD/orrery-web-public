package ontology

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestProposalQueryActionableUsesCurrentResolvedSuccessfulReceipt(t *testing.T) {
	ctx := context.Background()
	core, closeStore := newReceiptBindingTestStore(t, ctx, "proposal-query-actionable")
	defer closeStore()
	proposals := NewPlatformMetadataProposalStore(core)
	receipts := NewPlatformMetadataProposalPromotionReceiptStore(core)
	fixtures := []OntologyMetadataProposal{
		governanceProposalFixture("proposal-proposed", MetadataProposalStatusProposed),
		governanceProposalFixture("proposal-applied", MetadataProposalStatusAccepted),
		governanceProposalFixture("proposal-legacy-receipt", MetadataProposalStatusAccepted),
		governanceProposalFixture("proposal-rejected", MetadataProposalStatusRejected),
	}
	for _, proposal := range fixtures {
		if err := proposals.SaveMetadataProposal(ctx, proposal); err != nil {
			t.Fatalf("SaveMetadataProposal(%s): %v", proposal.ID, err)
		}
	}
	applied, err := proposals.GetMetadataProposalForTenant(ctx, "tenant-1", "proposal-applied")
	if err != nil {
		t.Fatalf("GetMetadataProposalForTenant applied: %v", err)
	}
	legacy, err := proposals.GetMetadataProposalForTenant(ctx, "tenant-1", "proposal-legacy-receipt")
	if err != nil {
		t.Fatalf("GetMetadataProposalForTenant legacy: %v", err)
	}
	for _, receipt := range []MetadataProposalPromotionReceipt{
		governanceReceiptFixture("receipt-current", applied, PromotionReceiptProposalBindingResolved, PromotionReceiptStatusApplied, time.Unix(20, 0)),
		governanceReceiptFixture("receipt-old-revision", OntologyMetadataProposal{ID: applied.ID, TenantID: applied.TenantID, Revision: applied.Revision + 1, ContentHash: applied.ContentHash}, PromotionReceiptProposalBindingResolved, PromotionReceiptStatusApplied, time.Unix(21, 0)),
		governanceReceiptFixture("receipt-legacy-unresolved", legacy, PromotionReceiptProposalBindingLegacyUnresolved, PromotionReceiptStatusNoop, time.Unix(22, 0)),
	} {
		if receipt.ProposalBindingStatus == PromotionReceiptProposalBindingLegacyUnresolved {
			receipt.ProposalRevision = 0
			receipt.ProposalContentHash = ""
		}
		if err := receipts.SavePromotionReceipt(ctx, receipt); err != nil {
			t.Fatalf("SavePromotionReceipt(%s): %v", receipt.ReceiptID, err)
		}
	}

	page, err := proposals.ListMetadataProposalCursorPage(ctx, MetadataProposalCursorQuery{TenantID: "tenant-1", Limit: 10})
	if err != nil {
		t.Fatalf("ListMetadataProposalCursorPage: %v", err)
	}
	refs := make([]MetadataProposalVersionRef, 0, len(page.Proposals))
	for _, proposal := range page.Proposals {
		if proposal.Status == MetadataProposalStatusAccepted {
			refs = append(refs, MetadataProposalVersionRef{ProposalID: proposal.ID, Revision: proposal.Revision, ContentHash: proposal.ContentHash})
		}
	}
	matching, err := receipts.ListCurrentSuccessfulPromotionReceipts(ctx, CurrentPromotionReceiptBatchQuery{TenantID: "tenant-1", Proposals: refs, Limit: 100})
	if err != nil {
		t.Fatalf("ListCurrentSuccessfulPromotionReceipts: %v", err)
	}
	if matching.HasMore {
		t.Fatal("current receipt join unexpectedly truncated")
	}
	receiptsByProposal := map[string][]MetadataProposalPromotionReceipt{}
	for _, receipt := range matching.Receipts {
		receiptsByProposal[receipt.ProposalID] = append(receiptsByProposal[receipt.ProposalID], receipt)
	}
	actionable := map[string]bool{}
	for _, proposal := range page.Proposals {
		if IsMetadataProposalActionable(proposal, receiptsByProposal[proposal.ID]) {
			actionable[proposal.ID] = true
		}
	}
	if len(actionable) != 2 || !actionable["proposal-proposed"] || !actionable["proposal-legacy-receipt"] || actionable["proposal-applied"] || actionable["proposal-rejected"] {
		t.Fatalf("actionable proposals = %+v", actionable)
	}
}

func TestProposalQueryReceiptsUsesStableCursorAndLiteralSearch(t *testing.T) {
	ctx := context.Background()
	core, closeStore := newReceiptBindingTestStore(t, ctx, "proposal-query-receipts")
	defer closeStore()
	proposals := NewPlatformMetadataProposalStore(core)
	proposalTimes := []time.Time{time.Unix(30, 0).UTC(), time.Unix(20, 0).UTC(), time.Unix(20, 0).UTC()}
	proposalIDs := []string{"proposal-newest", "proposal-a", "proposal-b"}
	for index, id := range proposalIDs {
		proposals.now = func() time.Time { return proposalTimes[index] }
		proposal := governanceProposalFixture(id, MetadataProposalStatusProposed)
		if id == "proposal-b" {
			proposal.TargetID = "task_100%"
		}
		if err := proposals.SaveMetadataProposal(ctx, proposal); err != nil {
			t.Fatalf("SaveMetadataProposal(%s): %v", id, err)
		}
	}
	first, err := proposals.ListMetadataProposalCursorPage(ctx, MetadataProposalCursorQuery{TenantID: "tenant-1", Limit: 2})
	if err != nil {
		t.Fatalf("first proposal page: %v", err)
	}
	if !first.HasMore || len(first.Proposals) != 2 || first.Proposals[0].ID != "proposal-newest" || first.Proposals[1].ID != "proposal-a" {
		t.Fatalf("first proposal page = %+v", first)
	}
	last := first.Proposals[len(first.Proposals)-1]
	second, err := proposals.ListMetadataProposalCursorPage(ctx, MetadataProposalCursorQuery{
		TenantID: "tenant-1", Limit: 2, After: MetadataProposalSeek{UpdatedAt: last.UpdatedAt, ID: last.ID},
	})
	if err != nil {
		t.Fatalf("second proposal page: %v", err)
	}
	if second.HasMore || len(second.Proposals) != 1 || second.Proposals[0].ID != "proposal-b" {
		t.Fatalf("second proposal page = %+v", second)
	}
	search, err := proposals.ListMetadataProposalCursorPage(ctx, MetadataProposalCursorQuery{TenantID: "tenant-1", Search: "_100%", Limit: 10})
	if err != nil {
		t.Fatalf("literal proposal search: %v", err)
	}
	if len(search.Proposals) != 1 || search.Proposals[0].ID != "proposal-b" {
		t.Fatalf("literal search page = %+v", search)
	}
	target, err := proposals.ListMetadataProposalCursorPage(ctx, MetadataProposalCursorQuery{
		TenantID: "tenant-1", TargetKind: "OBJECT_TYPE", TargetID: "TASK_100%", MetadataKey: "OWNER_DOMAIN", Limit: 10,
	})
	if err != nil {
		t.Fatalf("targeted proposal page: %v", err)
	}
	if len(target.Proposals) != 1 || target.Proposals[0].ID != "proposal-b" {
		t.Fatalf("targeted proposal page = %+v", target)
	}

	receipts := NewPlatformMetadataProposalPromotionReceiptStore(core)
	proposal := first.Proposals[0]
	for _, fixture := range []struct {
		id string
		at time.Time
	}{
		{id: "receipt-newest", at: time.Unix(50, 0).UTC()},
		{id: "receipt-a", at: time.Unix(40, 0).UTC()},
		{id: "receipt-b", at: time.Unix(40, 0).UTC()},
	} {
		receipts.now = func() time.Time { return fixture.at }
		receipt := governanceReceiptFixture(fixture.id, proposal, PromotionReceiptProposalBindingResolved, PromotionReceiptStatusApplied, fixture.at)
		if err := receipts.SavePromotionReceipt(ctx, receipt); err != nil {
			t.Fatalf("SavePromotionReceipt(%s): %v", fixture.id, err)
		}
	}
	receiptFirst, err := receipts.ListPromotionReceiptCursorPage(ctx, PromotionReceiptCursorQuery{TenantID: "tenant-1", ProposalID: proposal.ID, Limit: 2})
	if err != nil {
		t.Fatalf("first receipt page: %v", err)
	}
	if !receiptFirst.HasMore || len(receiptFirst.Receipts) != 2 || receiptFirst.Receipts[0].ReceiptID != "receipt-newest" || receiptFirst.Receipts[1].ReceiptID != "receipt-a" {
		t.Fatalf("first receipt page = %+v", receiptFirst)
	}
	receiptLast := receiptFirst.Receipts[len(receiptFirst.Receipts)-1]
	receiptSecond, err := receipts.ListPromotionReceiptCursorPage(ctx, PromotionReceiptCursorQuery{
		TenantID: "tenant-1", ProposalID: proposal.ID, Limit: 2,
		After: PromotionReceiptSeek{UpdatedAt: receiptLast.UpdatedAt, ReceiptID: receiptLast.ReceiptID},
	})
	if err != nil {
		t.Fatalf("second receipt page: %v", err)
	}
	if receiptSecond.HasMore || len(receiptSecond.Receipts) != 1 || receiptSecond.Receipts[0].ReceiptID != "receipt-b" {
		t.Fatalf("second receipt page = %+v", receiptSecond)
	}
}

func TestProposalQueryWrongTenantExactReadsAreUnavailable(t *testing.T) {
	ctx := context.Background()
	core, closeStore := newReceiptBindingTestStore(t, ctx, "proposal-query-wrong-tenant")
	defer closeStore()
	proposals := NewPlatformMetadataProposalStore(core)
	proposal := governanceProposalFixture("proposal-tenant", MetadataProposalStatusAccepted)
	if err := proposals.SaveMetadataProposal(ctx, proposal); err != nil {
		t.Fatalf("SaveMetadataProposal: %v", err)
	}
	stored, err := proposals.GetMetadataProposalForTenant(ctx, "tenant-1", proposal.ID)
	if err != nil {
		t.Fatalf("GetMetadataProposalForTenant: %v", err)
	}
	receipts := NewPlatformMetadataProposalPromotionReceiptStore(core)
	receipt := governanceReceiptFixture("receipt-tenant", stored, PromotionReceiptProposalBindingResolved, PromotionReceiptStatusApplied, time.Unix(60, 0))
	if err := receipts.SavePromotionReceipt(ctx, receipt); err != nil {
		t.Fatalf("SavePromotionReceipt: %v", err)
	}
	if _, err := proposals.GetMetadataProposalForTenant(ctx, "tenant-2", proposal.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong-tenant proposal error = %v", err)
	}
	if _, err := receipts.GetPromotionReceiptForTenant(ctx, "tenant-2", receipt.ReceiptID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong-tenant receipt error = %v", err)
	}
	page, err := receipts.ListPromotionReceiptCursorPage(ctx, PromotionReceiptCursorQuery{TenantID: "tenant-2", ProposalID: proposal.ID, Limit: 10})
	if err != nil {
		t.Fatalf("wrong-tenant receipt page: %v", err)
	}
	if len(page.Receipts) != 0 || page.HasMore {
		t.Fatalf("wrong-tenant receipt page = %+v", page)
	}
}

func governanceProposalFixture(id string, status MetadataProposalStatus) OntologyMetadataProposal {
	return OntologyMetadataProposal{
		ID: id, TenantID: "tenant-1", TargetKind: "object_type", TargetID: "task", MetadataKey: "owner_domain",
		ProposedValue: "mission", Source: "deterministic.owner", Status: status,
	}
}

func governanceReceiptFixture(id string, proposal OntologyMetadataProposal, binding PromotionReceiptProposalBindingStatus, status PromotionReceiptStatus, at time.Time) MetadataProposalPromotionReceipt {
	return MetadataProposalPromotionReceipt{
		ReceiptID: id, TenantID: proposal.TenantID, ProposalID: proposal.ID,
		ProposalRevision: proposal.Revision, ProposalContentHash: proposal.ContentHash, ProposalBindingStatus: binding,
		ActorID: "reviewer-1", ActorKind: "user", IdempotencyKey: "apply:" + id,
		Status: status, Applied: status == PromotionReceiptStatusApplied, AppliedAt: at.UTC(), CreatedAt: at.UTC(), UpdatedAt: at.UTC(),
	}
}
