package task

import (
	"context"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/platform/testkit"
	"orrery_v2/internal/platform/uow"
)

func TestTaskDeliveryServiceAcceptsAndStartsTask(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := newMemoryRepository(validTaskRecord(TaskStatusAssigned))
	svc := NewDeliveryService(env.UnitOfWork, repo, repo, DeliveryServiceOptions{Clock: env.Clock, IDGenerator: env.IDGenerator})

	if err := svc.AcceptTask(ctx, TaskID("task-001"), agentcore.AgentInstanceRef{ID: agentcore.AgentInstanceID("agent-worker-001")}); err != nil {
		t.Fatalf("AcceptTask returned error: %v", err)
	}
	if repo.task.Status != TaskStatusAccepted {
		t.Fatalf("status = %s, want accepted", repo.task.Status)
	}
	if err := svc.StartTask(ctx, TaskID("task-001"), agentcore.AgentInstanceRef{ID: agentcore.AgentInstanceID("agent-worker-001")}); err != nil {
		t.Fatalf("StartTask returned error: %v", err)
	}
	if repo.task.Status != TaskStatusInProgress {
		t.Fatalf("status = %s, want in_progress", repo.task.Status)
	}
	assertEventActorProfileID(t, repo.events, TaskEventTaskAccepted, agentcore.AgentProfileID("agent-worker-001"))
	assertEventActorProfileID(t, repo.events, TaskEventTaskStarted, agentcore.AgentProfileID("agent-worker-001"))
}

func TestTaskDeliveryServiceRequiresQAPlanBeforeStartingQAEnabledTask(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	record := validTaskRecord(TaskStatusAccepted)
	record.Contract.QAEnabled = true
	record.Contract.QAPlanRequired = true
	repo := newMemoryRepository(record)
	svc := NewDeliveryService(env.UnitOfWork, repo, repo, DeliveryServiceOptions{Clock: env.Clock, IDGenerator: env.IDGenerator})

	err := svc.StartTask(ctx, TaskID("task-001"), agentcore.AgentInstanceRef{ID: agentcore.AgentInstanceID("agent-worker-001")})
	if err == nil {
		t.Fatal("expected StartTask to require a completed QA plan")
	}
	if repo.task.Status != TaskStatusAccepted {
		t.Fatalf("status = %s, want accepted after rejected start", repo.task.Status)
	}

	repo.task.Contract.QAPlanCompleted = true
	if err := svc.StartTask(ctx, TaskID("task-001"), agentcore.AgentInstanceRef{ID: agentcore.AgentInstanceID("agent-worker-001")}); err != nil {
		t.Fatalf("StartTask with completed QA plan returned error: %v", err)
	}
	if repo.task.Status != TaskStatusInProgress {
		t.Fatalf("status = %s, want in_progress", repo.task.Status)
	}
}

func TestTaskDeliveryServiceSubmitsAgentResultAndCompletesWhenQADisabled(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := newMemoryRepository(validTaskRecord(TaskStatusInProgress))
	svc := NewDeliveryService(env.UnitOfWork, repo, repo, DeliveryServiceOptions{Clock: env.Clock, IDGenerator: env.IDGenerator})
	result := validAgentResultForContract()
	result.InternalTraceRef = "artifact://runtime-traces/run-001"
	result.UserReport = "已完成本次核对，详细结果由我在这里汇报。"

	submission, err := svc.SubmitAgentResult(ctx, TaskID("task-001"), result, SubmitOptions{IdempotencyKey: "submit-001"})
	if err != nil {
		t.Fatalf("SubmitAgentResult returned error: %v", err)
	}

	if repo.task.Status != TaskStatusApproved {
		t.Fatalf("status = %s, want approved", repo.task.Status)
	}
	if submission.ID == "" || submission.IdempotencyKey != "submit-001" {
		t.Fatalf("unexpected submission: %#v", submission)
	}
	if submission.InternalTraceRef != result.InternalTraceRef {
		t.Fatalf("internal trace ref = %q, want %q", submission.InternalTraceRef, result.InternalTraceRef)
	}
	if submission.UserReport != result.UserReport {
		t.Fatalf("Agent report lost at commit: %q", submission.UserReport)
	}
	if len(repo.deliverables) != 1 || len(repo.evidence) != 1 {
		t.Fatalf("expected deliverable and evidence submissions, got %#v %#v", repo.deliverables, repo.evidence)
	}
	assertEventType(t, repo.events, TaskEventAgentResultSubmitted)
	assertEventType(t, repo.events, TaskEventDeliverableSubmitted)
	assertEventType(t, repo.events, TaskEventEvidenceSubmitted)
	assertEventType(t, repo.events, TaskEventQASkipped)
}

