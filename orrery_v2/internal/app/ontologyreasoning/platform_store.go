package ontologyreasoning

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/platform/db"
)

type PlatformStore struct {
	store *db.Store
}

func NewPlatformStore(store *db.Store) *PlatformStore {
	return &PlatformStore{store: store}
}

func (s *PlatformStore) SaveRun(ctx context.Context, run ReasoningRun) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("%w: platform store is required", ErrInvalidRequest)
	}
	if strings.TrimSpace(run.ID) == "" {
		return fmt.Errorf("%w: run id is required", ErrInvalidRequest)
	}
	result, err := s.store.Exec(ctx, `
INSERT INTO ontology_reasoning_runs (
	id, tenant_id, engine, model, model_version, intent_hash, input_hash, prompt_hash,
	context_pack_hash, status, candidate_count, valid_candidate_count, rejected_candidate_count,
	review_required_count, request_json, context_pack_json, candidates_json, validation_json,
	proposals_json, error_message, created_at, updated_at, completed_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	engine = excluded.engine,
	model = excluded.model,
	model_version = excluded.model_version,
	intent_hash = excluded.intent_hash,
	input_hash = excluded.input_hash,
	prompt_hash = excluded.prompt_hash,
	context_pack_hash = excluded.context_pack_hash,
	status = excluded.status,
	candidate_count = excluded.candidate_count,
	valid_candidate_count = excluded.valid_candidate_count,
	rejected_candidate_count = excluded.rejected_candidate_count,
	review_required_count = excluded.review_required_count,
	request_json = excluded.request_json,
	context_pack_json = excluded.context_pack_json,
	candidates_json = excluded.candidates_json,
	validation_json = excluded.validation_json,
	proposals_json = excluded.proposals_json,
	error_message = excluded.error_message,
	updated_at = excluded.updated_at,
	completed_at = excluded.completed_at
WHERE ontology_reasoning_runs.tenant_id = excluded.tenant_id`,
		run.ID,
		run.TenantID,
		run.Engine,
		run.Model,
		run.ModelVersion,
		run.IntentHash,
		run.InputHash,
		run.PromptHash,
		run.ContextPackHash,
		string(run.Status),
		run.CandidateCount,
		run.ValidCandidateCount,
		run.RejectedCandidateCount,
		run.ReviewRequiredCount,
		mustReasoningJSON(run.Request),
		mustReasoningJSON(run.ContextPack),
		mustReasoningJSON(run.Candidates),
		mustReasoningJSON(run.Validation),
		mustReasoningJSON(run.Proposals),
		run.ErrorMessage,
		timeToUnixNano(run.CreatedAt),
		timeToUnixNano(run.UpdatedAt),
		timePtrToUnixNano(run.CompletedAt),
	)
	if err != nil {
		return fmt.Errorf("save ontology reasoning run: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("save ontology reasoning run rows affected: %w", err)
	} else if affected == 0 {
		return fmt.Errorf("%w: run id is owned by another tenant", ErrInvalidRequest)
	}
	return nil
}

func (s *PlatformStore) GetRun(ctx context.Context, tenantID string, runID string) (ReasoningRun, error) {
	if s == nil || s.store == nil {
		return ReasoningRun{}, fmt.Errorf("%w: platform store is required", ErrInvalidRequest)
	}
	row := s.store.QueryRow(ctx, `SELECT `+ontologyReasoningRunColumns()+` FROM ontology_reasoning_runs WHERE tenant_id = ? AND id = ?`, tenantID, runID)
	run, err := scanReasoningRun(row)
	if err == sql.ErrNoRows {
		return ReasoningRun{}, ErrNotFound
	}
	if err != nil {
		return ReasoningRun{}, fmt.Errorf("get ontology reasoning run: %w", err)
	}
	return run, nil
}

func (s *PlatformStore) ListRuns(ctx context.Context, tenantID string) ([]ReasoningRun, error) {
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("%w: platform store is required", ErrInvalidRequest)
	}
	rows, err := s.store.Query(ctx, `SELECT `+ontologyReasoningRunColumns()+` FROM ontology_reasoning_runs WHERE tenant_id = ? ORDER BY created_at DESC, id DESC`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list ontology reasoning runs: %w", err)
	}
	defer rows.Close()
	runs := []ReasoningRun{}
	for rows.Next() {
		run, err := scanReasoningRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate ontology reasoning runs: %w", err)
	}
	return runs, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func ontologyReasoningRunColumns() string {
	return `id, tenant_id, engine, model, model_version, intent_hash, input_hash, prompt_hash,
context_pack_hash, status, candidate_count, valid_candidate_count, rejected_candidate_count,
review_required_count, request_json, context_pack_json, candidates_json, validation_json,
proposals_json, error_message, created_at, updated_at, completed_at`
}

func scanReasoningRun(row rowScanner) (ReasoningRun, error) {
	var run ReasoningRun
	var status string
	var requestJSON, contextPackJSON, candidatesJSON, validationJSON, proposalsJSON string
	var createdAt, updatedAt, completedAt int64
	if err := row.Scan(
		&run.ID,
		&run.TenantID,
		&run.Engine,
		&run.Model,
		&run.ModelVersion,
		&run.IntentHash,
		&run.InputHash,
		&run.PromptHash,
		&run.ContextPackHash,
		&status,
		&run.CandidateCount,
		&run.ValidCandidateCount,
		&run.RejectedCandidateCount,
		&run.ReviewRequiredCount,
		&requestJSON,
		&contextPackJSON,
		&candidatesJSON,
		&validationJSON,
		&proposalsJSON,
		&run.ErrorMessage,
		&createdAt,
		&updatedAt,
		&completedAt,
	); err != nil {
		return ReasoningRun{}, err
	}
	run.Status = RunStatus(status)
	decodeReasoningJSON(requestJSON, &run.Request)
	decodeReasoningJSON(contextPackJSON, &run.ContextPack)
	decodeReasoningJSON(candidatesJSON, &run.Candidates)
	decodeReasoningJSON(validationJSON, &run.Validation)
	decodeReasoningJSON(proposalsJSON, &run.Proposals)
	run.CreatedAt = unixNanoToTime(createdAt)
	run.UpdatedAt = unixNanoToTime(updatedAt)
	if completed := unixNanoToTime(completedAt); !completed.IsZero() {
		run.CompletedAt = &completed
	}
	return run, nil
}

func mustReasoningJSON(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func decodeReasoningJSON(raw string, out any) {
	if strings.TrimSpace(raw) == "" {
		return
	}
	_ = json.Unmarshal([]byte(raw), out)
}

func timeToUnixNano(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UTC().UnixNano()
}

func timePtrToUnixNano(value *time.Time) int64 {
	if value == nil {
		return 0
	}
	return timeToUnixNano(*value)
}

func unixNanoToTime(value int64) time.Time {
	if value == 0 {
		return time.Time{}
	}
	return time.Unix(0, value).UTC()
}
