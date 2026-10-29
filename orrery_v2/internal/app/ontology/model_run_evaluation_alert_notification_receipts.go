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

type ModelRunEvaluationTrendAlertNotificationDeliveryStatus string

const (
	ModelRunEvaluationTrendAlertNotificationDeliveryDelivered ModelRunEvaluationTrendAlertNotificationDeliveryStatus = "delivered"
	ModelRunEvaluationTrendAlertNotificationDeliveryFailed    ModelRunEvaluationTrendAlertNotificationDeliveryStatus = "failed"
)

type ModelRunEvaluationTrendAlertNotificationDeliveryReceipt struct {
	ID           string                                                 `json:"id"`
	TenantID     string                                                 `json:"tenant_id,omitempty"`
	RuleID       string                                                 `json:"rule_id"`
	Status       ModelRunEvaluationTrendAlertNotificationDeliveryStatus `json:"status"`
	AlertStatus  ModelRunEvaluationTrendAlertStatus                     `json:"alert_status"`
	Reason       ModelRunEvaluationTrendAlertNotificationReason         `json:"reason,omitempty"`
	Channel      string                                                 `json:"channel,omitempty"`
	MessageID    string                                                 `json:"message_id,omitempty"`
	Attempt      int                                                    `json:"attempt,omitempty"`
	Retryable    bool                                                   `json:"retryable,omitempty"`
	NextRetryAt  time.Time                                              `json:"next_retry_at,omitempty"`
	Labels       map[string]string                                      `json:"labels,omitempty"`
	Metadata     map[string]string                                      `json:"metadata,omitempty"`
	ErrorMessage string                                                 `json:"error_message,omitempty"`
	SentAt       time.Time                                              `json:"sent_at,omitempty"`
	CreatedAt    time.Time                                              `json:"created_at,omitempty"`
	UpdatedAt    time.Time                                              `json:"updated_at,omitempty"`
}

type ModelRunEvaluationTrendAlertNotificationReceiptQuery struct {
	TenantID       string
	RuleID         string
	Status         ModelRunEvaluationTrendAlertNotificationDeliveryStatus
	Channel        string
	Retryable      bool
	RetryableSet   bool
	NextRetryDueAt time.Time
	Limit          int
	Offset         int
}

type ModelRunEvaluationTrendAlertNotificationReceiptPage struct {
	Receipts []ModelRunEvaluationTrendAlertNotificationDeliveryReceipt `json:"receipts"`
	Page     Page                                                      `json:"page"`
}

type ModelRunEvaluationTrendAlertNotificationReceiptSummary struct {
	TenantID  string                                                 `json:"tenant_id"`
	RuleID    string                                                 `json:"rule_id"`
	Status    ModelRunEvaluationTrendAlertNotificationDeliveryStatus `json:"status"`
	Channel   string                                                 `json:"channel"`
	Retryable bool                                                   `json:"retryable"`
	Count     int                                                    `json:"count"`
}

type ModelRunEvaluationTrendAlertNotificationReceiptSummaryPage struct {
	Summaries []ModelRunEvaluationTrendAlertNotificationReceiptSummary `json:"summaries"`
	Page      Page                                                     `json:"page"`
}

type ModelRunEvaluationTrendAlertNotificationReceiptStore interface {
	SaveModelRunEvaluationTrendAlertNotificationReceipt(ctx context.Context, receipt ModelRunEvaluationTrendAlertNotificationDeliveryReceipt) error
	ListModelRunEvaluationTrendAlertNotificationReceipts(ctx context.Context, query ModelRunEvaluationTrendAlertNotificationReceiptQuery) (ModelRunEvaluationTrendAlertNotificationReceiptPage, error)
	SummarizeModelRunEvaluationTrendAlertNotificationReceipts(ctx context.Context, query ModelRunEvaluationTrendAlertNotificationReceiptQuery) (ModelRunEvaluationTrendAlertNotificationReceiptSummaryPage, error)
	MarkModelRunEvaluationTrendAlertNotificationReceiptRetryConsumed(ctx context.Context, receiptID string, updatedAt time.Time) error
	MarkModelRunEvaluationTrendAlertNotificationReceiptRetryAbandoned(ctx context.Context, receiptID string, reason string, updatedAt time.Time) error
}

type MemoryModelRunEvaluationTrendAlertNotificationReceiptStore struct {
	mu       sync.RWMutex
	receipts map[string]ModelRunEvaluationTrendAlertNotificationDeliveryReceipt
	now      func() time.Time
}