func TestTaskDeliveryServicePersistsBlockedAgentResultWithoutApproval(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := newMemoryRepository(validTaskRecord(TaskStatusInProgress))
	svc := NewDeliveryService(env.UnitOfWork, repo, repo, DeliveryServiceOptions{Clock: env.Clock, IDGenerator: env.IDGenerator})
	result := validAgentResultForContract()
	result.Blocker = &agentcore.Blocker{Summary: "Ontology association is pending.", Report: agentcore.ArtifactRef{
		ID: "blocked-report", Kind: agentcore.ArtifactKindReport, URI: "artifact://blocked-report",
	}}
	result.Deliverables = nil
	result.Evidence = nil

	submission, err := svc.SubmitAgentResult(ctx, TaskID("task-001"), result, SubmitOptions{IdempotencyKey: "submit-blocked"})
	if err != nil {
		t.Fatalf("SubmitAgentResult: %v", err)
	}
	if repo.task.Status != TaskStatusBlocked || repo.task.LastTransitionReason != result.Blocker.Summary {
		t.Fatalf("blocked task = %+v", repo.task)
	}
	if submission.Blocker == nil || submission.Blocker.Summary != result.Blocker.Summary || submission.Blocker.Report != result.Blocker.Report {
		t.Fatalf("blocked submission = %+v", submission)
	}
	assertEventType(t, repo.events, TaskEventAgentResultSubmitted)
	assertEventType(t, repo.events, TaskEventTaskBlocked)
	for _, event := range repo.events {
		if event.Type == TaskEventQASkipped || event.Type == TaskEventQARequested {
			t.Fatalf("blocked result emitted QA terminal event: %+v", event)
		}
	}
}

func TestTaskDeliveryServiceSubmitsAgentResultToReviewWhenQACheckRequired(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	record := validTaskRecord(TaskStatusInProgress)
	record.Contract.QAEnabled = true
	record.Contract.QACheckRequired = true
	repo := newMemoryRepository(record)
	svc := NewDeliveryService(env.UnitOfWork, repo, repo, DeliveryServiceOptions{Clock: env.Clock, IDGenerator: env.IDGenerator})

	submission, err := svc.SubmitAgentResult(ctx, TaskID("task-001"), validAgentResultForContract(), SubmitOptions{IdempotencyKey: "submit-001"})
	if err != nil {
		t.Fatalf("SubmitAgentResult returned error: %v", err)
	}

	if repo.task.Status != TaskStatusInReview {
		t.Fatalf("status = %s, want in_review", repo.task.Status)
	}
	if submission.ID == "" || submission.IdempotencyKey != "submit-001" {
		t.Fatalf("unexpected submission: %#v", submission)
	}
	var qaRequest *TaskEvent
	for index := range repo.events {
		if repo.events[index].Type == TaskEventQARequested {
			qaRequest = &repo.events[index]
			break
		}
	}
	if qaRequest == nil {
		t.Fatalf("expected QA request event in %#v", repo.events)
	}
	payload, ok := qaRequest.Payload.(map[string]any)
	if !ok || payload["qa_ref"] != QARequestRef("task-001") || payload["status"] != "requested" {
		t.Fatalf("QA request payload = %#v", qaRequest.Payload)
	}
}

