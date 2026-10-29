package ontology

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"orrery_v2/internal/platform/db"
)

type MemoryMetadataProposalPromotionReceiptStore struct {
	mu       sync.RWMutex
	receipts map[string]MetadataProposalPromotionReceipt
	now      func() time.Time
}

func NewMemoryMetadataProposalPromotionReceiptStore() *MemoryMetadataProposalPromotionReceiptStore {
	return &MemoryMetadataProposalPromotionReceiptStore{
		receipts: map[string]MetadataProposalPromotionReceipt{},
		now:      time.Now,
	}
}

func (s *MemoryMetadataProposalPromotionReceiptStore) SavePromotionReceipt(ctx context.Context, receipt MetadataProposalPromotionReceipt) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	normalized, err := normalizePromotionReceipt(receipt, s.now)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.receipts[normalized.ReceiptID] = normalized
	return nil
}

func (s *MemoryMetadataProposalPromotionReceiptStore) GetPromotionReceipt(ctx context.Context, receiptID string) (MetadataProposalPromotionReceipt, error) {
	if err := ctx.Err(); err != nil {
		return MetadataProposalPromotionReceipt{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	receipt, ok := s.receipts[strings.TrimSpace(receiptID)]
	if !ok {
		return MetadataProposalPromotionReceipt{}, fmt.Errorf("%w: promotion receipt %s", ErrNotFound, receiptID)
	}
	return clonePromotionReceipt(receipt), nil
}

func (s *MemoryMetadataProposalPromotionReceiptStore) GetPromotionReceiptByIdempotencyKey(ctx context.Context, tenantID string, proposalID string, idempotencyKey string) (MetadataProposalPromotionReceipt, error) {
	if err := ctx.Err(); err != nil {
		return MetadataProposalPromotionReceipt{}, err
	}
	tenantID = strings.TrimSpace(tenantID)
	proposalID = strings.TrimSpace(proposalID)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	s.mu.RLock()
	defer s.mu.RUnlock()
	matched := make([]MetadataProposalPromotionReceipt, 0, 1)
	for _, receipt := range s.receipts {
		if receipt.ProposalID == proposalID && receipt.IdempotencyKey == idempotencyKey && (tenantID == "" || strings.TrimSpace(receipt.TenantID) == tenantID) {
			matched = append(matched, receipt)
		}
	}
	if len(matched) == 0 {
		return MetadataProposalPromotionReceipt{}, fmt.Errorf("%w: promotion receipt for tenant %s proposal %s and idempotency key", ErrNotFound, tenantID, proposalID)
	}
	sort.Slice(matched, func(i, j int) bool {
		if matched[i].CreatedAt.Equal(matched[j].CreatedAt) {
			return matched[i].ReceiptID < matched[j].ReceiptID
		}
		return matched[i].CreatedAt.Before(matched[j].CreatedAt)
	})
	return clonePromotionReceipt(matched[0]), nil
}

func (s *MemoryMetadataProposalPromotionReceiptStore) ListPromotionReceipts(ctx context.Context, query PromotionReceiptQuery) (PromotionReceiptPage, error) {
	if err := ctx.Err(); err != nil {
		return PromotionReceiptPage{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	matched := make([]MetadataProposalPromotionReceipt, 0, len(s.receipts))
	for _, receipt := range s.receipts {
		if promotionReceiptMatchesQuery(receipt, query) {
			matched = append(matched, clonePromotionReceipt(receipt))
		}
	}
	sortPromotionReceipts(matched)
	return promotionReceiptPage(matched, query), nil
}

type PlatformMetadataProposalPromotionReceiptStore struct {
	store *db.Store
	tx    db.Tx
	now   func() time.Time
}

func NewPlatformMetadataProposalPromotionReceiptStore(store *db.Store) *PlatformMetadataProposalPromotionReceiptStore {
	return &PlatformMetadataProposalPromotionReceiptStore{store: store, now: time.Now}
}

func NewPlatformMetadataProposalPromotionReceiptStoreTx(tx db.Tx) *PlatformMetadataProposalPromotionReceiptStore {
	return &PlatformMetadataProposalPromotionReceiptStore{tx: tx, now: time.Now}
}

func (s *PlatformMetadataProposalPromotionReceiptStore) ready() error {
	if s == nil || (s.store == nil && s.tx == nil) {
		return fmt.Errorf("%w: store is required", ErrInvalidDefinition)
	}
	return nil
}

func (s *PlatformMetadataProposalPromotionReceiptStore) exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if s.tx != nil {
		return s.tx.ExecContext(ctx, query, args...)
	}
	return s.store.Exec(ctx, query, args...)
}

func (s *PlatformMetadataProposalPromotionReceiptStore) query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if s.tx != nil {
		return s.tx.QueryContext(ctx, query, args...)
	}
	return s.store.Query(ctx, query, args...)
}

func (s *PlatformMetadataProposalPromotionReceiptStore) queryRow(ctx context.Context, query string, args ...any) rowScanner {
	if s.tx != nil {
		return s.tx.QueryRowContext(ctx, query, args...)
	}
	return s.store.QueryRow(ctx, query, args...)
}

func (s *PlatformMetadataProposalPromotionReceiptStore) SavePromotionReceipt(ctx context.Context, receipt MetadataProposalPromotionReceipt) error {
	if err := s.ready(); err != nil {
		return err
	}
	normalized, err := normalizePromotionReceipt(receipt, s.now)
	if err != nil {
		return err
	}
	_, err = s.exec(ctx, `
INSERT INTO ontology_metadata_promotion_receipts (
	receipt_id, tenant_id, proposal_id, proposal_revision, proposal_content_hash, proposal_binding_status,
	actor_id, actor_kind, idempotency_key, request_hash,
	status, applied, changes_json, issues_json, error_message, applied_at, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(receipt_id) DO NOTHING`,
		normalized.ReceiptID,
		normalized.TenantID,
		normalized.ProposalID,
		normalized.ProposalRevision,
		normalized.ProposalContentHash,
		normalized.ProposalBindingStatus,
		normalized.ActorID,
		normalized.ActorKind,
		normalized.IdempotencyKey,
		normalized.RequestHash,
		normalized.Status,
		normalized.Applied,
		mustJSON(normalized.Changes),
		mustJSON(normalized.Issues),
		normalized.ErrorMessage,
		timeToUnixNano(normalized.AppliedAt),
		timeToUnixNano(normalized.CreatedAt),
		timeToUnixNano(normalized.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("save ontology metadata promotion receipt: %w", err)
	}
	return nil
}

func (s *PlatformMetadataProposalPromotionReceiptStore) GetPromotionReceipt(ctx context.Context, receiptID string) (MetadataProposalPromotionReceipt, error) {
	if err := s.ready(); err != nil {
		return MetadataProposalPromotionReceipt{}, err
	}
	row := s.queryRow(ctx, promotionReceiptSelectSQL()+` WHERE receipt_id = ?`, strings.TrimSpace(receiptID))
	receipt, err := scanPromotionReceipt(row)
	if err == sql.ErrNoRows {
		return MetadataProposalPromotionReceipt{}, fmt.Errorf("%w: promotion receipt %s", ErrNotFound, receiptID)
	}
	return receipt, err
}

func (s *PlatformMetadataProposalPromotionReceiptStore) GetPromotionReceiptByIdempotencyKey(ctx context.Context, tenantID string, proposalID string, idempotencyKey string) (MetadataProposalPromotionReceipt, error) {
	if err := s.ready(); err != nil {
		return MetadataProposalPromotionReceipt{}, err
	}
	tenantID = strings.TrimSpace(tenantID)
	proposalID = strings.TrimSpace(proposalID)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	where := ` WHERE proposal_id = ? AND idempotency_key = ?`
	args := []any{proposalID, idempotencyKey}
	if tenantID != "" {
		where += ` AND tenant_id = ?`
		args = append(args, tenantID)
	}
	row := s.queryRow(ctx, promotionReceiptSelectSQL()+where+` ORDER BY created_at ASC, receipt_id ASC LIMIT 1`, args...)
	receipt, err := scanPromotionReceipt(row)
	if err == sql.ErrNoRows {
		return MetadataProposalPromotionReceipt{}, fmt.Errorf("%w: promotion receipt for tenant %s proposal %s and idempotency key", ErrNotFound, tenantID, proposalID)
	}
	return receipt, err
}

func (s *PlatformMetadataProposalPromotionReceiptStore) ListPromotionReceipts(ctx context.Context, query PromotionReceiptQuery) (PromotionReceiptPage, error) {
	if err := s.ready(); err != nil {
		return PromotionReceiptPage{}, err
	}
	limit := normalizeMetadataProposalLimit(query.Limit)
	offset := normalizeMetadataProposalOffset(query.Offset)
	where, args := promotionReceiptWhere(query)
	var total int
	if err := s.queryRow(ctx, `SELECT COUNT(*) FROM ontology_metadata_promotion_receipts`+where, args...).Scan(&total); err != nil {
		return PromotionReceiptPage{}, fmt.Errorf("count ontology metadata promotion receipts: %w", err)
	}
	selectArgs := append(args, limit, offset)
	rows, err := s.query(ctx, promotionReceiptSelectSQL()+where+` ORDER BY created_at DESC, receipt_id ASC LIMIT ? OFFSET ?`, selectArgs...)
	if err != nil {
		return PromotionReceiptPage{}, fmt.Errorf("list ontology metadata promotion receipts: %w", err)
	}
	defer rows.Close()
	receipts := []MetadataProposalPromotionReceipt{}
	for rows.Next() {
		receipt, err := scanPromotionReceipt(rows)
		if err != nil {
			return PromotionReceiptPage{}, err
		}
		receipts = append(receipts, receipt)
	}
	if err := rows.Err(); err != nil {
		return PromotionReceiptPage{}, err
	}
	return PromotionReceiptPage{
		Receipts: receipts,
		Page: Page{
			Limit:    limit,
			Offset:   offset,
			Total:    total,
			Returned: len(receipts),
			HasMore:  offset+len(receipts) < total,
		},
	}, nil
}

func normalizePromotionReceipt(receipt MetadataProposalPromotionReceipt, now func() time.Time) (MetadataProposalPromotionReceipt, error) {
	receipt.ReceiptID = strings.TrimSpace(receipt.ReceiptID)
	receipt.TenantID = strings.TrimSpace(receipt.TenantID)
	receipt.ProposalID = strings.TrimSpace(receipt.ProposalID)
	receipt.ProposalContentHash = strings.TrimSpace(receipt.ProposalContentHash)
	receipt.ActorID = strings.TrimSpace(receipt.ActorID)
	receipt.ActorKind = strings.TrimSpace(receipt.ActorKind)
	receipt.IdempotencyKey = strings.TrimSpace(receipt.IdempotencyKey)
	receipt.RequestHash = strings.TrimSpace(receipt.RequestHash)
	receipt.ErrorMessage = strings.TrimSpace(receipt.ErrorMessage)
	if receipt.ProposalBindingStatus == "" {
		if receipt.ProposalRevision > 0 && receipt.ProposalContentHash != "" {
			receipt.ProposalBindingStatus = PromotionReceiptProposalBindingResolved
		} else {
			receipt.ProposalBindingStatus = PromotionReceiptProposalBindingLegacyUnresolved
		}
	}
	switch receipt.ProposalBindingStatus {
	case PromotionReceiptProposalBindingResolved:
		if receipt.ProposalRevision <= 0 || receipt.ProposalContentHash == "" {
			return MetadataProposalPromotionReceipt{}, fmt.Errorf("%w: resolved promotion receipt proposal revision and content hash are required", ErrInvalidDefinition)
		}
	case PromotionReceiptProposalBindingLegacyUnresolved:
	default:
		return MetadataProposalPromotionReceipt{}, fmt.Errorf("%w: unsupported promotion receipt proposal binding status %s", ErrInvalidDefinition, receipt.ProposalBindingStatus)
	}
	if receipt.Status == "" {
		if receipt.Applied {
			receipt.Status = PromotionReceiptStatusApplied
		} else {
			receipt.Status = PromotionReceiptStatusNoop
		}
	}
	switch receipt.Status {
	case PromotionReceiptStatusApplied, PromotionReceiptStatusNoop, PromotionReceiptStatusBlocked, PromotionReceiptStatusFailed:
	default:
		return MetadataProposalPromotionReceipt{}, fmt.Errorf("%w: unsupported promotion receipt status %s", ErrInvalidDefinition, receipt.Status)
	}
	if receipt.Status == PromotionReceiptStatusApplied {
		receipt.Applied = true
	}
	if receipt.Status == PromotionReceiptStatusNoop || receipt.Status == PromotionReceiptStatusBlocked || receipt.Status == PromotionReceiptStatusFailed {
		receipt.Applied = false
	}
	if receipt.ReceiptID == "" || receipt.ProposalID == "" || receipt.ActorID == "" || receipt.IdempotencyKey == "" {
		return MetadataProposalPromotionReceipt{}, fmt.Errorf("%w: promotion receipt id, proposal_id, actor_id, and idempotency_key are required", ErrInvalidDefinition)
	}
	if now == nil {
		now = time.Now
	}
	at := now().UTC()
	if receipt.AppliedAt.IsZero() {
		receipt.AppliedAt = at
	}
	if receipt.CreatedAt.IsZero() {
		receipt.CreatedAt = receipt.AppliedAt
	}
	if receipt.UpdatedAt.IsZero() {
		receipt.UpdatedAt = receipt.AppliedAt
	}
	receipt.Changes = append([]MetadataProposalPromotionChange(nil), receipt.Changes...)
	receipt.Issues = append([]MetadataProposalPromotionIssue(nil), receipt.Issues...)
	return receipt, nil
}

func clonePromotionReceipt(src MetadataProposalPromotionReceipt) MetadataProposalPromotionReceipt {
	src.Changes = append([]MetadataProposalPromotionChange(nil), src.Changes...)
	src.Issues = append([]MetadataProposalPromotionIssue(nil), src.Issues...)
	return src
}

func sortPromotionReceipts(receipts []MetadataProposalPromotionReceipt) {
	sort.SliceStable(receipts, func(i, j int) bool {
		if receipts[i].CreatedAt.Equal(receipts[j].CreatedAt) {
			return receipts[i].ReceiptID < receipts[j].ReceiptID
		}
		return receipts[i].CreatedAt.After(receipts[j].CreatedAt)
	})
}

func promotionReceiptPage(receipts []MetadataProposalPromotionReceipt, query PromotionReceiptQuery) PromotionReceiptPage {
	limit := normalizeMetadataProposalLimit(query.Limit)
	offset := normalizeMetadataProposalOffset(query.Offset)
	total := len(receipts)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	items := make([]MetadataProposalPromotionReceipt, 0, end-offset)
	for _, receipt := range receipts[offset:end] {
		items = append(items, clonePromotionReceipt(receipt))
	}
	return PromotionReceiptPage{
		Receipts: items,
		Page: Page{
			Limit:    limit,
			Offset:   offset,
			Total:    total,
			Returned: len(items),
			HasMore:  end < total,
		},
	}
}

func promotionReceiptMatchesQuery(receipt MetadataProposalPromotionReceipt, query PromotionReceiptQuery) bool {
	if strings.TrimSpace(query.TenantID) != "" && receipt.TenantID != strings.TrimSpace(query.TenantID) {
		return false
	}
	if strings.TrimSpace(query.ProposalID) != "" && receipt.ProposalID != strings.TrimSpace(query.ProposalID) {
		return false
	}
	if strings.TrimSpace(query.ActorID) != "" && receipt.ActorID != strings.TrimSpace(query.ActorID) {
		return false
	}
	if strings.TrimSpace(string(query.Status)) != "" && string(receipt.Status) != strings.TrimSpace(string(query.Status)) {
		return false
	}
	if strings.TrimSpace(query.IdempotencyKey) != "" && receipt.IdempotencyKey != strings.TrimSpace(query.IdempotencyKey) {
		return false
	}
	return true
}

func promotionReceiptWhere(query PromotionReceiptQuery) (string, []any) {
	clauses := []string{}
	args := []any{}
	if strings.TrimSpace(query.TenantID) != "" {
		clauses = append(clauses, "tenant_id = ?")
		args = append(args, strings.TrimSpace(query.TenantID))
	}
	if strings.TrimSpace(query.ProposalID) != "" {
		clauses = append(clauses, "proposal_id = ?")
		args = append(args, strings.TrimSpace(query.ProposalID))
	}
	if strings.TrimSpace(query.ActorID) != "" {
		clauses = append(clauses, "actor_id = ?")
		args = append(args, strings.TrimSpace(query.ActorID))
	}
	if strings.TrimSpace(string(query.Status)) != "" {
		clauses = append(clauses, "status = ?")
		args = append(args, strings.TrimSpace(string(query.Status)))
	}
	if strings.TrimSpace(query.IdempotencyKey) != "" {
		clauses = append(clauses, "idempotency_key = ?")
		args = append(args, strings.TrimSpace(query.IdempotencyKey))
	}
	if len(clauses) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

func promotionReceiptSelectSQL() string {
	return `SELECT receipt_id, tenant_id, proposal_id, proposal_revision, proposal_content_hash, proposal_binding_status,
actor_id, actor_kind, idempotency_key, request_hash,
status, applied, changes_json, issues_json, error_message, applied_at, created_at, updated_at
FROM ontology_metadata_promotion_receipts`
}

func scanPromotionReceipt(row rowScanner) (MetadataProposalPromotionReceipt, error) {
	var receipt MetadataProposalPromotionReceipt
	var changesJSON, issuesJSON string
	var appliedAt, createdAt, updatedAt int64
	if err := row.Scan(&receipt.ReceiptID, &receipt.TenantID, &receipt.ProposalID, &receipt.ProposalRevision, &receipt.ProposalContentHash, &receipt.ProposalBindingStatus, &receipt.ActorID, &receipt.ActorKind, &receipt.IdempotencyKey, &receipt.RequestHash, &receipt.Status, &receipt.Applied, &changesJSON, &issuesJSON, &receipt.ErrorMessage, &appliedAt, &createdAt, &updatedAt); err != nil {
		return MetadataProposalPromotionReceipt{}, err
	}
	mustDecodeJSON(changesJSON, &receipt.Changes)
	mustDecodeJSON(issuesJSON, &receipt.Issues)
	receipt.AppliedAt = unixNanoToTime(appliedAt)
	receipt.CreatedAt = unixNanoToTime(createdAt)
	receipt.UpdatedAt = unixNanoToTime(updatedAt)
	return receipt, nil
}
