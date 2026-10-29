package task

import (
	"context"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/testkit"
	"orrery_v2/internal/platform/uow"
)

func TestPlatformRepositoryPersistsTaskAndSubmissionsInUnitOfWork(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := NewPlatformRepository()
	record := validTaskRecord(TaskStatusInProgress)
	submission := TaskSubmission{
		ID:               TaskSubmissionID("submission-001"),
		TaskID:           record.Contract.ID,
		AgentID:          agentcore.AgentID("agent-worker-001"),
		RunID:            agentcore.AgentRunID("run-001"),
		AgentResultRef:   agentcore.AgentResultID("result-001"),
		InternalTraceRef: "artifact://runtime-traces/run-001",
		UserReport:       "Exact Agent report",
		Summary:          agentcore.Summary{Text: "done"},
		SubmittedAt:      time.Date(2026, 5, 31, 1, 2, 3, 0, time.UTC),
		IdempotencyKey:   "submit-001",
	}
	deliverables := []DeliverableSubmission{{
		ID:           DeliverableSubmissionID("deliverable-submission-001"),
		SubmissionID: submission.ID,
		TaskID:       record.Contract.ID,
		AgentID:      submission.AgentID,
		Deliverable:  validAgentResultForContract().Deliverables[0],
		SubmittedAt:  submission.SubmittedAt,
	}}
	evidence := []EvidenceSubmission{{
		ID:           EvidenceSubmissionID("evidence-submission-001"),
		SubmissionID: submission.ID,
		TaskID:       record.Contract.ID,
		AgentID:      submission.AgentID,
		Evidence:     validAgentResultForContract().Evidence[0],
		SubmittedAt:  submission.SubmittedAt,
	}}

	if err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		if err := repo.SaveTask(ctx, tx, record); err != nil {
			return err
		}
		return repo.SaveSubmission(ctx, tx, submission, deliverables, evidence)
	}); err != nil {
		t.Fatalf("save repository records: %v", err)
	}

	if err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		got, err := repo.GetTask(ctx, tx, record.Contract.ID)
		if err != nil {
			return err
		}
		if got.Status != TaskStatusInProgress || got.Contract.ID != record.Contract.ID {
			t.Fatalf("unexpected task record: %#v", got)
		}
		byKey, ok, err := repo.FindSubmissionByIdempotencyKey(ctx, tx, record.Contract.ID, "submit-001")
		if err != nil {
			return err
		}
		if !ok || byKey.ID != submission.ID || byKey.InternalTraceRef != submission.InternalTraceRef || byKey.UserReport != submission.UserReport {
			t.Fatalf("submission by idempotency = %#v ok=%v", byKey, ok)
		}
		latest, ok, err := repo.LatestSubmission(ctx, tx, record.Contract.ID)
		if err != nil {
			return err
		}
		if !ok || latest.ID != submission.ID || latest.InternalTraceRef != submission.InternalTraceRef || latest.UserReport != submission.UserReport {
			t.Fatalf("latest submission = %#v ok=%v", latest, ok)
		}
		storedDeliverables, err := repo.ListDeliverableSubmissions(ctx, tx, submission.ID)
		if err != nil {
			return err
		}
		if len(storedDeliverables) != 1 || storedDeliverables[0].ID != deliverables[0].ID {
			t.Fatalf("deliverable submissions = %#v", storedDeliverables)
		}
		return nil
	}); err != nil {
		t.Fatalf("load repository records: %v", err)
	}
}

func TestTaskSubmissionPayloadWithoutInternalTraceRefDefaultsToEmpty(t *testing.T) {
	var submission TaskSubmission
	if err := decodePayload(db.Record{"payload": `{"ID":"submission-legacy"}`}, &submission); err != nil {
		t.Fatalf("decode legacy task submission: %v", err)
	}
	if submission.ID != "submission-legacy" || submission.InternalTraceRef != "" {
		t.Fatalf("legacy task submission = %#v", submission)
	}
}

