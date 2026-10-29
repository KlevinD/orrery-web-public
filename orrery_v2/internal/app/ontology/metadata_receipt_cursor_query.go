package ontology

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
)

type PromotionReceiptSeek struct {
	UpdatedAt time.Time
	ReceiptID string
}

type PromotionReceiptCursorQuery struct {
	TenantID            string
	ProposalID          string
	Status              PromotionReceiptStatus
	SuccessfulOnly      bool
	CurrentProposalOnly bool
	ProposalRevision    int64
	ProposalContentHash string
	Limit               int
	After               PromotionReceiptSeek
}

type PromotionReceiptCursorPage struct {
	Receipts []MetadataProposalPromotionReceipt
	HasMore  bool
}

type MetadataProposalVersionRef struct {
	ProposalID  string
	Revision    int64
	ContentHash string
}

type CurrentPromotionReceiptBatchQuery struct {
	TenantID  string
	Proposals []MetadataProposalVersionRef
	Limit     int
}

func (s *MemoryMetadataProposalPromotionReceiptStore) GetPromotionReceiptForTenant(ctx context.Context, tenantID, receiptID string) (MetadataProposalPromotionReceipt, error) {
	if err := ctx.Err(); err != nil {
		return MetadataProposalPromotionReceipt{}, err
	}
	tenantID = strings.TrimSpace(tenantID)
	receiptID = strings.TrimSpace(receiptID)
	if tenantID == "" || receiptID == "" || len(tenantID) > 256 || len(receiptID) > 256 {
		return MetadataProposalPromotionReceipt{}, fmt.Errorf("%w: tenant and receipt id are required", ErrInvalidDefinition)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	receipt, ok := s.receipts[receiptID]
	if !ok || receipt.TenantID != tenantID {
		return MetadataProposalPromotionReceipt{}, fmt.Errorf("%w: promotion receipt %s", ErrNotFound, receiptID)
	}
	return clonePromotionReceipt(receipt), nil
}

func (s *PlatformMetadataProposalPromotionReceiptStore) GetPromotionReceiptForTenant(ctx context.Context, tenantID, receiptID string) (MetadataProposalPromotionReceipt, error) {
	if err := s.ready(); err != nil {
		return MetadataProposalPromotionReceipt{}, err
	}
	tenantID = strings.TrimSpace(tenantID)
	receiptID = strings.TrimSpace(receiptID)
	if tenantID == "" || receiptID == "" || len(tenantID) > 256 || len(receiptID) > 256 {
		return MetadataProposalPromotionReceipt{}, fmt.Errorf("%w: tenant and receipt id are required", ErrInvalidDefinition)
	}
	receipt, err := scanPromotionReceipt(s.queryRow(ctx, promotionReceiptSelectSQL()+` WHERE tenant_id = ? AND receipt_id = ?`, tenantID, receiptID))
	if err == sql.ErrNoRows {
		return MetadataProposalPromotionReceipt{}, fmt.Errorf("%w: promotion receipt %s", ErrNotFound, receiptID)
	}
	return receipt, err
}

func (s *MemoryMetadataProposalPromotionReceiptStore) ListPromotionReceiptCursorPage(ctx context.Context, query PromotionReceiptCursorQuery) (PromotionReceiptCursorPage, error) {
	query, err := normalizePromotionReceiptCursorQuery(query)
	if err != nil {
		return PromotionReceiptCursorPage{}, err
	}
	if err := ctx.Err(); err != nil {
		return PromotionReceiptCursorPage{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	matched := make([]MetadataProposalPromotionReceipt, 0, len(s.receipts))
	for _, receipt := range s.receipts {
		if !promotionReceiptCursorMatches(receipt, query) || !promotionReceiptAfterSeek(receipt, query.After) {
			continue
		}
		matched = append(matched, clonePromotionReceipt(receipt))
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
	return PromotionReceiptCursorPage{Receipts: matched, HasMore: hasMore}, nil
}

func (s *PlatformMetadataProposalPromotionReceiptStore) ListPromotionReceiptCursorPage(ctx context.Context, query PromotionReceiptCursorQuery) (PromotionReceiptCursorPage, error) {
	if err := s.ready(); err != nil {
		return PromotionReceiptCursorPage{}, err
	}
	query, err := normalizePromotionReceiptCursorQuery(query)
	if err != nil {
		return PromotionReceiptCursorPage{}, err
	}
	where, args := promotionReceiptCursorWhere(query)
	args = append(args, query.Limit+1)
	rows, err := s.query(ctx, promotionReceiptSelectSQL()+where+` ORDER BY updated_at DESC, receipt_id ASC LIMIT ?`, args...)
	if err != nil {
		return PromotionReceiptCursorPage{}, fmt.Errorf("list ontology promotion receipt cursor page: %w", err)
	}
	defer rows.Close()
	receipts := make([]MetadataProposalPromotionReceipt, 0, query.Limit+1)
	for rows.Next() {
		receipt, err := scanPromotionReceipt(rows)
		if err != nil {
			return PromotionReceiptCursorPage{}, err
		}
		receipts = append(receipts, receipt)
	}
	if err := rows.Err(); err != nil {
		return PromotionReceiptCursorPage{}, err
	}
	hasMore := len(receipts) > query.Limit
	if hasMore {
		receipts = receipts[:query.Limit]
	}
	return PromotionReceiptCursorPage{Receipts: receipts, HasMore: hasMore}, nil
}

func (s *MemoryMetadataProposalPromotionReceiptStore) ListCurrentSuccessfulPromotionReceipts(ctx context.Context, query CurrentPromotionReceiptBatchQuery) (PromotionReceiptCursorPage, error) {
	query, err := normalizeCurrentPromotionReceiptBatchQuery(query)
	if err != nil {
		return PromotionReceiptCursorPage{}, err
	}
	if err := ctx.Err(); err != nil {
		return PromotionReceiptCursorPage{}, err
	}
	refs := metadataProposalVersionRefSet(query.Proposals)
	s.mu.RLock()
	defer s.mu.RUnlock()
	receipts := make([]MetadataProposalPromotionReceipt, 0)
	for _, receipt := range s.receipts {
		ref, ok := refs[receipt.ProposalID]
		if !ok || receipt.TenantID != query.TenantID || !receiptMatchesCurrentSuccessfulProposal(receipt, ref) {
			continue
		}
		receipts = append(receipts, clonePromotionReceipt(receipt))
	}
	sortCurrentPromotionReceipts(receipts)
	hasMore := len(receipts) > query.Limit
	if hasMore {
		receipts = receipts[:query.Limit]
	}
	return PromotionReceiptCursorPage{Receipts: receipts, HasMore: hasMore}, nil
}

func (s *PlatformMetadataProposalPromotionReceiptStore) ListCurrentSuccessfulPromotionReceipts(ctx context.Context, query CurrentPromotionReceiptBatchQuery) (PromotionReceiptCursorPage, error) {
	if err := s.ready(); err != nil {
		return PromotionReceiptCursorPage{}, err
	}
	query, err := normalizeCurrentPromotionReceiptBatchQuery(query)
	if err != nil {
		return PromotionReceiptCursorPage{}, err
	}
	clauses := make([]string, 0, len(query.Proposals))
	args := []any{query.TenantID, PromotionReceiptProposalBindingResolved, PromotionReceiptStatusApplied, PromotionReceiptStatusNoop}
	for _, ref := range query.Proposals {
		clauses = append(clauses, "(proposal_id = ? AND proposal_revision = ? AND proposal_content_hash = ?)")
		args = append(args, ref.ProposalID, ref.Revision, ref.ContentHash)
	}
	args = append(args, query.Limit+1)
	rows, err := s.query(ctx, promotionReceiptSelectSQL()+`
 WHERE tenant_id = ? AND proposal_binding_status = ? AND status IN (?, ?) AND (`+strings.Join(clauses, " OR ")+`)
 ORDER BY proposal_id ASC, updated_at DESC, receipt_id ASC LIMIT ?`, args...)
	if err != nil {
		return PromotionReceiptCursorPage{}, fmt.Errorf("list current successful ontology promotion receipts: %w", err)
	}
	defer rows.Close()
	receipts := make([]MetadataProposalPromotionReceipt, 0, query.Limit+1)
	for rows.Next() {
		receipt, err := scanPromotionReceipt(rows)
		if err != nil {
			return PromotionReceiptCursorPage{}, err
		}
		receipts = append(receipts, receipt)
	}
	if err := rows.Err(); err != nil {
		return PromotionReceiptCursorPage{}, err
	}
	hasMore := len(receipts) > query.Limit
	if hasMore {
		receipts = receipts[:query.Limit]
	}
	return PromotionReceiptCursorPage{Receipts: receipts, HasMore: hasMore}, nil
}

func normalizePromotionReceiptCursorQuery(query PromotionReceiptCursorQuery) (PromotionReceiptCursorQuery, error) {
	query.TenantID = strings.TrimSpace(query.TenantID)
	query.ProposalID = strings.TrimSpace(query.ProposalID)
	query.ProposalContentHash = strings.TrimSpace(query.ProposalContentHash)
	query.After.ReceiptID = strings.TrimSpace(query.After.ReceiptID)
	if query.TenantID == "" || len(query.TenantID) > 256 || len(query.ProposalID) > 256 || len(query.ProposalContentHash) > 128 || len(query.After.ReceiptID) > 256 || query.Limit <= 0 || query.Limit > metadataGovernanceQueryMaxLimit {
		return PromotionReceiptCursorQuery{}, fmt.Errorf("%w: valid tenant and limit are required", ErrInvalidDefinition)
	}
	if query.Status != "" {
		switch query.Status {
		case PromotionReceiptStatusApplied, PromotionReceiptStatusNoop, PromotionReceiptStatusBlocked, PromotionReceiptStatusFailed:
		default:
			return PromotionReceiptCursorQuery{}, fmt.Errorf("%w: unsupported promotion receipt status", ErrInvalidDefinition)
		}
	}
	if query.SuccessfulOnly && query.Status != "" && query.Status != PromotionReceiptStatusApplied && query.Status != PromotionReceiptStatusNoop {
		return PromotionReceiptCursorQuery{}, fmt.Errorf("%w: successful receipt query has incompatible status", ErrInvalidDefinition)
	}
	if query.CurrentProposalOnly && (query.ProposalID == "" || query.ProposalRevision <= 0 || query.ProposalContentHash == "") {
		return PromotionReceiptCursorQuery{}, fmt.Errorf("%w: current proposal receipt query requires proposal id, revision and content hash", ErrInvalidDefinition)
	}
	if !query.CurrentProposalOnly && (query.ProposalRevision != 0 || query.ProposalContentHash != "") {
		return PromotionReceiptCursorQuery{}, fmt.Errorf("%w: proposal revision filters require current proposal mode", ErrInvalidDefinition)
	}
	if query.After.UpdatedAt.IsZero() != (query.After.ReceiptID == "") {
		return PromotionReceiptCursorQuery{}, fmt.Errorf("%w: receipt seek requires updated_at and receipt_id", ErrInvalidDefinition)
	}
	query.After.UpdatedAt = query.After.UpdatedAt.UTC()
	return query, nil
}

func normalizeCurrentPromotionReceiptBatchQuery(query CurrentPromotionReceiptBatchQuery) (CurrentPromotionReceiptBatchQuery, error) {
	query.TenantID = strings.TrimSpace(query.TenantID)
	if query.TenantID == "" || len(query.TenantID) > 256 || len(query.Proposals) == 0 || len(query.Proposals) > 100 || query.Limit <= 0 || query.Limit > 500 {
		return CurrentPromotionReceiptBatchQuery{}, fmt.Errorf("%w: valid tenant, proposal refs and limit are required", ErrInvalidDefinition)
	}
	query.Proposals = append([]MetadataProposalVersionRef(nil), query.Proposals...)
	seen := map[string]struct{}{}
	for index := range query.Proposals {
		query.Proposals[index].ProposalID = strings.TrimSpace(query.Proposals[index].ProposalID)
		query.Proposals[index].ContentHash = strings.TrimSpace(query.Proposals[index].ContentHash)
		ref := query.Proposals[index]
		if ref.ProposalID == "" || len(ref.ProposalID) > 256 || ref.Revision <= 0 || ref.ContentHash == "" || len(ref.ContentHash) > 128 {
			return CurrentPromotionReceiptBatchQuery{}, fmt.Errorf("%w: current proposal ref is invalid", ErrInvalidDefinition)
		}
		if _, duplicate := seen[ref.ProposalID]; duplicate {
			return CurrentPromotionReceiptBatchQuery{}, fmt.Errorf("%w: duplicate current proposal ref", ErrInvalidDefinition)
		}
		seen[ref.ProposalID] = struct{}{}
	}
	sort.Slice(query.Proposals, func(i, j int) bool { return query.Proposals[i].ProposalID < query.Proposals[j].ProposalID })
	return query, nil
}

func promotionReceiptCursorWhere(query PromotionReceiptCursorQuery) (string, []any) {
	clauses := []string{"tenant_id = ?"}
	args := []any{query.TenantID}
	if query.ProposalID != "" {
		clauses = append(clauses, "proposal_id = ?")
		args = append(args, query.ProposalID)
	}
	if query.Status != "" {
		clauses = append(clauses, "status = ?")
		args = append(args, query.Status)
	} else if query.SuccessfulOnly {
		clauses = append(clauses, "status IN (?, ?)")
		args = append(args, PromotionReceiptStatusApplied, PromotionReceiptStatusNoop)
	}
	if query.CurrentProposalOnly {
		clauses = append(clauses, "proposal_revision = ?", "proposal_content_hash = ?", "proposal_binding_status = ?")
		args = append(args, query.ProposalRevision, query.ProposalContentHash, PromotionReceiptProposalBindingResolved)
	}
	if query.After.ReceiptID != "" {
		updatedAt := timeToUnixNano(query.After.UpdatedAt)
		clauses = append(clauses, "(updated_at < ? OR (updated_at = ? AND receipt_id > ?))")
		args = append(args, updatedAt, updatedAt, query.After.ReceiptID)
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

func promotionReceiptCursorMatches(receipt MetadataProposalPromotionReceipt, query PromotionReceiptCursorQuery) bool {
	if receipt.TenantID != query.TenantID || query.ProposalID != "" && receipt.ProposalID != query.ProposalID || query.Status != "" && receipt.Status != query.Status {
		return false
	}
	if query.SuccessfulOnly && receipt.Status != PromotionReceiptStatusApplied && receipt.Status != PromotionReceiptStatusNoop {
		return false
	}
	if query.CurrentProposalOnly && (receipt.ProposalRevision != query.ProposalRevision || receipt.ProposalContentHash != query.ProposalContentHash || receipt.ProposalBindingStatus != PromotionReceiptProposalBindingResolved) {
		return false
	}
	return true
}

func promotionReceiptAfterSeek(receipt MetadataProposalPromotionReceipt, after PromotionReceiptSeek) bool {
	if after.ReceiptID == "" {
		return true
	}
	return receipt.UpdatedAt.Before(after.UpdatedAt) || receipt.UpdatedAt.Equal(after.UpdatedAt) && receipt.ReceiptID > after.ReceiptID
}

func metadataProposalVersionRefSet(refs []MetadataProposalVersionRef) map[string]MetadataProposalVersionRef {
	out := make(map[string]MetadataProposalVersionRef, len(refs))
	for _, ref := range refs {
		out[ref.ProposalID] = ref
	}
	return out
}

func receiptMatchesCurrentSuccessfulProposal(receipt MetadataProposalPromotionReceipt, ref MetadataProposalVersionRef) bool {
	return receipt.ProposalRevision == ref.Revision &&
		receipt.ProposalContentHash == ref.ContentHash &&
		receipt.ProposalBindingStatus == PromotionReceiptProposalBindingResolved &&
		(receipt.Status == PromotionReceiptStatusApplied || receipt.Status == PromotionReceiptStatusNoop)
}

func sortCurrentPromotionReceipts(receipts []MetadataProposalPromotionReceipt) {
	sort.SliceStable(receipts, func(i, j int) bool {
		if receipts[i].ProposalID != receipts[j].ProposalID {
			return receipts[i].ProposalID < receipts[j].ProposalID
		}
		if receipts[i].UpdatedAt.Equal(receipts[j].UpdatedAt) {
			return receipts[i].ReceiptID < receipts[j].ReceiptID
		}
		return receipts[i].UpdatedAt.After(receipts[j].UpdatedAt)
	})
}

func IsMetadataProposalActionable(proposal OntologyMetadataProposal, visibleSuccessfulReceipts []MetadataProposalPromotionReceipt) bool {
	switch proposal.Status {
	case MetadataProposalStatusProposed:
		return true
	case MetadataProposalStatusAccepted:
		for _, receipt := range visibleSuccessfulReceipts {
			if receipt.TenantID == proposal.TenantID &&
				receipt.ProposalID == proposal.ID &&
				receipt.ProposalRevision == proposal.Revision &&
				receipt.ProposalContentHash == proposal.ContentHash &&
				receipt.ProposalBindingStatus == PromotionReceiptProposalBindingResolved &&
				(receipt.Status == PromotionReceiptStatusApplied || receipt.Status == PromotionReceiptStatusNoop) {
				return false
			}
		}
		return true
	default:
		return false
	}
}