func TestTaskDeliveryServiceRejectsStaleExpectedVersion(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := newMemoryRepository(validTaskRecord(TaskStatusInProgress))
	svc := NewDeliveryService(env.UnitOfWork, repo, repo, DeliveryServiceOptions{Clock: env.Clock, IDGenerator: env.IDGenerator})

	if _, err := svc.SubmitAgentResult(ctx, TaskID("task-001"), validAgentResultForContract(), SubmitOptions{IdempotencyKey: "submit-001", ExpectedVersion: 99}); err == nil {
		t.Fatal("expected stale expected version to reject submission")
	}
	if len(repo.submissions) != 0 {
		t.Fatalf("submission count = %d, want 0", len(repo.submissions))
	}
}

func TestTaskDeliveryServiceRejectsInvalidResultAndTerminalTask(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := newMemoryRepository(validTaskRecord(TaskStatusInProgress))
	svc := NewDeliveryService(env.UnitOfWork, repo, repo, DeliveryServiceOptions{Clock: env.Clock, IDGenerator: env.IDGenerator})
	result := validAgentResultForContract()
	result.Summary = agentcore.Summary{}

	if _, err := svc.SubmitAgentResult(ctx, TaskID("task-001"), result, SubmitOptions{}); err == nil {
		t.Fatal("expected missing summary to reject submission")
	}

	repo.task.Status = TaskStatusApproved
	if _, err := svc.SubmitAgentResult(ctx, TaskID("task-001"), validAgentResultForContract(), SubmitOptions{}); err == nil {
		t.Fatal("expected approved task to reject submission")
	}
}

func TestTaskDeliveryServiceSubmissionIdempotency(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := newMemoryRepository(validTaskRecord(TaskStatusInProgress))
	svc := NewDeliveryService(env.UnitOfWork, repo, repo, DeliveryServiceOptions{Clock: env.Clock, IDGenerator: env.IDGenerator})

	first, err := svc.SubmitAgentResult(ctx, TaskID("task-001"), validAgentResultForContract(), SubmitOptions{IdempotencyKey: "submit-001"})
	if err != nil {
		t.Fatalf("first submit returned error: %v", err)
	}
	second, err := svc.SubmitAgentResult(ctx, TaskID("task-001"), validAgentResultForContract(), SubmitOptions{IdempotencyKey: "submit-001"})
	if err != nil {
		t.Fatalf("second submit returned error: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("idempotent submit returned different submission: %s vs %s", first.ID, second.ID)
	}
	if len(repo.submissions) != 1 {
		t.Fatalf("submission count = %d, want 1", len(repo.submissions))
	}
}

func validTaskRecord(status TaskStatus) TaskRecord {
	return TaskRecord{
		Contract:        validTaskContract(),
		Status:          status,
		Version:         1,
		StatusChangedAt: time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC),
	}
}

type memoryRepository struct {
	task              TaskRecord
	submissions       map[string]TaskSubmission
	submissionByKey   map[string]string
	deliverables      []DeliverableSubmission
	evidence          []EvidenceSubmission
	events            []TaskEvent
	openReworkRequest *ReworkRequest
	qaResults         map[string]QAResultRecord
	qaResultByKey     map[string]string
	acceptanceReports []AcceptanceReport
}

func newMemoryRepository(task TaskRecord) *memoryRepository {
	return &memoryRepository{
		task:            task,
		submissions:     map[string]TaskSubmission{},
		submissionByKey: map[string]string{},
		qaResults:       map[string]QAResultRecord{},
		qaResultByKey:   map[string]string{},
	}
}

func (r *memoryRepository) GetTask(context.Context, uow.Tx, TaskID) (TaskRecord, error) {
	return r.task, nil
}

func (r *memoryRepository) SaveTask(_ context.Context, _ uow.Tx, record TaskRecord) error {
	r.task = record
	return nil
}

func (r *memoryRepository) FindSubmissionByIdempotencyKey(_ context.Context, _ uow.Tx, _ TaskID, key string) (TaskSubmission, bool, error) {
	id, ok := r.submissionByKey[key]
	if !ok {
		return TaskSubmission{}, false, nil
	}
	return r.submissions[id], true, nil
}

