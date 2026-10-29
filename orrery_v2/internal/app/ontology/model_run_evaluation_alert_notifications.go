package ontology

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"

	"orrery_v2/internal/platform/db"
)

type ModelRunEvaluationTrendAlertNotificationReason string

const (
	ModelRunEvaluationTrendAlertNotificationReasonFiring         ModelRunEvaluationTrendAlertNotificationReason = "firing"
	ModelRunEvaluationTrendAlertNotificationReasonResolved       ModelRunEvaluationTrendAlertNotificationReason = "resolved"
	ModelRunEvaluationTrendAlertNotificationReasonRepeatWindow   ModelRunEvaluationTrendAlertNotificationReason = "repeat_window"
	ModelRunEvaluationTrendAlertNotificationReasonRecoveryWindow ModelRunEvaluationTrendAlertNotificationReason = "recovery_window"
	ModelRunEvaluationTrendAlertNotificationReasonSilencedWindow ModelRunEvaluationTrendAlertNotificationReason = "silenced_window"
	ModelRunEvaluationTrendAlertNotificationReasonSilencedLabels ModelRunEvaluationTrendAlertNotificationReason = "silenced_labels"
	ModelRunEvaluationTrendAlertNotificationReasonUnsupported    ModelRunEvaluationTrendAlertNotificationReason = "unsupported_status"
)

type ModelRunEvaluationTrendAlertNotificationPolicy struct {
	RepeatWindow   time.Duration
	RecoveryWindow time.Duration
	SilencedUntil  time.Time
	SilenceLabels  map[string]string
}

type ModelRunEvaluationTrendAlertNotificationHistory struct {
	LastNotificationStatus ModelRunEvaluationTrendAlertStatus
	LastNotificationAt     time.Time
}

type ModelRunEvaluationTrendAlertNotificationHistoryRecord struct {
	ID                     string                             `json:"id"`
	TenantID               string                             `json:"tenant_id,omitempty"`
	RuleID                 string                             `json:"rule_id"`
	Labels                 map[string]string                  `json:"labels,omitempty"`
	LastNotificationStatus ModelRunEvaluationTrendAlertStatus `json:"last_notification_status"`
	LastNotificationAt     time.Time                          `json:"last_notification_at,omitempty"`
	CreatedAt              time.Time                          `json:"created_at,omitempty"`
	UpdatedAt              time.Time                          `json:"updated_at,omitempty"`
}

type ModelRunEvaluationTrendAlertNotificationHistoryQuery struct {
	TenantID string
	RuleID   string
	Labels   map[string]string
}

type ModelRunEvaluationTrendAlertNotificationHistoryStore interface {
	SaveModelRunEvaluationTrendAlertNotificationHistory(ctx context.Context, record ModelRunEvaluationTrendAlertNotificationHistoryRecord) error
	GetModelRunEvaluationTrendAlertNotificationHistory(ctx context.Context, query ModelRunEvaluationTrendAlertNotificationHistoryQuery) (ModelRunEvaluationTrendAlertNotificationHistoryRecord, bool, error)
}

type ModelRunEvaluationTrendAlertNotificationDecision struct {
	Notify         bool
	Reason         ModelRunEvaluationTrendAlertNotificationReason
	Status         ModelRunEvaluationTrendAlertStatus
	NextEligibleAt time.Time
}

type ModelRunEvaluationTrendAlertNotificationPlan struct {
	Decision     ModelRunEvaluationTrendAlertNotificationDecision
	History      ModelRunEvaluationTrendAlertNotificationHistoryRecord
	HistoryFound bool
}