func TestPlatformRepositoryLatestSubmissionUsesAcceptedTaskVersionWhenTimestampsTie(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := NewPlatformRepository()
	at := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	first := TaskSubmission{ID: "submission-first", TaskID: "task-001", SubmittedAt: at, IdempotencyKey: "first", TaskVersionAccepted: 3}
	second := TaskSubmission{ID: "submission-second", TaskID: "task-001", SubmittedAt: at, IdempotencyKey: "second", TaskVersionAccepted: 7}
	if err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		if err := repo.SaveSubmission(ctx, tx, first, nil, nil); err != nil {
			return err
		}
		return repo.SaveSubmission(ctx, tx, second, nil, nil)
	}); err != nil {
		t.Fatal(err)
	}
	if err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		latest, found, err := repo.LatestSubmission(ctx, tx, "task-001")
		if err != nil {
			return err
		}
		if !found || latest.ID != second.ID {
			t.Fatalf("latest submission = %+v found=%v", latest, found)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPlatformRepositoryPersistsQAReworkAndAcceptanceRecords(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := NewPlatformRepository()
	record := validTaskRecord(TaskStatusInReview)
	request := NewReworkRequest(ReworkRequestOptions{
		ID:             ReworkRequestID("rework-001"),
		OriginalTaskID: record.Contract.ID,
		TargetAssignee: record.Contract.Assignee,
		Reason:         "missing evidence",
		RequestedBy:    agentcore.AgentID("qa-agent"),
		CreatedAt:      env.Clock.Now(),
	})
	qaResult := QAResultRecord{
		ID:               QAResultID("qa-result-001"),
		TaskID:           record.Contract.ID,
		SubmissionID:     "submission-001",
		CheckedBy:        agentcore.AgentID("qa-agent"),
		Status:           QAStatusRejected,
		RejectionReasons: []string{"missing evidence"},
		IdempotencyKey:   "qa-001",
		CheckedAt:        env.Clock.Now(),
	}
	report := AcceptanceReport{
		ID:           AcceptanceReportID("report-001"),
		TaskID:       record.Contract.ID,
		SubmissionID: "submission-001",
		QAResultID:   qaResult.ID,
		CreatedBy:    qaResult.CheckedBy,
		Summary:      "approved",
		CreatedAt:    env.Clock.Now(),
	}

	if err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		if err := repo.SaveTask(ctx, tx, record); err != nil {
			return err
		}
		if err := repo.SaveReworkRequest(ctx, tx, request); err != nil {
			return err
		}
		if err := repo.SaveQAResult(ctx, tx, qaResult); err != nil {
			return err
		}
		return repo.SaveAcceptanceReport(ctx, tx, report)
	}); err != nil {
		t.Fatalf("save QA records: %v", err)
	}

	if err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		gotRework, ok, err := repo.FindOpenReworkRequest(ctx, tx, record.Contract.ID)
		if err != nil {
			return err
		}
		if !ok || gotRework.ID != request.ID {
			t.Fatalf("open rework = %#v ok=%v", gotRework, ok)
		}
		gotQA, ok, err := repo.FindQAResultByIdempotencyKey(ctx, tx, record.Contract.ID, "qa-001")
		if err != nil {
			return err
		}
		if !ok || gotQA.ID != qaResult.ID {
			t.Fatalf("qa by idempotency = %#v ok=%v", gotQA, ok)
		}
		return nil
	}); err != nil {
		t.Fatalf("load QA records: %v", err)
	}
}

