package task

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	missioncore "orrery_v2/internal/mission/core"
	"orrery_v2/internal/mission/storage"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/uow"
)

type PlatformRepository struct{}

func NewPlatformRepository() *PlatformRepository {
	return &PlatformRepository{}
}

func (r *PlatformRepository) GetTask(ctx context.Context, tx uow.Tx, id TaskID) (TaskRecord, error) {
	record, ok, err := storage.Get(ctx, tx, storage.TableTaskRecords, id.String())
	if err != nil {
		return TaskRecord{}, err
	}
	if !ok {
		return TaskRecord{}, fmt.Errorf("%w: task %s", ErrNotFound, id)
	}
	var out TaskRecord
	if err := decodePayload(record, &out); err != nil {
		return TaskRecord{}, err
	}
	return out, nil
}

func (r *PlatformRepository) SaveTask(ctx context.Context, tx uow.Tx, record TaskRecord) error {
	payload, err := encodePayload(record)
	if err != nil {
		return err
	}
	return storage.Put(ctx, tx, storage.TableTaskRecords, record.Contract.ID.String(), db.Record{
		"task_id":    record.Contract.ID.String(),
		"mission_id": record.Contract.MissionID.String(),
		"status":     string(record.Status),
		"version":    float64(record.Version),
		"payload":    payload,
	})
}

func (r *PlatformRepository) FindSubmissionByIdempotencyKey(ctx context.Context, tx uow.Tx, taskID TaskID, key string) (TaskSubmission, bool, error) {
	record, ok, err := storage.Get(ctx, tx, storage.TableTaskSubmissionIdempotency, idempotencyRecordKey(taskID.String(), key))
	if err != nil {
		return TaskSubmission{}, false, err
	}
	if !ok {
		return TaskSubmission{}, false, nil
	}
	submissionID, _ := record["submission_id"].(string)
	if submissionID == "" {
		return TaskSubmission{}, false, fmt.Errorf("submission idempotency record is missing submission_id")
	}
	submission, err := r.getSubmission(ctx, tx, TaskSubmissionID(submissionID))
	return submission, err == nil, err
}