type ModelRunEvaluationTrendAlertNotificationReceipt struct {
	Channel   string            `json:"channel,omitempty"`
	MessageID string            `json:"message_id,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	SentAt    time.Time         `json:"sent_at,omitempty"`
}

type ModelRunEvaluationTrendAlertNotificationDispatchResult struct {
	Plan            ModelRunEvaluationTrendAlertNotificationPlan
	Receipt         ModelRunEvaluationTrendAlertNotificationReceipt
	DeliveryReceipt ModelRunEvaluationTrendAlertNotificationDeliveryReceipt
	Delivered       bool
}

type ModelRunEvaluationTrendAlertNotificationSender interface {
	SendModelRunEvaluationTrendAlertNotification(ctx context.Context, event ModelRunEvaluationTrendAlertAuditEvent, plan ModelRunEvaluationTrendAlertNotificationPlan) (ModelRunEvaluationTrendAlertNotificationReceipt, error)
}

type MemoryModelRunEvaluationTrendAlertNotificationHistoryStore struct {
	mu      sync.RWMutex
	records map[string]ModelRunEvaluationTrendAlertNotificationHistoryRecord
	now     func() time.Time
}

func NewMemoryModelRunEvaluationTrendAlertNotificationHistoryStore() *MemoryModelRunEvaluationTrendAlertNotificationHistoryStore {
	return &MemoryModelRunEvaluationTrendAlertNotificationHistoryStore{records: map[string]ModelRunEvaluationTrendAlertNotificationHistoryRecord{}, now: time.Now}
}

func (s *MemoryModelRunEvaluationTrendAlertNotificationHistoryStore) SaveModelRunEvaluationTrendAlertNotificationHistory(ctx context.Context, record ModelRunEvaluationTrendAlertNotificationHistoryRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	normalized, err := normalizeModelRunEvaluationTrendAlertNotificationHistoryRecord(record, s.now)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if previous, ok := s.records[normalized.ID]; ok && !previous.CreatedAt.IsZero() {
		normalized.CreatedAt = previous.CreatedAt
	}
	s.records[normalized.ID] = normalized
	return nil
}

func (s *MemoryModelRunEvaluationTrendAlertNotificationHistoryStore) GetModelRunEvaluationTrendAlertNotificationHistory(ctx context.Context, query ModelRunEvaluationTrendAlertNotificationHistoryQuery) (ModelRunEvaluationTrendAlertNotificationHistoryRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return ModelRunEvaluationTrendAlertNotificationHistoryRecord{}, false, err
	}
	id, err := modelRunEvaluationTrendAlertNotificationHistoryIDFromQuery(query)
	if err != nil {
		return ModelRunEvaluationTrendAlertNotificationHistoryRecord{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.records[id]
	if !ok {
		return ModelRunEvaluationTrendAlertNotificationHistoryRecord{}, false, nil
	}
	return cloneModelRunEvaluationTrendAlertNotificationHistoryRecord(record), true, nil
}

type PlatformModelRunEvaluationTrendAlertNotificationHistoryStore struct {
	store *db.Store
	tx    db.Tx
	now   func() time.Time
}

func NewPlatformModelRunEvaluationTrendAlertNotificationHistoryStore(store *db.Store) *PlatformModelRunEvaluationTrendAlertNotificationHistoryStore {
	return &PlatformModelRunEvaluationTrendAlertNotificationHistoryStore{store: store, now: time.Now}
}

func NewPlatformModelRunEvaluationTrendAlertNotificationHistoryStoreTx(tx db.Tx) *PlatformModelRunEvaluationTrendAlertNotificationHistoryStore {
	return &PlatformModelRunEvaluationTrendAlertNotificationHistoryStore{tx: tx, now: time.Now}
}

func (s *PlatformModelRunEvaluationTrendAlertNotificationHistoryStore) ready() error {
	if s == nil || (s.store == nil && s.tx == nil) {
		return fmt.Errorf("%w: model run evaluation trend alert notification history store is required", ErrInvalidDefinition)
	}
	return nil
}

func (s *PlatformModelRunEvaluationTrendAlertNotificationHistoryStore) exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if s.tx != nil {
		return s.tx.ExecContext(ctx, query, args...)
	}
	return s.store.Exec(ctx, query, args...)
}

func (s *PlatformModelRunEvaluationTrendAlertNotificationHistoryStore) queryRow(ctx context.Context, query string, args ...any) rowScanner {
	if s.tx != nil {
		return s.tx.QueryRowContext(ctx, query, args...)
	}
	return s.store.QueryRow(ctx, query, args...)
}

func (s *PlatformModelRunEvaluationTrendAlertNotificationHistoryStore) SaveModelRunEvaluationTrendAlertNotificationHistory(ctx context.Context, record ModelRunEvaluationTrendAlertNotificationHistoryRecord) error {
	if err := s.ready(); err != nil {
		return err
	}
	normalized, err := normalizeModelRunEvaluationTrendAlertNotificationHistoryRecord(record, s.now)
	if err != nil {
		return err
	}
	_, err = s.exec(ctx, `
INSERT INTO ontology_model_run_evaluation_alert_notification_history (
	id, tenant_id, rule_id, labels_json, last_notification_status, last_notification_at, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	tenant_id = excluded.tenant_id,
	rule_id = excluded.rule_id,
	labels_json = excluded.labels_json,
	last_notification_status = excluded.last_notification_status,
	last_notification_at = excluded.last_notification_at,
	updated_at = excluded.updated_at`,
		normalized.ID,
		normalized.TenantID,
		normalized.RuleID,
		mustJSON(normalized.Labels),
		normalized.LastNotificationStatus,
		timeToUnixNano(normalized.LastNotificationAt),
		timeToUnixNano(normalized.CreatedAt),
		timeToUnixNano(normalized.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("save ontology model run evaluation alert notification history: %w", err)
	}
	return nil
}

func (s *PlatformModelRunEvaluationTrendAlertNotificationHistoryStore) GetModelRunEvaluationTrendAlertNotificationHistory(ctx context.Context, query ModelRunEvaluationTrendAlertNotificationHistoryQuery) (ModelRunEvaluationTrendAlertNotificationHistoryRecord, bool, error) {
	if err := s.ready(); err != nil {
		return ModelRunEvaluationTrendAlertNotificationHistoryRecord{}, false, err
	}
	id, err := modelRunEvaluationTrendAlertNotificationHistoryIDFromQuery(query)
	if err != nil {
		return ModelRunEvaluationTrendAlertNotificationHistoryRecord{}, false, err
	}
	record, err := scanModelRunEvaluationTrendAlertNotificationHistoryRecord(s.queryRow(ctx, `SELECT id, tenant_id, rule_id, labels_json, last_notification_status, last_notification_at, created_at, updated_at FROM ontology_model_run_evaluation_alert_notification_history WHERE id = ?`, id))
	if err != nil {
		if err == sql.ErrNoRows {
			return ModelRunEvaluationTrendAlertNotificationHistoryRecord{}, false, nil
		}
		return ModelRunEvaluationTrendAlertNotificationHistoryRecord{}, false, err
	}
	return record, true, nil
}

func DecideModelRunEvaluationTrendAlertNotification(event ModelRunEvaluationTrendAlertAuditEvent, history ModelRunEvaluationTrendAlertNotificationHistory, policy ModelRunEvaluationTrendAlertNotificationPolicy, now time.Time) ModelRunEvaluationTrendAlertNotificationDecision {
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	status := ModelRunEvaluationTrendAlertStatus(strings.TrimSpace(string(event.Status)))
	decision := ModelRunEvaluationTrendAlertNotificationDecision{Status: status}
	if !policy.SilencedUntil.IsZero() && now.Before(policy.SilencedUntil.UTC()) {
		decision.Reason = ModelRunEvaluationTrendAlertNotificationReasonSilencedWindow
		decision.NextEligibleAt = policy.SilencedUntil.UTC()
		return decision
	}
	if modelRunEvaluationTrendAlertNotificationLabelsMatch(event.Labels, policy.SilenceLabels) {
		decision.Reason = ModelRunEvaluationTrendAlertNotificationReasonSilencedLabels
		return decision
	}
	switch status {
	case ModelRunEvaluationTrendAlertFiring:
		if history.LastNotificationStatus == ModelRunEvaluationTrendAlertFiring && policy.RepeatWindow > 0 && !history.LastNotificationAt.IsZero() {
			next := history.LastNotificationAt.UTC().Add(policy.RepeatWindow)
			if now.Before(next) {
				decision.Reason = ModelRunEvaluationTrendAlertNotificationReasonRepeatWindow
				decision.NextEligibleAt = next
				return decision
			}
		}
		decision.Notify = true
		decision.Reason = ModelRunEvaluationTrendAlertNotificationReasonFiring
		return decision
	case ModelRunEvaluationTrendAlertResolved:
		if history.LastNotificationStatus == ModelRunEvaluationTrendAlertResolved && policy.RecoveryWindow > 0 && !history.LastNotificationAt.IsZero() {
			next := history.LastNotificationAt.UTC().Add(policy.RecoveryWindow)
			if now.Before(next) {
				decision.Reason = ModelRunEvaluationTrendAlertNotificationReasonRecoveryWindow
				decision.NextEligibleAt = next
				return decision
			}
		}
		decision.Notify = true
		decision.Reason = ModelRunEvaluationTrendAlertNotificationReasonResolved
		return decision
	default:
		decision.Reason = ModelRunEvaluationTrendAlertNotificationReasonUnsupported
		return decision
	}
}

func PlanModelRunEvaluationTrendAlertNotification(ctx context.Context, store ModelRunEvaluationTrendAlertNotificationHistoryStore, event ModelRunEvaluationTrendAlertAuditEvent, policy ModelRunEvaluationTrendAlertNotificationPolicy, now time.Time) (ModelRunEvaluationTrendAlertNotificationPlan, error) {
	if store == nil {
		return ModelRunEvaluationTrendAlertNotificationPlan{}, fmt.Errorf("%w: model run evaluation trend alert notification history store is required", ErrInvalidDefinition)
	}
	history, found, err := store.GetModelRunEvaluationTrendAlertNotificationHistory(ctx, ModelRunEvaluationTrendAlertNotificationHistoryQuery{
		TenantID: event.TenantID,
		RuleID:   event.RuleID,
		Labels:   event.Labels,
	})
	if err != nil {
		return ModelRunEvaluationTrendAlertNotificationPlan{}, err
	}
	return ModelRunEvaluationTrendAlertNotificationPlan{
		Decision:     DecideModelRunEvaluationTrendAlertNotification(event, history.NotificationHistory(), policy, now),
		History:      history,
		HistoryFound: found,
	}, nil
}

func DispatchModelRunEvaluationTrendAlertNotification(ctx context.Context, store ModelRunEvaluationTrendAlertNotificationHistoryStore, sender ModelRunEvaluationTrendAlertNotificationSender, event ModelRunEvaluationTrendAlertAuditEvent, policy ModelRunEvaluationTrendAlertNotificationPolicy, now time.Time) (ModelRunEvaluationTrendAlertNotificationDispatchResult, error) {
	return dispatchModelRunEvaluationTrendAlertNotification(ctx, store, nil, sender, event, policy, now)
}

func DispatchModelRunEvaluationTrendAlertNotificationWithReceipts(ctx context.Context, store ModelRunEvaluationTrendAlertNotificationHistoryStore, receipts ModelRunEvaluationTrendAlertNotificationReceiptStore, sender ModelRunEvaluationTrendAlertNotificationSender, event ModelRunEvaluationTrendAlertAuditEvent, policy ModelRunEvaluationTrendAlertNotificationPolicy, now time.Time) (ModelRunEvaluationTrendAlertNotificationDispatchResult, error) {
	return dispatchModelRunEvaluationTrendAlertNotification(ctx, store, receipts, sender, event, policy, now)
}

func dispatchModelRunEvaluationTrendAlertNotification(ctx context.Context, store ModelRunEvaluationTrendAlertNotificationHistoryStore, receipts ModelRunEvaluationTrendAlertNotificationReceiptStore, sender ModelRunEvaluationTrendAlertNotificationSender, event ModelRunEvaluationTrendAlertAuditEvent, policy ModelRunEvaluationTrendAlertNotificationPolicy, now time.Time) (ModelRunEvaluationTrendAlertNotificationDispatchResult, error) {
	plan, err := PlanModelRunEvaluationTrendAlertNotification(ctx, store, event, policy, now)
	if err != nil {
		return ModelRunEvaluationTrendAlertNotificationDispatchResult{}, err
	}
	result := ModelRunEvaluationTrendAlertNotificationDispatchResult{Plan: plan}
	if !plan.Decision.Notify {
		return result, nil
	}
	if sender == nil {
		return result, fmt.Errorf("%w: model run evaluation trend alert notification sender is required", ErrInvalidDefinition)
	}
	receipt, err := sender.SendModelRunEvaluationTrendAlertNotification(ctx, event, plan)
	if err != nil {
		if receipts != nil {
			failedReceipt := modelRunEvaluationTrendAlertNotificationDeliveryReceiptFromFailure(event, plan, now, err)
			result.DeliveryReceipt = failedReceipt
			if saveErr := receipts.SaveModelRunEvaluationTrendAlertNotificationReceipt(ctx, failedReceipt); saveErr != nil {
				return result, saveErr
			}
		}
		return result, err
	}
	notifiedAt := receipt.SentAt
	if notifiedAt.IsZero() {
		notifiedAt = now
	}
	deliveryReceipt := modelRunEvaluationTrendAlertNotificationDeliveryReceiptFromSuccess(event, plan, receipt, notifiedAt)
	if receipts != nil {
		if err := receipts.SaveModelRunEvaluationTrendAlertNotificationReceipt(ctx, deliveryReceipt); err != nil {
			return result, err
		}
	}
	if err := store.SaveModelRunEvaluationTrendAlertNotificationHistory(ctx, ModelRunEvaluationTrendAlertNotificationHistoryRecordFromEvent(event, notifiedAt)); err != nil {
		return result, err
	}
	result.Receipt = normalizeModelRunEvaluationTrendAlertNotificationReceipt(receipt)
	result.DeliveryReceipt = deliveryReceipt
	result.Delivered = true
	return result, nil
}

func ModelRunEvaluationTrendAlertNotificationHistoryRecordFromEvent(event ModelRunEvaluationTrendAlertAuditEvent, notifiedAt time.Time) ModelRunEvaluationTrendAlertNotificationHistoryRecord {
	if notifiedAt.IsZero() {
		notifiedAt = event.ObservedAt
	}
	return ModelRunEvaluationTrendAlertNotificationHistoryRecord{
		TenantID:               event.TenantID,
		RuleID:                 event.RuleID,
		Labels:                 cloneStringMetadata(event.Labels),
		LastNotificationStatus: event.Status,
		LastNotificationAt:     notifiedAt,
	}
}

func (r ModelRunEvaluationTrendAlertNotificationHistoryRecord) NotificationHistory() ModelRunEvaluationTrendAlertNotificationHistory {
	return ModelRunEvaluationTrendAlertNotificationHistory{
		LastNotificationStatus: r.LastNotificationStatus,
		LastNotificationAt:     r.LastNotificationAt,
	}
}

func normalizeModelRunEvaluationTrendAlertNotificationHistoryRecord(record ModelRunEvaluationTrendAlertNotificationHistoryRecord, now func() time.Time) (ModelRunEvaluationTrendAlertNotificationHistoryRecord, error) {
	record.ID = strings.TrimSpace(record.ID)
	record.TenantID = strings.TrimSpace(record.TenantID)
	record.RuleID = normalizeID(record.RuleID)
	record.Labels = cloneStringMetadata(record.Labels)
	record.LastNotificationStatus = ModelRunEvaluationTrendAlertStatus(strings.TrimSpace(string(record.LastNotificationStatus)))
	if record.TenantID == "" && record.Labels != nil {
		record.TenantID = strings.TrimSpace(record.Labels["tenant_id"])
	}
	if record.ID == "" && record.TenantID != "" && record.RuleID != "" {
		record.ID = modelRunEvaluationTrendAlertNotificationHistoryID(record.TenantID, record.RuleID, record.Labels)
	}
	if record.ID == "" || record.TenantID == "" || record.RuleID == "" || record.LastNotificationStatus == "" || record.LastNotificationAt.IsZero() {
		return ModelRunEvaluationTrendAlertNotificationHistoryRecord{}, fmt.Errorf("%w: model run evaluation trend alert notification history id, tenant_id, rule_id, status, and notified_at are required", ErrInvalidDefinition)
	}
	if record.LastNotificationStatus != ModelRunEvaluationTrendAlertFiring && record.LastNotificationStatus != ModelRunEvaluationTrendAlertResolved {
		return ModelRunEvaluationTrendAlertNotificationHistoryRecord{}, fmt.Errorf("%w: model run evaluation trend alert notification history status is not supported", ErrInvalidDefinition)
	}
	if now == nil {
		now = time.Now
	}
	at := now().UTC()
	record.LastNotificationAt = record.LastNotificationAt.UTC()
	if record.CreatedAt.IsZero() {
		record.CreatedAt = at
	} else {
		record.CreatedAt = record.CreatedAt.UTC()
	}
	record.UpdatedAt = at
	return record, nil
}

func modelRunEvaluationTrendAlertNotificationHistoryIDFromQuery(query ModelRunEvaluationTrendAlertNotificationHistoryQuery) (string, error) {
	tenantID := strings.TrimSpace(query.TenantID)
	ruleID := normalizeID(query.RuleID)
	labels := cloneStringMetadata(query.Labels)
	if tenantID == "" && labels != nil {
		tenantID = strings.TrimSpace(labels["tenant_id"])
	}
	if tenantID == "" || ruleID == "" {
		return "", fmt.Errorf("%w: model run evaluation trend alert notification history tenant_id and rule_id are required", ErrInvalidDefinition)
	}
	return modelRunEvaluationTrendAlertNotificationHistoryID(tenantID, ruleID, labels), nil
}

func modelRunEvaluationTrendAlertNotificationHistoryID(tenantID string, ruleID string, labels map[string]string) string {
	return "ontology_model_run_evaluation_alert_notification_history:" + hashMappingRule(map[string]any{
		"tenant_id": strings.TrimSpace(tenantID),
		"rule_id":   normalizeID(ruleID),
		"labels":    modelRunEvaluationTrendAlertNotificationHistoryKeyLabels(labels),
	})
}

func modelRunEvaluationTrendAlertNotificationHistoryKeyLabels(labels map[string]string) map[string]string {
	out := cloneStringMetadata(labels)
	delete(out, "snapshot_recorded_to")
	return out
}

func cloneModelRunEvaluationTrendAlertNotificationHistoryRecord(record ModelRunEvaluationTrendAlertNotificationHistoryRecord) ModelRunEvaluationTrendAlertNotificationHistoryRecord {
	record.Labels = cloneStringMetadata(record.Labels)
	return record
}

func scanModelRunEvaluationTrendAlertNotificationHistoryRecord(row rowScanner) (ModelRunEvaluationTrendAlertNotificationHistoryRecord, error) {
	record := ModelRunEvaluationTrendAlertNotificationHistoryRecord{}
	var labelsJSON string
	var lastNotificationAt, createdAt, updatedAt int64
	if err := row.Scan(&record.ID, &record.TenantID, &record.RuleID, &labelsJSON, &record.LastNotificationStatus, &lastNotificationAt, &createdAt, &updatedAt); err != nil {
		return ModelRunEvaluationTrendAlertNotificationHistoryRecord{}, err
	}
	mustDecodeJSON(labelsJSON, &record.Labels)
	record.Labels = cloneStringMetadata(record.Labels)
	record.LastNotificationAt = unixNanoToTime(lastNotificationAt)
	record.CreatedAt = unixNanoToTime(createdAt)
	record.UpdatedAt = unixNanoToTime(updatedAt)
	return record, nil
}

func normalizeModelRunEvaluationTrendAlertNotificationReceipt(receipt ModelRunEvaluationTrendAlertNotificationReceipt) ModelRunEvaluationTrendAlertNotificationReceipt {
	receipt.Channel = strings.TrimSpace(receipt.Channel)
	receipt.MessageID = strings.TrimSpace(receipt.MessageID)
	receipt.Metadata = cloneStringMetadata(receipt.Metadata)
	if !receipt.SentAt.IsZero() {
		receipt.SentAt = receipt.SentAt.UTC()
	}
	return receipt
}

func modelRunEvaluationTrendAlertNotificationLabelsMatch(labels map[string]string, required map[string]string) bool {
	if len(required) == 0 {
		return false
	}
	for key, value := range required {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if strings.TrimSpace(labels[key]) != strings.TrimSpace(value) {
			return false
		}
	}
	return true
}
