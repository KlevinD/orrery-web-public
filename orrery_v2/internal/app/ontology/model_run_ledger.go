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

type ModelRunStatus string

const (
	ModelRunStatusStarted   ModelRunStatus = "started"
	ModelRunStatusSucceeded ModelRunStatus = "succeeded"
	ModelRunStatusFailed    ModelRunStatus = "failed"
)

type OntologyModelRun struct {
	ID                    string         `json:"id"`
	TenantID              string         `json:"tenant_id,omitempty"`
	Engine                string         `json:"engine"`
	Model                 string         `json:"model"`
	ModelVersion          string         `json:"model_version"`
	RunID                 string         `json:"run_id"`
	InputHash             string         `json:"input_hash"`
	PromptHash            string         `json:"prompt_hash"`
	SourceRef             string         `json:"source_ref,omitempty"`
	SourceVersion         string         `json:"source_version,omitempty"`
	Status                ModelRunStatus `json:"status"`
	CandidateCount        int            `json:"candidate_count"`
	ValidCandidateCount   int            `json:"valid_candidate_count"`
	SkippedCandidateCount int            `json:"skipped_candidate_count"`
	ProposalCount         int            `json:"proposal_count"`
	AnalysisTotal         int            `json:"analysis_total"`
	AnalysisBlockingCount int            `json:"analysis_blocking_count"`
	AnalysisWarningCount  int            `json:"analysis_warning_count"`
	ErrorMessage          string         `json:"error_message,omitempty"`
	RecordedAt            time.Time      `json:"recorded_at,omitempty"`
	CreatedAt             time.Time      `json:"created_at,omitempty"`
	UpdatedAt             time.Time      `json:"updated_at,omitempty"`

	RawPrompt string `json:"-"`
	RawInput  string `json:"-"`
	RawOutput string `json:"-"`
}

type ModelRunQuery struct {
	TenantID     string
	Engine       string
	RunID        string
	Status       ModelRunStatus
	RecordedFrom time.Time
	RecordedTo   time.Time
	Limit        int
	Offset       int
}

type ModelRunPage struct {
	Runs []OntologyModelRun `json:"runs"`
	Page Page               `json:"page"`
}

type ModelRunLedger interface {
	SaveModelRun(ctx context.Context, run OntologyModelRun) error
	ListModelRuns(ctx context.Context, query ModelRunQuery) (ModelRunPage, error)
}

type ModelRunEvaluationTrendAggregator interface {
	ListModelRunEvaluationTrendBuckets(ctx context.Context, query ModelRunEvaluationTrendQuery) (ModelRunEvaluationTrendPage, error)
}

type MemoryModelRunLedger struct {
	mu   sync.RWMutex
	runs map[string]OntologyModelRun
	now  func() time.Time
}

func NewMemoryModelRunLedger() *MemoryModelRunLedger {
	return &MemoryModelRunLedger{runs: map[string]OntologyModelRun{}, now: time.Now}
}

func (l *MemoryModelRunLedger) SaveModelRun(ctx context.Context, run OntologyModelRun) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	normalized, err := normalizeModelRun(run, l.now)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if previous, ok := l.runs[normalized.ID]; ok && !previous.CreatedAt.IsZero() {
		normalized.CreatedAt = previous.CreatedAt
	}
	l.runs[normalized.ID] = normalized
	return nil
}

