package ontology

import (
	"context"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/platform/clock"
)

const defaultModelRunEvaluationTrendAlertNotificationSweeperName = "ontology.model_run_evaluation_alert_notification.dispatcher"

type ModelRunEvaluationTrendAlertNotificationSweeperOptions struct {
	Name     string
	Alerts   ModelRunEvaluationTrendAlertAuditStore
	History  ModelRunEvaluationTrendAlertNotificationHistoryStore
	Receipts ModelRunEvaluationTrendAlertNotificationReceiptStore
	Sender   ModelRunEvaluationTrendAlertNotificationSender
	Clock    clock.Clock
	Policy   ModelRunEvaluationTrendAlertNotificationPolicy
	Policies ModelRunEvaluationTrendAlertNotificationPolicyStore
	Query    ModelRunEvaluationTrendAlertAuditQuery
}

type ModelRunEvaluationTrendAlertNotificationSweepReport struct {
	States       int `json:"states"`
	Delivered    int `json:"delivered"`
	Suppressed   int `json:"suppressed"`
	Deduplicated int `json:"deduplicated"`
}

type ModelRunEvaluationTrendAlertNotificationSweeper struct {
	name       string
	alerts     ModelRunEvaluationTrendAlertAuditStore
	history    ModelRunEvaluationTrendAlertNotificationHistoryStore
	receipts   ModelRunEvaluationTrendAlertNotificationReceiptStore
	sender     ModelRunEvaluationTrendAlertNotificationSender
	clock      clock.Clock
	policy     ModelRunEvaluationTrendAlertNotificationPolicy
	policies   ModelRunEvaluationTrendAlertNotificationPolicyStore
	query      ModelRunEvaluationTrendAlertAuditQuery
	lastReport ModelRunEvaluationTrendAlertNotificationSweepReport
}

func NewModelRunEvaluationTrendAlertNotificationSweeper(opts ModelRunEvaluationTrendAlertNotificationSweeperOptions) *ModelRunEvaluationTrendAlertNotificationSweeper {
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		name = defaultModelRunEvaluationTrendAlertNotificationSweeperName
	}
	sweepClock := opts.Clock
	if sweepClock == nil {
		sweepClock = clock.Real{}
	}
	return &ModelRunEvaluationTrendAlertNotificationSweeper{
		name:     name,
		alerts:   opts.Alerts,
		history:  opts.History,
		receipts: opts.Receipts,
		sender:   opts.Sender,
		clock:    sweepClock,
		policy:   opts.Policy,
		policies: opts.Policies,
		query:    opts.Query,
	}
}

func (s *ModelRunEvaluationTrendAlertNotificationSweeper) Name() string {
	if s == nil || strings.TrimSpace(s.name) == "" {
		return defaultModelRunEvaluationTrendAlertNotificationSweeperName
	}
	return s.name
}

func (s *ModelRunEvaluationTrendAlertNotificationSweeper) Sweep(ctx context.Context) error {
	if s == nil || s.alerts == nil || s.history == nil || s.sender == nil {
		return fmt.Errorf("%w: model run evaluation trend alert notification sweeper requires alerts, history, and sender", ErrInvalidDefinition)
	}
	now := s.now()
	page, err := s.alerts.ListModelRunEvaluationTrendAlertAuditCurrentStates(ctx, s.query)
	if err != nil {
		return err
	}
	report := ModelRunEvaluationTrendAlertNotificationSweepReport{States: len(page.States)}
	for _, event := range modelRunEvaluationTrendAlertNotificationSweeperEvents(page.States) {
		if err := ctx.Err(); err != nil {
			return err
		}
		result, err := s.dispatch(ctx, event, now)
		if err != nil {
			return err
		}
		if result.Delivered {
			report.Delivered++
		} else {
			report.Suppressed++
		}
	}
	report.Deduplicated = report.States - report.Delivered - report.Suppressed
	if report.Deduplicated < 0 {
		report.Deduplicated = 0
	}
	s.lastReport = report
	return nil
}

