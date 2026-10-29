package ontology

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"orrery_v2/internal/platform/db"
)

type ModelRunEvaluationTrendAlertMetric string
type ModelRunEvaluationTrendAlertStatus string

const (
	ModelRunEvaluationTrendAlertMetricRunTotal             ModelRunEvaluationTrendAlertMetric = "run_total"
	ModelRunEvaluationTrendAlertMetricBlockingTotal        ModelRunEvaluationTrendAlertMetric = "blocking_total"
	ModelRunEvaluationTrendAlertMetricWarningTotal         ModelRunEvaluationTrendAlertMetric = "warning_total"
	ModelRunEvaluationTrendAlertMetricGateWarningTotal     ModelRunEvaluationTrendAlertMetric = "gate_warning_total"
	ModelRunEvaluationTrendAlertMetricThresholdResultTotal ModelRunEvaluationTrendAlertMetric = "metric_threshold_result_total"
)

const (
	ModelRunEvaluationTrendAlertFiring   ModelRunEvaluationTrendAlertStatus = "firing"
	ModelRunEvaluationTrendAlertResolved ModelRunEvaluationTrendAlertStatus = "resolved"
)

type ModelRunEvaluationTrendAlertRule struct {
	RuleID                string                                  `json:"rule_id"`
	DisplayName           string                                  `json:"display_name,omitempty"`
	Severity              ModelRunEvaluationThresholdSeverity     `json:"severity,omitempty"`
	Metric                ModelRunEvaluationTrendAlertMetric      `json:"metric"`
	MetricThresholdRuleID string                                  `json:"metric_threshold_rule_id,omitempty"`
	ThresholdStatus       ModelRunEvaluationThresholdResultStatus `json:"threshold_status,omitempty"`
	Operator              ModelRunEvaluationMetricOperator        `json:"operator"`
	Threshold             float64                                 `json:"threshold"`
	ReasonCode            ModelRunEvaluationReasonCode            `json:"reason_code,omitempty"`
	WarningCode           ModelRunEvaluationReasonCode            `json:"warning_code,omitempty"`
}

type ModelRunEvaluationTrendAlertResult struct {
	RuleID        string                              `json:"rule_id"`
	DisplayName   string                              `json:"display_name,omitempty"`
	Severity      ModelRunEvaluationThresholdSeverity `json:"severity,omitempty"`
	Status        ModelRunEvaluationTrendAlertStatus  `json:"status"`
	Metric        ModelRunEvaluationTrendAlertMetric  `json:"metric"`
	Operator      ModelRunEvaluationMetricOperator    `json:"operator"`
	Threshold     float64                             `json:"threshold"`
	ObservedValue float64                             `json:"observed_value"`
	Labels        map[string]string                   `json:"labels,omitempty"`
}

type ModelRunEvaluationTrendAlertAuditEvent struct {
	ID         string                              `json:"id"`
	EventType  string                              `json:"event_type"`
	TenantID   string                              `json:"tenant_id,omitempty"`
	RuleID     string                              `json:"rule_id"`
	Severity   ModelRunEvaluationThresholdSeverity `json:"severity,omitempty"`
	Status     ModelRunEvaluationTrendAlertStatus  `json:"status"`
	Labels     map[string]string                   `json:"labels,omitempty"`
	Details    map[string]string                   `json:"details,omitempty"`
	ObservedAt time.Time                           `json:"observed_at,omitempty"`
	CreatedAt  time.Time                           `json:"created_at,omitempty"`
	UpdatedAt  time.Time                           `json:"updated_at,omitempty"`
}

type ModelRunEvaluationTrendAlertAuditQuery struct {
	TenantID     string
	RuleID       string
	Status       ModelRunEvaluationTrendAlertStatus
	LabelKey     string
	LabelValue   string
	ObservedFrom time.Time
	ObservedTo   time.Time
	Limit        int
	Offset       int
}

type ModelRunEvaluationTrendAlertAuditPage struct {
	Events []ModelRunEvaluationTrendAlertAuditEvent `json:"events"`
	Page   Page                                     `json:"page"`
}

type ModelRunEvaluationTrendAlertAuditState struct {
	TenantID   string                              `json:"tenant_id,omitempty"`
	RuleID     string                              `json:"rule_id"`
	Severity   ModelRunEvaluationThresholdSeverity `json:"severity,omitempty"`
	Status     ModelRunEvaluationTrendAlertStatus  `json:"status"`
	Labels     map[string]string                   `json:"labels,omitempty"`
	Details    map[string]string                   `json:"details,omitempty"`
	ObservedAt time.Time                           `json:"observed_at,omitempty"`
	UpdatedAt  time.Time                           `json:"updated_at,omitempty"`
}

