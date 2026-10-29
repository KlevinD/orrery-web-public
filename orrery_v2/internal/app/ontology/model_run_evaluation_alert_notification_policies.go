package ontology

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"orrery_v2/internal/platform/db"
)

type ModelRunEvaluationTrendAlertNotificationPolicyRecord struct {
	ID             string            `json:"id"`
	TenantID       string            `json:"tenant_id"`
	RuleID         string            `json:"rule_id,omitempty"`
	Channel        string            `json:"channel"`
	Endpoint       string            `json:"endpoint,omitempty"`
	Headers        map[string]string `json:"headers,omitempty"`
	RepeatWindow   time.Duration     `json:"repeat_window,omitempty"`
	RecoveryWindow time.Duration     `json:"recovery_window,omitempty"`
	MaxAttempts    int               `json:"max_attempts,omitempty"`
	Enabled        bool              `json:"enabled"`
	CreatedAt      time.Time         `json:"created_at,omitempty"`
	UpdatedAt      time.Time         `json:"updated_at,omitempty"`
}

func (r *ModelRunEvaluationTrendAlertNotificationPolicyRecord) UnmarshalJSON(data []byte) error {
	type alias ModelRunEvaluationTrendAlertNotificationPolicyRecord
	var raw struct {
		alias
		RepeatWindow   flexibleDuration `json:"repeat_window,omitempty"`
		RecoveryWindow flexibleDuration `json:"recovery_window,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*r = ModelRunEvaluationTrendAlertNotificationPolicyRecord(raw.alias)
	r.RepeatWindow = time.Duration(raw.RepeatWindow)
	r.RecoveryWindow = time.Duration(raw.RecoveryWindow)
	return nil
}

type flexibleDuration time.Duration

func (d *flexibleDuration) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		if strings.TrimSpace(text) == "" {
			*d = 0
			return nil
		}
		parsed, err := time.ParseDuration(text)
		if err != nil {
			return err
		}
		*d = flexibleDuration(parsed)
		return nil
	}
	var raw int64
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*d = flexibleDuration(time.Duration(raw))
	return nil
}

type ModelRunEvaluationTrendAlertNotificationPolicyQuery struct {
	TenantID    string
	RuleID      string
	Channel     string
	EnabledOnly bool
	Limit       int
	Offset      int
}

type ModelRunEvaluationTrendAlertNotificationPolicyPage struct {
	Policies []ModelRunEvaluationTrendAlertNotificationPolicyRecord `json:"policies"`
	Page     Page                                                   `json:"page"`
}

type ModelRunEvaluationTrendAlertNotificationPolicyStore interface {
	SaveModelRunEvaluationTrendAlertNotificationPolicy(ctx context.Context, policy ModelRunEvaluationTrendAlertNotificationPolicyRecord) error
	ListModelRunEvaluationTrendAlertNotificationPolicies(ctx context.Context, query ModelRunEvaluationTrendAlertNotificationPolicyQuery) (ModelRunEvaluationTrendAlertNotificationPolicyPage, error)
}

type MemoryModelRunEvaluationTrendAlertNotificationPolicyStore struct {
	mu       sync.RWMutex
	policies map[string]ModelRunEvaluationTrendAlertNotificationPolicyRecord
	now      func() time.Time
}

func NewMemoryModelRunEvaluationTrendAlertNotificationPolicyStore() *MemoryModelRunEvaluationTrendAlertNotificationPolicyStore {
	return &MemoryModelRunEvaluationTrendAlertNotificationPolicyStore{
		policies: map[string]ModelRunEvaluationTrendAlertNotificationPolicyRecord{},
		now:      time.Now,
	}
}

func (s *MemoryModelRunEvaluationTrendAlertNotificationPolicyStore) SaveModelRunEvaluationTrendAlertNotificationPolicy(ctx context.Context, policy ModelRunEvaluationTrendAlertNotificationPolicyRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	normalized, err := normalizeModelRunEvaluationTrendAlertNotificationPolicyRecord(policy, s.now)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if previous, ok := s.policies[normalized.ID]; ok && !previous.CreatedAt.IsZero() {
		normalized.CreatedAt = previous.CreatedAt
	}
	s.policies[normalized.ID] = normalized
	return nil
}

func (s *MemoryModelRunEvaluationTrendAlertNotificationPolicyStore) ListModelRunEvaluationTrendAlertNotificationPolicies(ctx context.Context, query ModelRunEvaluationTrendAlertNotificationPolicyQuery) (ModelRunEvaluationTrendAlertNotificationPolicyPage, error) {
	if err := ctx.Err(); err != nil {
		return ModelRunEvaluationTrendAlertNotificationPolicyPage{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	matched := make([]ModelRunEvaluationTrendAlertNotificationPolicyRecord, 0, len(s.policies))
	for _, policy := range s.policies {
		if modelRunEvaluationTrendAlertNotificationPolicyMatches(policy, query) {
			matched = append(matched, cloneModelRunEvaluationTrendAlertNotificationPolicyRecord(policy))
		}
	}
	sortModelRunEvaluationTrendAlertNotificationPolicies(matched)
	return modelRunEvaluationTrendAlertNotificationPolicyPage(matched, query), nil
}

type PlatformModelRunEvaluationTrendAlertNotificationPolicyStore struct {
	store *db.Store
	tx    db.Tx
	now   func() time.Time
}

func NewPlatformModelRunEvaluationTrendAlertNotificationPolicyStore(store *db.Store) *PlatformModelRunEvaluationTrendAlertNotificationPolicyStore {
	return &PlatformModelRunEvaluationTrendAlertNotificationPolicyStore{store: store, now: time.Now}
}

func NewPlatformModelRunEvaluationTrendAlertNotificationPolicyStoreTx(tx db.Tx) *PlatformModelRunEvaluationTrendAlertNotificationPolicyStore {
	return &PlatformModelRunEvaluationTrendAlertNotificationPolicyStore{tx: tx, now: time.Now}
}

func (s *PlatformModelRunEvaluationTrendAlertNotificationPolicyStore) ready() error {
	if s == nil || (s.store == nil && s.tx == nil) {
		return fmt.Errorf("%w: model run evaluation trend alert notification policy store is required", ErrInvalidDefinition)
	}
	return nil
}

func (s *PlatformModelRunEvaluationTrendAlertNotificationPolicyStore) exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if s.tx != nil {
		return s.tx.ExecContext(ctx, query, args...)
	}
	return s.store.Exec(ctx, query, args...)
}

func (s *PlatformModelRunEvaluationTrendAlertNotificationPolicyStore) query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if s.tx != nil {
		return s.tx.QueryContext(ctx, query, args...)
	}
	return s.store.Query(ctx, query, args...)
}

func (s *PlatformModelRunEvaluationTrendAlertNotificationPolicyStore) queryRow(ctx context.Context, query string, args ...any) rowScanner {
	if s.tx != nil {
		return s.tx.QueryRowContext(ctx, query, args...)
	}
	return s.store.QueryRow(ctx, query, args...)
}

func (s *PlatformModelRunEvaluationTrendAlertNotificationPolicyStore) SaveModelRunEvaluationTrendAlertNotificationPolicy(ctx context.Context, policy ModelRunEvaluationTrendAlertNotificationPolicyRecord) error {
	if err := s.ready(); err != nil {
		return err
	}
	normalized, err := normalizeModelRunEvaluationTrendAlertNotificationPolicyRecord(policy, s.now)
	if err != nil {
		return err
	}
	_, err = s.exec(ctx, `
INSERT INTO ontology_model_run_evaluation_alert_notification_policies (
	id, tenant_id, rule_id, channel, endpoint, headers_json, repeat_window_seconds,
	recovery_window_seconds, max_attempts, enabled, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	rule_id = excluded.rule_id,
	channel = excluded.channel,
	endpoint = excluded.endpoint,
	headers_json = excluded.headers_json,
	repeat_window_seconds = excluded.repeat_window_seconds,
	recovery_window_seconds = excluded.recovery_window_seconds,
	max_attempts = excluded.max_attempts,
	enabled = excluded.enabled,
	updated_at = excluded.updated_at`,
		normalized.ID,
		normalized.TenantID,
		normalized.RuleID,
		normalized.Channel,
		normalized.Endpoint,
		mustJSON(normalized.Headers),
		int64(normalized.RepeatWindow.Seconds()),
		int64(normalized.RecoveryWindow.Seconds()),
		normalized.MaxAttempts,
		normalized.Enabled,
		timeToUnixNano(normalized.CreatedAt),
		timeToUnixNano(normalized.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("save ontology model run evaluation alert notification policy: %w", err)
	}
	return nil
}

func (s *PlatformModelRunEvaluationTrendAlertNotificationPolicyStore) ListModelRunEvaluationTrendAlertNotificationPolicies(ctx context.Context, query ModelRunEvaluationTrendAlertNotificationPolicyQuery) (ModelRunEvaluationTrendAlertNotificationPolicyPage, error) {
	if err := s.ready(); err != nil {
		return ModelRunEvaluationTrendAlertNotificationPolicyPage{}, err
	}
	limit := normalizeMetadataProposalLimit(query.Limit)
	offset := normalizeMetadataProposalOffset(query.Offset)
	where, args := modelRunEvaluationTrendAlertNotificationPolicyWhere(query)
	var total int
	if err := s.queryRow(ctx, `SELECT COUNT(*) FROM ontology_model_run_evaluation_alert_notification_policies`+where, args...).Scan(&total); err != nil {
		return ModelRunEvaluationTrendAlertNotificationPolicyPage{}, fmt.Errorf("count ontology model run evaluation alert notification policies: %w", err)
	}
	rows, err := s.query(ctx, modelRunEvaluationTrendAlertNotificationPolicySelectSQL()+where+` ORDER BY updated_at DESC, id ASC LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	if err != nil {
		return ModelRunEvaluationTrendAlertNotificationPolicyPage{}, fmt.Errorf("list ontology model run evaluation alert notification policies: %w", err)
	}
	defer rows.Close()
	policies := []ModelRunEvaluationTrendAlertNotificationPolicyRecord{}
	for rows.Next() {
		policy, err := scanModelRunEvaluationTrendAlertNotificationPolicyRecord(rows)
		if err != nil {
			return ModelRunEvaluationTrendAlertNotificationPolicyPage{}, err
		}
		policies = append(policies, policy)
	}
	if err := rows.Err(); err != nil {
		return ModelRunEvaluationTrendAlertNotificationPolicyPage{}, err
	}
	return ModelRunEvaluationTrendAlertNotificationPolicyPage{
		Policies: policies,
		Page:     Page{Limit: limit, Offset: offset, Total: total, Returned: len(policies), HasMore: offset+len(policies) < total},
	}, nil
}

func normalizeModelRunEvaluationTrendAlertNotificationPolicyRecord(policy ModelRunEvaluationTrendAlertNotificationPolicyRecord, now func() time.Time) (ModelRunEvaluationTrendAlertNotificationPolicyRecord, error) {
	policy.ID = strings.TrimSpace(policy.ID)
	policy.TenantID = strings.TrimSpace(policy.TenantID)
	policy.RuleID = normalizeID(policy.RuleID)
	policy.Channel = normalizeID(policy.Channel)
	policy.Endpoint = strings.TrimSpace(policy.Endpoint)
	policy.Headers = cloneStringMetadata(policy.Headers)
	if policy.MaxAttempts <= 0 {
		policy.MaxAttempts = defaultModelRunEvaluationTrendAlertNotificationRetryMaxAttempts
	}
	if policy.TenantID == "" || policy.Channel == "" {
		return ModelRunEvaluationTrendAlertNotificationPolicyRecord{}, fmt.Errorf("%w: model run evaluation trend alert notification policy tenant_id and channel are required", ErrInvalidDefinition)
	}
	if policy.ID == "" {
		policy.ID = modelRunEvaluationTrendAlertNotificationPolicyID(policy.TenantID, policy.RuleID, policy.Channel)
	}
	if now == nil {
		now = time.Now
	}
	at := now().UTC()
	if policy.CreatedAt.IsZero() {
		policy.CreatedAt = at
	} else {
		policy.CreatedAt = policy.CreatedAt.UTC()
	}
	policy.UpdatedAt = at
	return policy, nil
}

func modelRunEvaluationTrendAlertNotificationPolicyID(tenantID string, ruleID string, channel string) string {
	return "ontology_model_run_evaluation_alert_notification_policy:" + hashMappingRule(map[string]any{
		"tenant_id": strings.TrimSpace(tenantID),
		"rule_id":   normalizeID(ruleID),
		"channel":   normalizeID(channel),
	})
}

func cloneModelRunEvaluationTrendAlertNotificationPolicyRecord(policy ModelRunEvaluationTrendAlertNotificationPolicyRecord) ModelRunEvaluationTrendAlertNotificationPolicyRecord {
	policy.Headers = cloneStringMetadata(policy.Headers)
	return policy
}

func sortModelRunEvaluationTrendAlertNotificationPolicies(policies []ModelRunEvaluationTrendAlertNotificationPolicyRecord) {
	sort.SliceStable(policies, func(i, j int) bool {
		if policies[i].UpdatedAt.Equal(policies[j].UpdatedAt) {
			return policies[i].ID < policies[j].ID
		}
		return policies[i].UpdatedAt.After(policies[j].UpdatedAt)
	})
}

func modelRunEvaluationTrendAlertNotificationPolicyPage(policies []ModelRunEvaluationTrendAlertNotificationPolicyRecord, query ModelRunEvaluationTrendAlertNotificationPolicyQuery) ModelRunEvaluationTrendAlertNotificationPolicyPage {
	limit := normalizeMetadataProposalLimit(query.Limit)
	offset := normalizeMetadataProposalOffset(query.Offset)
	total := len(policies)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	items := make([]ModelRunEvaluationTrendAlertNotificationPolicyRecord, 0, end-offset)
	for _, policy := range policies[offset:end] {
		items = append(items, cloneModelRunEvaluationTrendAlertNotificationPolicyRecord(policy))
	}
	return ModelRunEvaluationTrendAlertNotificationPolicyPage{
		Policies: items,
		Page:     Page{Limit: limit, Offset: offset, Total: total, Returned: len(items), HasMore: end < total},
	}
}

func modelRunEvaluationTrendAlertNotificationPolicyMatches(policy ModelRunEvaluationTrendAlertNotificationPolicyRecord, query ModelRunEvaluationTrendAlertNotificationPolicyQuery) bool {
	return (strings.TrimSpace(query.TenantID) == "" || policy.TenantID == strings.TrimSpace(query.TenantID)) &&
		(normalizeID(query.RuleID) == "" || policy.RuleID == normalizeID(query.RuleID)) &&
		(normalizeID(query.Channel) == "" || policy.Channel == normalizeID(query.Channel)) &&
		(!query.EnabledOnly || policy.Enabled)
}

func modelRunEvaluationTrendAlertNotificationPolicyWhere(query ModelRunEvaluationTrendAlertNotificationPolicyQuery) (string, []any) {
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
	if channel := normalizeID(query.Channel); channel != "" {
		clauses = append(clauses, "channel = ?")
		args = append(args, channel)
	}
	if query.EnabledOnly {
		clauses = append(clauses, "enabled = ?")
		args = append(args, true)
	}
	if len(clauses) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

func modelRunEvaluationTrendAlertNotificationPolicySelectSQL() string {
	return `SELECT id, tenant_id, rule_id, channel, endpoint, headers_json, repeat_window_seconds,
recovery_window_seconds, max_attempts, enabled, created_at, updated_at
FROM ontology_model_run_evaluation_alert_notification_policies`
}

func scanModelRunEvaluationTrendAlertNotificationPolicyRecord(row rowScanner) (ModelRunEvaluationTrendAlertNotificationPolicyRecord, error) {
	policy := ModelRunEvaluationTrendAlertNotificationPolicyRecord{}
	var headersJSON string
	var repeatSeconds, recoverySeconds, createdAt, updatedAt int64
	if err := row.Scan(&policy.ID, &policy.TenantID, &policy.RuleID, &policy.Channel, &policy.Endpoint, &headersJSON, &repeatSeconds, &recoverySeconds, &policy.MaxAttempts, &policy.Enabled, &createdAt, &updatedAt); err != nil {
		return ModelRunEvaluationTrendAlertNotificationPolicyRecord{}, err
	}
	mustDecodeJSON(headersJSON, &policy.Headers)
	policy.Headers = cloneStringMetadata(policy.Headers)
	policy.RepeatWindow = time.Duration(repeatSeconds) * time.Second
	policy.RecoveryWindow = time.Duration(recoverySeconds) * time.Second
	policy.CreatedAt = unixNanoToTime(createdAt)
	policy.UpdatedAt = unixNanoToTime(updatedAt)
	return policy, nil
}