func TestPlatformRepositoryDeleteMissionTasksRemovesTaskAndDependentRecords(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := NewPlatformRepository()
	record := validTaskRecord(TaskStatusInReview)
	submission := TaskSubmission{
		ID:             TaskSubmissionID("submission-delete-001"),
		TaskID:         record.Contract.ID,
		AgentID:        agentcore.AgentID("agent-worker-001"),
		RunID:          agentcore.AgentRunID("run-001"),
		AgentResultRef: agentcore.AgentResultID("result-001"),
		Summary:        agentcore.Summary{Text: "done"},
		SubmittedAt:    time.Date(2026, 5, 31, 1, 2, 3, 0, time.UTC),
		IdempotencyKey: "submit-delete-001",
	}
	qaResult := QAResultRecord{
		ID:             QAResultID("qa-delete-001"),
		TaskID:         record.Contract.ID,
		SubmissionID:   string(submission.ID),
		CheckedBy:      agentcore.AgentID("qa-agent"),
		Status:         QAStatusApproved,
		IdempotencyKey: "qa-delete-001",
		CheckedAt:      env.Clock.Now(),
	}
	report := AcceptanceReport{
		ID:           AcceptanceReportID("report-delete-001"),
		TaskID:       record.Contract.ID,
		SubmissionID: string(submission.ID),
		QAResultID:   qaResult.ID,
		CreatedBy:    qaResult.CheckedBy,
		Summary:      "approved",
		CreatedAt:    env.Clock.Now(),
	}

	if err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		if err := repo.SaveTask(ctx, tx, record); err != nil {
			return err
		}
		if err := repo.SaveSubmission(ctx, tx, submission, nil, nil); err != nil {
			return err
		}
		if err := repo.SaveQAResult(ctx, tx, qaResult); err != nil {
			return err
		}
		return repo.SaveAcceptanceReport(ctx, tx, report)
	}); err != nil {
		t.Fatalf("save records: %v", err)
	}

	if err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		return repo.DeleteMissionTasks(ctx, tx, record.Contract.MissionID)
	}); err != nil {
		t.Fatalf("delete mission tasks: %v", err)
	}

	if err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		if _, err := repo.GetTask(ctx, tx, record.Contract.ID); err == nil {
			t.Fatal("task record still exists after mission task delete")
		}
		if _, ok, err := repo.FindSubmissionByIdempotencyKey(ctx, tx, record.Contract.ID, submission.IdempotencyKey); err != nil {
			return err
		} else if ok {
			t.Fatal("submission idempotency record still exists after mission task delete")
		}
		if _, ok, err := repo.FindQAResultByIdempotencyKey(ctx, tx, record.Contract.ID, qaResult.IdempotencyKey); err != nil {
			return err
		} else if ok {
			t.Fatal("QA idempotency record still exists after mission task delete")
		}
		return nil
	}); err != nil {
		t.Fatalf("load after delete: %v", err)
	}
}

func TestPlatformRepositoryDeleteTaskRemovesTaskAndDependentRecords(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := NewPlatformRepository()
	record := validTaskRecord(TaskStatusInReview)
	submission := TaskSubmission{
		ID:             TaskSubmissionID("submission-delete-task-001"),
		TaskID:         record.Contract.ID,
		AgentID:        agentcore.AgentID("agent-worker-001"),
		RunID:          agentcore.AgentRunID("run-001"),
		AgentResultRef: agentcore.AgentResultID("result-001"),
		Summary:        agentcore.Summary{Text: "done"},
		SubmittedAt:    time.Date(2026, 5, 31, 1, 2, 3, 0, time.UTC),
		IdempotencyKey: "submit-delete-task-001",
	}

	if err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		if err := repo.SaveTask(ctx, tx, record); err != nil {
			return err
		}
		return repo.SaveSubmission(ctx, tx, submission, nil, nil)
	}); err != nil {
		t.Fatalf("save records: %v", err)
	}

	if err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		return repo.DeleteTask(ctx, tx, record.Contract.ID)
	}); err != nil {
		t.Fatalf("delete task: %v", err)
	}

	if err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		if _, err := repo.GetTask(ctx, tx, record.Contract.ID); err == nil {
			t.Fatal("task record still exists after task delete")
		}
		if _, ok, err := repo.FindSubmissionByIdempotencyKey(ctx, tx, record.Contract.ID, submission.IdempotencyKey); err != nil {
			return err
		} else if ok {
			t.Fatal("submission idempotency record still exists after task delete")
		}
		return nil
	}); err != nil {
		t.Fatalf("load after delete: %v", err)
	}
}