func (r *PlatformRepository) SaveSubmission(ctx context.Context, tx uow.Tx, submission TaskSubmission, deliverables []DeliverableSubmission, evidence []EvidenceSubmission) error {
	payload, err := encodePayload(submission)
	if err != nil {
		return err
	}
	if err := storage.Put(ctx, tx, storage.TableTaskSubmissions, string(submission.ID), db.Record{
		"submission_id":   string(submission.ID),
		"task_id":         submission.TaskID.String(),
		"submitted_at":    submission.SubmittedAt.Format(timeFormatRFC3339Nano),
		"idempotency_key": submission.IdempotencyKey,
		"payload":         payload,
	}); err != nil {
		return err
	}
	if err := storage.Put(ctx, tx, storage.TableTaskSubmissionIdempotency, idempotencyRecordKey(submission.TaskID.String(), submission.IdempotencyKey), db.Record{
		"task_id":         submission.TaskID.String(),
		"submission_id":   string(submission.ID),
		"idempotency_key": submission.IdempotencyKey,
	}); err != nil {
		return err
	}
	for _, item := range deliverables {
		payload, err := encodePayload(item)
		if err != nil {
			return err
		}
		if err := storage.Put(ctx, tx, storage.TableTaskDeliverableSubmissions, string(item.ID), db.Record{
			"deliverable_submission_id": string(item.ID),
			"task_id":                   item.TaskID.String(),
			"submission_id":             string(item.SubmissionID),
			"payload":                   payload,
		}); err != nil {
			return err
		}
	}
	for _, item := range evidence {
		payload, err := encodePayload(item)
		if err != nil {
			return err
		}
		if err := storage.Put(ctx, tx, storage.TableTaskEvidenceSubmissions, string(item.ID), db.Record{
			"evidence_submission_id": string(item.ID),
			"task_id":                item.TaskID.String(),
			"submission_id":          string(item.SubmissionID),
			"payload":                payload,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (r *PlatformRepository) LatestSubmission(ctx context.Context, tx uow.Tx, taskID TaskID) (TaskSubmission, bool, error) {
	table, err := storage.Snapshot(ctx, tx, storage.TableTaskSubmissions)
	if err != nil {
		return TaskSubmission{}, false, err
	}
	var submissions []TaskSubmission
	for _, record := range table {
		if gotTaskID, _ := record["task_id"].(string); gotTaskID != taskID.String() {
			continue
		}
		var submission TaskSubmission
		if err := decodePayload(record, &submission); err != nil {
			return TaskSubmission{}, false, err
		}
		submissions = append(submissions, submission)
	}
	if len(submissions) == 0 {
		return TaskSubmission{}, false, nil
	}
	sort.Slice(submissions, func(i, j int) bool {
		if submissions[i].TaskVersionAccepted != submissions[j].TaskVersionAccepted {
			return submissions[i].TaskVersionAccepted > submissions[j].TaskVersionAccepted
		}
		if submissions[i].SubmittedAt.Equal(submissions[j].SubmittedAt) {
			return submissions[i].ID > submissions[j].ID
		}
		return submissions[i].SubmittedAt.After(submissions[j].SubmittedAt)
	})
	return submissions[0], true, nil
}

func (r *PlatformRepository) ListDeliverableSubmissions(ctx context.Context, tx uow.Tx, submissionID TaskSubmissionID) ([]DeliverableSubmission, error) {
	table, err := storage.Snapshot(ctx, tx, storage.TableTaskDeliverableSubmissions)
	if err != nil {
		return nil, err
	}
	var out []DeliverableSubmission
	for _, record := range table {
		if gotSubmissionID, _ := record["submission_id"].(string); gotSubmissionID != string(submissionID) {
			continue
		}
		var item DeliverableSubmission
		if err := decodePayload(record, &item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (r *PlatformRepository) ListEvidenceSubmissions(ctx context.Context, tx uow.Tx, submissionID TaskSubmissionID) ([]EvidenceSubmission, error) {
	table, err := storage.Snapshot(ctx, tx, storage.TableTaskEvidenceSubmissions)
	if err != nil {
		return nil, err
	}
	var out []EvidenceSubmission
	for _, record := range table {
		if gotSubmissionID, _ := record["submission_id"].(string); gotSubmissionID != string(submissionID) {
			continue
		}
		var item EvidenceSubmission
		if err := decodePayload(record, &item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (r *PlatformRepository) FindOpenReworkRequest(ctx context.Context, tx uow.Tx, taskID TaskID) (ReworkRequest, bool, error) {
	table, err := storage.Snapshot(ctx, tx, storage.TableTaskReworkRequests)
	if err != nil {
		return ReworkRequest{}, false, err
	}
	var requests []ReworkRequest
	for _, record := range table {
		if gotTaskID, _ := record["task_id"].(string); gotTaskID != taskID.String() {
			continue
		}
		if status, _ := record["status"].(string); !isActiveReworkStatus(ReworkStatus(status)) {
			continue
		}
		var request ReworkRequest
		if err := decodePayload(record, &request); err != nil {
			return ReworkRequest{}, false, err
		}
		requests = append(requests, request)
	}
	if len(requests) == 0 {
		return ReworkRequest{}, false, nil
	}
	sort.Slice(requests, func(i, j int) bool {
		return requests[i].CreatedAt.After(requests[j].CreatedAt)
	})
	return requests[0], true, nil
}

func isActiveReworkStatus(status ReworkStatus) bool {
	return status == ReworkStatusOpen || status == ReworkStatusAssigned
}

func (r *PlatformRepository) SaveReworkRequest(ctx context.Context, tx uow.Tx, request ReworkRequest) error {
	payload, err := encodePayload(request)
	if err != nil {
		return err
	}
	return storage.Put(ctx, tx, storage.TableTaskReworkRequests, string(request.ID), db.Record{
		"rework_id":  string(request.ID),
		"task_id":    request.OriginalTaskID.String(),
		"status":     string(request.Status),
		"created_at": request.CreatedAt.Format(timeFormatRFC3339Nano),
		"payload":    payload,
	})
}

func (r *PlatformRepository) FindQAResultByIdempotencyKey(ctx context.Context, tx uow.Tx, taskID TaskID, key string) (QAResultRecord, bool, error) {
	record, ok, err := storage.Get(ctx, tx, storage.TableTaskQAIdempotency, idempotencyRecordKey(taskID.String(), key))
	if err != nil {
		return QAResultRecord{}, false, err
	}
	if !ok {
		return QAResultRecord{}, false, nil
	}
	resultID, _ := record["qa_result_id"].(string)
	if resultID == "" {
		return QAResultRecord{}, false, fmt.Errorf("QA idempotency record is missing qa_result_id")
	}
	record, ok, err = storage.Get(ctx, tx, storage.TableTaskQAResults, resultID)
	if err != nil {
		return QAResultRecord{}, false, err
	}
	if !ok {
		return QAResultRecord{}, false, fmt.Errorf("QA result %s is missing", resultID)
	}
	var result QAResultRecord
	if err := decodePayload(record, &result); err != nil {
		return QAResultRecord{}, false, err
	}
	return result, true, nil
}

func (r *PlatformRepository) SaveQAResult(ctx context.Context, tx uow.Tx, result QAResultRecord) error {
	payload, err := encodePayload(result)
	if err != nil {
		return err
	}
	if err := storage.Put(ctx, tx, storage.TableTaskQAResults, string(result.ID), db.Record{
		"qa_result_id":    string(result.ID),
		"task_id":         result.TaskID.String(),
		"status":          string(result.Status),
		"idempotency_key": result.IdempotencyKey,
		"checked_at":      result.CheckedAt.Format(timeFormatRFC3339Nano),
		"payload":         payload,
	}); err != nil {
		return err
	}
	return storage.Put(ctx, tx, storage.TableTaskQAIdempotency, idempotencyRecordKey(result.TaskID.String(), result.IdempotencyKey), db.Record{
		"task_id":         result.TaskID.String(),
		"qa_result_id":    string(result.ID),
		"idempotency_key": result.IdempotencyKey,
	})
}

func (r *PlatformRepository) SaveAcceptanceReport(ctx context.Context, tx uow.Tx, report AcceptanceReport) error {
	payload, err := encodePayload(report)
	if err != nil {
		return err
	}
	return storage.Put(ctx, tx, storage.TableTaskAcceptanceReports, string(report.ID), db.Record{
		"report_id":     string(report.ID),
		"task_id":       report.TaskID.String(),
		"submission_id": report.SubmissionID,
		"qa_result_id":  string(report.QAResultID),
		"created_at":    report.CreatedAt.Format(timeFormatRFC3339Nano),
		"payload":       payload,
	})
}

func (r *PlatformRepository) DeleteMissionTasks(ctx context.Context, tx uow.Tx, missionID missioncore.MissionID) error {
	if missionID.IsZero() {
		return validationError(CodeMissingRequiredField, "mission_id", "mission id is required")
	}
	taskRecords, err := storage.Snapshot(ctx, tx, storage.TableTaskRecords)
	if err != nil {
		return err
	}
	taskIDs := map[string]bool{}
	for _, record := range taskRecords {
		if got, _ := record["mission_id"].(string); got == missionID.String() {
			if taskID, _ := record["task_id"].(string); taskID != "" {
				taskIDs[taskID] = true
			}
		}
	}
	if len(taskIDs) == 0 {
		return nil
	}
	return r.deleteTaskRecords(ctx, tx, taskIDs)
}

func (r *PlatformRepository) DeleteTask(ctx context.Context, tx uow.Tx, taskID TaskID) error {
	if taskID.IsZero() {
		return validationError(CodeMissingRequiredField, "task_id", "task id is required")
	}
	return r.deleteTaskRecords(ctx, tx, map[string]bool{taskID.String(): true})
}

func (r *PlatformRepository) deleteTaskRecords(ctx context.Context, tx uow.Tx, taskIDs map[string]bool) error {
	if len(taskIDs) == 0 {
		return nil
	}
	tables := []storage.TableSpec{
		storage.TableTaskRecords,
		storage.TableTaskSubmissions,
		storage.TableTaskSubmissionIdempotency,
		storage.TableTaskDeliverableSubmissions,
		storage.TableTaskEvidenceSubmissions,
		storage.TableTaskReworkRequests,
		storage.TableTaskQAResults,
		storage.TableTaskQAIdempotency,
		storage.TableTaskAcceptanceReports,
	}
	for _, table := range tables {
		records, err := storage.Snapshot(ctx, tx, table)
		if err != nil {
			return err
		}
		for _, record := range records {
			if recordBelongsToTasks(record, taskIDs) {
				if err := deleteStorageRecord(ctx, tx, table, record); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (r *PlatformRepository) getSubmission(ctx context.Context, tx uow.Tx, id TaskSubmissionID) (TaskSubmission, error) {
	record, ok, err := storage.Get(ctx, tx, storage.TableTaskSubmissions, string(id))
	if err != nil {
		return TaskSubmission{}, err
	}
	if !ok {
		return TaskSubmission{}, fmt.Errorf("%w: submission %s", ErrNotFound, id)
	}
	var submission TaskSubmission
	if err := decodePayload(record, &submission); err != nil {
		return TaskSubmission{}, err
	}
	return submission, nil
}

func deleteStorageRecord(ctx context.Context, tx uow.Tx, table storage.TableSpec, record db.Record) error {
	key := storageRecordKey(table, record)
	if key == "" {
		return fmt.Errorf("mission storage table %s delete key is missing", table.LogicalName)
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM platform_records WHERE table_name = ? AND record_key = ?`, table.LogicalName, key)
	return err
}

func storageRecordKey(table storage.TableSpec, record db.Record) string {
	parts := make([]string, 0, len(table.KeyFields))
	for _, field := range table.KeyFields {
		value, _ := record[field].(string)
		if value == "" {
			return ""
		}
		parts = append(parts, value)
	}
	return strings.Join(parts, "|")
}

func recordBelongsToTasks(record db.Record, taskIDs map[string]bool) bool {
	if len(taskIDs) == 0 {
		return false
	}
	taskID, _ := record["task_id"].(string)
	if taskID == "" {
		return false
	}
	return taskIDs[taskID]
}

const timeFormatRFC3339Nano = "2006-01-02T15:04:05.999999999Z07:00"

func idempotencyRecordKey(taskID, key string) string {
	return taskID + "|" + key
}

func encodePayload(value any) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode task repository payload: %w", err)
	}
	return string(payload), nil
}

func decodePayload(record db.Record, out any) error {
	raw, ok := record["payload"].(string)
	if !ok || raw == "" {
		return fmt.Errorf("task repository payload missing")
	}
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		return fmt.Errorf("decode task repository payload: %w", err)
	}
	return nil
}
