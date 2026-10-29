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
	"orrery_v2/internal/platform/metrics"
)

type ModelRunEvaluationTrendMetricSample struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels,omitempty"`
	Value  float64           `json:"value"`
}

type ModelRunEvaluationTrendMetricRecording struct {
	ID           string                              `json:"id"`
	TenantID     string                              `json:"tenant_id,omitempty"`
	Engine       string                              `json:"engine,omitempty"`
	Model        string                              `json:"model,omitempty"`
	ModelVersion string                              `json:"model_version,omitempty"`
	MetricSample ModelRunEvaluationTrendMetricSample `json:"metric_sample"`
	RecordedAt   time.Time                           `json:"recorded_at,omitempty"`
	CreatedAt    time.Time                           `json:"created_at,omitempty"`
	UpdatedAt    time.Time                           `json:"updated_at,omitempty"`
}

type ModelRunEvaluationTrendMetricRecordingQuery struct {
	TenantID     string
	Engine       string
	MetricName   string
	LabelKey     string
	LabelValue   string
	RecordedAt   time.Time
	RecordedFrom time.Time
	RecordedTo   time.Time
	Limit        int
	Offset       int
}

type ModelRunEvaluationTrendMetricRecordingPage struct {
	Recordings []ModelRunEvaluationTrendMetricRecording `json:"recordings"`
	Page       Page                                     `json:"page"`
}

type ModelRunEvaluationTrendMetricRecordingCollectionReport struct {
	Collected int `json:"collected"`
	Scanned   int `json:"scanned"`
	Pages     int `json:"pages"`
}

type ModelRunEvaluationTrendMetricRecordingStore interface {
	SaveModelRunEvaluationTrendMetricRecording(ctx context.Context, recording ModelRunEvaluationTrendMetricRecording) error
	ListModelRunEvaluationTrendMetricRecordings(ctx context.Context, query ModelRunEvaluationTrendMetricRecordingQuery) (ModelRunEvaluationTrendMetricRecordingPage, error)
}

type MemoryModelRunEvaluationTrendMetricRecordingStore struct {
	mu         sync.RWMutex
	recordings map[string]ModelRunEvaluationTrendMetricRecording
	now        func() time.Time
}

func NewMemoryModelRunEvaluationTrendMetricRecordingStore() *MemoryModelRunEvaluationTrendMetricRecordingStore {
	return &MemoryModelRunEvaluationTrendMetricRecordingStore{recordings: map[string]ModelRunEvaluationTrendMetricRecording{}, now: time.Now}
}

func (s *MemoryModelRunEvaluationTrendMetricRecordingStore) SaveModelRunEvaluationTrendMetricRecording(ctx context.Context, recording ModelRunEvaluationTrendMetricRecording) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	normalized, err := normalizeModelRunEvaluationTrendMetricRecording(recording, s.now)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if previous, ok := s.recordings[normalized.ID]; ok && !previous.CreatedAt.IsZero() {
		normalized.CreatedAt = previous.CreatedAt
	}
	s.recordings[normalized.ID] = normalized
	return nil
}

func (s *MemoryModelRunEvaluationTrendMetricRecordingStore) ListModelRunEvaluationTrendMetricRecordings(ctx context.Context, query ModelRunEvaluationTrendMetricRecordingQuery) (ModelRunEvaluationTrendMetricRecordingPage, error) {
	if err := ctx.Err(); err != nil {
		return ModelRunEvaluationTrendMetricRecordingPage{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	matched := make([]ModelRunEvaluationTrendMetricRecording, 0, len(s.recordings))
	for _, recording := range s.recordings {
		if modelRunEvaluationTrendMetricRecordingMatches(recording, query) {
			matched = append(matched, cloneModelRunEvaluationTrendMetricRecording(recording))
		}
	}
	sort.Slice(matched, func(i, j int) bool {
		if matched[i].RecordedAt.Equal(matched[j].RecordedAt) {
			return matched[i].ID < matched[j].ID
		}
		return matched[i].RecordedAt.After(matched[j].RecordedAt)
	})
	return modelRunEvaluationTrendMetricRecordingPage(matched, query), nil
}

type PlatformModelRunEvaluationTrendMetricRecordingStore struct {
	store *db.Store
	tx    db.Tx
	now   func() time.Time
}

func NewPlatformModelRunEvaluationTrendMetricRecordingStore(store *db.Store) *PlatformModelRunEvaluationTrendMetricRecordingStore {
	return &PlatformModelRunEvaluationTrendMetricRecordingStore{store: store, now: time.Now}
}

func NewPlatformModelRunEvaluationTrendMetricRecordingStoreTx(tx db.Tx) *PlatformModelRunEvaluationTrendMetricRecordingStore {
	return &PlatformModelRunEvaluationTrendMetricRecordingStore{tx: tx, now: time.Now}
}

func (s *PlatformModelRunEvaluationTrendMetricRecordingStore) ready() error {
	if s == nil || (s.store == nil && s.tx == nil) {
		return fmt.Errorf("%w: model run evaluation trend metric recording store is required", ErrInvalidDefinition)
	}
	return nil
}

func (s *PlatformModelRunEvaluationTrendMetricRecordingStore) exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if s.tx != nil {
		return s.tx.ExecContext(ctx, query, args...)
	}
	return s.store.Exec(ctx, query, args...)
}

func (s *PlatformModelRunEvaluationTrendMetricRecordingStore) query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if s.tx != nil {
		return s.tx.QueryContext(ctx, query, args...)
	}
	return s.store.Query(ctx, query, args...)
}

