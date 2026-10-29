package httptransport

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	missioncontrol "orrery_v2/internal/app/missioncontrol"
	missioncore "orrery_v2/internal/mission/core"
)

func TestTaskSubmitResultRequiresIdempotencyKeyAndCallsAppService(t *testing.T) {
	at := time.Date(2026, 6, 1, 11, 0, 0, 0, time.UTC)
	command := &fakeMissionCommand{receipt: missioncontrol.CommandReceiptDTO{
		CommandID:  "cmd-1",
		Status:     "accepted",
		Version:    7,
		AcceptedAt: at,
	}}
	handler := newTestRouter(RouterOptions{MissionCommand: command})

	body := `{
		"agent_result_id":"result-1",
		"agent_id":"agent-1",
		"run_id":"run-1",
		"summary":"Implemented HTTP transport",
		"deliverables":[{"id":"deliverable-1","expected_deliverable_id":"expected-1","title":"HTTP transport","artifact":{"id":"artifact-1","kind":"code","uri":"artifact://transport"}}],
		"evidence":[{"id":"evidence-1","evidence_requirement_id":"req-1","kind":"test_log","artifact":{"id":"log-1","kind":"log","uri":"artifact://tests"}}]
	}`
	req := authedHTTPTestRequest(http.MethodPost, "/api/missions/mission-1/tasks/task-1/submit-result", bytes.NewBufferString(body))
	req.Header.Set("X-User-ID", "agent-1")
	req.Header.Set("X-Viewer-Kind", "agent")
	req.Header.Set("Idempotency-Key", "submit-1")
	req.Header.Set("If-Match", `"6"`)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if command.submit.MissionID != missioncore.MissionID("mission-1") || command.submit.TaskID != missioncore.TaskID("task-1") {
		t.Fatalf("submit ids = %+v", command.submit)
	}
	if command.submit.IdempotencyKey != "submit-1" || command.submit.ExpectedVersion != 6 {
		t.Fatalf("submit request metadata = %+v", command.submit)
	}
	if command.submit.Result.InternalTraceRef != "" {
		t.Fatalf("transport accepted internal trace: %+v", command.submit.Result)
	}
	var receipt missioncontrol.CommandReceiptDTO
	if err := json.NewDecoder(rec.Body).Decode(&receipt); err != nil {
		t.Fatalf("decode receipt: %v", err)
	}
	if receipt.CommandID != "cmd-1" || receipt.Version != 7 {
		t.Fatalf("receipt = %+v", receipt)
	}
}

func TestTaskSubmitResultRejectsMissingIdempotencyKeyBeforeAppService(t *testing.T) {
	command := &fakeMissionCommand{}
	handler := newTestRouter(RouterOptions{MissionCommand: command})
	req := authedHTTPTestRequest(http.MethodPost, "/api/missions/mission-1/tasks/task-1/submit-result", bytes.NewBufferString(`{"summary":"x"}`))
	req.Header.Set("X-User-ID", "agent-1")
	req.Header.Set("X-Viewer-Kind", "agent")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if command.submitCalled {
		t.Fatal("app command service should not be called without idempotency key")
	}
	var body ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if body.Code != "invalid_request" || body.Details["field"] != "idempotency_key" {
		t.Fatalf("error = %+v", body)
	}
}

func TestTaskSubmitResultRejectsInternalTraceField(t *testing.T) {
	command := &fakeMissionCommand{}
	handler := newTestRouter(RouterOptions{MissionCommand: command})
	req := authedHTTPTestRequest(http.MethodPost, "/api/missions/mission-1/tasks/task-1/submit-result", bytes.NewBufferString(`{"summary":"x","internal_trace_ref":"trace-1"}`))
	req.Header.Set("X-User-ID", "agent-1")
	req.Header.Set("X-Viewer-Kind", "agent")
	req.Header.Set("Idempotency-Key", "submit-1")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if command.submitCalled {
		t.Fatal("app command service should not be called when internal trace is present")
	}
	var body ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if body.Code != "invalid_request" || body.Details["field"] != "internal_trace_ref" {
		t.Fatalf("error = %+v", body)
	}
}