func (l *MemoryModelRunLedger) ListModelRuns(ctx context.Context, query ModelRunQuery) (ModelRunPage, error) {
	if err := ctx.Err(); err != nil {
		return ModelRunPage{}, err
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	matched := make([]OntologyModelRun, 0, len(l.runs))
	for _, run := range l.runs {
		if modelRunMatches(run, query) {
			matched = append(matched, cloneModelRun(run))
		}
	}
	sort.Slice(matched, func(i, j int) bool {
		if matched[i].RecordedAt.Equal(matched[j].RecordedAt) {
			return matched[i].ID < matched[j].ID
		}
		return matched[i].RecordedAt.Before(matched[j].RecordedAt)
	})
	return modelRunPage(matched, query), nil
}

type PlatformModelRunLedger struct {
	store *db.Store
	tx    db.Tx
	now   func() time.Time
}

func NewPlatformModelRunLedger(store *db.Store) *PlatformModelRunLedger {
	return &PlatformModelRunLedger{store: store, now: time.Now}
}

func NewPlatformModelRunLedgerTx(tx db.Tx) *PlatformModelRunLedger {
	return &PlatformModelRunLedger{tx: tx, now: time.Now}
}

func (l *PlatformModelRunLedger) ready() error {
	if l == nil || (l.store == nil && l.tx == nil) {
		return fmt.Errorf("%w: model run ledger store is required", ErrInvalidDefinition)
	}
	return nil
}

func (l *PlatformModelRunLedger) exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if err := l.ready(); err != nil {
		return nil, err
	}
	if l.tx != nil {
		return l.tx.ExecContext(ctx, query, args...)
	}
	return l.store.Exec(ctx, query, args...)
}

func (l *PlatformModelRunLedger) query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if err := l.ready(); err != nil {
		return nil, err
	}
	if l.tx != nil {
		return l.tx.QueryContext(ctx, query, args...)
	}
	return l.store.Query(ctx, query, args...)
}

func (l *PlatformModelRunLedger) queryRow(ctx context.Context, query string, args ...any) rowScanner {
	if l.tx != nil {
		return l.tx.QueryRowContext(ctx, query, args...)
	}
	return l.store.QueryRow(ctx, query, args...)
}