func (r *memoryRepository) SaveSubmission(_ context.Context, _ uow.Tx, submission TaskSubmission, deliverables []DeliverableSubmission, evidence []EvidenceSubmission) error {
	r.seedSubmission(submission)
	r.deliverables = append(r.deliverables, deliverables...)
	r.evidence = append(r.evidence, evidence...)
	return nil
}

func (r *memoryRepository) seedSubmission(submission TaskSubmission) {
	r.submissions[string(submission.ID)] = submission
	r.submissionByKey[submission.IdempotencyKey] = string(submission.ID)
}

func (r *memoryRepository) seedEvidence(submission EvidenceSubmission) {
	r.evidence = append(r.evidence, submission)
}

func (r *memoryRepository) LatestSubmission(context.Context, uow.Tx, TaskID) (TaskSubmission, bool, error) {
	var latest TaskSubmission
	for _, submission := range r.submissions {
		if latest.SubmittedAt.IsZero() || submission.SubmittedAt.After(latest.SubmittedAt) {
			latest = submission
		}
	}
	return latest, latest.ID != "", nil
}

func (r *memoryRepository) ListDeliverableSubmissions(_ context.Context, _ uow.Tx, submissionID TaskSubmissionID) ([]DeliverableSubmission, error) {
	var out []DeliverableSubmission
	for _, item := range r.deliverables {
		if item.SubmissionID == submissionID {
			out = append(out, item)
		}
	}
	return out, nil
}

func (r *memoryRepository) ListEvidenceSubmissions(_ context.Context, _ uow.Tx, submissionID TaskSubmissionID) ([]EvidenceSubmission, error) {
	var out []EvidenceSubmission
	for _, item := range r.evidence {
		if item.SubmissionID == submissionID {
			out = append(out, item)
		}
	}
	return out, nil
}

func (r *memoryRepository) FindOpenReworkRequest(context.Context, uow.Tx, TaskID) (ReworkRequest, bool, error) {
	if r.openReworkRequest == nil {
		return ReworkRequest{}, false, nil
	}
	return *r.openReworkRequest, true, nil
}

func (r *memoryRepository) SaveReworkRequest(_ context.Context, _ uow.Tx, request ReworkRequest) error {
	r.openReworkRequest = &request
	return nil
}

func (r *memoryRepository) FindQAResultByIdempotencyKey(_ context.Context, _ uow.Tx, _ TaskID, key string) (QAResultRecord, bool, error) {
	id, ok := r.qaResultByKey[key]
	if !ok {
		return QAResultRecord{}, false, nil
	}
	return r.qaResults[id], true, nil
}

func (r *memoryRepository) SaveQAResult(_ context.Context, _ uow.Tx, result QAResultRecord) error {
	r.qaResults[string(result.ID)] = result
	r.qaResultByKey[result.IdempotencyKey] = string(result.ID)
	return nil
}

func (r *memoryRepository) SaveAcceptanceReport(_ context.Context, _ uow.Tx, report AcceptanceReport) error {
	r.acceptanceReports = append(r.acceptanceReports, report)
	return nil
}

func (r *memoryRepository) AppendTaskEvent(_ context.Context, _ uow.Tx, event TaskEvent) error {
	r.events = append(r.events, event)
	return nil
}

func assertEventType(t *testing.T, events []TaskEvent, eventType TaskEventType) {
	t.Helper()
	for _, event := range events {
		if event.Type == eventType {
			return
		}
	}
	t.Fatalf("expected event %s in %#v", eventType, events)
}

func assertEventActorProfileID(t *testing.T, events []TaskEvent, eventType TaskEventType, want agentcore.AgentProfileID) {
	t.Helper()
	for _, event := range events {
		if event.Type == eventType {
			if event.ActorProfileID != want {
				t.Fatalf("%s actor profile id = %s, want %s", eventType, event.ActorProfileID, want)
			}
			return
		}
	}
	t.Fatalf("expected event %s in %#v", eventType, events)
}