type ModelRunEvaluationTrendAlertAuditStatePage struct {
	States []ModelRunEvaluationTrendAlertAuditState `json:"states"`
	Page   Page                                     `json:"page"`
}

type ModelRunEvaluationTrendAlertAuditStore interface {
	SaveModelRunEvaluationTrendAlertAuditEvent(ctx context.Context, event ModelRunEvaluationTrendAlertAuditEvent) error
	ListModelRunEvaluationTrendAlertAuditEvents(ctx context.Context, query ModelRunEvaluationTrendAlertAuditQuery) (ModelRunEvaluationTrendAlertAuditPage, error)
	ListModelRunEvaluationTrendAlertAuditCurrentStates(ctx context.Context, query ModelRunEvaluationTrendAlertAuditQuery) (ModelRunEvaluationTrendAlertAuditStatePage, error)
}

type MemoryModelRunEvaluationTrendAlertAuditStore struct {
	mu     sync.RWMutex
	events map[string]ModelRunEvaluationTrendAlertAuditEvent
	now    func() time.Time
}

func NewMemoryModelRunEvaluationTrendAlertAuditStore() *MemoryModelRunEvaluationTrendAlertAuditStore {
	return &MemoryModelRunEvaluationTrendAlertAuditStore{events: map[string]ModelRunEvaluationTrendAlertAuditEvent{}, now: time.Now}
}

func (s *MemoryModelRunEvaluationTrendAlertAuditStore) SaveModelRunEvaluationTrendAlertAuditEvent(ctx context.Context, event ModelRunEvaluationTrendAlertAuditEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	normalized, err := normalizeModelRunEvaluationTrendAlertAuditEvent(event, s.now)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if previous, ok := s.events[normalized.ID]; ok && !previous.CreatedAt.IsZero() {
		normalized.CreatedAt = previous.CreatedAt
	}
	s.events[normalized.ID] = normalized
	return nil
}

func (s *MemoryModelRunEvaluationTrendAlertAuditStore) ListModelRunEvaluationTrendAlertAuditEvents(ctx context.Context, query ModelRunEvaluationTrendAlertAuditQuery) (ModelRunEvaluationTrendAlertAuditPage, error) {
	if err := ctx.Err(); err != nil {
		return ModelRunEvaluationTrendAlertAuditPage{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	matched := make([]ModelRunEvaluationTrendAlertAuditEvent, 0, len(s.events))
	for _, event := range s.events {
		if modelRunEvaluationTrendAlertAuditEventMatches(event, query) {
			matched = append(matched, cloneModelRunEvaluationTrendAlertAuditEvent(event))
		}
	}
	sort.Slice(matched, func(i, j int) bool {
		if matched[i].ObservedAt.Equal(matched[j].ObservedAt) {
			return matched[i].ID < matched[j].ID
		}
		return matched[i].ObservedAt.After(matched[j].ObservedAt)
	})
	return modelRunEvaluationTrendAlertAuditPage(matched, query), nil
}

func (s *MemoryModelRunEvaluationTrendAlertAuditStore) ListModelRunEvaluationTrendAlertAuditCurrentStates(ctx context.Context, query ModelRunEvaluationTrendAlertAuditQuery) (ModelRunEvaluationTrendAlertAuditStatePage, error) {
	if err := ctx.Err(); err != nil {
		return ModelRunEvaluationTrendAlertAuditStatePage{}, err
	}
	eventQuery := query
	eventQuery.Status = ""
	s.mu.RLock()
	defer s.mu.RUnlock()
	events := make([]ModelRunEvaluationTrendAlertAuditEvent, 0, len(s.events))
	for _, event := range s.events {
		if modelRunEvaluationTrendAlertAuditEventMatches(event, eventQuery) {
			events = append(events, cloneModelRunEvaluationTrendAlertAuditEvent(event))
		}
	}
	return modelRunEvaluationTrendAlertAuditStatePage(events, query), nil
}

type PlatformModelRunEvaluationTrendAlertAuditStore struct {
	store *db.Store
	tx    db.Tx
	now   func() time.Time
}

func NewPlatformModelRunEvaluationTrendAlertAuditStore(store *db.Store) *PlatformModelRunEvaluationTrendAlertAuditStore {
	return &PlatformModelRunEvaluationTrendAlertAuditStore{store: store, now: time.Now}
}

func NewPlatformModelRunEvaluationTrendAlertAuditStoreTx(tx db.Tx) *PlatformModelRunEvaluationTrendAlertAuditStore {
	return &PlatformModelRunEvaluationTrendAlertAuditStore{tx: tx, now: time.Now}
}

func (s *PlatformModelRunEvaluationTrendAlertAuditStore) ready() error {
	if s == nil || (s.store == nil && s.tx == nil) {
		return fmt.Errorf("%w: model run evaluation trend alert audit store is required", ErrInvalidDefinition)
	}
	return nil
}

func (s *PlatformModelRunEvaluationTrendAlertAuditStore) exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if s.tx != nil {
		return s.tx.ExecContext(ctx, query, args...)
	}
	return s.store.Exec(ctx, query, args...)
}

func (s *PlatformModelRunEvaluationTrendAlertAuditStore) query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if s.tx != nil {
		return s.tx.QueryContext(ctx, query, args...)
	}
	return s.store.Query(ctx, query, args...)
}

