package outbound

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	gwbindings "orrery_v2/internal/gateway/binding"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/outbox"
)

const (
	RecoveryCooldown      = 30 * time.Minute
	recoveryBatchAttempts = 4
	recoveryMaxAttempts   = 24
)

// ListByIdempotencyKey returns the original records without regenerating their
// content. A queued request is not evidence of successful delivery.
func (s *Service) ListByIdempotencyKey(ctx context.Context, tenantID, humanProfileID, key string) ([]Delivery, error) {
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(humanProfileID) == "" || strings.TrimSpace(key) == "" {
		return nil, ErrInvalidInput
	}
	if err := s.EnsureSchema(ctx); err != nil {
		return nil, err
	}
	rows, err := s.jobs.Query(ctx, `SELECT id, tenant_id, channel_key, account_id, binding_id, human_profile_id,
		external_tenant_id, recipient_id, event_type, title, body, message_format, idempotency_key, status, attempts,
		external_message_id, last_error, created_at, updated_at, delivered_at FROM gateway_message_deliveries
		WHERE tenant_id = ? AND human_profile_id = ? AND idempotency_key = ? ORDER BY channel_key`, tenantID, humanProfileID, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Delivery
	for rows.Next() {
		delivery, err := scanDelivery(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, delivery)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i, delivery := range result {
		current, err := s.reconcileTerminalOutboxState(ctx, delivery)
		if err != nil {
			return nil, err
		}
		result[i] = current
	}
	return result, nil
}

// RecoverDeadLetter is internal-only. Its caller must revalidate the business
// item and notification consent. It preserves the delivery ID, provider client
// ID, content and cumulative attempts; permanent failures are never reopened.
func (s *Service) RecoverDeadLetter(ctx context.Context, tenantID, humanProfileID, accountID, deliveryID string) (bool, error) {
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(humanProfileID) == "" || strings.TrimSpace(accountID) == "" || strings.TrimSpace(deliveryID) == "" {
		return false, ErrInvalidInput
	}
	if err := s.EnsureSchema(ctx); err != nil {
		return false, err
	}
	delivery, err := s.load(ctx, tenantID, deliveryID)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	now := s.now().UTC()
	if delivery.HumanProfileID != humanProfileID || delivery.AccountID != accountID || delivery.Status != "dead_letter" ||
		delivery.ExternalMessageID != "" || !delivery.DeliveredAt.IsZero() || now.Before(delivery.UpdatedAt.Add(RecoveryCooldown)) {
		return false, nil
	}
	if s.bindings == nil {
		return false, ErrBindingMissing
	}
	bindingCtx, cancel := context.WithTimeout(ctx, deliveryBindingTimeout)
	binding, err := s.bindings.Resolve(bindingCtx, gwbindings.ExternalIdentity{
		ChannelKey: delivery.ChannelKey, AccountID: accountID, ExternalTenantID: delivery.ExternalTenantID, ExternalUserID: delivery.RecipientID,
	})
	cancel()
	if errors.Is(err, gwbindings.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if binding.ID != delivery.BindingID || binding.TenantID != tenantID || binding.HumanProfileID != humanProfileID {
		return false, nil
	}
	recovered := false
	err = s.jobs.WithTx(ctx, func(tx db.Tx) error {
		// Reopen only an exhausted, fully released job. A live or stale sender
		// cannot race this transaction into a second delivery.
		var jobID string
		var attempts, maxAttempts int
		err := tx.QueryRowContext(ctx, `SELECT id, attempts, max_attempts FROM platform_outbox_jobs
			WHERE topic = ? AND idempotency_key = ? AND status = ?`, Topic, "gateway-delivery:"+delivery.ID, outbox.StatusDeadLetter).Scan(&jobID, &attempts, &maxAttempts)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if maxAttempts < 12 || maxAttempts >= recoveryMaxAttempts || attempts < maxAttempts || attempts >= recoveryMaxAttempts {
			return nil
		}
		nextBudget := attempts + recoveryBatchAttempts
		if nextBudget > recoveryMaxAttempts {
			nextBudget = recoveryMaxAttempts
		}
		result, err := tx.ExecContext(ctx, `UPDATE platform_outbox_jobs SET status = ?, max_attempts = ?,
			worker_id = NULL, locked_until = NULL, next_attempt_at = ?, updated_at = ?
			WHERE id = ? AND status = ? AND attempts = ? AND max_attempts = ?`,
			outbox.StatusPending, nextBudget, now.UnixNano(), now.UnixNano(), jobID, outbox.StatusDeadLetter, attempts, maxAttempts)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil || count == 0 {
			return err
		}
		result, err = tx.ExecContext(ctx, `UPDATE gateway_message_deliveries SET status = 'retrying', updated_at = ?
			WHERE id = ? AND tenant_id = ? AND human_profile_id = ? AND account_id = ? AND status = 'dead_letter'
			AND updated_at = ? AND external_message_id = '' AND delivered_at = 0`,
			now.UnixNano(), delivery.ID, tenantID, humanProfileID, accountID, delivery.UpdatedAt.UnixNano())
		if err != nil {
			return err
		}
		count, err = result.RowsAffected()
		if err != nil {
			return err
		}
		if count != 1 {
			return fmt.Errorf("gateway recovery lost its delivery claim")
		}
		recovered = true
		return nil
	})
	return recovered && err == nil, err
}
