package ontology

import (
	"context"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/platform/clock"
)

const defaultModelRunEvaluationTrendAlertNotificationRetrySweeperName = "ontology.model_run_evaluation_alert_notification.retry"
const defaultModelRunEvaluationTrendAlertNotificationRetryMaxAttempts = 3

type ModelRunEvaluationTrendAlertNotificationRetrySweeperOptions struct {
	Name         string
	Receipts     ModelRunEvaluationTrendAlertNotificationReceiptStore
	History      ModelRunEvaluationTrendAlertNotificationHistoryStore
	Sender       ModelRunEvaluationTrendAlertNotificationSender
	Clock        clock.Clock
	MaxAttempts  int
	RetryBackoff time.Duration
	Query        ModelRunEvaluationTrendAlertNotificationReceiptQuery
}

type ModelRunEvaluationTrendAlertNotificationRetrySweepReport struct {
	Due       int `json:"due"`
	Attempted int `json:"attempted"`
	Delivered int `json:"delivered"`
	Failed    int `json:"failed"`
	Retryable int `json:"retryable"`
	Abandoned int `json:"abandoned"`
}

type ModelRunEvaluationTrendAlertNotificationRetrySweeper struct {
	name         string
	receipts     ModelRunEvaluationTrendAlertNotificationReceiptStore
	history      ModelRunEvaluationTrendAlertNotificationHistoryStore
	sender       ModelRunEvaluationTrendAlertNotificationSender
	clock        clock.Clock
	maxAttempts  int
	retryBackoff time.Duration
	query        ModelRunEvaluationTrendAlertNotificationReceiptQuery
	lastReport   ModelRunEvaluationTrendAlertNotificationRetrySweepReport
}

func NewModelRunEvaluationTrendAlertNotificationRetrySweeper(opts ModelRunEvaluationTrendAlertNotificationRetrySweeperOptions) *ModelRunEvaluationTrendAlertNotificationRetrySweeper {
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		name = defaultModelRunEvaluationTrendAlertNotificationRetrySweeperName
	}
	sweepClock := opts.Clock
	if sweepClock == nil {
		sweepClock = clock.Real{}
	}
	maxAttempts := opts.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = defaultModelRunEvaluationTrendAlertNotificationRetryMaxAttempts
	}
	return &ModelRunEvaluationTrendAlertNotificationRetrySweeper{
		name:         name,
		receipts:     opts.Receipts,
		history:      opts.History,
		sender:       opts.Sender,
		clock:        sweepClock,
		maxAttempts:  maxAttempts,
		retryBackoff: opts.RetryBackoff,
		query:        opts.Query,
	}
}

func (s *ModelRunEvaluationTrendAlertNotificationRetrySweeper) Name() string {
	if s == nil || strings.TrimSpace(s.name) == "" {
		return defaultModelRunEvaluationTrendAlertNotificationRetrySweeperName
	}
	return s.name
}

func (s *ModelRunEvaluationTrendAlertNotificationRetrySweeper) Sweep(ctx context.Context) error {
	if s == nil || s.receipts == nil {
		return fmt.Errorf("%w: model run evaluation trend alert notification retry sweeper requires receipt store", ErrInvalidDefinition)
	}
	now := s.now()
	query := s.query
	query.Status = ModelRunEvaluationTrendAlertNotificationDeliveryFailed
	query.Retryable = true
	query.NextRetryDueAt = now
	page, err := s.receipts.ListModelRunEvaluationTrendAlertNotificationReceipts(ctx, query)
	if err != nil {
		return err
	}
	report := ModelRunEvaluationTrendAlertNotificationRetrySweepReport{Due: len(page.Receipts)}
	for _, receipt := range page.Receipts {
		if err := ctx.Err(); err != nil {
			return err
		}
		if receipt.Attempt >= s.maxAttempts {
			if err := s.receipts.MarkModelRunEvaluationTrendAlertNotificationReceiptRetryAbandoned(ctx, receipt.ID, "max retry attempts reached", now); err != nil {
				return err
			}
			report.Abandoned++
			continue
		}
		if s.history == nil || s.sender == nil {
			report.Retryable++
			continue
		}
		report.Attempted++
		delivered, err := s.retry(ctx, receipt, now)
		if err != nil {
			return err
		}
		if delivered {
			report.Delivered++
		} else {
			report.Failed++
			if receipt.Attempt+1 < s.maxAttempts {
				report.Retryable++
			} else {
				report.Abandoned++
			}
		}
	}
	s.lastReport = report
	return nil
}