func (s *PlatformModelRunEvaluationTrendAlertAuditStore) queryRow(ctx context.Context, query string, args ...any) rowScanner {
	if s.tx != nil {
		return s.tx.QueryRowContext(ctx, query, args...)
	}
	return s.store.QueryRow(ctx, query, args...)
}

func (s *PlatformModelRunEvaluationTrendAlertAuditStore) dialect() db.Dialect {
	if s != nil && s.tx != nil {
		if dialecter, ok := s.tx.(interface{ Dialect() db.Dialect }); ok {
			return dialecter.Dialect()
		}
	}
	if s != nil && s.store != nil {
		return s.store.Dialect()
	}
	return db.DialectSQLite
}

func (s *PlatformModelRunEvaluationTrendAlertAuditStore) SaveModelRunEvaluationTrendAlertAuditEvent(ctx context.Context, event ModelRunEvaluationTrendAlertAuditEvent) error {
	if err := s.ready(); err != nil {
		return err
	}
	normalized, err := normalizeModelRunEvaluationTrendAlertAuditEvent(event, s.now)
	if err != nil {
		return err
	}
	_, err = s.exec(ctx, `
INSERT INTO ontology_model_run_evaluation_alert_audits (
	id, tenant_id, event_type, rule_id, severity, status, labels_json, details_json, observed_at, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	event_type = excluded.event_type,
	rule_id = excluded.rule_id,
	severity = excluded.severity,
	status = excluded.status,
	labels_json = excluded.labels_json,
	details_json = excluded.details_json,
	observed_at = excluded.observed_at,
	updated_at = excluded.updated_at`,
		normalized.ID,
		normalized.TenantID,
		normalized.EventType,
		normalized.RuleID,
		normalized.Severity,
		normalized.Status,
		mustJSON(normalized.Labels),
		mustJSON(normalized.Details),
		timeToUnixNano(normalized.ObservedAt),
		timeToUnixNano(normalized.CreatedAt),
		timeToUnixNano(normalized.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("save ontology model run evaluation alert audit: %w", err)
	}
	return nil
}

func (s *PlatformModelRunEvaluationTrendAlertAuditStore) ListModelRunEvaluationTrendAlertAuditEvents(ctx context.Context, query ModelRunEvaluationTrendAlertAuditQuery) (ModelRunEvaluationTrendAlertAuditPage, error) {
	if err := s.ready(); err != nil {
		return ModelRunEvaluationTrendAlertAuditPage{}, err
	}
	limit := normalizeMetadataProposalLimit(query.Limit)
	offset := normalizeMetadataProposalOffset(query.Offset)
	where, args := modelRunEvaluationTrendAlertAuditWhere(query, s.dialect())
	var total int
	if err := s.queryRow(ctx, `SELECT COUNT(*) FROM ontology_model_run_evaluation_alert_audits`+where, args...).Scan(&total); err != nil {
		return ModelRunEvaluationTrendAlertAuditPage{}, fmt.Errorf("count ontology model run evaluation alert audits: %w", err)
	}
	rows, err := s.query(ctx, `SELECT id, tenant_id, event_type, rule_id, severity, status, labels_json, details_json,
observed_at, created_at, updated_at FROM ontology_model_run_evaluation_alert_audits`+where+` ORDER BY observed_at DESC, id ASC LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	if err != nil {
		return ModelRunEvaluationTrendAlertAuditPage{}, fmt.Errorf("list ontology model run evaluation alert audits: %w", err)
	}
	defer rows.Close()
	events := []ModelRunEvaluationTrendAlertAuditEvent{}
	for rows.Next() {
		event, err := scanModelRunEvaluationTrendAlertAuditEvent(rows)
		if err != nil {
			return ModelRunEvaluationTrendAlertAuditPage{}, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return ModelRunEvaluationTrendAlertAuditPage{}, err
	}
	return ModelRunEvaluationTrendAlertAuditPage{
		Events: events,
		Page:   Page{Limit: limit, Offset: offset, Total: total, Returned: len(events), HasMore: offset+len(events) < total},
	}, nil
}

func (s *PlatformModelRunEvaluationTrendAlertAuditStore) ListModelRunEvaluationTrendAlertAuditCurrentStates(ctx context.Context, query ModelRunEvaluationTrendAlertAuditQuery) (ModelRunEvaluationTrendAlertAuditStatePage, error) {
	if err := s.ready(); err != nil {
		return ModelRunEvaluationTrendAlertAuditStatePage{}, err
	}
	events := []ModelRunEvaluationTrendAlertAuditEvent{}
	scanQuery := query
	scanQuery.Limit = 200
	scanQuery.Offset = 0
	scanQuery.Status = ""
	for {
		page, err := s.ListModelRunEvaluationTrendAlertAuditEvents(ctx, scanQuery)
		if err != nil {
			return ModelRunEvaluationTrendAlertAuditStatePage{}, err
		}
		events = append(events, page.Events...)
		if !page.Page.HasMore {
			break
		}
		scanQuery.Offset += page.Page.Returned
		if page.Page.Returned == 0 {
			break
		}
	}
	return modelRunEvaluationTrendAlertAuditStatePage(events, query), nil
}

func (s *PlatformModelRunEvaluationTrendAlertAuditStore) StatusCounts(ctx context.Context) (map[string]int64, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	rows, err := s.query(ctx, `SELECT status, COUNT(*) FROM ontology_model_run_evaluation_alert_audits GROUP BY status`)
	if err != nil {
		return nil, fmt.Errorf("count ontology model run evaluation alert audits by status: %w", err)
	}
	defer rows.Close()
	counts := map[string]int64{}
	for rows.Next() {
		var status string
		var count int64
		if err := rows.Scan(&status, &count); err != nil {
			return nil, fmt.Errorf("scan ontology model run evaluation alert audit status count: %w", err)
		}
		counts[status] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate ontology model run evaluation alert audit status counts: %w", err)
	}
	return counts, nil
}

func EvaluateModelRunEvaluationTrendAlertRules(page ModelRunEvaluationTrendPage, rules []ModelRunEvaluationTrendAlertRule) []ModelRunEvaluationTrendAlertResult {
	results := []ModelRunEvaluationTrendAlertResult{}
	for _, bucket := range page.Buckets {
		baseLabels := modelRunEvaluationTrendMetricLabels(bucket, page.SnapshotRecordedTo)
		for _, rule := range rules {
			normalized, ok := normalizeModelRunEvaluationTrendAlertRule(rule)
			if !ok {
				continue
			}
			value := modelRunEvaluationTrendAlertObservedValue(bucket, normalized)
			labels := cloneMetricLabels(baseLabels)
			if normalized.ReasonCode != "" {
				labels["reason"] = string(normalized.ReasonCode)
			}
			if normalized.WarningCode != "" {
				labels["warning"] = string(normalized.WarningCode)
			}
			if normalized.MetricThresholdRuleID != "" {
				labels["metric_threshold_rule_id"] = normalized.MetricThresholdRuleID
			}
			if normalized.ThresholdStatus != "" {
				labels["threshold_status"] = string(normalized.ThresholdStatus)
			}
			status := ModelRunEvaluationTrendAlertResolved
			if modelRunEvaluationMetricThresholdPassed(value, ModelRunEvaluationMetricThreshold{
				Operator:  normalized.Operator,
				Threshold: normalized.Threshold,
			}) {
				status = ModelRunEvaluationTrendAlertFiring
			}
			results = append(results, ModelRunEvaluationTrendAlertResult{
				RuleID:        normalized.RuleID,
				DisplayName:   normalized.DisplayName,
				Severity:      normalized.Severity,
				Status:        status,
				Metric:        normalized.Metric,
				Operator:      normalized.Operator,
				Threshold:     normalized.Threshold,
				ObservedValue: value,
				Labels:        labels,
			})
		}
	}
	return results
}

func BuildModelRunEvaluationTrendAlertRulesFromPolicyProfile(profile ModelRunEvaluationPolicyProfile, minFailures int) []ModelRunEvaluationTrendAlertRule {
	if minFailures <= 0 {
		minFailures = 1
	}
	rules := []ModelRunEvaluationTrendAlertRule{}
	for _, threshold := range normalizeModelRunEvaluationMetricThresholds(profile.MetricThresholds) {
		if threshold.RuleID == "" {
			continue
		}
		rule := ModelRunEvaluationTrendAlertRule{
			RuleID:                threshold.RuleID + ".trend_failure",
			DisplayName:           modelRunEvaluationThresholdAlertDisplayName(threshold),
			Severity:              threshold.Severity,
			Metric:                ModelRunEvaluationTrendAlertMetricThresholdResultTotal,
			MetricThresholdRuleID: threshold.RuleID,
			ThresholdStatus:       ModelRunEvaluationThresholdResultFailed,
			Operator:              ModelRunEvaluationMetricOperatorGTE,
			Threshold:             float64(minFailures),
		}
		if threshold.Blocking {
			rule.ReasonCode = ModelRunEvaluationReasonMetricThresholdFailed
		} else {
			rule.WarningCode = ModelRunEvaluationReasonMetricThresholdFailed
		}
		rules = append(rules, rule)
	}
	return rules
}

func BuildModelRunEvaluationTrendAlertAuditEvents(results []ModelRunEvaluationTrendAlertResult, observedAt time.Time) []ModelRunEvaluationTrendAlertAuditEvent {
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	} else {
		observedAt = observedAt.UTC()
	}
	events := []ModelRunEvaluationTrendAlertAuditEvent{}
	for _, result := range results {
		if strings.TrimSpace(result.RuleID) == "" || result.Status == "" {
			continue
		}
		labels := cloneStringMetadata(result.Labels)
		details := map[string]string{
			"metric":         string(result.Metric),
			"operator":       string(result.Operator),
			"threshold":      strconv.FormatFloat(result.Threshold, 'f', -1, 64),
			"observed_value": strconv.FormatFloat(result.ObservedValue, 'f', -1, 64),
		}
		if result.DisplayName != "" {
			details["display_name"] = result.DisplayName
		}
		if labels["metric_threshold_rule_id"] != "" {
			details["metric_threshold_rule_id"] = labels["metric_threshold_rule_id"]
		}
		if labels["threshold_status"] != "" {
			details["threshold_status"] = labels["threshold_status"]
		}
		events = append(events, ModelRunEvaluationTrendAlertAuditEvent{
			ID:         modelRunEvaluationTrendAlertAuditEventID(result, labels, observedAt),
			EventType:  "ontology.model_run_evaluation_alert." + string(result.Status),
			TenantID:   labels["tenant_id"],
			RuleID:     result.RuleID,
			Severity:   result.Severity,
			Status:     result.Status,
			Labels:     labels,
			Details:    details,
			ObservedAt: observedAt,
		})
	}
	return events
}

func RecordModelRunEvaluationTrendAlertAudits(ctx context.Context, ledger ModelRunLedger, artifacts ModelRunEvaluationArtifactStore, policies ModelRunEvaluationPolicyProfileStore, store ModelRunEvaluationTrendAlertAuditStore, query ModelRunEvaluationTrendQuery, rules []ModelRunEvaluationTrendAlertRule, observedAt time.Time) (int, ModelRunEvaluationTrendPage, error) {
	page, err := ListModelRunEvaluationTrendsWithEvaluationPolicyProfiles(ctx, ledger, artifacts, policies, query)
	if err != nil {
		return 0, ModelRunEvaluationTrendPage{}, err
	}
	if store == nil {
		return 0, page, nil
	}
	recorded := 0
	for _, event := range BuildModelRunEvaluationTrendAlertAuditEvents(EvaluateModelRunEvaluationTrendAlertRules(page, rules), observedAt) {
		if err := ctx.Err(); err != nil {
			return recorded, page, err
		}
		if err := store.SaveModelRunEvaluationTrendAlertAuditEvent(ctx, event); err != nil {
			return recorded, page, err
		}
		recorded++
	}
	return recorded, page, nil
}

func modelRunEvaluationThresholdAlertDisplayName(threshold ModelRunEvaluationMetricThreshold) string {
	if strings.TrimSpace(threshold.DisplayName) != "" {
		return strings.TrimSpace(threshold.DisplayName) + " trend failures"
	}
	if strings.TrimSpace(threshold.MetricKey) != "" {
		return strings.TrimSpace(threshold.MetricKey) + " trend failures"
	}
	return "Metric threshold trend failures"
}

func normalizeModelRunEvaluationTrendAlertAuditEvent(event ModelRunEvaluationTrendAlertAuditEvent, now func() time.Time) (ModelRunEvaluationTrendAlertAuditEvent, error) {
	event.ID = strings.TrimSpace(event.ID)
	event.EventType = normalizeID(event.EventType)
	event.TenantID = strings.TrimSpace(event.TenantID)
	event.RuleID = normalizeID(event.RuleID)
	event.Severity = ModelRunEvaluationThresholdSeverity(strings.TrimSpace(string(event.Severity)))
	event.Status = ModelRunEvaluationTrendAlertStatus(strings.TrimSpace(string(event.Status)))
	event.Labels = cloneStringMetadata(event.Labels)
	event.Details = cloneStringMetadata(event.Details)
	if event.TenantID == "" && event.Labels != nil {
		event.TenantID = strings.TrimSpace(event.Labels["tenant_id"])
	}
	if event.ID == "" && event.RuleID != "" && event.Status != "" {
		event.ID = modelRunEvaluationTrendAlertAuditEventID(ModelRunEvaluationTrendAlertResult{
			RuleID:        event.RuleID,
			Severity:      event.Severity,
			Status:        event.Status,
			ObservedValue: parseModelRunEvaluationAlertAuditFloat(event.Details["observed_value"]),
			Labels:        event.Labels,
		}, event.Labels, event.ObservedAt)
	}
	if event.ID == "" || event.EventType == "" || event.TenantID == "" || event.RuleID == "" || event.Status == "" {
		return ModelRunEvaluationTrendAlertAuditEvent{}, fmt.Errorf("%w: model run evaluation trend alert audit id, event_type, tenant_id, rule_id, and status are required", ErrInvalidDefinition)
	}
	if event.Severity != "" && !modelRunEvaluationThresholdSeverityValid(event.Severity) {
		return ModelRunEvaluationTrendAlertAuditEvent{}, fmt.Errorf("%w: model run evaluation trend alert audit severity is not supported", ErrInvalidDefinition)
	}
	if event.Status != ModelRunEvaluationTrendAlertFiring && event.Status != ModelRunEvaluationTrendAlertResolved {
		return ModelRunEvaluationTrendAlertAuditEvent{}, fmt.Errorf("%w: model run evaluation trend alert audit status is not supported", ErrInvalidDefinition)
	}
	if now == nil {
		now = time.Now
	}
	at := now().UTC()
	if event.ObservedAt.IsZero() {
		event.ObservedAt = at
	} else {
		event.ObservedAt = event.ObservedAt.UTC()
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = at
	}
	event.UpdatedAt = at
	return event, nil
}

func normalizeModelRunEvaluationTrendAlertRule(rule ModelRunEvaluationTrendAlertRule) (ModelRunEvaluationTrendAlertRule, bool) {
	rule.RuleID = normalizeID(rule.RuleID)
	rule.DisplayName = strings.TrimSpace(rule.DisplayName)
	rule.Severity = ModelRunEvaluationThresholdSeverity(strings.TrimSpace(string(rule.Severity)))
	rule.Metric = ModelRunEvaluationTrendAlertMetric(normalizeID(string(rule.Metric)))
	rule.MetricThresholdRuleID = normalizeID(rule.MetricThresholdRuleID)
	rule.ThresholdStatus = ModelRunEvaluationThresholdResultStatus(strings.TrimSpace(string(rule.ThresholdStatus)))
	rule.Operator = ModelRunEvaluationMetricOperator(strings.TrimSpace(string(rule.Operator)))
	rule.ReasonCode = ModelRunEvaluationReasonCode(normalizeID(string(rule.ReasonCode)))
	rule.WarningCode = ModelRunEvaluationReasonCode(normalizeID(string(rule.WarningCode)))
	if rule.Severity == "" {
		rule.Severity = ModelRunEvaluationThresholdSeverityWarning
	}
	return rule, rule.RuleID != "" && modelRunEvaluationTrendAlertMetricValid(rule.Metric) && modelRunEvaluationTrendAlertRuleMetricScopeValid(rule) && modelRunEvaluationMetricOperatorValid(rule.Operator)
}

func modelRunEvaluationTrendAlertMetricValid(metric ModelRunEvaluationTrendAlertMetric) bool {
	switch metric {
	case ModelRunEvaluationTrendAlertMetricRunTotal, ModelRunEvaluationTrendAlertMetricBlockingTotal, ModelRunEvaluationTrendAlertMetricWarningTotal, ModelRunEvaluationTrendAlertMetricGateWarningTotal, ModelRunEvaluationTrendAlertMetricThresholdResultTotal:
		return true
	default:
		return false
	}
}

func modelRunEvaluationTrendAlertRuleMetricScopeValid(rule ModelRunEvaluationTrendAlertRule) bool {
	if rule.Metric != ModelRunEvaluationTrendAlertMetricThresholdResultTotal {
		return true
	}
	return rule.MetricThresholdRuleID != "" && modelRunEvaluationThresholdResultStatusValid(rule.ThresholdStatus)
}

func modelRunEvaluationThresholdResultStatusValid(status ModelRunEvaluationThresholdResultStatus) bool {
	switch status {
	case ModelRunEvaluationThresholdResultPassed, ModelRunEvaluationThresholdResultFailed, ModelRunEvaluationThresholdResultMissing, ModelRunEvaluationThresholdResultInvalid:
		return true
	default:
		return false
	}
}

func modelRunEvaluationTrendAlertAuditEventID(result ModelRunEvaluationTrendAlertResult, labels map[string]string, observedAt time.Time) string {
	return "ontology_model_run_evaluation_alert_audit:" + hashMappingRule(map[string]any{
		"rule_id":                  result.RuleID,
		"status":                   string(result.Status),
		"tenant_id":                labels["tenant_id"],
		"engine":                   labels["engine"],
		"model":                    labels["model"],
		"model_version":            labels["model_version"],
		"reason":                   labels["reason"],
		"warning":                  labels["warning"],
		"metric_threshold_rule_id": labels["metric_threshold_rule_id"],
		"threshold_status":         labels["threshold_status"],
		"snapshot_recorded_to":     labels["snapshot_recorded_to"],
		"observed_at":              observedAt.Format(time.RFC3339Nano),
		"observed_value":           strconv.FormatFloat(result.ObservedValue, 'f', -1, 64),
	})
}

func modelRunEvaluationTrendAlertObservedValue(bucket ModelRunEvaluationTrendBucket, rule ModelRunEvaluationTrendAlertRule) float64 {
	if rule.Metric == ModelRunEvaluationTrendAlertMetricThresholdResultTotal {
		return float64(bucket.MetricThresholdResultCounts[rule.MetricThresholdRuleID][rule.ThresholdStatus])
	}
	if rule.ReasonCode != "" {
		return float64(bucket.ReasonCounts[rule.ReasonCode])
	}
	if rule.WarningCode != "" {
		return float64(bucket.WarningCounts[rule.WarningCode])
	}
	switch rule.Metric {
	case ModelRunEvaluationTrendAlertMetricRunTotal:
		return float64(bucket.RunTotal)
	case ModelRunEvaluationTrendAlertMetricBlockingTotal:
		return float64(bucket.BlockingTotal)
	case ModelRunEvaluationTrendAlertMetricWarningTotal:
		return float64(bucket.WarningTotal)
	case ModelRunEvaluationTrendAlertMetricGateWarningTotal:
		return float64(bucket.GateWarningTotal)
	default:
		return 0
	}
}

func modelRunEvaluationTrendAlertAuditEventMatches(event ModelRunEvaluationTrendAlertAuditEvent, query ModelRunEvaluationTrendAlertAuditQuery) bool {
	return (strings.TrimSpace(query.TenantID) == "" || event.TenantID == strings.TrimSpace(query.TenantID)) &&
		(normalizeID(query.RuleID) == "" || event.RuleID == normalizeID(query.RuleID)) &&
		(strings.TrimSpace(string(query.Status)) == "" || event.Status == ModelRunEvaluationTrendAlertStatus(strings.TrimSpace(string(query.Status)))) &&
		modelRunEvaluationTrendAlertAuditLabelMatches(event.Labels, query) &&
		(query.ObservedFrom.IsZero() || !event.ObservedAt.Before(query.ObservedFrom.UTC())) &&
		(query.ObservedTo.IsZero() || event.ObservedAt.Before(query.ObservedTo.UTC()))
}

func modelRunEvaluationTrendAlertAuditWhere(query ModelRunEvaluationTrendAlertAuditQuery, dialect db.Dialect) (string, []any) {
	clauses := []string{}
	args := []any{}
	if tenantID := strings.TrimSpace(query.TenantID); tenantID != "" {
		clauses = append(clauses, "tenant_id = ?")
		args = append(args, tenantID)
	}
	if ruleID := normalizeID(query.RuleID); ruleID != "" {
		clauses = append(clauses, "rule_id = ?")
		args = append(args, ruleID)
	}
	if status := strings.TrimSpace(string(query.Status)); status != "" {
		clauses = append(clauses, "status = ?")
		args = append(args, status)
	}
	if labelKey := strings.TrimSpace(query.LabelKey); labelKey != "" {
		if dialect == db.DialectPostgres {
			clauses = append(clauses, "labels_json ->> ? = ?")
		} else {
			clauses = append(clauses, "json_extract(labels_json, ?) = ?")
			labelKey = "$." + labelKey
		}
		args = append(args, labelKey, strings.TrimSpace(query.LabelValue))
	}
	if !query.ObservedFrom.IsZero() {
		clauses = append(clauses, "observed_at >= ?")
		args = append(args, timeToUnixNano(query.ObservedFrom.UTC()))
	}
	if !query.ObservedTo.IsZero() {
		clauses = append(clauses, "observed_at < ?")
		args = append(args, timeToUnixNano(query.ObservedTo.UTC()))
	}
	if len(clauses) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

func modelRunEvaluationTrendAlertAuditLabelMatches(labels map[string]string, query ModelRunEvaluationTrendAlertAuditQuery) bool {
	labelKey := strings.TrimSpace(query.LabelKey)
	if labelKey == "" {
		return true
	}
	value, ok := labels[labelKey]
	return ok && strings.TrimSpace(value) == strings.TrimSpace(query.LabelValue)
}

func modelRunEvaluationTrendAlertAuditPage(events []ModelRunEvaluationTrendAlertAuditEvent, query ModelRunEvaluationTrendAlertAuditQuery) ModelRunEvaluationTrendAlertAuditPage {
	limit := normalizeMetadataProposalLimit(query.Limit)
	offset := normalizeMetadataProposalOffset(query.Offset)
	total := len(events)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	items := append([]ModelRunEvaluationTrendAlertAuditEvent(nil), events[offset:end]...)
	return ModelRunEvaluationTrendAlertAuditPage{Events: items, Page: Page{Limit: limit, Offset: offset, Total: total, Returned: len(items), HasMore: end < total}}
}

func modelRunEvaluationTrendAlertAuditStatePage(events []ModelRunEvaluationTrendAlertAuditEvent, query ModelRunEvaluationTrendAlertAuditQuery) ModelRunEvaluationTrendAlertAuditStatePage {
	latest := map[string]ModelRunEvaluationTrendAlertAuditEvent{}
	for _, event := range events {
		key := modelRunEvaluationTrendAlertAuditStateKey(event)
		current, ok := latest[key]
		if !ok || event.ObservedAt.After(current.ObservedAt) || (event.ObservedAt.Equal(current.ObservedAt) && event.ID < current.ID) {
			latest[key] = event
		}
	}
	states := make([]ModelRunEvaluationTrendAlertAuditState, 0, len(latest))
	for _, event := range latest {
		if strings.TrimSpace(string(query.Status)) != "" && event.Status != ModelRunEvaluationTrendAlertStatus(strings.TrimSpace(string(query.Status))) {
			continue
		}
		states = append(states, modelRunEvaluationTrendAlertAuditStateFromEvent(event))
	}
	sort.Slice(states, func(i, j int) bool {
		if states[i].ObservedAt.Equal(states[j].ObservedAt) {
			if states[i].RuleID == states[j].RuleID {
				return mustJSON(states[i].Labels) < mustJSON(states[j].Labels)
			}
			return states[i].RuleID < states[j].RuleID
		}
		return states[i].ObservedAt.After(states[j].ObservedAt)
	})
	limit := normalizeMetadataProposalLimit(query.Limit)
	offset := normalizeMetadataProposalOffset(query.Offset)
	total := len(states)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	items := append([]ModelRunEvaluationTrendAlertAuditState(nil), states[offset:end]...)
	return ModelRunEvaluationTrendAlertAuditStatePage{States: items, Page: Page{Limit: limit, Offset: offset, Total: total, Returned: len(items), HasMore: end < total}}
}

func modelRunEvaluationTrendAlertAuditStateKey(event ModelRunEvaluationTrendAlertAuditEvent) string {
	return event.TenantID + "\x00" + event.RuleID + "\x00" + mustJSON(event.Labels)
}

func modelRunEvaluationTrendAlertAuditStateFromEvent(event ModelRunEvaluationTrendAlertAuditEvent) ModelRunEvaluationTrendAlertAuditState {
	return ModelRunEvaluationTrendAlertAuditState{
		TenantID:   event.TenantID,
		RuleID:     event.RuleID,
		Severity:   event.Severity,
		Status:     event.Status,
		Labels:     cloneStringMetadata(event.Labels),
		Details:    cloneStringMetadata(event.Details),
		ObservedAt: event.ObservedAt,
		UpdatedAt:  event.UpdatedAt,
	}
}

func scanModelRunEvaluationTrendAlertAuditEvent(row rowScanner) (ModelRunEvaluationTrendAlertAuditEvent, error) {
	var event ModelRunEvaluationTrendAlertAuditEvent
	var severity, status string
	var labelsJSON, detailsJSON string
	var observedAt, createdAt, updatedAt int64
	if err := row.Scan(&event.ID, &event.TenantID, &event.EventType, &event.RuleID, &severity, &status, &labelsJSON, &detailsJSON, &observedAt, &createdAt, &updatedAt); err != nil {
		return ModelRunEvaluationTrendAlertAuditEvent{}, err
	}
	event.Severity = ModelRunEvaluationThresholdSeverity(severity)
	event.Status = ModelRunEvaluationTrendAlertStatus(status)
	mustDecodeJSON(labelsJSON, &event.Labels)
	event.Labels = cloneStringMetadata(event.Labels)
	mustDecodeJSON(detailsJSON, &event.Details)
	event.Details = cloneStringMetadata(event.Details)
	event.ObservedAt = unixNanoToTime(observedAt)
	event.CreatedAt = unixNanoToTime(createdAt)
	event.UpdatedAt = unixNanoToTime(updatedAt)
	return event, nil
}

func cloneModelRunEvaluationTrendAlertAuditEvent(event ModelRunEvaluationTrendAlertAuditEvent) ModelRunEvaluationTrendAlertAuditEvent {
	event.Labels = cloneStringMetadata(event.Labels)
	event.Details = cloneStringMetadata(event.Details)
	return event
}

func parseModelRunEvaluationAlertAuditFloat(value string) float64 {
	parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		return 0
	}
	return parsed
}
