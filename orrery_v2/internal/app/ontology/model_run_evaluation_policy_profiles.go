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

type ModelRunEvaluationMetricOperator string
type ModelRunEvaluationThresholdSeverity string

const (
	ModelRunEvaluationMetricOperatorGTE ModelRunEvaluationMetricOperator = "gte"
	ModelRunEvaluationMetricOperatorLTE ModelRunEvaluationMetricOperator = "lte"
	ModelRunEvaluationMetricOperatorGT  ModelRunEvaluationMetricOperator = "gt"
	ModelRunEvaluationMetricOperatorLT  ModelRunEvaluationMetricOperator = "lt"
	ModelRunEvaluationMetricOperatorEQ  ModelRunEvaluationMetricOperator = "eq"
)

const (
	ModelRunEvaluationThresholdSeverityCritical ModelRunEvaluationThresholdSeverity = "critical"
	ModelRunEvaluationThresholdSeverityWarning  ModelRunEvaluationThresholdSeverity = "warning"
	ModelRunEvaluationThresholdSeverityInfo     ModelRunEvaluationThresholdSeverity = "info"
)

type ModelRunEvaluationMetricThreshold struct {
	RuleID      string                              `json:"rule_id,omitempty"`
	DisplayName string                              `json:"display_name,omitempty"`
	MetricKey   string                              `json:"metric_key"`
	Operator    ModelRunEvaluationMetricOperator    `json:"operator"`
	Threshold   float64                             `json:"threshold"`
	Blocking    bool                                `json:"blocking"`
	Severity    ModelRunEvaluationThresholdSeverity `json:"severity,omitempty"`
	Description string                              `json:"description,omitempty"`
}

type ModelRunEvaluationPolicyProfile struct {
	ID                    string                              `json:"id"`
	TenantID              string                              `json:"tenant_id"`
	Engine                string                              `json:"engine"`
	Model                 string                              `json:"model,omitempty"`
	ModelVersion          string                              `json:"model_version,omitempty"`
	RequiredArtifactKind  string                              `json:"required_artifact_kind"`
	RequiredDatasetRef    string                              `json:"required_dataset_ref,omitempty"`
	RequiredPolicyVersion string                              `json:"required_policy_version"`
	MetricThresholds      []ModelRunEvaluationMetricThreshold `json:"metric_thresholds,omitempty"`
	Enabled               bool                                `json:"enabled"`
	Metadata              map[string]string                   `json:"metadata,omitempty"`
	CreatedAt             time.Time                           `json:"created_at,omitempty"`
	UpdatedAt             time.Time                           `json:"updated_at,omitempty"`
}

type ModelRunEvaluationPolicyProfileQuery struct {
	TenantID     string
	Engine       string
	Model        string
	ModelVersion string
	EnabledOnly  bool
	Limit        int
	Offset       int
}

type ModelRunEvaluationPolicyProfilePage struct {
	Profiles []ModelRunEvaluationPolicyProfile `json:"profiles"`
	Page     Page                              `json:"page"`
}

type ModelRunEvaluationPolicyProfileStore interface {
	SaveModelRunEvaluationPolicyProfile(ctx context.Context, profile ModelRunEvaluationPolicyProfile) error
	ListModelRunEvaluationPolicyProfiles(ctx context.Context, query ModelRunEvaluationPolicyProfileQuery) (ModelRunEvaluationPolicyProfilePage, error)
}

type MemoryModelRunEvaluationPolicyProfileStore struct {
	mu       sync.RWMutex
	profiles map[string]ModelRunEvaluationPolicyProfile
	now      func() time.Time
}

func NewMemoryModelRunEvaluationPolicyProfileStore() *MemoryModelRunEvaluationPolicyProfileStore {
	return &MemoryModelRunEvaluationPolicyProfileStore{profiles: map[string]ModelRunEvaluationPolicyProfile{}, now: time.Now}
}

func (s *MemoryModelRunEvaluationPolicyProfileStore) SaveModelRunEvaluationPolicyProfile(ctx context.Context, profile ModelRunEvaluationPolicyProfile) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	normalized, err := normalizeModelRunEvaluationPolicyProfile(profile, s.now)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if previous, ok := s.profiles[normalized.ID]; ok && !previous.CreatedAt.IsZero() {
		normalized.CreatedAt = previous.CreatedAt
	}
	s.profiles[normalized.ID] = normalized
	return nil
}