func TestTaskStartAgentRunRequiresIdempotencyAndCallsAppService(t *testing.T) {
	at := time.Date(2026, 6, 6, 10, 30, 0, 0, time.UTC)
	command := &fakeMissionCommand{receipt: missioncontrol.CommandReceiptDTO{
		CommandID:  "run-1",
		Status:     "accepted",
		MissionID:  "mission-1",
		TaskID:     "task-1",
		Version:    8,
		AcceptedAt: at,
	}}
	handler := newTestRouter(RouterOptions{MissionCommand: command})

	req := authedHTTPTestRequest(http.MethodPost, "/api/missions/mission-1/tasks/task-1/agent-runs", bytes.NewBufferString(`{"agent_id":"agent-1","runtime_kind":"codex","prompt":"Implement task","queue_id":"mission:mission-1"}`))
	req.Header.Set("X-User-ID", "operator")
	req.Header.Set("X-Viewer-Kind", "user")
	req.Header.Set("Idempotency-Key", "start-run-1")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !command.startRunCalled {
		t.Fatal("StartAgentRun was not called")
	}
	if command.startRun.MissionID != missioncore.MissionID("mission-1") || command.startRun.TaskID != missioncore.TaskID("task-1") || command.startRun.AgentID != "agent-1" || command.startRun.RuntimeKind != "codex" || command.startRun.IdempotencyKey != "start-run-1" {
		t.Fatalf("start run command = %+v", command.startRun)
	}
	var receipt missioncontrol.CommandReceiptDTO
	if err := json.NewDecoder(rec.Body).Decode(&receipt); err != nil {
		t.Fatalf("decode receipt: %v", err)
	}
	if receipt.CommandID != "run-1" || receipt.Version != 8 {
		t.Fatalf("receipt = %+v", receipt)
	}
}

func TestTaskCancelAgentRunRequiresIdempotencyAndCallsAppService(t *testing.T) {
	at := time.Date(2026, 6, 6, 11, 0, 0, 0, time.UTC)
	command := &fakeMissionCommand{receipt: missioncontrol.CommandReceiptDTO{
		CommandID:  "run-1",
		Status:     "accepted",
		MissionID:  "mission-1",
		TaskID:     "task-1",
		Version:    9,
		AcceptedAt: at,
	}}
	handler := newTestRouter(RouterOptions{MissionCommand: command})

	req := authedHTTPTestRequest(http.MethodPost, "/api/missions/mission-1/agent-runs/run-1/cancel", bytes.NewBufferString(`{"reason":"operator stopped duplicate work"}`))
	req.Header.Set("X-User-ID", "operator")
	req.Header.Set("X-Viewer-Kind", "user")
	req.Header.Set("Idempotency-Key", "cancel-run-1")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !command.cancelRunCalled {
		t.Fatal("CancelAgentRun was not called")
	}
	if command.cancelRun.MissionID != missioncore.MissionID("mission-1") || command.cancelRun.RunID != "run-1" || command.cancelRun.Reason != "operator stopped duplicate work" || command.cancelRun.IdempotencyKey != "cancel-run-1" {
		t.Fatalf("cancel run command = %+v", command.cancelRun)
	}
	var receipt missioncontrol.CommandReceiptDTO
	if err := json.NewDecoder(rec.Body).Decode(&receipt); err != nil {
		t.Fatalf("decode receipt: %v", err)
	}
	if receipt.CommandID != "run-1" || receipt.Version != 9 {
		t.Fatalf("receipt = %+v", receipt)
	}
}

func TestTaskRetryAgentRunRequiresIdempotencyAndCallsAppService(t *testing.T) {
	at := time.Date(2026, 6, 6, 11, 30, 0, 0, time.UTC)
	command := &fakeMissionCommand{receipt: missioncontrol.CommandReceiptDTO{
		CommandID:  "run-retry",
		Status:     "accepted",
		MissionID:  "mission-1",
		TaskID:     "task-1",
		Version:    10,
		AcceptedAt: at,
	}}
	handler := newTestRouter(RouterOptions{MissionCommand: command})

	req := authedHTTPTestRequest(http.MethodPost, "/api/missions/mission-1/agent-runs/run-failed/retry", bytes.NewBufferString(`{}`))
	req.Header.Set("X-User-ID", "operator")
	req.Header.Set("X-Viewer-Kind", "user")
	req.Header.Set("Idempotency-Key", "retry-run-1")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !command.retryRunCalled {
		t.Fatal("RetryAgentRun was not called")
	}
	if command.retryRun.MissionID != missioncore.MissionID("mission-1") || command.retryRun.RunID != "run-failed" || command.retryRun.IdempotencyKey != "retry-run-1" {
		t.Fatalf("retry run command = %+v", command.retryRun)
	}
	var receipt missioncontrol.CommandReceiptDTO
	if err := json.NewDecoder(rec.Body).Decode(&receipt); err != nil {
		t.Fatalf("decode receipt: %v", err)
	}
	if receipt.CommandID != "run-retry" || receipt.Version != 10 {
		t.Fatalf("receipt = %+v", receipt)
	}
}

