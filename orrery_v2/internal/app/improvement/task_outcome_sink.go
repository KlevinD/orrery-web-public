package improvement

import (
	"context"
	"encoding/json"
	"fmt"
	"orrery_v2/internal/mission/storage"
	missiontask "orrery_v2/internal/mission/task"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/uow"
)

// TaskOutcomeEventSink captures only new domain writes, in their original
// transaction. It neither executes tasks nor replays historical business work.
type TaskOutcomeEventSink struct {
	Next       missiontask.EventSink
	Repository missiontask.Repository
}

func (s TaskOutcomeEventSink) AppendTaskEvent(ctx context.Context, tx uow.Tx, event missiontask.TaskEvent) error {
	if s.Next == nil || s.Repository == nil {
		return ErrUnavailable
	}
	if err := s.Next.AppendTaskEvent(ctx, tx, event); err != nil {
		return err
	}
	var submissionID string
	var qa *missiontask.QAResultRecord
	var skipped *missiontask.QASkipRecord
	switch event.Type {
	case missiontask.TaskEventAgentResultSubmitted:
		item, ok := event.Payload.(missiontask.TaskSubmission)
		if !ok {
			return ErrInvalid
		}
		submissionID = string(item.ID)
	case missiontask.TaskEventQAApproved, missiontask.TaskEventQARejected:
		item, ok := event.Payload.(missiontask.QAResultRecord)
		if !ok {
			return ErrInvalid
		}
		qa = &item
		submissionID = item.SubmissionID
	case missiontask.TaskEventQASkipped:
		item, ok := event.Payload.(missiontask.QASkipRecord)
		if !ok {
			return ErrInvalid
		}
		skipped = &item
		submissionID = string(item.SubmissionID)
	default:
		return nil
	}
	raw, exists, err := db.GetRecordTx(ctx, tx, storage.TaskSubmissions, submissionID)
	if err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	var submission missiontask.TaskSubmission
	payload, ok := raw["payload"].(string)
	if !ok || json.Unmarshal([]byte(payload), &submission) != nil || string(submission.ID) != submissionID || submission.TaskID != event.TaskID {
		return ErrInvalid
	}
	// Human-only and imported submissions without execution provenance have no
	// runtime association; they keep their original domain behavior.
	if submission.RunID == "" || submission.InternalTraceRef == "" || submission.RuntimeAttemptID == "" {
		return nil
	}
	task, err := s.Repository.GetTask(ctx, tx, event.TaskID)
	if err != nil {
		return err
	}
	if task.Contract.MissionID.String() != event.MissionID || event.OccurredAt.IsZero() || event.OccurredAt.Before(submission.SubmittedAt) {
		return ErrInvalid
	}
	if event.Type == missiontask.TaskEventAgentResultSubmitted && Digest(event.Payload) != Digest(submission) {
		return ErrInvalid
	}
	if qa != nil && (qa.TaskID != event.TaskID || qa.ID == "" || !qa.CheckedAt.Equal(event.OccurredAt) ||
		(event.Type == missiontask.TaskEventQAApproved && qa.Status != missiontask.QAStatusApproved) ||
		(event.Type == missiontask.TaskEventQARejected && qa.Status != missiontask.QAStatusRejected && qa.Status != missiontask.QAStatusNeedsRework)) {
		return ErrInvalid
	}
	if skipped != nil && (skipped.TaskID != event.TaskID || task.Contract.QACheckRequired || !skipped.SkippedAt.Equal(event.OccurredAt)) {
		return ErrInvalid
	}
	bindings := struct {
		Deliverables any
		Evidence     any
	}{submission.DeliverableRefs, submission.EvidenceRefs}
	outcome := TaskOutcome{SourceKey: businessSourceKey(event.MissionID, event.TaskID.String(), submission.RunID.String(), submission.InternalTraceRef, submission.RuntimeAttemptID), RuntimeAttemptID: submission.RuntimeAttemptID,
		MissionID: event.MissionID, TaskID: event.TaskID.String(), RunID: submission.RunID.String(), AgentID: submission.AgentID.String(), TraceRef: submission.InternalTraceRef,
		SubmissionID: submissionID, ResultID: submission.AgentResultRef.String(), TaskVersion: submission.TaskVersionAccepted,
		ContractSHA256: Digest(task.Contract), SourceSHA256: Digest(event.Payload), Stage: "submission", Status: string(task.Status), QARequired: task.Contract.QACheckRequired,
		Blocked: submission.Blocker != nil, Deliverables: len(submission.DeliverableRefs), Evidence: len(submission.EvidenceRefs), SubmissionBindingsSHA256: Digest(bindings), OccurredAt: event.OccurredAt.UTC()}
	outcome.QAStatus = "pending"
	if !outcome.QARequired {
		outcome.QAStatus = "not_required_pending"
	}
	if outcome.Blocked {
		outcome.QAStatus = "not_assessed"
	}
	if envelope := task.Contract.ExecutionEnvelope; envelope != nil && envelope.Verify() == nil {
		outcome.ExecutionTenantID = envelope.TenantID
	}
	stageID := submissionID
	if qa != nil {
		outcome.Stage = "qa"
		outcome.QAResultID = string(qa.ID)
		outcome.QAStatus = string(qa.Status)
		outcome.AcceptanceReportID = qa.AcceptanceReportRef
		stageID = string(qa.ID)
	}
	if skipped != nil {
		outcome.Stage = "qa_not_required"
		outcome.QAStatus = "not_required"
		stageID = string(skipped.ID)
	}
	outcome.ID = "task-outcome:" + Digest(outcome)
	key := outcome.SourceKey + "/" + Digest([]string{outcome.Stage, stageID})
	created, err := db.InsertRecordTx(ctx, tx, taskOutcomeTable, key, encode(outcome))
	if err != nil {
		return err
	}
	if created {
		if outcome.Stage == "submission" {
			return captureTaskArtifactManifest(ctx, tx, outcome, submission)
		}
		return nil
	}
	prior, exists, err := db.GetRecordTx(ctx, tx, taskOutcomeTable, key)
	if err != nil {
		return err
	}
	if !exists || prior["sha256"] != Digest(outcome) {
		return fmt.Errorf("%w: committed task outcome changed", ErrConflict)
	}
	return nil
}

var _ missiontask.EventSink = TaskOutcomeEventSink{}
