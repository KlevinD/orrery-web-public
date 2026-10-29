package ontology

import (
	"context"
	"fmt"
	"strings"

	"orrery_v2/internal/platform/db"
)

type MetadataReceiptBindingBackfillOptions struct {
	Apply          bool
	Limit          int
	AfterReceiptID string
}

type MetadataReceiptBindingBackfillIssue struct {
	ReceiptID string `json:"receipt_id"`
	Reason    string `json:"reason"`
}

type MetadataReceiptBindingBackfillReport struct {
	DryRun             bool                                  `json:"dry_run"`
	Inspected          int                                   `json:"inspected"`
	Resolved           int                                   `json:"resolved"`
	Updated            int                                   `json:"updated"`
	LegacyUnresolved   int                                   `json:"legacy_unresolved"`
	Issues             []MetadataReceiptBindingBackfillIssue `json:"issues,omitempty"`
	NextAfterReceiptID string                                `json:"next_after_receipt_id,omitempty"`
	HasMore            bool                                  `json:"has_more"`
}

func BackfillMetadataReceiptProposalBindings(ctx context.Context, store *db.Store, opts MetadataReceiptBindingBackfillOptions) (MetadataReceiptBindingBackfillReport, error) {
	report := MetadataReceiptBindingBackfillReport{DryRun: !opts.Apply}
	if store == nil {
		return report, fmt.Errorf("%w: store is required", ErrInvalidDefinition)
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	rows, err := store.Query(ctx, promotionReceiptSelectSQL()+`
 WHERE receipt_id > ? AND proposal_binding_status = ?
 ORDER BY receipt_id ASC LIMIT ?`, strings.TrimSpace(opts.AfterReceiptID), PromotionReceiptProposalBindingLegacyUnresolved, limit+1)
	if err != nil {
		return report, fmt.Errorf("list legacy ontology promotion receipts: %w", err)
	}
	defer rows.Close()

	receipts := make([]MetadataProposalPromotionReceipt, 0, limit+1)
	for rows.Next() {
		receipt, err := scanPromotionReceipt(rows)
		if err != nil {
			return report, fmt.Errorf("scan legacy ontology promotion receipt: %w", err)
		}
		receipts = append(receipts, receipt)
	}
	if err := rows.Err(); err != nil {
		return report, err
	}
	if len(receipts) > limit {
		report.HasMore = true
		receipts = receipts[:limit]
	}

	proposals := NewPlatformMetadataProposalStore(store)
	for _, receipt := range receipts {
		report.Inspected++
		report.NextAfterReceiptID = receipt.ReceiptID
		proposal, err := proposals.GetMetadataProposalForTenant(ctx, receipt.TenantID, receipt.ProposalID)
		if err != nil {
			report.addUnresolved(receipt.ReceiptID, "tenant-scoped proposal is unavailable")
			continue
		}
		if err := proveLegacyReceiptProposalBinding(receipt, proposal); err != nil {
			report.addUnresolved(receipt.ReceiptID, err.Error())
			continue
		}
		report.Resolved++
		if !opts.Apply {
			continue
		}
		result, err := store.Exec(ctx, `
UPDATE ontology_metadata_promotion_receipts
SET proposal_revision = ?,
	proposal_content_hash = ?,
	proposal_binding_status = ?
WHERE receipt_id = ? AND tenant_id = ? AND proposal_id = ? AND proposal_binding_status = ?`,
			proposal.Revision,
			proposal.ContentHash,
			PromotionReceiptProposalBindingResolved,
			receipt.ReceiptID,
			receipt.TenantID,
			receipt.ProposalID,
			PromotionReceiptProposalBindingLegacyUnresolved,
		)
		if err != nil {
			return report, fmt.Errorf("backfill ontology promotion receipt %s: %w", receipt.ReceiptID, err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return report, err
		}
		if affected > 0 {
			report.Updated++
		}
	}
	return report, nil
}

func (r *MetadataReceiptBindingBackfillReport) addUnresolved(receiptID string, reason string) {
	r.LegacyUnresolved++
	r.Issues = append(r.Issues, MetadataReceiptBindingBackfillIssue{ReceiptID: receiptID, Reason: reason})
}

func proveLegacyReceiptProposalBinding(receipt MetadataProposalPromotionReceipt, proposal OntologyMetadataProposal) error {
	if strings.TrimSpace(receipt.RequestHash) == "" {
		return fmt.Errorf("legacy request hash is unavailable")
	}
	if proposal.Revision <= 0 || proposal.ContentHash == "" || proposal.ContentHash != CanonicalMetadataProposalContentHash(proposal) {
		return fmt.Errorf("canonical proposal revision or content hash is unavailable")
	}
	expectedVersion, ok := legacyReceiptRegistryExpectedVersion(receipt.Changes)
	if !ok {
		return fmt.Errorf("legacy registry expected version is ambiguous")
	}
	req := MetadataProposalPromotionApplyRequest{
		ActorKind:         receipt.ActorKind,
		ConfirmationToken: fmt.Sprintf("apply:%s:%d:%d", strings.TrimSpace(receipt.ProposalID), len(receipt.Changes), len(receipt.Issues)),
	}
	tenantCandidates := []string{strings.TrimSpace(receipt.TenantID)}
	if tenantCandidates[0] != "" {
		tenantCandidates = append(tenantCandidates, "")
	}
	versionCandidates := []int{expectedVersion}
	if expectedVersion != 0 {
		versionCandidates = append(versionCandidates, 0)
	}
	matched := false
	for _, tenantID := range tenantCandidates {
		for _, version := range versionCandidates {
			req.RegistryExpectedVersion = version
			candidate := legacyMetadataPromotionRequestHash(req, proposal, tenantID, receipt.ActorID)
			if candidate == receipt.RequestHash {
				matched = true
			}
		}
	}
	if !matched {
		return fmt.Errorf("legacy request hash does not prove current canonical proposal content")
	}
	return nil
}

func legacyReceiptRegistryExpectedVersion(changes []MetadataProposalPromotionChange) (int, bool) {
	if len(changes) == 0 {
		return 0, false
	}
	expected := changes[0].ExpectedVersion
	for _, change := range changes[1:] {
		if change.ExpectedVersion != expected {
			return 0, false
		}
	}
	return expected, true
}