func (s *PlatformModelRunEvaluationTrendMetricRecordingStore) queryRow(ctx context.Context, query string, args ...any) rowScanner {
	if s.tx != nil {
		return s.tx.QueryRowContext(ctx, query, args...)
	}
	return s.store.QueryRow(ctx, query, args...)
}

func (s *PlatformModelRunEvaluationTrendMetricRecordingStore) dialect() db.Dialect {
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

func (s *PlatformModelRunEvaluationTrendMetricRecordingStore) SaveModelRunEvaluationTrendMetricRecording(ctx context.Context, recording ModelRunEvaluationTrendMetricRecording) error {
	if err := s.ready(); err != nil {
		return err
	}
	normalized, err := normalizeModelRunEvaluationTrendMetricRecording(recording, s.now)
	if err != nil {
		return err
	}
	_, err = s.exec(ctx, `
INSERT INTO ontology_model_run_evaluation_metric_recordings (
	id, tenant_id, engine, model, model_version, metric_name, value, labels_json, recorded_at, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	tenant_id = excluded.tenant_id,
	engine = excluded.engine,
	model = excluded.model,
	model_version = excluded.model_version,
	metric_name = excluded.metric_name,
	value = excluded.value,
	labels_json = excluded.labels_json,
	recorded_at = excluded.recorded_at,
	updated_at = excluded.updated_at`,
		normalized.ID,
		normalized.TenantID,
		normalized.Engine,
		normalized.Model,
		normalized.ModelVersion,
		normalized.MetricSample.Name,
		normalized.MetricSample.Value,
		mustJSON(normalized.MetricSample.Labels),
		timeToUnixNano(normalized.RecordedAt),
		timeToUnixNano(normalized.CreatedAt),
		timeToUnixNano(normalized.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("save ontology model run evaluation metric recording: %w", err)
	}
	return nil
}

func (s *PlatformModelRunEvaluationTrendMetricRecordingStore) ListModelRunEvaluationTrendMetricRecordings(ctx context.Context, query ModelRunEvaluationTrendMetricRecordingQuery) (ModelRunEvaluationTrendMetricRecordingPage, error) {
	if err := s.ready(); err != nil {
		return ModelRunEvaluationTrendMetricRecordingPage{}, err
	}
	limit := normalizeMetadataProposalLimit(query.Limit)
	offset := normalizeMetadataProposalOffset(query.Offset)
	where, args := modelRunEvaluationTrendMetricRecordingWhere(query, s.dialect())
	var total int
	if err := s.queryRow(ctx, `SELECT COUNT(*) FROM ontology_model_run_evaluation_metric_recordings`+where, args...).Scan(&total); err != nil {
		return ModelRunEvaluationTrendMetricRecordingPage{}, fmt.Errorf("count ontology model run evaluation metric recordings: %w", err)
	}
	rows, err := s.query(ctx, `SELECT id, tenant_id, engine, model, model_version, metric_name, value, labels_json,
recorded_at, created_at, updated_at FROM ontology_model_run_evaluation_metric_recordings`+where+` ORDER BY recorded_at DESC, id ASC LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	if err != nil {
		return ModelRunEvaluationTrendMetricRecordingPage{}, fmt.Errorf("list ontology model run evaluation metric recordings: %w", err)
	}
	defer rows.Close()
	recordings := []ModelRunEvaluationTrendMetricRecording{}
	for rows.Next() {
		recording, err := scanModelRunEvaluationTrendMetricRecording(rows)
		if err != nil {
			return ModelRunEvaluationTrendMetricRecordingPage{}, err
		}
		recordings = append(recordings, recording)
	}
	if err := rows.Err(); err != nil {
		return ModelRunEvaluationTrendMetricRecordingPage{}, err
	}
	return ModelRunEvaluationTrendMetricRecordingPage{
		Recordings: recordings,
		Page:       Page{Limit: limit, Offset: offset, Total: total, Returned: len(recordings), HasMore: offset+len(recordings) < total},
	}, nil
}

func BuildModelRunEvaluationTrendMetricSamples(page ModelRunEvaluationTrendPage) []ModelRunEvaluationTrendMetricSample {
	samples := []ModelRunEvaluationTrendMetricSample{}
	for _, bucket := range page.Buckets {
		labels := modelRunEvaluationTrendMetricLabels(bucket, page.SnapshotRecordedTo)
		samples = append(samples,
			modelRunEvaluationTrendMetricSample("orrery_ontology_model_run_evaluation_runs_total", labels, bucket.RunTotal),
			modelRunEvaluationTrendMetricSample("orrery_ontology_model_run_evaluation_proposals_total", labels, bucket.ProposalTotal),
			modelRunEvaluationTrendMetricSample("orrery_ontology_model_run_evaluation_analysis_blocking_total", labels, bucket.BlockingTotal),
			modelRunEvaluationTrendMetricSample("orrery_ontology_model_run_evaluation_analysis_warning_total", labels, bucket.WarningTotal),
			modelRunEvaluationTrendMetricSample("orrery_ontology_model_run_evaluation_gate_warning_total", labels, bucket.GateWarningTotal),
		)
		for _, status := range sortedModelRunEvaluationGateStatuses(bucket.StatusCounts) {
			statusLabels := cloneMetricLabels(labels)
			statusLabels["status"] = string(status)
			samples = append(samples, modelRunEvaluationTrendMetricSample("orrery_ontology_model_run_evaluation_status_total", statusLabels, bucket.StatusCounts[status]))
		}
		for _, reason := range sortedModelRunEvaluationReasonCodes(bucket.ReasonCounts) {
			reasonLabels := cloneMetricLabels(labels)
			reasonLabels["reason"] = string(reason)
			samples = append(samples, modelRunEvaluationTrendMetricSample("orrery_ontology_model_run_evaluation_reason_total", reasonLabels, bucket.ReasonCounts[reason]))
		}
		for _, warning := range sortedModelRunEvaluationReasonCodes(bucket.WarningCounts) {
			warningLabels := cloneMetricLabels(labels)
			warningLabels["warning"] = string(warning)
			samples = append(samples, modelRunEvaluationTrendMetricSample("orrery_ontology_model_run_evaluation_warning_total", warningLabels, bucket.WarningCounts[warning]))
		}
		for _, ruleID := range sortedModelRunEvaluationThresholdRuleIDs(bucket.MetricThresholdResultCounts) {
			for _, status := range sortedModelRunEvaluationThresholdResultStatuses(bucket.MetricThresholdResultCounts[ruleID]) {
				thresholdLabels := cloneMetricLabels(labels)
				thresholdLabels["rule_id"] = ruleID
				thresholdLabels["threshold_status"] = string(status)
				samples = append(samples, modelRunEvaluationTrendMetricSample("orrery_ontology_model_run_evaluation_threshold_result_total", thresholdLabels, bucket.MetricThresholdResultCounts[ruleID][status]))
			}
		}
	}
	return samples
}

func RecordModelRunEvaluationTrendMetricSamples(ctx context.Context, sink *metrics.BoundedSink, samples []ModelRunEvaluationTrendMetricSample) int {
	if sink == nil {
		return 0
	}
	recorded := 0
	for _, sample := range samples {
		if err := ctx.Err(); err != nil {
			return recorded
		}
		fields := map[string]any{"value": sample.Value}
		for key, value := range sample.Labels {
			fields[key] = value
		}
		if sink.Capture(metrics.Event{Name: sample.Name, Fields: fields}) {
			recorded++
		}
	}
	return recorded
}

func RecordModelRunEvaluationTrendPageMetrics(ctx context.Context, store ModelRunEvaluationTrendMetricRecordingStore, page ModelRunEvaluationTrendPage, recordedAt time.Time) (int, error) {
	if store == nil {
		return 0, nil
	}
	recorded := 0
	for _, sample := range BuildModelRunEvaluationTrendMetricSamples(page) {
		if err := ctx.Err(); err != nil {
			return recorded, err
		}
		if err := store.SaveModelRunEvaluationTrendMetricRecording(ctx, ModelRunEvaluationTrendMetricRecording{
			MetricSample: sample,
			RecordedAt:   recordedAt,
		}); err != nil {
			return recorded, err
		}
		recorded++
	}
	return recorded, nil
}

func RecordModelRunEvaluationTrendMetrics(ctx context.Context, ledger ModelRunLedger, artifacts ModelRunEvaluationArtifactStore, policies ModelRunEvaluationPolicyProfileStore, store ModelRunEvaluationTrendMetricRecordingStore, query ModelRunEvaluationTrendQuery, recordedAt time.Time) (int, ModelRunEvaluationTrendPage, error) {
	page, err := ListModelRunEvaluationTrendsWithEvaluationPolicyProfiles(ctx, ledger, artifacts, policies, query)
	if err != nil {
		return 0, ModelRunEvaluationTrendPage{}, err
	}
	recorded, err := RecordModelRunEvaluationTrendPageMetrics(ctx, store, page, recordedAt)
	if err != nil {
		return recorded, page, err
	}
	return recorded, page, nil
}

func CollectModelRunEvaluationTrendMetricRecordings(ctx context.Context, store ModelRunEvaluationTrendMetricRecordingStore, sink *metrics.BoundedSink, query ModelRunEvaluationTrendMetricRecordingQuery) (int, ModelRunEvaluationTrendMetricRecordingPage, error) {
	if store == nil {
		return 0, ModelRunEvaluationTrendMetricRecordingPage{}, nil
	}
	page, err := store.ListModelRunEvaluationTrendMetricRecordings(ctx, query)
	if err != nil {
		return 0, ModelRunEvaluationTrendMetricRecordingPage{}, err
	}
	samples := make([]ModelRunEvaluationTrendMetricSample, 0, len(page.Recordings))
	for _, recording := range page.Recordings {
		samples = append(samples, recording.MetricSample)
	}
	return RecordModelRunEvaluationTrendMetricSamples(ctx, sink, samples), page, nil
}

func CollectAllModelRunEvaluationTrendMetricRecordings(ctx context.Context, store ModelRunEvaluationTrendMetricRecordingStore, sink *metrics.BoundedSink, query ModelRunEvaluationTrendMetricRecordingQuery) (ModelRunEvaluationTrendMetricRecordingCollectionReport, error) {
	if store == nil {
		return ModelRunEvaluationTrendMetricRecordingCollectionReport{}, nil
	}
	query.Offset = normalizeMetadataProposalOffset(query.Offset)
	report := ModelRunEvaluationTrendMetricRecordingCollectionReport{}
	for {
		collected, page, err := CollectModelRunEvaluationTrendMetricRecordings(ctx, store, sink, query)
		if err != nil {
			return report, err
		}
		report.Collected += collected
		report.Scanned += len(page.Recordings)
		if len(page.Recordings) > 0 || page.Page.Total > 0 {
			report.Pages++
		}
		if !page.Page.HasMore || len(page.Recordings) == 0 {
			return report, nil
		}
		query.Offset = page.Page.Offset + len(page.Recordings)
	}
}

func normalizeModelRunEvaluationTrendMetricRecording(recording ModelRunEvaluationTrendMetricRecording, now func() time.Time) (ModelRunEvaluationTrendMetricRecording, error) {
	recording.ID = strings.TrimSpace(recording.ID)
	recording.MetricSample.Name = strings.TrimSpace(recording.MetricSample.Name)
	recording.MetricSample.Labels = cloneStringMetadata(recording.MetricSample.Labels)
	if recording.TenantID == "" {
		recording.TenantID = recording.MetricSample.Labels["tenant_id"]
	}
	if recording.Engine == "" {
		recording.Engine = recording.MetricSample.Labels["engine"]
	}
	if recording.Model == "" {
		recording.Model = recording.MetricSample.Labels["model"]
	}
	if recording.ModelVersion == "" {
		recording.ModelVersion = recording.MetricSample.Labels["model_version"]
	}
	recording.TenantID = strings.TrimSpace(recording.TenantID)
	recording.Engine = normalizeID(recording.Engine)
	recording.Model = strings.TrimSpace(recording.Model)
	recording.ModelVersion = strings.TrimSpace(recording.ModelVersion)
	if recording.MetricSample.Name == "" || recording.TenantID == "" {
		return ModelRunEvaluationTrendMetricRecording{}, fmt.Errorf("%w: model run evaluation trend metric recording metric name and tenant_id are required", ErrInvalidDefinition)
	}
	if now == nil {
		now = time.Now
	}
	at := now().UTC()
	if recording.RecordedAt.IsZero() {
		recording.RecordedAt = at
	} else {
		recording.RecordedAt = recording.RecordedAt.UTC()
	}
	if recording.ID == "" {
		recording.ID = modelRunEvaluationTrendMetricRecordingID(recording)
	}
	if recording.CreatedAt.IsZero() {
		recording.CreatedAt = at
	}
	recording.UpdatedAt = at
	return recording, nil
}

func modelRunEvaluationTrendMetricLabels(bucket ModelRunEvaluationTrendBucket, snapshot time.Time) map[string]string {
	labels := map[string]string{
		"tenant_id":     strings.TrimSpace(bucket.TenantID),
		"engine":        normalizeID(bucket.Engine),
		"model":         strings.TrimSpace(bucket.Model),
		"model_version": strings.TrimSpace(bucket.ModelVersion),
	}
	if !snapshot.IsZero() {
		labels["snapshot_recorded_to"] = snapshot.UTC().Format(time.RFC3339Nano)
	}
	return labels
}

func modelRunEvaluationTrendMetricSample(name string, labels map[string]string, value int) ModelRunEvaluationTrendMetricSample {
	return ModelRunEvaluationTrendMetricSample{
		Name:   name,
		Labels: cloneMetricLabels(labels),
		Value:  float64(value),
	}
}

func modelRunEvaluationTrendMetricRecordingID(recording ModelRunEvaluationTrendMetricRecording) string {
	return "ontology_model_run_evaluation_metric_recording:" + hashMappingRule(map[string]any{
		"metric_name":          recording.MetricSample.Name,
		"tenant_id":            recording.TenantID,
		"engine":               recording.Engine,
		"model":                recording.Model,
		"model_version":        recording.ModelVersion,
		"status":               recording.MetricSample.Labels["status"],
		"reason":               recording.MetricSample.Labels["reason"],
		"warning":              recording.MetricSample.Labels["warning"],
		"rule_id":              recording.MetricSample.Labels["rule_id"],
		"threshold_status":     recording.MetricSample.Labels["threshold_status"],
		"snapshot_recorded_to": recording.MetricSample.Labels["snapshot_recorded_to"],
		"recorded_at":          recording.RecordedAt.Format(time.RFC3339Nano),
	})
}

func modelRunEvaluationTrendMetricRecordingMatches(recording ModelRunEvaluationTrendMetricRecording, query ModelRunEvaluationTrendMetricRecordingQuery) bool {
	return (strings.TrimSpace(query.TenantID) == "" || recording.TenantID == strings.TrimSpace(query.TenantID)) &&
		(normalizeID(query.Engine) == "" || recording.Engine == normalizeID(query.Engine)) &&
		(strings.TrimSpace(query.MetricName) == "" || recording.MetricSample.Name == strings.TrimSpace(query.MetricName)) &&
		modelRunEvaluationTrendMetricRecordingLabelMatches(recording.MetricSample.Labels, query) &&
		(query.RecordedAt.IsZero() || recording.RecordedAt.Equal(query.RecordedAt.UTC())) &&
		(query.RecordedFrom.IsZero() || !recording.RecordedAt.Before(query.RecordedFrom.UTC())) &&
		(query.RecordedTo.IsZero() || recording.RecordedAt.Before(query.RecordedTo.UTC()))
}

func modelRunEvaluationTrendMetricRecordingWhere(query ModelRunEvaluationTrendMetricRecordingQuery, dialect db.Dialect) (string, []any) {
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
	if metricName := strings.TrimSpace(query.MetricName); metricName != "" {
		clauses = append(clauses, "metric_name = ?")
		args = append(args, metricName)
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
	if !query.RecordedAt.IsZero() {
		clauses = append(clauses, "recorded_at = ?")
		args = append(args, timeToUnixNano(query.RecordedAt.UTC()))
	}
	if !query.RecordedFrom.IsZero() {
		clauses = append(clauses, "recorded_at >= ?")
		args = append(args, timeToUnixNano(query.RecordedFrom.UTC()))
	}
	if !query.RecordedTo.IsZero() {
		clauses = append(clauses, "recorded_at < ?")
		args = append(args, timeToUnixNano(query.RecordedTo.UTC()))
	}
	if len(clauses) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

func modelRunEvaluationTrendMetricRecordingLabelMatches(labels map[string]string, query ModelRunEvaluationTrendMetricRecordingQuery) bool {
	labelKey := strings.TrimSpace(query.LabelKey)
	if labelKey == "" {
		return true
	}
	value, ok := labels[labelKey]
	return ok && strings.TrimSpace(value) == strings.TrimSpace(query.LabelValue)
}

func modelRunEvaluationTrendMetricRecordingPage(recordings []ModelRunEvaluationTrendMetricRecording, query ModelRunEvaluationTrendMetricRecordingQuery) ModelRunEvaluationTrendMetricRecordingPage {
	limit := normalizeMetadataProposalLimit(query.Limit)
	offset := normalizeMetadataProposalOffset(query.Offset)
	total := len(recordings)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	items := append([]ModelRunEvaluationTrendMetricRecording(nil), recordings[offset:end]...)
	return ModelRunEvaluationTrendMetricRecordingPage{Recordings: items, Page: Page{Limit: limit, Offset: offset, Total: total, Returned: len(items), HasMore: end < total}}
}

func scanModelRunEvaluationTrendMetricRecording(row rowScanner) (ModelRunEvaluationTrendMetricRecording, error) {
	var recording ModelRunEvaluationTrendMetricRecording
	var metricName string
	var labelsJSON string
	var recordedAt, createdAt, updatedAt int64
	if err := row.Scan(&recording.ID, &recording.TenantID, &recording.Engine, &recording.Model, &recording.ModelVersion, &metricName, &recording.MetricSample.Value, &labelsJSON, &recordedAt, &createdAt, &updatedAt); err != nil {
		return ModelRunEvaluationTrendMetricRecording{}, err
	}
	recording.MetricSample.Name = metricName
	mustDecodeJSON(labelsJSON, &recording.MetricSample.Labels)
	recording.MetricSample.Labels = cloneStringMetadata(recording.MetricSample.Labels)
	recording.RecordedAt = unixNanoToTime(recordedAt)
	recording.CreatedAt = unixNanoToTime(createdAt)
	recording.UpdatedAt = unixNanoToTime(updatedAt)
	return recording, nil
}

func cloneModelRunEvaluationTrendMetricRecording(recording ModelRunEvaluationTrendMetricRecording) ModelRunEvaluationTrendMetricRecording {
	recording.MetricSample.Labels = cloneStringMetadata(recording.MetricSample.Labels)
	return recording
}

func cloneMetricLabels(labels map[string]string) map[string]string {
	if labels == nil {
		return nil
	}
	cloned := make(map[string]string, len(labels))
	for key, value := range labels {
		cloned[key] = value
	}
	return cloned
}

func sortedModelRunEvaluationGateStatuses(counts map[ModelRunEvaluationGateStatus]int) []ModelRunEvaluationGateStatus {
	values := make([]ModelRunEvaluationGateStatus, 0, len(counts))
	for value, count := range counts {
		if count > 0 {
			values = append(values, value)
		}
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	return values
}

func sortedModelRunEvaluationReasonCodes(counts map[ModelRunEvaluationReasonCode]int) []ModelRunEvaluationReasonCode {
	values := make([]ModelRunEvaluationReasonCode, 0, len(counts))
	for value, count := range counts {
		if count > 0 {
			values = append(values, value)
		}
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	return values
}

func sortedModelRunEvaluationThresholdRuleIDs(counts map[string]map[ModelRunEvaluationThresholdResultStatus]int) []string {
	values := make([]string, 0, len(counts))
	for value, statuses := range counts {
		if normalizeID(value) == "" || len(statuses) == 0 {
			continue
		}
		values = append(values, normalizeID(value))
	}
	sort.Strings(values)
	return values
}

func sortedModelRunEvaluationThresholdResultStatuses(counts map[ModelRunEvaluationThresholdResultStatus]int) []ModelRunEvaluationThresholdResultStatus {
	values := make([]ModelRunEvaluationThresholdResultStatus, 0, len(counts))
	for value, count := range counts {
		if value != "" && count > 0 {
			values = append(values, value)
		}
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	return values
}