func (s *MemoryModelRunEvaluationPolicyProfileStore) ListModelRunEvaluationPolicyProfiles(ctx context.Context, query ModelRunEvaluationPolicyProfileQuery) (ModelRunEvaluationPolicyProfilePage, error) {
	if err := ctx.Err(); err != nil {
		return ModelRunEvaluationPolicyProfilePage{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	matched := make([]ModelRunEvaluationPolicyProfile, 0, len(s.profiles))
	for _, profile := range s.profiles {
		if modelRunEvaluationPolicyProfileMatches(profile, query) {
			matched = append(matched, cloneModelRunEvaluationPolicyProfile(profile))
		}
	}
	sort.Slice(matched, func(i, j int) bool {
		left := modelRunEvaluationPolicyProfileSpecificity(matched[i])
		right := modelRunEvaluationPolicyProfileSpecificity(matched[j])
		if left != right {
			return left > right
		}
		if matched[i].UpdatedAt.Equal(matched[j].UpdatedAt) {
			return matched[i].ID < matched[j].ID
		}
		return matched[i].UpdatedAt.After(matched[j].UpdatedAt)
	})
	return modelRunEvaluationPolicyProfilePage(matched, query), nil
}

type PlatformModelRunEvaluationPolicyProfileStore struct {
	store *db.Store
	tx    db.Tx
	now   func() time.Time
}

func NewPlatformModelRunEvaluationPolicyProfileStore(store *db.Store) *PlatformModelRunEvaluationPolicyProfileStore {
	return &PlatformModelRunEvaluationPolicyProfileStore{store: store, now: time.Now}
}

func NewPlatformModelRunEvaluationPolicyProfileStoreTx(tx db.Tx) *PlatformModelRunEvaluationPolicyProfileStore {
	return &PlatformModelRunEvaluationPolicyProfileStore{tx: tx, now: time.Now}
}

func (s *PlatformModelRunEvaluationPolicyProfileStore) ready() error {
	if s == nil || (s.store == nil && s.tx == nil) {
		return fmt.Errorf("%w: model run evaluation policy profile store is required", ErrInvalidDefinition)
	}
	return nil
}

func (s *PlatformModelRunEvaluationPolicyProfileStore) exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if s.tx != nil {
		return s.tx.ExecContext(ctx, query, args...)
	}
	return s.store.Exec(ctx, query, args...)
}

func (s *PlatformModelRunEvaluationPolicyProfileStore) query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if s.tx != nil {
		return s.tx.QueryContext(ctx, query, args...)
	}
	return s.store.Query(ctx, query, args...)
}

func (s *PlatformModelRunEvaluationPolicyProfileStore) queryRow(ctx context.Context, query string, args ...any) rowScanner {
	if s.tx != nil {
		return s.tx.QueryRowContext(ctx, query, args...)
	}
	return s.store.QueryRow(ctx, query, args...)
}

