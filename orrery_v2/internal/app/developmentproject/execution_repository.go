package developmentproject

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"orrery_v2/internal/platform/db"
)

const (
	workspaceTable           = "core.development_workspaces"
	executionRunTable        = "core.todo_execution_runs"
	commitRecordTable        = "core.todo_commit_records"
	submissionTable          = "core.todo_code_submissions"
	mergeOperationTable      = "core.development_merge_operations"
	submissionOperationTable = "core.development_submission_operations"
)

type ExecutionRepository struct{ router *db.Router }

func NewExecutionRepository(router *db.Router) *ExecutionRepository {
	return &ExecutionRepository{router: router}
}

func (r *ExecutionRepository) CreateWorkspace(ctx context.Context, workspace DevelopmentWorkspace) (bool, error) {
	return r.create(ctx, workspaceTable, executionKey(workspace.TenantID, workspace.WorkspaceID), executionRecord(workspace.TenantID, workspace.WorkspaceID, workspace.DevelopmentProjectID, workspace.TodoID, workspace.Status, workspace.Version, workspace))
}

func (r *ExecutionRepository) GetWorkspace(ctx context.Context, tenantID, workspaceID string) (DevelopmentWorkspace, error) {
	var out DevelopmentWorkspace
	if err := r.get(ctx, workspaceTable, tenantID, workspaceID, &out); err != nil {
		return DevelopmentWorkspace{}, err
	}
	out.TenantID = normalizeTenantID(tenantID)
	return out, nil
}

func (r *ExecutionRepository) UpdateWorkspace(ctx context.Context, workspace DevelopmentWorkspace, expectedVersion int64) error {
	return r.update(ctx, workspaceTable, workspace.TenantID, workspace.WorkspaceID, workspace.DevelopmentProjectID, workspace.TodoID, workspace.Status, workspace.Version, expectedVersion, workspace)
}

func (r *ExecutionRepository) ListWorkspaces(ctx context.Context, tenantID string) ([]DevelopmentWorkspace, error) {
	var out []DevelopmentWorkspace
	if err := r.list(ctx, workspaceTable, tenantID, &out); err != nil {
		return nil, err
	}
	for index := range out {
		out[index].TenantID = normalizeTenantID(tenantID)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].LastActiveAt.Equal(out[j].LastActiveAt) {
			return out[i].WorkspaceID < out[j].WorkspaceID
		}
		return out[i].LastActiveAt.After(out[j].LastActiveAt)
	})
	return out, nil
}

func (r *ExecutionRepository) CreateRun(ctx context.Context, run TodoExecutionRun) (bool, error) {
	return r.create(ctx, executionRunTable, executionKey(run.TenantID, run.ExecutionRunID), executionRecord(run.TenantID, run.ExecutionRunID, run.DevelopmentProjectID, run.TodoID, run.Status, run.Version, run))
}

func (r *ExecutionRepository) GetRun(ctx context.Context, tenantID, runID string) (TodoExecutionRun, error) {
	var out TodoExecutionRun
	if err := r.get(ctx, executionRunTable, tenantID, runID, &out); err != nil {
		return TodoExecutionRun{}, err
	}
	out.TenantID = normalizeTenantID(tenantID)
	return out, nil
}

func (r *ExecutionRepository) UpdateRun(ctx context.Context, run TodoExecutionRun, expectedVersion int64) error {
	return r.update(ctx, executionRunTable, run.TenantID, run.ExecutionRunID, run.DevelopmentProjectID, run.TodoID, run.Status, run.Version, expectedVersion, run)
}