func TestTaskAgentRunControlAcceptsEmptyRequestBody(t *testing.T) {
	command := &fakeMissionCommand{receipt: missioncontrol.CommandReceiptDTO{CommandID: "run-1", Status: "accepted", MissionID: "mission-1"}}
	handler := newTestRouter(RouterOptions{MissionCommand: command})

	cancel := authedHTTPTestRequest(http.MethodPost, "/api/missions/mission-1/agent-runs/run-1/cancel", bytes.NewBuffer(nil))
	cancel.Header.Set("X-User-ID", "operator")
	cancel.Header.Set("X-Viewer-Kind", "user")
	cancel.Header.Set("Idempotency-Key", "cancel-run-empty")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, cancel)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("cancel status = %d, body = %s", rec.Code, rec.Body.String())
	}

	retry := authedHTTPTestRequest(http.MethodPost, "/api/missions/mission-1/agent-runs/run-1/retry", bytes.NewBuffer(nil))
	retry.Header.Set("X-User-ID", "operator")
	retry.Header.Set("X-Viewer-Kind", "user")
	retry.Header.Set("Idempotency-Key", "retry-run-empty")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, retry)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("retry status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

type fakeMissionCommand struct {
	receipt         missioncontrol.CommandReceiptDTO
	err             error
	submit          missioncontrol.SubmitResultCommand
	submitCalled    bool
	startRun        missioncontrol.StartAgentRunCommand
	startRunCalled  bool
	cancelRun       missioncontrol.CancelAgentRunCommand
	cancelRunCalled bool
	retryRun        missioncontrol.RetryAgentRunCommand
	retryRunCalled  bool
}

func (f *fakeMissionCommand) SubmitAgentResult(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.SubmitResultCommand) (missioncontrol.CommandReceiptDTO, error) {
	f.submitCalled = true
	f.submit = cmd
	return f.receipt, f.err
}

func (f *fakeMissionCommand) SubmitQADecision(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.QADecisionCommand) (missioncontrol.CommandReceiptDTO, error) {
	return missioncontrol.CommandReceiptDTO{}, f.err
}

func (f *fakeMissionCommand) PostDiscussionMessage(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.PostDiscussionMessageCommand) (missioncontrol.CommandReceiptDTO, error) {
	return missioncontrol.CommandReceiptDTO{}, f.err
}

func (f *fakeMissionCommand) ReplyDiscussionMessage(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.ReplyDiscussionMessageCommand) (missioncontrol.CommandReceiptDTO, error) {
	return missioncontrol.CommandReceiptDTO{}, f.err
}

func (f *fakeMissionCommand) ListInbox(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.ListInboxCommand) (missioncontrol.InboxPageDTO, error) {
	return missioncontrol.InboxPageDTO{}, f.err
}

func (f *fakeMissionCommand) MarkInboxRead(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.InboxItemCommand) (missioncontrol.CommandReceiptDTO, error) {
	return missioncontrol.CommandReceiptDTO{}, f.err
}

func (f *fakeMissionCommand) CloseInboxItem(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.InboxItemCommand) (missioncontrol.CommandReceiptDTO, error) {
	return missioncontrol.CommandReceiptDTO{}, f.err
}

func (f *fakeMissionCommand) CreateMission(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.CreateMissionCommand) (missioncontrol.MissionDTO, error) {
	return missioncontrol.MissionDTO{}, f.err
}

func (f *fakeMissionCommand) DeleteMission(ctx context.Context, viewer missioncontrol.ViewerRef, missionID missioncore.MissionID) error {
	return f.err
}

func (f *fakeMissionCommand) CancelMission(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.CancelMissionCommand) (missioncontrol.CommandReceiptDTO, error) {
	return f.receipt, f.err
}

func (f *fakeMissionCommand) CreateTask(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.CreateTaskCommand) (missioncontrol.TaskCardDTO, error) {
	return missioncontrol.TaskCardDTO{}, f.err
}

func (f *fakeMissionCommand) AssignTask(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.AssignTaskCommand) (missioncontrol.CommandReceiptDTO, error) {
	return missioncontrol.CommandReceiptDTO{}, f.err
}

func (f *fakeMissionCommand) StartAgentRun(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.StartAgentRunCommand) (missioncontrol.CommandReceiptDTO, error) {
	f.startRunCalled = true
	f.startRun = cmd
	return f.receipt, f.err
}

func (f *fakeMissionCommand) CancelAgentRun(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.CancelAgentRunCommand) (missioncontrol.CommandReceiptDTO, error) {
	f.cancelRunCalled = true
	f.cancelRun = cmd
	return f.receipt, f.err
}

func (f *fakeMissionCommand) RetryAgentRun(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.RetryAgentRunCommand) (missioncontrol.CommandReceiptDTO, error) {
	f.retryRunCalled = true
	f.retryRun = cmd
	return f.receipt, f.err
}