func (s *ModelRunEvaluationTrendAlertNotificationSweeper) dispatch(ctx context.Context, event ModelRunEvaluationTrendAlertAuditEvent, now time.Time) (ModelRunEvaluationTrendAlertNotificationDispatchResult, error) {
	policy, err := s.dispatchPolicy(ctx, event)
	if err != nil {
		return ModelRunEvaluationTrendAlertNotificationDispatchResult{}, err
	}
	if s.receipts != nil {
		return DispatchModelRunEvaluationTrendAlertNotificationWithReceipts(ctx, s.history, s.receipts, s.sender, event, policy, now)
	}
	return DispatchModelRunEvaluationTrendAlertNotification(ctx, s.history, s.sender, event, policy, now)
}

func (s *ModelRunEvaluationTrendAlertNotificationSweeper) LastReport() ModelRunEvaluationTrendAlertNotificationSweepReport {
	if s == nil {
		return ModelRunEvaluationTrendAlertNotificationSweepReport{}
	}
	return s.lastReport
}

func (s *ModelRunEvaluationTrendAlertNotificationSweeper) now() time.Time {
	if s == nil || s.clock == nil {
		return time.Now().UTC()
	}
	return s.clock.Now().UTC()
}

func (s *ModelRunEvaluationTrendAlertNotificationSweeper) dispatchPolicy(ctx context.Context, event ModelRunEvaluationTrendAlertAuditEvent) (ModelRunEvaluationTrendAlertNotificationPolicy, error) {
	policy := s.policy
	if s == nil || s.policies == nil {
		return policy, nil
	}
	page, err := s.policies.ListModelRunEvaluationTrendAlertNotificationPolicies(ctx, ModelRunEvaluationTrendAlertNotificationPolicyQuery{
		TenantID:    event.TenantID,
		RuleID:      event.RuleID,
		EnabledOnly: true,
		Limit:       1,
	})
	if err != nil {
		return ModelRunEvaluationTrendAlertNotificationPolicy{}, err
	}
	if len(page.Policies) == 0 {
		page, err = s.policies.ListModelRunEvaluationTrendAlertNotificationPolicies(ctx, ModelRunEvaluationTrendAlertNotificationPolicyQuery{
			TenantID:    event.TenantID,
			EnabledOnly: true,
			Limit:       1,
		})
		if err != nil {
			return ModelRunEvaluationTrendAlertNotificationPolicy{}, err
		}
	}
	if len(page.Policies) == 0 {
		return policy, nil
	}
	record := page.Policies[0]
	if record.RepeatWindow > 0 {
		policy.RepeatWindow = record.RepeatWindow
	}
	if record.RecoveryWindow > 0 {
		policy.RecoveryWindow = record.RecoveryWindow
	}
	return policy, nil
}

func modelRunEvaluationTrendAlertNotificationSweeperEvents(states []ModelRunEvaluationTrendAlertAuditState) []ModelRunEvaluationTrendAlertAuditEvent {
	latest := map[string]ModelRunEvaluationTrendAlertAuditEvent{}
	for _, state := range states {
		event := modelRunEvaluationTrendAlertNotificationEventFromState(state)
		key := modelRunEvaluationTrendAlertNotificationHistoryID(event.TenantID, event.RuleID, event.Labels)
		current, ok := latest[key]
		if !ok || event.ObservedAt.After(current.ObservedAt) || (event.ObservedAt.Equal(current.ObservedAt) && event.RuleID < current.RuleID) {
			latest[key] = event
		}
	}
	events := make([]ModelRunEvaluationTrendAlertAuditEvent, 0, len(latest))
	for _, event := range latest {
		events = append(events, event)
	}
	return events
}

func modelRunEvaluationTrendAlertNotificationEventFromState(state ModelRunEvaluationTrendAlertAuditState) ModelRunEvaluationTrendAlertAuditEvent {
	return ModelRunEvaluationTrendAlertAuditEvent{
		EventType:  "ontology.model_run_evaluation_alert." + string(state.Status),
		TenantID:   state.TenantID,
		RuleID:     state.RuleID,
		Severity:   state.Severity,
		Status:     state.Status,
		Labels:     cloneStringMetadata(state.Labels),
		Details:    cloneStringMetadata(state.Details),
		ObservedAt: state.ObservedAt,
		UpdatedAt:  state.UpdatedAt,
	}
}