func (s *PlatformModelRunEvaluationPolicyProfileStore) SaveModelRunEvaluationPolicyProfile(ctx context.Context, profile ModelRunEvaluationPolicyProfile) error {
	if err := s.ready(); err != nil {
		return err
	}
	normalized, err := normalizeModelRunEvaluationPolicyProfile(profile, s.now)
	if err != nil {
		return err
	}
	_, err = s.exec(ctx, `
INSERT INTO ontology_model_run_evaluation_policy_profiles (
	id, tenant_id, engine, model, model_version, required_artifact_kind,
	required_dataset_ref, required_policy_version, metric_thresholds_json, enabled, metadata_json, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	required_artifact_kind = excluded.required_artifact_kind,
	required_dataset_ref = excluded.required_dataset_ref,
	required_policy_version = excluded.required_policy_version,
	metric_thresholds_json = excluded.metric_thresholds_json,
	enabled = excluded.enabled,
	metadata_json = excluded.metadata_json,
	updated_at = excluded.updated_at`,
		normalized.ID,
		normalized.TenantID,
		normalized.Engine,
		normalized.Model,
		normalized.ModelVersion,
		normalized.RequiredArtifactKind,
		normalized.RequiredDatasetRef,
		normalized.RequiredPolicyVersion,
		mustJSON(normalized.MetricThresholds),
		normalized.Enabled,
		mustJSON(normalized.Metadata),
		timeToUnixNano(normalized.CreatedAt),
		timeToUnixNano(normalized.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("save ontology model run evaluation policy profile: %w", err)
	}
	return nil
}

func (s *PlatformModelRunEvaluationPolicyProfileStore) ListModelRunEvaluationPolicyProfiles(ctx context.Context, query ModelRunEvaluationPolicyProfileQuery) (ModelRunEvaluationPolicyProfilePage, error) {
	if err := s.ready(); err != nil {
		return ModelRunEvaluationPolicyProfilePage{}, err
	}
	limit := normalizeMetadataProposalLimit(query.Limit)
	offset := normalizeMetadataProposalOffset(query.Offset)
	where, args := modelRunEvaluationPolicyProfileWhere(query)
	var total int
	if err := s.queryRow(ctx, `SELECT COUNT(*) FROM ontology_model_run_evaluation_policy_profiles`+where, args...).Scan(&total); err != nil {
		return ModelRunEvaluationPolicyProfilePage{}, fmt.Errorf("count ontology model run evaluation policy profiles: %w", err)
	}
	rows, err := s.query(ctx, `SELECT id, tenant_id, engine, model, model_version, required_artifact_kind,
required_dataset_ref, required_policy_version, metric_thresholds_json, enabled, metadata_json, created_at, updated_at
FROM ontology_model_run_evaluation_policy_profiles`+where+` ORDER BY
CASE WHEN model <> '' THEN 1 ELSE 0 END + CASE WHEN model_version <> '' THEN 1 ELSE 0 END DESC,
updated_at DESC, id ASC LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	if err != nil {
		return ModelRunEvaluationPolicyProfilePage{}, fmt.Errorf("list ontology model run evaluation policy profiles: %w", err)
	}
	defer rows.Close()
	profiles := []ModelRunEvaluationPolicyProfile{}
	for rows.Next() {
		profile, err := scanModelRunEvaluationPolicyProfile(rows)
		if err != nil {
			return ModelRunEvaluationPolicyProfilePage{}, err
		}
		profiles = append(profiles, profile)
	}
	if err := rows.Err(); err != nil {
		return ModelRunEvaluationPolicyProfilePage{}, err
	}
	return ModelRunEvaluationPolicyProfilePage{
		Profiles: profiles,
		Page:     Page{Limit: limit, Offset: offset, Total: total, Returned: len(profiles), HasMore: offset+len(profiles) < total},
	}, nil
}

func ResolveModelRunEvaluationPolicyProfile(ctx context.Context, store ModelRunEvaluationPolicyProfileStore, run OntologyModelRun) (ModelRunEvaluationPolicyProfile, bool, error) {
	if store == nil {
		return ModelRunEvaluationPolicyProfile{}, false, nil
	}
	page, err := store.ListModelRunEvaluationPolicyProfiles(ctx, ModelRunEvaluationPolicyProfileQuery{
		TenantID:    run.TenantID,
		Engine:      run.Engine,
		EnabledOnly: true,
		Limit:       100,
	})
	if err != nil {
		return ModelRunEvaluationPolicyProfile{}, false, err
	}
	runModel := strings.TrimSpace(run.Model)
	runVersion := strings.TrimSpace(run.ModelVersion)
	for _, profile := range page.Profiles {
		if profile.Model != "" && profile.Model != runModel {
			continue
		}
		if profile.ModelVersion != "" && profile.ModelVersion != runVersion {
			continue
		}
		return profile, true, nil
	}
	return ModelRunEvaluationPolicyProfile{}, false, nil
}

func ModelRunProposalQueryFromPolicyProfile(query ModelRunProposalQuery, profile ModelRunEvaluationPolicyProfile) ModelRunProposalQuery {
	if query.RequiredArtifactKind == "" {
		query.RequiredArtifactKind = profile.RequiredArtifactKind
	}
	if query.RequiredDatasetRef == "" {
		query.RequiredDatasetRef = profile.RequiredDatasetRef
	}
	if query.RequiredPolicyVersion == "" {
		query.RequiredPolicyVersion = profile.RequiredPolicyVersion
	}
	if len(query.MetricThresholds) == 0 {
		query.MetricThresholds = cloneModelRunEvaluationMetricThresholds(profile.MetricThresholds)
	}
	return query
}

func normalizeModelRunEvaluationPolicyProfile(profile ModelRunEvaluationPolicyProfile, now func() time.Time) (ModelRunEvaluationPolicyProfile, error) {
	profile.TenantID = strings.TrimSpace(profile.TenantID)
	profile.Engine = normalizeID(profile.Engine)
	profile.Model = strings.TrimSpace(profile.Model)
	profile.ModelVersion = strings.TrimSpace(profile.ModelVersion)
	profile.RequiredArtifactKind = normalizeID(profile.RequiredArtifactKind)
	profile.RequiredDatasetRef = strings.TrimSpace(profile.RequiredDatasetRef)
	profile.RequiredPolicyVersion = strings.TrimSpace(profile.RequiredPolicyVersion)
	profile.MetricThresholds = normalizeModelRunEvaluationMetricThresholds(profile.MetricThresholds)
	profile.Metadata = cloneStringMetadata(profile.Metadata)
	if profile.TenantID == "" || profile.Engine == "" || profile.RequiredArtifactKind == "" || profile.RequiredPolicyVersion == "" {
		return ModelRunEvaluationPolicyProfile{}, fmt.Errorf("%w: model run evaluation policy profile tenant_id, engine, required_artifact_kind, and required_policy_version are required", ErrInvalidDefinition)
	}
	for _, threshold := range profile.MetricThresholds {
		if threshold.MetricKey == "" ||
			!modelRunEvaluationMetricOperatorValid(threshold.Operator) ||
			!modelRunEvaluationThresholdSeverityValid(threshold.Severity) {
			return ModelRunEvaluationPolicyProfile{}, fmt.Errorf("%w: model run evaluation policy profile metric thresholds require metric_key, supported operator, and supported severity", ErrInvalidDefinition)
		}
	}
	if profile.ID == "" {
		profile.ID = modelRunEvaluationPolicyProfileID(profile.TenantID, profile.Engine, profile.Model, profile.ModelVersion)
	}
	if now == nil {
		now = time.Now
	}
	at := now().UTC()
	if profile.CreatedAt.IsZero() {
		profile.CreatedAt = at
	}
	profile.UpdatedAt = at
	return profile, nil
}

func modelRunEvaluationPolicyProfileID(tenantID string, engine string, model string, modelVersion string) string {
	return "ontology_model_run_evaluation_policy_profile:" + hashMappingRule(map[string]any{
		"tenant_id":     strings.TrimSpace(tenantID),
		"engine":        normalizeID(engine),
		"model":         strings.TrimSpace(model),
		"model_version": strings.TrimSpace(modelVersion),
	})
}

func modelRunEvaluationPolicyProfileMatches(profile ModelRunEvaluationPolicyProfile, query ModelRunEvaluationPolicyProfileQuery) bool {
	return (strings.TrimSpace(query.TenantID) == "" || profile.TenantID == strings.TrimSpace(query.TenantID)) &&
		(normalizeID(query.Engine) == "" || profile.Engine == normalizeID(query.Engine)) &&
		(strings.TrimSpace(query.Model) == "" || profile.Model == strings.TrimSpace(query.Model)) &&
		(strings.TrimSpace(query.ModelVersion) == "" || profile.ModelVersion == strings.TrimSpace(query.ModelVersion)) &&
		(!query.EnabledOnly || profile.Enabled)
}

func modelRunEvaluationPolicyProfileWhere(query ModelRunEvaluationPolicyProfileQuery) (string, []any) {
	clauses := []string{}
	args := []any{}
	if tenantID := strings.TrimSpace(query.TenantID); tenantID != "" {
		clauses = append(clauses, "tenant_id = ?")
		args = append(args, tenantID)
	}
	if engine := normalizeID(query.Engine); engine != "" {
		clauses = append(clauses, "engine = ?")
		args = append(args, engine)
	}
	if model := strings.TrimSpace(query.Model); model != "" {
		clauses = append(clauses, "model = ?")
		args = append(args, model)
	}
	if modelVersion := strings.TrimSpace(query.ModelVersion); modelVersion != "" {
		clauses = append(clauses, "model_version = ?")
		args = append(args, modelVersion)
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

func modelRunEvaluationPolicyProfilePage(profiles []ModelRunEvaluationPolicyProfile, query ModelRunEvaluationPolicyProfileQuery) ModelRunEvaluationPolicyProfilePage {
	limit := normalizeMetadataProposalLimit(query.Limit)
	offset := normalizeMetadataProposalOffset(query.Offset)
	total := len(profiles)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	items := append([]ModelRunEvaluationPolicyProfile(nil), profiles[offset:end]...)
	return ModelRunEvaluationPolicyProfilePage{Profiles: items, Page: Page{Limit: limit, Offset: offset, Total: total, Returned: len(items), HasMore: end < total}}
}

func modelRunEvaluationPolicyProfileSpecificity(profile ModelRunEvaluationPolicyProfile) int {
	score := 0
	if profile.Model != "" {
		score++
	}
	if profile.ModelVersion != "" {
		score++
	}
	return score
}

func scanModelRunEvaluationPolicyProfile(row rowScanner) (ModelRunEvaluationPolicyProfile, error) {
	var profile ModelRunEvaluationPolicyProfile
	var metricThresholdsJSON, metadataJSON string
	var createdAt, updatedAt int64
	if err := row.Scan(&profile.ID, &profile.TenantID, &profile.Engine, &profile.Model, &profile.ModelVersion, &profile.RequiredArtifactKind, &profile.RequiredDatasetRef, &profile.RequiredPolicyVersion, &metricThresholdsJSON, &profile.Enabled, &metadataJSON, &createdAt, &updatedAt); err != nil {
		return ModelRunEvaluationPolicyProfile{}, err
	}
	mustDecodeJSON(metricThresholdsJSON, &profile.MetricThresholds)
	profile.MetricThresholds = normalizeModelRunEvaluationMetricThresholds(profile.MetricThresholds)
	mustDecodeJSON(metadataJSON, &profile.Metadata)
	profile.Metadata = cloneStringMetadata(profile.Metadata)
	profile.CreatedAt = unixNanoToTime(createdAt)
	profile.UpdatedAt = unixNanoToTime(updatedAt)
	return profile, nil
}

func cloneModelRunEvaluationPolicyProfile(profile ModelRunEvaluationPolicyProfile) ModelRunEvaluationPolicyProfile {
	profile.MetricThresholds = cloneModelRunEvaluationMetricThresholds(profile.MetricThresholds)
	profile.Metadata = cloneStringMetadata(profile.Metadata)
	return profile
}

func normalizeModelRunEvaluationMetricThresholds(thresholds []ModelRunEvaluationMetricThreshold) []ModelRunEvaluationMetricThreshold {
	if len(thresholds) == 0 {
		return nil
	}
	normalized := make([]ModelRunEvaluationMetricThreshold, 0, len(thresholds))
	for _, threshold := range thresholds {
		threshold.RuleID = normalizeID(threshold.RuleID)
		threshold.DisplayName = strings.TrimSpace(threshold.DisplayName)
		threshold.MetricKey = strings.TrimSpace(threshold.MetricKey)
		threshold.Operator = ModelRunEvaluationMetricOperator(strings.TrimSpace(string(threshold.Operator)))
		threshold.Severity = ModelRunEvaluationThresholdSeverity(strings.TrimSpace(string(threshold.Severity)))
		threshold.Description = strings.TrimSpace(threshold.Description)
		if threshold.RuleID == "" {
			threshold.RuleID = modelRunEvaluationMetricThresholdRuleID(threshold)
		}
		if threshold.Severity == "" {
			if threshold.Blocking {
				threshold.Severity = ModelRunEvaluationThresholdSeverityCritical
			} else {
				threshold.Severity = ModelRunEvaluationThresholdSeverityWarning
			}
		}
		normalized = append(normalized, threshold)
	}
	return normalized
}

func modelRunEvaluationMetricThresholdRuleID(threshold ModelRunEvaluationMetricThreshold) string {
	return "metric_threshold:" + hashMappingRule(map[string]any{
		"metric_key": strings.TrimSpace(threshold.MetricKey),
		"operator":   strings.TrimSpace(string(threshold.Operator)),
		"threshold":  threshold.Threshold,
		"blocking":   threshold.Blocking,
	})
}

func cloneModelRunEvaluationMetricThresholds(thresholds []ModelRunEvaluationMetricThreshold) []ModelRunEvaluationMetricThreshold {
	if len(thresholds) == 0 {
		return nil
	}
	out := make([]ModelRunEvaluationMetricThreshold, len(thresholds))
	copy(out, thresholds)
	return out
}

func modelRunEvaluationMetricOperatorValid(operator ModelRunEvaluationMetricOperator) bool {
	switch operator {
	case ModelRunEvaluationMetricOperatorGTE, ModelRunEvaluationMetricOperatorLTE, ModelRunEvaluationMetricOperatorGT, ModelRunEvaluationMetricOperatorLT, ModelRunEvaluationMetricOperatorEQ:
		return true
	default:
		return false
	}
}

func modelRunEvaluationThresholdSeverityValid(severity ModelRunEvaluationThresholdSeverity) bool {
	switch severity {
	case ModelRunEvaluationThresholdSeverityCritical, ModelRunEvaluationThresholdSeverityWarning, ModelRunEvaluationThresholdSeverityInfo:
		return true
	default:
		return false
	}
}