func NewMemoryModelRunEvaluationTrendAlertNotificationReceiptStore() *MemoryModelRunEvaluationTrendAlertNotificationReceiptStore {
	return &MemoryModelRunEvaluationTrendAlertNotificationReceiptStore{
		receipts: map[string]ModelRunEvaluationTrendAlertNotificationDeliveryReceipt{},
		now:      time.Now,
	}
}

func (s *MemoryModelRunEvaluationTrendAlertNotificationReceiptStore) SaveModelRunEvaluationTrendAlertNotificationReceipt(ctx context.Context, receipt ModelRunEvaluationTrendAlertNotificationDeliveryReceipt) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	normalized, err := normalizeModelRunEvaluationTrendAlertNotificationDeliveryReceipt(receipt, s.now)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.receipts[normalized.ID] = normalized
	return nil
}

func (s *MemoryModelRunEvaluationTrendAlertNotificationReceiptStore) ListModelRunEvaluationTrendAlertNotificationReceipts(ctx context.Context, query ModelRunEvaluationTrendAlertNotificationReceiptQuery) (ModelRunEvaluationTrendAlertNotificationReceiptPage, error) {
	if err := ctx.Err(); err != nil {
		return ModelRunEvaluationTrendAlertNotificationReceiptPage{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	matched := make([]ModelRunEvaluationTrendAlertNotificationDeliveryReceipt, 0, len(s.receipts))
	for _, receipt := range s.receipts {
		if modelRunEvaluationTrendAlertNotificationReceiptMatchesQuery(receipt, query) {
			matched = append(matched, cloneModelRunEvaluationTrendAlertNotificationDeliveryReceipt(receipt))
		}
	}
	sortModelRunEvaluationTrendAlertNotificationReceipts(matched)
	return modelRunEvaluationTrendAlertNotificationReceiptPage(matched, query), nil
}

func (s *MemoryModelRunEvaluationTrendAlertNotificationReceiptStore) SummarizeModelRunEvaluationTrendAlertNotificationReceipts(ctx context.Context, query ModelRunEvaluationTrendAlertNotificationReceiptQuery) (ModelRunEvaluationTrendAlertNotificationReceiptSummaryPage, error) {
	if err := ctx.Err(); err != nil {
		return ModelRunEvaluationTrendAlertNotificationReceiptSummaryPage{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	summariesByKey := map[modelRunEvaluationTrendAlertNotificationReceiptSummaryKey]ModelRunEvaluationTrendAlertNotificationReceiptSummary{}
	for _, receipt := range s.receipts {
		if !modelRunEvaluationTrendAlertNotificationReceiptMatchesQuery(receipt, query) {
			continue
		}
		key := modelRunEvaluationTrendAlertNotificationReceiptSummaryKey{
			tenantID:  receipt.TenantID,
			ruleID:    receipt.RuleID,
			channel:   receipt.Channel,
			status:    receipt.Status,
			retryable: receipt.Retryable,
		}
		summary := summariesByKey[key]
		if summary.Count == 0 {
			summary = ModelRunEvaluationTrendAlertNotificationReceiptSummary{
				TenantID:  receipt.TenantID,
				RuleID:    receipt.RuleID,
				Channel:   receipt.Channel,
				Status:    receipt.Status,
				Retryable: receipt.Retryable,
			}
		}
		summary.Count++
		summariesByKey[key] = summary
	}
	summaries := make([]ModelRunEvaluationTrendAlertNotificationReceiptSummary, 0, len(summariesByKey))
	for _, summary := range summariesByKey {
		summaries = append(summaries, summary)
	}
	sortModelRunEvaluationTrendAlertNotificationReceiptSummaries(summaries)
	return modelRunEvaluationTrendAlertNotificationReceiptSummaryPage(summaries, query), nil
}

func (s *MemoryModelRunEvaluationTrendAlertNotificationReceiptStore) MarkModelRunEvaluationTrendAlertNotificationReceiptRetryAbandoned(ctx context.Context, receiptID string, reason string, updatedAt time.Time) error {
	return s.markModelRunEvaluationTrendAlertNotificationReceiptRetryClosed(ctx, receiptID, strings.TrimSpace(reason), true, updatedAt)
}

func (s *MemoryModelRunEvaluationTrendAlertNotificationReceiptStore) MarkModelRunEvaluationTrendAlertNotificationReceiptRetryConsumed(ctx context.Context, receiptID string, updatedAt time.Time) error {
	return s.markModelRunEvaluationTrendAlertNotificationReceiptRetryClosed(ctx, receiptID, "", false, updatedAt)
}

func (s *MemoryModelRunEvaluationTrendAlertNotificationReceiptStore) markModelRunEvaluationTrendAlertNotificationReceiptRetryClosed(ctx context.Context, receiptID string, reason string, overwriteError bool, updatedAt time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	receiptID = strings.TrimSpace(receiptID)
	if receiptID == "" {
		return fmt.Errorf("%w: model run evaluation trend alert notification receipt id is required", ErrInvalidDefinition)
	}
	if updatedAt.IsZero() {
		updatedAt = s.now().UTC()
	} else {
		updatedAt = updatedAt.UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	receipt, ok := s.receipts[receiptID]
	if !ok {
		return fmt.Errorf("%w: model run evaluation trend alert notification receipt %s", ErrNotFound, receiptID)
	}
	receipt.Retryable = false
	receipt.NextRetryAt = time.Time{}
	if overwriteError {
		receipt.ErrorMessage = strings.TrimSpace(reason)
	}
	receipt.UpdatedAt = updatedAt
	s.receipts[receiptID] = receipt
	return nil
}

type PlatformModelRunEvaluationTrendAlertNotificationReceiptStore struct {
	store *db.Store
	tx    db.Tx
	now   func() time.Time
}

func modelRunEvaluationTrendAlertNotificationDeliveryReceiptFromSuccess(event ModelRunEvaluationTrendAlertAuditEvent, plan ModelRunEvaluationTrendAlertNotificationPlan, receipt ModelRunEvaluationTrendAlertNotificationReceipt, sentAt time.Time) ModelRunEvaluationTrendAlertNotificationDeliveryReceipt {
	receipt = normalizeModelRunEvaluationTrendAlertNotificationReceipt(receipt)
	if sentAt.IsZero() {
		sentAt = receipt.SentAt
	}
	if sentAt.IsZero() {
		sentAt = event.ObservedAt
	}
	return ModelRunEvaluationTrendAlertNotificationDeliveryReceipt{
		TenantID:    event.TenantID,
		RuleID:      event.RuleID,
		Status:      ModelRunEvaluationTrendAlertNotificationDeliveryDelivered,
		AlertStatus: event.Status,
		Reason:      plan.Decision.Reason,
		Channel:     receipt.Channel,
		MessageID:   receipt.MessageID,
		Attempt:     1,
		Labels:      cloneStringMetadata(event.Labels),
		Metadata:    cloneStringMetadata(receipt.Metadata),
		SentAt:      sentAt,
	}
}

func modelRunEvaluationTrendAlertNotificationDeliveryReceiptFromFailure(event ModelRunEvaluationTrendAlertAuditEvent, plan ModelRunEvaluationTrendAlertNotificationPlan, failedAt time.Time, cause error) ModelRunEvaluationTrendAlertNotificationDeliveryReceipt {
	if failedAt.IsZero() {
		failedAt = event.ObservedAt
	}
	message := ""
	if cause != nil {
		message = cause.Error()
	}
	return ModelRunEvaluationTrendAlertNotificationDeliveryReceipt{
		TenantID:     event.TenantID,
		RuleID:       event.RuleID,
		Status:       ModelRunEvaluationTrendAlertNotificationDeliveryFailed,
		AlertStatus:  event.Status,
		Reason:       plan.Decision.Reason,
		Attempt:      1,
		Retryable:    true,
		NextRetryAt:  failedAt,
		Labels:       cloneStringMetadata(event.Labels),
		ErrorMessage: message,
		SentAt:       failedAt,
	}
}

func NewPlatformModelRunEvaluationTrendAlertNotificationReceiptStore(store *db.Store) *PlatformModelRunEvaluationTrendAlertNotificationReceiptStore {
	return &PlatformModelRunEvaluationTrendAlertNotificationReceiptStore{store: store, now: time.Now}
}

func NewPlatformModelRunEvaluationTrendAlertNotificationReceiptStoreTx(tx db.Tx) *PlatformModelRunEvaluationTrendAlertNotificationReceiptStore {
	return &PlatformModelRunEvaluationTrendAlertNotificationReceiptStore{tx: tx, now: time.Now}
}

func (s *PlatformModelRunEvaluationTrendAlertNotificationReceiptStore) ready() error {
	if s == nil || (s.store == nil && s.tx == nil) {
		return fmt.Errorf("%w: model run evaluation trend alert notification receipt store is required", ErrInvalidDefinition)
	}
	return nil
}

func (s *PlatformModelRunEvaluationTrendAlertNotificationReceiptStore) exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if s.tx != nil {
		return s.tx.ExecContext(ctx, query, args...)
	}
	return s.store.Exec(ctx, query, args...)
}

func (s *PlatformModelRunEvaluationTrendAlertNotificationReceiptStore) query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if s.tx != nil {
		return s.tx.QueryContext(ctx, query, args...)
	}
	return s.store.Query(ctx, query, args...)
}

func (s *PlatformModelRunEvaluationTrendAlertNotificationReceiptStore) queryRow(ctx context.Context, query string, args ...any) rowScanner {
	if s.tx != nil {
		return s.tx.QueryRowContext(ctx, query, args...)
	}
	return s.store.QueryRow(ctx, query, args...)
}

func (s *PlatformModelRunEvaluationTrendAlertNotificationReceiptStore) SaveModelRunEvaluationTrendAlertNotificationReceipt(ctx context.Context, receipt ModelRunEvaluationTrendAlertNotificationDeliveryReceipt) error {
	if err := s.ready(); err != nil {
		return err
	}
	normalized, err := normalizeModelRunEvaluationTrendAlertNotificationDeliveryReceipt(receipt, s.now)
	if err != nil {
		return err
	}
	_, err = s.exec(ctx, `
INSERT INTO ontology_model_run_evaluation_alert_notification_receipts (
	id, tenant_id, rule_id, delivery_status, alert_status, reason, channel, message_id,
	attempt, retryable, next_retry_at, labels_json, metadata_json, error_message, sent_at, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO NOTHING`,
		normalized.ID,
		normalized.TenantID,
		normalized.RuleID,
		normalized.Status,
		normalized.AlertStatus,
		normalized.Reason,
		normalized.Channel,
		normalized.MessageID,
		normalized.Attempt,
		normalized.Retryable,
		timeToUnixNano(normalized.NextRetryAt),
		mustJSON(normalized.Labels),
		mustJSON(normalized.Metadata),
		normalized.ErrorMessage,
		timeToUnixNano(normalized.SentAt),
		timeToUnixNano(normalized.CreatedAt),
		timeToUnixNano(normalized.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("save ontology model run evaluation alert notification receipt: %w", err)
	}
	return nil
}

func (s *PlatformModelRunEvaluationTrendAlertNotificationReceiptStore) ListModelRunEvaluationTrendAlertNotificationReceipts(ctx context.Context, query ModelRunEvaluationTrendAlertNotificationReceiptQuery) (ModelRunEvaluationTrendAlertNotificationReceiptPage, error) {
	if err := s.ready(); err != nil {
		return ModelRunEvaluationTrendAlertNotificationReceiptPage{}, err
	}
	limit := normalizeMetadataProposalLimit(query.Limit)
	offset := normalizeMetadataProposalOffset(query.Offset)
	where, args := modelRunEvaluationTrendAlertNotificationReceiptWhere(query)
	var total int
	if err := s.queryRow(ctx, `SELECT COUNT(*) FROM ontology_model_run_evaluation_alert_notification_receipts`+where, args...).Scan(&total); err != nil {
		return ModelRunEvaluationTrendAlertNotificationReceiptPage{}, fmt.Errorf("count ontology model run evaluation alert notification receipts: %w", err)
	}
	selectArgs := append(args, limit, offset)
	rows, err := s.query(ctx, modelRunEvaluationTrendAlertNotificationReceiptSelectSQL()+where+` ORDER BY created_at DESC, id ASC LIMIT ? OFFSET ?`, selectArgs...)
	if err != nil {
		return ModelRunEvaluationTrendAlertNotificationReceiptPage{}, fmt.Errorf("list ontology model run evaluation alert notification receipts: %w", err)
	}
	defer rows.Close()
	receipts := []ModelRunEvaluationTrendAlertNotificationDeliveryReceipt{}
	for rows.Next() {
		receipt, err := scanModelRunEvaluationTrendAlertNotificationDeliveryReceipt(rows)
		if err != nil {
			return ModelRunEvaluationTrendAlertNotificationReceiptPage{}, err
		}
		receipts = append(receipts, receipt)
	}
	if err := rows.Err(); err != nil {
		return ModelRunEvaluationTrendAlertNotificationReceiptPage{}, err
	}
	return ModelRunEvaluationTrendAlertNotificationReceiptPage{
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

func (s *PlatformModelRunEvaluationTrendAlertNotificationReceiptStore) SummarizeModelRunEvaluationTrendAlertNotificationReceipts(ctx context.Context, query ModelRunEvaluationTrendAlertNotificationReceiptQuery) (ModelRunEvaluationTrendAlertNotificationReceiptSummaryPage, error) {
	if err := s.ready(); err != nil {
		return ModelRunEvaluationTrendAlertNotificationReceiptSummaryPage{}, err
	}
	limit := normalizeMetadataProposalLimit(query.Limit)
	offset := normalizeMetadataProposalOffset(query.Offset)
	where, args := modelRunEvaluationTrendAlertNotificationReceiptWhere(query)
	groupBy := ` GROUP BY tenant_id, rule_id, channel, delivery_status, retryable`
	var total int
	if err := s.queryRow(ctx, `SELECT COUNT(*) FROM (SELECT 1 FROM ontology_model_run_evaluation_alert_notification_receipts`+where+groupBy+`) AS grouped`, args...).Scan(&total); err != nil {
		return ModelRunEvaluationTrendAlertNotificationReceiptSummaryPage{}, fmt.Errorf("count ontology model run evaluation alert notification receipt summaries: %w", err)
	}
	selectArgs := append(append([]any{}, args...), limit, offset)
	rows, err := s.query(ctx, `SELECT tenant_id, rule_id, channel, delivery_status, retryable, COUNT(*)
FROM ontology_model_run_evaluation_alert_notification_receipts`+where+groupBy+`
ORDER BY COUNT(*) DESC, tenant_id ASC, rule_id ASC, channel ASC, delivery_status ASC, retryable DESC
LIMIT ? OFFSET ?`, selectArgs...)
	if err != nil {
		return ModelRunEvaluationTrendAlertNotificationReceiptSummaryPage{}, fmt.Errorf("summarize ontology model run evaluation alert notification receipts: %w", err)
	}
	defer rows.Close()
	summaries := []ModelRunEvaluationTrendAlertNotificationReceiptSummary{}
	for rows.Next() {
		summary := ModelRunEvaluationTrendAlertNotificationReceiptSummary{}
		if err := rows.Scan(&summary.TenantID, &summary.RuleID, &summary.Channel, &summary.Status, &summary.Retryable, &summary.Count); err != nil {
			return ModelRunEvaluationTrendAlertNotificationReceiptSummaryPage{}, err
		}
		summaries = append(summaries, summary)
	}
	if err := rows.Err(); err != nil {
		return ModelRunEvaluationTrendAlertNotificationReceiptSummaryPage{}, err
	}
	return ModelRunEvaluationTrendAlertNotificationReceiptSummaryPage{
		Summaries: summaries,
		Page: Page{
			Limit:    limit,
			Offset:   offset,
			Total:    total,
			Returned: len(summaries),
			HasMore:  offset+len(summaries) < total,
		},
	}, nil
}

func (s *PlatformModelRunEvaluationTrendAlertNotificationReceiptStore) MarkModelRunEvaluationTrendAlertNotificationReceiptRetryAbandoned(ctx context.Context, receiptID string, reason string, updatedAt time.Time) error {
	if err := s.ready(); err != nil {
		return err
	}
	receiptID = strings.TrimSpace(receiptID)
	if receiptID == "" {
		return fmt.Errorf("%w: model run evaluation trend alert notification receipt id is required", ErrInvalidDefinition)
	}
	if updatedAt.IsZero() {
		updatedAt = s.now().UTC()
	} else {
		updatedAt = updatedAt.UTC()
	}
	result, err := s.exec(ctx, `UPDATE ontology_model_run_evaluation_alert_notification_receipts SET retryable = ?, next_retry_at = ?, error_message = ?, updated_at = ? WHERE id = ?`,
		false,
		int64(0),
		strings.TrimSpace(reason),
		timeToUnixNano(updatedAt),
		receiptID,
	)
	if err != nil {
		return fmt.Errorf("mark ontology model run evaluation alert notification receipt retry abandoned: %w", err)
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return fmt.Errorf("%w: model run evaluation trend alert notification receipt %s", ErrNotFound, receiptID)
	}
	return nil
}

func (s *PlatformModelRunEvaluationTrendAlertNotificationReceiptStore) MarkModelRunEvaluationTrendAlertNotificationReceiptRetryConsumed(ctx context.Context, receiptID string, updatedAt time.Time) error {
	if err := s.ready(); err != nil {
		return err
	}
	receiptID = strings.TrimSpace(receiptID)
	if receiptID == "" {
		return fmt.Errorf("%w: model run evaluation trend alert notification receipt id is required", ErrInvalidDefinition)
	}
	if updatedAt.IsZero() {
		updatedAt = s.now().UTC()
	} else {
		updatedAt = updatedAt.UTC()
	}
	result, err := s.exec(ctx, `UPDATE ontology_model_run_evaluation_alert_notification_receipts SET retryable = ?, next_retry_at = ?, updated_at = ? WHERE id = ?`,
		false,
		int64(0),
		timeToUnixNano(updatedAt),
		receiptID,
	)
	if err != nil {
		return fmt.Errorf("mark ontology model run evaluation alert notification receipt retry consumed: %w", err)
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return fmt.Errorf("%w: model run evaluation trend alert notification receipt %s", ErrNotFound, receiptID)
	}
	return nil
}

func normalizeModelRunEvaluationTrendAlertNotificationDeliveryReceipt(receipt ModelRunEvaluationTrendAlertNotificationDeliveryReceipt, now func() time.Time) (ModelRunEvaluationTrendAlertNotificationDeliveryReceipt, error) {
	receipt.ID = strings.TrimSpace(receipt.ID)
	receipt.TenantID = strings.TrimSpace(receipt.TenantID)
	receipt.RuleID = normalizeID(receipt.RuleID)
	receipt.Status = ModelRunEvaluationTrendAlertNotificationDeliveryStatus(strings.TrimSpace(string(receipt.Status)))
	receipt.AlertStatus = ModelRunEvaluationTrendAlertStatus(strings.TrimSpace(string(receipt.AlertStatus)))
	receipt.Reason = ModelRunEvaluationTrendAlertNotificationReason(strings.TrimSpace(string(receipt.Reason)))
	receipt.Channel = strings.TrimSpace(receipt.Channel)
	receipt.MessageID = strings.TrimSpace(receipt.MessageID)
	receipt.ErrorMessage = strings.TrimSpace(receipt.ErrorMessage)
	receipt.Labels = cloneStringMetadata(receipt.Labels)
	receipt.Metadata = cloneStringMetadata(receipt.Metadata)
	if receipt.TenantID == "" && receipt.Labels != nil {
		receipt.TenantID = strings.TrimSpace(receipt.Labels["tenant_id"])
	}
	if receipt.Attempt <= 0 {
		receipt.Attempt = 1
	}
	switch receipt.Status {
	case ModelRunEvaluationTrendAlertNotificationDeliveryDelivered, ModelRunEvaluationTrendAlertNotificationDeliveryFailed:
	default:
		return ModelRunEvaluationTrendAlertNotificationDeliveryReceipt{}, fmt.Errorf("%w: unsupported model run evaluation trend alert notification receipt status %s", ErrInvalidDefinition, receipt.Status)
	}
	switch receipt.AlertStatus {
	case ModelRunEvaluationTrendAlertFiring, ModelRunEvaluationTrendAlertResolved:
	default:
		return ModelRunEvaluationTrendAlertNotificationDeliveryReceipt{}, fmt.Errorf("%w: unsupported model run evaluation trend alert notification receipt alert status %s", ErrInvalidDefinition, receipt.AlertStatus)
	}
	if receipt.ID == "" && receipt.TenantID != "" && receipt.RuleID != "" {
		receipt.ID = modelRunEvaluationTrendAlertNotificationDeliveryReceiptID(receipt)
	}
	if receipt.ID == "" || receipt.TenantID == "" || receipt.RuleID == "" || receipt.SentAt.IsZero() {
		return ModelRunEvaluationTrendAlertNotificationDeliveryReceipt{}, fmt.Errorf("%w: model run evaluation trend alert notification receipt id, tenant_id, rule_id, and sent_at are required", ErrInvalidDefinition)
	}
	if now == nil {
		now = time.Now
	}
	at := now().UTC()
	receipt.SentAt = receipt.SentAt.UTC()
	if !receipt.NextRetryAt.IsZero() {
		receipt.NextRetryAt = receipt.NextRetryAt.UTC()
	}
	if receipt.CreatedAt.IsZero() {
		receipt.CreatedAt = receipt.SentAt
	} else {
		receipt.CreatedAt = receipt.CreatedAt.UTC()
	}
	if receipt.UpdatedAt.IsZero() {
		receipt.UpdatedAt = at
	} else {
		receipt.UpdatedAt = receipt.UpdatedAt.UTC()
	}
	return receipt, nil
}

func modelRunEvaluationTrendAlertNotificationDeliveryReceiptID(receipt ModelRunEvaluationTrendAlertNotificationDeliveryReceipt) string {
	return "ontology_model_run_evaluation_alert_notification_receipt:" + hashMappingRule(map[string]any{
		"tenant_id":  strings.TrimSpace(receipt.TenantID),
		"rule_id":    normalizeID(receipt.RuleID),
		"status":     strings.TrimSpace(string(receipt.Status)),
		"channel":    strings.TrimSpace(receipt.Channel),
		"message_id": strings.TrimSpace(receipt.MessageID),
		"attempt":    receipt.Attempt,
		"sent_at":    receipt.SentAt.UTC().Format(time.RFC3339Nano),
		"labels":     modelRunEvaluationTrendAlertNotificationHistoryKeyLabels(receipt.Labels),
	})
}

func cloneModelRunEvaluationTrendAlertNotificationDeliveryReceipt(receipt ModelRunEvaluationTrendAlertNotificationDeliveryReceipt) ModelRunEvaluationTrendAlertNotificationDeliveryReceipt {
	receipt.Labels = cloneStringMetadata(receipt.Labels)
	receipt.Metadata = cloneStringMetadata(receipt.Metadata)
	return receipt
}

func sortModelRunEvaluationTrendAlertNotificationReceipts(receipts []ModelRunEvaluationTrendAlertNotificationDeliveryReceipt) {
	sort.SliceStable(receipts, func(i, j int) bool {
		if receipts[i].CreatedAt.Equal(receipts[j].CreatedAt) {
			return receipts[i].ID < receipts[j].ID
		}
		return receipts[i].CreatedAt.After(receipts[j].CreatedAt)
	})
}

func modelRunEvaluationTrendAlertNotificationReceiptPage(receipts []ModelRunEvaluationTrendAlertNotificationDeliveryReceipt, query ModelRunEvaluationTrendAlertNotificationReceiptQuery) ModelRunEvaluationTrendAlertNotificationReceiptPage {
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
	items := make([]ModelRunEvaluationTrendAlertNotificationDeliveryReceipt, 0, end-offset)
	for _, receipt := range receipts[offset:end] {
		items = append(items, cloneModelRunEvaluationTrendAlertNotificationDeliveryReceipt(receipt))
	}
	return ModelRunEvaluationTrendAlertNotificationReceiptPage{
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

type modelRunEvaluationTrendAlertNotificationReceiptSummaryKey struct {
	tenantID  string
	ruleID    string
	channel   string
	status    ModelRunEvaluationTrendAlertNotificationDeliveryStatus
	retryable bool
}

func sortModelRunEvaluationTrendAlertNotificationReceiptSummaries(summaries []ModelRunEvaluationTrendAlertNotificationReceiptSummary) {
	sort.SliceStable(summaries, func(i, j int) bool {
		if summaries[i].Count != summaries[j].Count {
			return summaries[i].Count > summaries[j].Count
		}
		if summaries[i].TenantID != summaries[j].TenantID {
			return summaries[i].TenantID < summaries[j].TenantID
		}
		if summaries[i].RuleID != summaries[j].RuleID {
			return summaries[i].RuleID < summaries[j].RuleID
		}
		if summaries[i].Channel != summaries[j].Channel {
			return summaries[i].Channel < summaries[j].Channel
		}
		if summaries[i].Status != summaries[j].Status {
			return summaries[i].Status < summaries[j].Status
		}
		return summaries[i].Retryable && !summaries[j].Retryable
	})
}

func modelRunEvaluationTrendAlertNotificationReceiptSummaryPage(summaries []ModelRunEvaluationTrendAlertNotificationReceiptSummary, query ModelRunEvaluationTrendAlertNotificationReceiptQuery) ModelRunEvaluationTrendAlertNotificationReceiptSummaryPage {
	limit := normalizeMetadataProposalLimit(query.Limit)
	offset := normalizeMetadataProposalOffset(query.Offset)
	total := len(summaries)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	items := make([]ModelRunEvaluationTrendAlertNotificationReceiptSummary, 0, end-offset)
	items = append(items, summaries[offset:end]...)
	return ModelRunEvaluationTrendAlertNotificationReceiptSummaryPage{
		Summaries: items,
		Page: Page{
			Limit:    limit,
			Offset:   offset,
			Total:    total,
			Returned: len(items),
			HasMore:  end < total,
		},
	}
}

func modelRunEvaluationTrendAlertNotificationReceiptMatchesQuery(receipt ModelRunEvaluationTrendAlertNotificationDeliveryReceipt, query ModelRunEvaluationTrendAlertNotificationReceiptQuery) bool {
	if strings.TrimSpace(query.TenantID) != "" && receipt.TenantID != strings.TrimSpace(query.TenantID) {
		return false
	}
	if normalizeID(query.RuleID) != "" && receipt.RuleID != normalizeID(query.RuleID) {
		return false
	}
	if strings.TrimSpace(string(query.Status)) != "" && string(receipt.Status) != strings.TrimSpace(string(query.Status)) {
		return false
	}
	if strings.TrimSpace(query.Channel) != "" && receipt.Channel != strings.TrimSpace(query.Channel) {
		return false
	}
	if query.Retryable || query.RetryableSet {
		if receipt.Retryable != query.Retryable {
			return false
		}
	}
	if !query.NextRetryDueAt.IsZero() {
		if receipt.NextRetryAt.IsZero() || receipt.NextRetryAt.After(query.NextRetryDueAt.UTC()) {
			return false
		}
	}
	return true
}

func modelRunEvaluationTrendAlertNotificationReceiptWhere(query ModelRunEvaluationTrendAlertNotificationReceiptQuery) (string, []any) {
	clauses := []string{}
	args := []any{}
	if strings.TrimSpace(query.TenantID) != "" {
		clauses = append(clauses, "tenant_id = ?")
		args = append(args, strings.TrimSpace(query.TenantID))
	}
	if normalizeID(query.RuleID) != "" {
		clauses = append(clauses, "rule_id = ?")
		args = append(args, normalizeID(query.RuleID))
	}
	if strings.TrimSpace(string(query.Status)) != "" {
		clauses = append(clauses, "delivery_status = ?")
		args = append(args, strings.TrimSpace(string(query.Status)))
	}
	if strings.TrimSpace(query.Channel) != "" {
		clauses = append(clauses, "channel = ?")
		args = append(args, strings.TrimSpace(query.Channel))
	}
	if query.Retryable || query.RetryableSet {
		clauses = append(clauses, "retryable = ?")
		args = append(args, query.Retryable)
	}
	if !query.NextRetryDueAt.IsZero() {
		clauses = append(clauses, "next_retry_at > ?")
		args = append(args, int64(0))
		clauses = append(clauses, "next_retry_at <= ?")
		args = append(args, timeToUnixNano(query.NextRetryDueAt.UTC()))
	}
	if len(clauses) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

func modelRunEvaluationTrendAlertNotificationReceiptSelectSQL() string {
	return `SELECT id, tenant_id, rule_id, delivery_status, alert_status, reason, channel, message_id,
attempt, retryable, next_retry_at, labels_json, metadata_json, error_message, sent_at, created_at, updated_at
FROM ontology_model_run_evaluation_alert_notification_receipts`
}

func scanModelRunEvaluationTrendAlertNotificationDeliveryReceipt(row rowScanner) (ModelRunEvaluationTrendAlertNotificationDeliveryReceipt, error) {
	receipt := ModelRunEvaluationTrendAlertNotificationDeliveryReceipt{}
	var labelsJSON, metadataJSON string
	var nextRetryAt, sentAt, createdAt, updatedAt int64
	if err := row.Scan(&receipt.ID, &receipt.TenantID, &receipt.RuleID, &receipt.Status, &receipt.AlertStatus, &receipt.Reason, &receipt.Channel, &receipt.MessageID, &receipt.Attempt, &receipt.Retryable, &nextRetryAt, &labelsJSON, &metadataJSON, &receipt.ErrorMessage, &sentAt, &createdAt, &updatedAt); err != nil {
		return ModelRunEvaluationTrendAlertNotificationDeliveryReceipt{}, err
	}
	mustDecodeJSON(labelsJSON, &receipt.Labels)
	mustDecodeJSON(metadataJSON, &receipt.Metadata)
	receipt.Labels = cloneStringMetadata(receipt.Labels)
	receipt.Metadata = cloneStringMetadata(receipt.Metadata)
	receipt.NextRetryAt = unixNanoToTime(nextRetryAt)
	receipt.SentAt = unixNanoToTime(sentAt)
	receipt.CreatedAt = unixNanoToTime(createdAt)
	receipt.UpdatedAt = unixNanoToTime(updatedAt)
	return receipt, nil
}