func (s *ModelRunEvaluationTrendAlertNotificationRetrySweeper) LastReport() ModelRunEvaluationTrendAlertNotificationRetrySweepReport {
	if s == nil {
		return ModelRunEvaluationTrendAlertNotificationRetrySweepReport{}
	}
	return s.lastReport
}

func (s *ModelRunEvaluationTrendAlertNotificationRetrySweeper) now() time.Time {
	if s == nil || s.clock == nil {
		return time.Now().UTC()
	}
	return s.clock.Now().UTC()
}

func (s *ModelRunEvaluationTrendAlertNotificationRetrySweeper) retry(ctx context.Context, receipt ModelRunEvaluationTrendAlertNotificationDeliveryReceipt, now time.Time) (bool, error) {
	event := modelRunEvaluationTrendAlertNotificationEventFromDeliveryReceipt(receipt)
	plan := ModelRunEvaluationTrendAlertNotificationPlan{
		Decision: ModelRunEvaluationTrendAlertNotificationDecision{
			Notify: true,
			Reason: receipt.Reason,
			Status: receipt.AlertStatus,
		},
	}
	sent, err := s.sender.SendModelRunEvaluationTrendAlertNotification(ctx, event, plan)
	if err != nil {
		if markErr := s.receipts.MarkModelRunEvaluationTrendAlertNotificationReceiptRetryConsumed(ctx, receipt.ID, now); markErr != nil {
			return false, markErr
		}
		next := modelRunEvaluationTrendAlertNotificationRetryFailedReceipt(receipt, now, receipt.Attempt+1, s.maxAttempts, s.retryBackoff, err)
		if saveErr := s.receipts.SaveModelRunEvaluationTrendAlertNotificationReceipt(ctx, next); saveErr != nil {
			return false, saveErr
		}
		return false, nil
	}
	notifiedAt := sent.SentAt
	if notifiedAt.IsZero() {
		notifiedAt = now
	}
	if err := s.receipts.MarkModelRunEvaluationTrendAlertNotificationReceiptRetryConsumed(ctx, receipt.ID, now); err != nil {
		return false, err
	}
	deliveryReceipt := modelRunEvaluationTrendAlertNotificationDeliveryReceiptFromSuccess(event, plan, sent, notifiedAt)
	deliveryReceipt.Attempt = receipt.Attempt + 1
	if err := s.receipts.SaveModelRunEvaluationTrendAlertNotificationReceipt(ctx, deliveryReceipt); err != nil {
		return false, err
	}
	if err := s.history.SaveModelRunEvaluationTrendAlertNotificationHistory(ctx, ModelRunEvaluationTrendAlertNotificationHistoryRecordFromEvent(event, notifiedAt)); err != nil {
		return false, err
	}
	return true, nil
}

func modelRunEvaluationTrendAlertNotificationEventFromDeliveryReceipt(receipt ModelRunEvaluationTrendAlertNotificationDeliveryReceipt) ModelRunEvaluationTrendAlertAuditEvent {
	observedAt := receipt.SentAt
	if observedAt.IsZero() {
		observedAt = receipt.UpdatedAt
	}
	return ModelRunEvaluationTrendAlertAuditEvent{
		EventType:  "ontology.model_run_evaluation_alert." + string(receipt.AlertStatus),
		TenantID:   receipt.TenantID,
		RuleID:     receipt.RuleID,
		Status:     receipt.AlertStatus,
		Labels:     cloneStringMetadata(receipt.Labels),
		ObservedAt: observedAt,
		UpdatedAt:  receipt.UpdatedAt,
	}
}

func modelRunEvaluationTrendAlertNotificationRetryFailedReceipt(receipt ModelRunEvaluationTrendAlertNotificationDeliveryReceipt, failedAt time.Time, attempt int, maxAttempts int, backoff time.Duration, cause error) ModelRunEvaluationTrendAlertNotificationDeliveryReceipt {
	message := ""
	if cause != nil {
		message = cause.Error()
	}
	nextRetryAt := time.Time{}
	retryable := attempt < maxAttempts
	if retryable {
		nextRetryAt = failedAt.UTC().Add(backoff)
	}
	return ModelRunEvaluationTrendAlertNotificationDeliveryReceipt{
		TenantID:     receipt.TenantID,
		RuleID:       receipt.RuleID,
		Status:       ModelRunEvaluationTrendAlertNotificationDeliveryFailed,
		AlertStatus:  receipt.AlertStatus,
		Reason:       receipt.Reason,
		Attempt:      attempt,
		Retryable:    retryable,
		NextRetryAt:  nextRetryAt,
		Labels:       cloneStringMetadata(receipt.Labels),
		ErrorMessage: message,
		SentAt:       failedAt,
	}
}