func (r *ExecutionRepository) ListRuns(ctx context.Context, tenantID string) ([]TodoExecutionRun, error) {
	var out []TodoExecutionRun
	if err := r.list(ctx, executionRunTable, tenantID, &out); err != nil {
		return nil, err
	}
	for index := range out {
		out[index].TenantID = normalizeTenantID(tenantID)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ExecutionRunID < out[j].ExecutionRunID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

func (r *ExecutionRepository) FindRunForMissionTask(ctx context.Context, tenantID, missionID, missionTaskID string) (TodoExecutionRun, error) {
	missionID, missionTaskID = strings.TrimSpace(missionID), strings.TrimSpace(missionTaskID)
	if !validResourceID(missionID) || !validResourceID(missionTaskID) {
		return TodoExecutionRun{}, ErrInvalidRequest
	}
	runs, err := r.ListRuns(ctx, tenantID)
	if err != nil {
		return TodoExecutionRun{}, err
	}
	var match TodoExecutionRun
	for _, run := range runs {
		if run.MissionID != missionID || run.MissionTaskID != missionTaskID {
			continue
		}
		if match.ExecutionRunID != "" {
			return TodoExecutionRun{}, ErrDependency
		}
		match = run
	}
	if match.ExecutionRunID == "" {
		return TodoExecutionRun{}, ErrNotFound
	}
	return match, nil
}

func (r *ExecutionRepository) CreateCommit(ctx context.Context, record TodoCommitRecord) (bool, error) {
	return r.create(ctx, commitRecordTable, executionKey(record.TenantID, record.CommitRecordID), executionRecord(record.TenantID, record.CommitRecordID, record.DevelopmentProjectID, record.TodoID, "created", 1, record))
}

func (r *ExecutionRepository) GetCommit(ctx context.Context, tenantID, commitRecordID string) (TodoCommitRecord, error) {
	var out TodoCommitRecord
	if err := r.get(ctx, commitRecordTable, tenantID, commitRecordID, &out); err != nil {
		return TodoCommitRecord{}, err
	}
	out.TenantID = normalizeTenantID(tenantID)
	return out, nil
}

func (r *ExecutionRepository) ListCommits(ctx context.Context, tenantID string) ([]TodoCommitRecord, error) {
	var out []TodoCommitRecord
	if err := r.list(ctx, commitRecordTable, tenantID, &out); err != nil {
		return nil, err
	}
	for index := range out {
		out[index].TenantID = normalizeTenantID(tenantID)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CommitRecordID < out[j].CommitRecordID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

func (r *ExecutionRepository) CreateSubmission(ctx context.Context, submission TodoCodeSubmission) (bool, error) {
	return r.create(ctx, submissionTable, executionKey(submission.TenantID, submission.SubmissionID), executionRecord(submission.TenantID, submission.SubmissionID, submission.DevelopmentProjectID, submission.TodoID, submission.Status, submission.Version, submission))
}

func (r *ExecutionRepository) GetSubmission(ctx context.Context, tenantID, submissionID string) (TodoCodeSubmission, error) {
	var out TodoCodeSubmission
	if err := r.get(ctx, submissionTable, tenantID, submissionID, &out); err != nil {
		return TodoCodeSubmission{}, err
	}
	out.TenantID = normalizeTenantID(tenantID)
	return out, nil
}

func (r *ExecutionRepository) UpdateSubmission(ctx context.Context, submission TodoCodeSubmission, expectedVersion int64) error {
	return r.update(ctx, submissionTable, submission.TenantID, submission.SubmissionID, submission.DevelopmentProjectID, submission.TodoID, submission.Status, submission.Version, expectedVersion, submission)
}

func (r *ExecutionRepository) ListSubmissions(ctx context.Context, tenantID string) ([]TodoCodeSubmission, error) {
	var out []TodoCodeSubmission
	if err := r.list(ctx, submissionTable, tenantID, &out); err != nil {
		return nil, err
	}
	for index := range out {
		out[index].TenantID = normalizeTenantID(tenantID)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].Revision > out[j].Revision
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

type MergeOperation struct {
	TenantID       string                   `json:"tenant_id"`
	IdempotencyKey string                   `json:"idempotency_key"`
	RequestHash    string                   `json:"request_hash"`
	Status         string                   `json:"status"`
	Receipt        *DevelopmentMergeReceipt `json:"receipt,omitempty"`
	FailureReason  string                   `json:"failure_reason,omitempty"`
	CreatedAt      time.Time                `json:"created_at"`
	UpdatedAt      time.Time                `json:"updated_at"`
	Version        int64                    `json:"version"`
}

type SubmissionOperation struct {
	TenantID       string              `json:"tenant_id"`
	OperationID    string              `json:"operation_id"`
	Action         string              `json:"action"`
	IdempotencyKey string              `json:"idempotency_key"`
	RequestHash    string              `json:"request_hash"`
	Status         string              `json:"status"`
	Submission     *TodoCodeSubmission `json:"submission,omitempty"`
	Audit          *AuditEvent         `json:"audit,omitempty"`
	FailureReason  string              `json:"failure_reason,omitempty"`
	CreatedAt      time.Time           `json:"created_at"`
	UpdatedAt      time.Time           `json:"updated_at"`
	Version        int64               `json:"version"`
}

func (r *ExecutionRepository) AcquireSubmissionOperation(ctx context.Context, operation SubmissionOperation) (SubmissionOperation, bool, error) {
	created, err := r.create(ctx, submissionOperationTable, executionKey(operation.TenantID, operation.OperationID), executionRecord(operation.TenantID, operation.OperationID, "", "", operation.Status, operation.Version, operation))
	if err != nil {
		return SubmissionOperation{}, false, err
	}
	if created {
		return operation, true, nil
	}
	var existing SubmissionOperation
	if err := r.get(ctx, submissionOperationTable, operation.TenantID, operation.OperationID, &existing); err != nil {
		return SubmissionOperation{}, false, err
	}
	if existing.Action != operation.Action || existing.IdempotencyKey != operation.IdempotencyKey || existing.RequestHash != operation.RequestHash {
		return SubmissionOperation{}, false, ErrIdempotency
	}
	if existing.Status == "failed" {
		restarted := operation
		restarted.CreatedAt = existing.CreatedAt
		restarted.Version = existing.Version + 1
		if err := r.update(ctx, submissionOperationTable, existing.TenantID, existing.OperationID, "", "", restarted.Status, restarted.Version, existing.Version, restarted); err != nil {
			return SubmissionOperation{}, false, err
		}
		return restarted, true, nil
	}
	return existing, false, nil
}

func (r *ExecutionRepository) CompleteSubmissionOperation(ctx context.Context, operation SubmissionOperation, expectedVersion int64) error {
	return r.update(ctx, submissionOperationTable, operation.TenantID, operation.OperationID, "", "", operation.Status, operation.Version, expectedVersion, operation)
}

func (r *ExecutionRepository) AcquireMergeOperation(ctx context.Context, operation MergeOperation) (MergeOperation, bool, error) {
	created, err := r.create(ctx, mergeOperationTable, executionKey(operation.TenantID, operation.IdempotencyKey), executionRecord(operation.TenantID, operation.IdempotencyKey, "", "", operation.Status, operation.Version, operation))
	if err != nil {
		return MergeOperation{}, false, err
	}
	if created {
		return operation, true, nil
	}
	var existing MergeOperation
	if err := r.get(ctx, mergeOperationTable, operation.TenantID, operation.IdempotencyKey, &existing); err != nil {
		return MergeOperation{}, false, err
	}
	if existing.RequestHash != operation.RequestHash {
		return MergeOperation{}, false, ErrIdempotency
	}
	return existing, false, nil
}

func (r *ExecutionRepository) CompleteMergeOperation(ctx context.Context, operation MergeOperation, expectedVersion int64) error {
	return r.update(ctx, mergeOperationTable, operation.TenantID, operation.IdempotencyKey, "", "", operation.Status, operation.Version, expectedVersion, operation)
}

func (r *ExecutionRepository) GetMergeOperation(ctx context.Context, tenantID, idempotencyKey string) (MergeOperation, error) {
	var operation MergeOperation
	if err := r.get(ctx, mergeOperationTable, tenantID, strings.TrimSpace(idempotencyKey), &operation); err != nil {
		return MergeOperation{}, err
	}
	return operation, nil
}

func (r *ExecutionRepository) create(ctx context.Context, table, key string, record db.Record) (bool, error) {
	if r == nil || r.router == nil {
		return false, ErrDependency
	}
	return r.router.CreateRecord(ctx, table, key, record)
}

func (r *ExecutionRepository) get(ctx context.Context, table, tenantID, id string, out any) error {
	if r == nil || r.router == nil {
		return ErrDependency
	}
	tenantID, id = normalizeTenantID(tenantID), strings.TrimSpace(id)
	if !validTenantKeySegment(tenantID) || !validResourceID(id) {
		return ErrInvalidRequest
	}
	record, ok, err := r.router.GetRecord(ctx, table, executionKey(tenantID, id))
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	if recordString(record, "tenant_id") != tenantID || recordString(record, "resource_id") != id {
		return ErrDependency
	}
	payload, _ := record["payload"].(string)
	if payload == "" || json.Unmarshal([]byte(payload), out) != nil {
		return ErrDependency
	}
	return nil
}

func (r *ExecutionRepository) update(ctx context.Context, table, tenantID, id, projectID, todoID, status string, version, expectedVersion int64, value any) error {
	if r == nil || r.router == nil {
		return ErrDependency
	}
	key := executionKey(tenantID, id)
	current, ok, err := r.router.GetRecord(ctx, table, key)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	if expectedVersion <= 0 || recordInt64(current["version"]) != expectedVersion {
		return ErrConflict
	}
	next := executionRecord(tenantID, id, projectID, todoID, status, version, value)
	swapped, err := r.router.CompareAndSwapRecord(ctx, table, key, current, next)
	if err != nil {
		return err
	}
	if !swapped {
		return ErrConflict
	}
	return nil
}

func (r *ExecutionRepository) list(ctx context.Context, table, tenantID string, out any) error {
	if r == nil || r.router == nil {
		return ErrDependency
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	tenantID = normalizeTenantID(tenantID)
	store, physical, err := r.router.StoreForTable(table)
	if err != nil {
		return err
	}
	records, err := store.ListRecordTable(ctx, physical)
	if err != nil {
		return err
	}
	prefix := tenantID + "::"
	payloads := make([]json.RawMessage, 0)
	for key, record := range records {
		if !strings.HasPrefix(key, prefix) || recordString(record, "tenant_id") != tenantID {
			continue
		}
		payload, _ := record["payload"].(string)
		if payload == "" {
			return ErrDependency
		}
		payloads = append(payloads, json.RawMessage(payload))
	}
	encoded, err := json.Marshal(payloads)
	if err != nil {
		return err
	}
	return json.Unmarshal(encoded, out)
}

func executionRecord(tenantID, id, projectID, todoID, status string, version int64, value any) db.Record {
	payload, _ := json.Marshal(value)
	return db.Record{
		"tenant_id": normalizeTenantID(tenantID), "resource_id": strings.TrimSpace(id),
		"development_project_id": strings.TrimSpace(projectID), "todo_id": strings.TrimSpace(todoID),
		"status": strings.TrimSpace(status), "version": version, "payload": string(payload),
	}
}

func executionKey(tenantID, id string) string {
	return normalizeTenantID(tenantID) + "::" + strings.TrimSpace(id)
}

func (r *ExecutionRepository) validateRecordIdentity(table, key string, record db.Record) error {
	if executionKey(recordString(record, "tenant_id"), recordString(record, "resource_id")) != key {
		return fmt.Errorf("%w: %s record identity mismatch", ErrDependency, table)
	}
	return nil
}