func (l *PlatformModelRunLedger) SaveModelRun(ctx context.Context, run OntologyModelRun) error {
	if err := l.ready(); err != nil {
		return err
	}
	normalized, err := normalizeModelRun(run, l.now)
	if err != nil {
		return err
	}
	_, err = l.exec(ctx, `
INSERT INTO ontology_model_runs (
	id, tenant_id, engine, model, model_version, run_id, input_hash, prompt_hash,
	source_ref, source_version, status, candidate_count, valid_candidate_count,
	skipped_candidate_count, proposal_count, analysis_total, analysis_blocking_count,
	analysis_warning_count, error_message, recorded_at, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	model = excluded.model,
	model_version = excluded.model_version,
	input_hash = excluded.input_hash,
	prompt_hash = excluded.prompt_hash,
	source_ref = excluded.source_ref,
	source_version = excluded.source_version,
	status = excluded.status,
	candidate_count = excluded.candidate_count,
	valid_candidate_count = excluded.valid_candidate_count,
	skipped_candidate_count = excluded.skipped_candidate_count,
	proposal_count = excluded.proposal_count,
	analysis_total = excluded.analysis_total,
	analysis_blocking_count = excluded.analysis_blocking_count,
	analysis_warning_count = excluded.analysis_warning_count,
	error_message = excluded.error_message,
	recorded_at = excluded.recorded_at,
	updated_at = excluded.updated_at`,
		normalized.ID,
		normalized.TenantID,
		normalized.Engine,
		normalized.Model,
		normalized.ModelVersion,
		normalized.RunID,
		normalized.InputHash,
		normalized.PromptHash,
		normalized.SourceRef,
		normalized.SourceVersion,
		normalized.Status,
		normalized.CandidateCount,
		normalized.ValidCandidateCount,
		normalized.SkippedCandidateCount,
		normalized.ProposalCount,
		normalized.AnalysisTotal,
		normalized.AnalysisBlockingCount,
		normalized.AnalysisWarningCount,
		normalized.ErrorMessage,
		timeToUnixNano(normalized.RecordedAt),
		timeToUnixNano(normalized.CreatedAt),
		timeToUnixNano(normalized.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("save ontology model run: %w", err)
	}
	return nil
}

func (l *PlatformModelRunLedger) ListModelRuns(ctx context.Context, query ModelRunQuery) (ModelRunPage, error) {
	if err := l.ready(); err != nil {
		return ModelRunPage{}, err
	}
	limit := normalizeMetadataProposalLimit(query.Limit)
	offset := normalizeMetadataProposalOffset(query.Offset)
	where, args := modelRunWhere(query)
	var total int
	if err := l.queryRow(ctx, `SELECT COUNT(*) FROM ontology_model_runs`+where, args...).Scan(&total); err != nil {
		return ModelRunPage{}, fmt.Errorf("count ontology model runs: %w", err)
	}
	rows, err := l.query(ctx, `SELECT id, tenant_id, engine, model, model_version, run_id, input_hash, prompt_hash,
source_ref, source_version, status, candidate_count, valid_candidate_count, skipped_candidate_count,
proposal_count, analysis_total, analysis_blocking_count, analysis_warning_count, error_message,
recorded_at, created_at, updated_at FROM ontology_model_runs`+where+` ORDER BY recorded_at ASC, id ASC LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	if err != nil {
		return ModelRunPage{}, fmt.Errorf("list ontology model runs: %w", err)
	}
	defer rows.Close()
	runs := []OntologyModelRun{}
	for rows.Next() {
		run, err := scanModelRun(rows)
		if err != nil {
			return ModelRunPage{}, err
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return ModelRunPage{}, err
	}
	return ModelRunPage{
		Runs: runs,
		Page: Page{Limit: limit, Offset: offset, Total: total, Returned: len(runs), HasMore: offset+len(runs) < total},
	}, nil
}

func (l *PlatformModelRunLedger) ListModelRunEvaluationTrendBuckets(ctx context.Context, query ModelRunEvaluationTrendQuery) (ModelRunEvaluationTrendPage, error) {
	if err := l.ready(); err != nil {
		return ModelRunEvaluationTrendPage{}, err
	}
	normalizedQuery, err := normalizeModelRunEvaluationTrendSnapshot(query, time.Now)
	if err != nil {
		return ModelRunEvaluationTrendPage{}, err
	}
	query = normalizedQuery
	where, args := modelRunWhere(ModelRunQuery{
		TenantID:     strings.TrimSpace(query.TenantID),
		Engine:       query.Engine,
		Status:       query.Status,
		RecordedFrom: query.RecordedFrom,
		RecordedTo:   query.RecordedTo,
	})
	rows, err := l.query(ctx, `SELECT tenant_id, engine, model, model_version,
COUNT(*) AS run_total,
SUM(CASE WHEN status != ? AND status != ? THEN 1 ELSE 0 END) AS pending_reason_total,
SUM(CASE WHEN status != ? AND status != ? AND proposal_count > 0 AND analysis_blocking_count = 0 THEN 1 ELSE 0 END) AS pending_total,
SUM(CASE WHEN status = ? AND proposal_count > 0 AND analysis_blocking_count = 0 THEN 1 ELSE 0 END) AS passed_total,
SUM(CASE WHEN status = ? OR proposal_count = 0 OR analysis_blocking_count > 0 THEN 1 ELSE 0 END) AS blocked_total,
SUM(CASE WHEN status = ? THEN 1 ELSE 0 END) AS failed_total,
SUM(CASE WHEN proposal_count = 0 THEN 1 ELSE 0 END) AS no_proposals_total,
SUM(CASE WHEN analysis_blocking_count > 0 THEN 1 ELSE 0 END) AS blocking_issue_total,
SUM(proposal_count) AS proposal_total,
SUM(analysis_blocking_count) AS blocking_total,
SUM(analysis_warning_count) AS warning_total
FROM ontology_model_runs`+where+`
GROUP BY tenant_id, engine, model, model_version
ORDER BY engine ASC, model ASC, model_version ASC`, append([]any{string(ModelRunStatusSucceeded), string(ModelRunStatusFailed), string(ModelRunStatusSucceeded), string(ModelRunStatusFailed), string(ModelRunStatusSucceeded), string(ModelRunStatusFailed), string(ModelRunStatusFailed)}, args...)...)
	if err != nil {
		return ModelRunEvaluationTrendPage{}, fmt.Errorf("aggregate ontology model run evaluation trends: %w", err)
	}
	defer rows.Close()
	buckets := []ModelRunEvaluationTrendBucket{}
	allBuckets := []ModelRunEvaluationTrendBucket{}
	for rows.Next() {
		var bucket ModelRunEvaluationTrendBucket
		var pendingReasonTotal, pendingTotal, passedTotal, blockedTotal, failedTotal, noProposalsTotal, blockingIssueTotal int
		if err := rows.Scan(
			&bucket.TenantID,
			&bucket.Engine,
			&bucket.Model,
			&bucket.ModelVersion,
			&bucket.RunTotal,
			&pendingReasonTotal,
			&pendingTotal,
			&passedTotal,
			&blockedTotal,
			&failedTotal,
			&noProposalsTotal,
			&blockingIssueTotal,
			&bucket.ProposalTotal,
			&bucket.BlockingTotal,
			&bucket.WarningTotal,
		); err != nil {
			return ModelRunEvaluationTrendPage{}, fmt.Errorf("scan ontology model run evaluation trend bucket: %w", err)
		}
		bucket.StatusCounts = map[ModelRunEvaluationGateStatus]int{}
		bucket.ReasonCounts = map[ModelRunEvaluationReasonCode]int{}
		bucket.WarningCounts = map[ModelRunEvaluationReasonCode]int{}
		bucket.MetricThresholdResultCounts = map[string]map[ModelRunEvaluationThresholdResultStatus]int{}
		if passedTotal > 0 {
			bucket.StatusCounts[ModelRunEvaluationGatePassed] = passedTotal
		}
		if blockedTotal > 0 {
			bucket.StatusCounts[ModelRunEvaluationGateBlocked] = blockedTotal
		}
		if pendingTotal > 0 {
			bucket.StatusCounts[ModelRunEvaluationGatePending] = pendingTotal
		}
		if pendingReasonTotal > 0 {
			bucket.ReasonCounts[ModelRunEvaluationReasonRunPending] = pendingReasonTotal
		}
		if failedTotal > 0 {
			bucket.ReasonCounts[ModelRunEvaluationReasonRunFailed] = failedTotal
		}
		if noProposalsTotal > 0 {
			bucket.ReasonCounts[ModelRunEvaluationReasonNoProposals] = noProposalsTotal
		}
		if blockingIssueTotal > 0 {
			bucket.ReasonCounts[ModelRunEvaluationReasonBlockingIssues] = blockingIssueTotal
		}
		allBuckets = append(allBuckets, bucket)
		if modelRunEvaluationTrendBucketMatches(bucket, query) {
			buckets = append(buckets, bucket)
		}
	}
	if err := rows.Err(); err != nil {
		return ModelRunEvaluationTrendPage{}, err
	}
	annotateModelRunEvaluationTrendBaselineComparisons(buckets, allBuckets, query)
	return modelRunEvaluationTrendPage(buckets, query), nil
}

func normalizeModelRun(run OntologyModelRun, now func() time.Time) (OntologyModelRun, error) {
	run.TenantID = strings.TrimSpace(run.TenantID)
	run.Engine = normalizeID(run.Engine)
	run.Model = strings.TrimSpace(run.Model)
	run.ModelVersion = strings.TrimSpace(run.ModelVersion)
	run.RunID = strings.TrimSpace(run.RunID)
	run.InputHash = strings.TrimSpace(run.InputHash)
	run.PromptHash = strings.TrimSpace(run.PromptHash)
	run.SourceRef = strings.TrimSpace(run.SourceRef)
	run.SourceVersion = strings.TrimSpace(run.SourceVersion)
	run.ErrorMessage = strings.TrimSpace(run.ErrorMessage)
	run.RawPrompt = ""
	run.RawInput = ""
	run.RawOutput = ""
	if run.Engine == "" || run.Model == "" || run.ModelVersion == "" || run.RunID == "" || run.InputHash == "" || run.PromptHash == "" {
		return OntologyModelRun{}, fmt.Errorf("%w: model run engine, model, model_version, run_id, input_hash, and prompt_hash are required", ErrInvalidDefinition)
	}
	if run.Status == "" {
		run.Status = ModelRunStatusStarted
	}
	if run.ID == "" {
		run.ID = modelRunID(run.TenantID, run.Engine, run.RunID)
	}
	if now == nil {
		now = time.Now
	}
	at := now().UTC()
	if run.CreatedAt.IsZero() {
		run.CreatedAt = at
	}
	if run.RecordedAt.IsZero() {
		run.RecordedAt = at
	}
	run.UpdatedAt = at
	return run, nil
}

func modelRunID(tenantID string, engine string, runID string) string {
	return "ontology_model_run:" + hashMappingRule(map[string]any{
		"tenant_id": strings.TrimSpace(tenantID),
		"engine":    normalizeID(engine),
		"run_id":    strings.TrimSpace(runID),
	})
}

func modelRunMatches(run OntologyModelRun, query ModelRunQuery) bool {
	if !(strings.TrimSpace(query.TenantID) == "" || run.TenantID == strings.TrimSpace(query.TenantID)) ||
		!(normalizeID(query.Engine) == "" || run.Engine == normalizeID(query.Engine)) ||
		!(strings.TrimSpace(query.RunID) == "" || run.RunID == strings.TrimSpace(query.RunID)) ||
		!(query.Status == "" || run.Status == query.Status) {
		return false
	}
	if !query.RecordedFrom.IsZero() && run.RecordedAt.Before(query.RecordedFrom.UTC()) {
		return false
	}
	if !query.RecordedTo.IsZero() && !run.RecordedAt.Before(query.RecordedTo.UTC()) {
		return false
	}
	return true
}

func modelRunWhere(query ModelRunQuery) (string, []any) {
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
	if runID := strings.TrimSpace(query.RunID); runID != "" {
		clauses = append(clauses, "run_id = ?")
		args = append(args, runID)
	}
	if query.Status != "" {
		clauses = append(clauses, "status = ?")
		args = append(args, string(query.Status))
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

func modelRunPage(runs []OntologyModelRun, query ModelRunQuery) ModelRunPage {
	limit := normalizeMetadataProposalLimit(query.Limit)
	offset := normalizeMetadataProposalOffset(query.Offset)
	total := len(runs)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	items := append([]OntologyModelRun(nil), runs[offset:end]...)
	return ModelRunPage{Runs: items, Page: Page{Limit: limit, Offset: offset, Total: total, Returned: len(items), HasMore: end < total}}
}

func scanModelRun(row rowScanner) (OntologyModelRun, error) {
	var run OntologyModelRun
	var status string
	var recordedAt, createdAt, updatedAt int64
	if err := row.Scan(&run.ID, &run.TenantID, &run.Engine, &run.Model, &run.ModelVersion, &run.RunID, &run.InputHash, &run.PromptHash, &run.SourceRef, &run.SourceVersion, &status, &run.CandidateCount, &run.ValidCandidateCount, &run.SkippedCandidateCount, &run.ProposalCount, &run.AnalysisTotal, &run.AnalysisBlockingCount, &run.AnalysisWarningCount, &run.ErrorMessage, &recordedAt, &createdAt, &updatedAt); err != nil {
		return OntologyModelRun{}, err
	}
	run.Status = ModelRunStatus(status)
	run.RecordedAt = unixNanoToTime(recordedAt)
	run.CreatedAt = unixNanoToTime(createdAt)
	run.UpdatedAt = unixNanoToTime(updatedAt)
	return run, nil
}

func cloneModelRun(run OntologyModelRun) OntologyModelRun {
	run.RawPrompt = ""
	run.RawInput = ""
	run.RawOutput = ""
	return run
}
