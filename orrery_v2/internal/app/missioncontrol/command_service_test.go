package missioncontrol_test

import (
	"context"
	"errors"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/missioncontrol"
	missioncore "orrery_v2/internal/mission/core"
	missiontask "orrery_v2/internal/mission/task"
)

func TestCommandServiceSubmitsAgentResultThroughDeliveryPort(t *testing.T) {
	at := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	port := &fakeDeliveryPort{submission: missiontask.TaskSubmission{
		ID:                  "submission-1",
		TaskID:              "task-1",
		AgentID:             "agent-1",
		SubmittedAt:         at,
		TaskVersionAccepted: 8,
	}}
	service := newAuthorizedCommandService(port)

	receipt, err := service.SubmitAgentResult(context.Background(),
		missioncontrol.ViewerRef{ViewerID: "agent-1", ViewerKind: missioncontrol.ViewerKindAgent},
		missioncontrol.SubmitResultCommand{
			RequestID:       "req-1",
			IdempotencyKey:  "submit-1",
			ExpectedVersion: 8,
			MissionID:       "mission-1",
			TaskID:          "task-1",
			Result: agentcore.AgentResult{
				ID:               "result-1",
				RunID:            "run-1",
				Summary:          agentcore.Summary{Text: "Done"},
				InternalTraceRef: "trace-should-not-cross-transport",
			},
		})
	if err != nil {
		t.Fatalf("SubmitAgentResult returned error: %v", err)
	}

	if port.taskID != missiontask.TaskID("task-1") {
		t.Fatalf("task id = %q", port.taskID)
	}
	if port.opts.IdempotencyKey != "submit-1" {
		t.Fatalf("idempotency key = %q", port.opts.IdempotencyKey)
	}
	if port.opts.ExpectedVersion != 8 {
		t.Fatalf("expected version = %d", port.opts.ExpectedVersion)
	}
	if port.result.MissionID != missioncore.MissionID("mission-1") || port.result.TaskID != missioncore.TaskID("task-1") || port.result.AgentID != agentcore.AgentID("agent-1") {
		t.Fatalf("result was not completed from command/viewer: %+v", port.result)
	}
	if port.result.InternalTraceRef != "" {
		t.Fatalf("internal trace crossed app command boundary: %+v", port.result)
	}
	if receipt.CommandID != "submission-1" || receipt.Status != "accepted" || receipt.Version != 8 || !receipt.AcceptedAt.Equal(at) {
		t.Fatalf("receipt = %+v", receipt)
	}
}

func TestCommandServiceRecordsAuditForSuccessfulSubmitResult(t *testing.T) {
	at := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	port := &fakeDeliveryPort{submission: missiontask.TaskSubmission{
		ID:                  "submission-1",
		TaskID:              "task-1",
		AgentID:             "agent-1",
		SubmittedAt:         at,
		TaskVersionAccepted: 8,
	}}
	audit := &fakeMissionAuditRecorder{}
	service := newAuthorizedCommandService(port)
	service.SetAuditRecorder(audit)

	viewer := missioncontrol.ViewerRef{TenantID: "tenant-a", ViewerID: "agent-1", ViewerKind: missioncontrol.ViewerKindAgent}
	cmd := missioncontrol.SubmitResultCommand{
		RequestID:      "corr-1",
		IdempotencyKey: "submit-1",
		MissionID:      "mission-1",
		TaskID:         "task-1",
		Result:         agentcore.AgentResult{ID: "result-1", Summary: agentcore.Summary{Text: "Done"}},
	}
	_, err := service.SubmitAgentResult(context.Background(), viewer, cmd)
	if err != nil {
		t.Fatalf("SubmitAgentResult returned error: %v", err)
	}
	if _, err := service.SubmitAgentResult(context.Background(), viewer, cmd); err != nil {
		t.Fatalf("SubmitAgentResult replay returned error: %v", err)
	}
	if len(audit.events) != 2 {
		t.Fatalf("audit events = %+v", audit.events)
	}
	event := audit.events[0]
	if event.AuditID == "" || audit.events[1].AuditID != event.AuditID || event.TenantID != "tenant-a" || event.ActorID != "agent-1" || event.ViewerID != "agent-1" || event.MissionID != "mission-1" || event.EntityType != "task" || event.EntityID != "task-1" {
		t.Fatalf("audit event identity = %+v", event)
	}
	if event.Action != "task.submit_result" || event.Result != "succeeded" || event.CorrelationID != "corr-1" {
		t.Fatalf("audit event outcome = %+v", event)
	}
}

func TestCommandServiceBusinessApprovalAuditIncludesPermissionFields(t *testing.T) {
	audit := &fakeMissionAuditRecorder{}
	service := newAuthorizedCommandService(nil)
	service.SetAuditRecorder(audit)

	_, err := service.DecideBusinessApproval(context.Background(),
		missioncontrol.ViewerRef{ViewerID: "ops-lead", ViewerKind: missioncontrol.ViewerKindAgent},
		missioncontrol.BusinessApprovalDecisionCommand{
			RequestID:      "corr-approval",
			IdempotencyKey: "approval-key",
			ApprovalType:   "tool_access",
			MissionID:      "mission-1",
			ApprovalRef:    "tool-approval-1",
			Decision:       "approved",
			Message:        "approved deployment",
			Metadata:       map[string]string{"agent_id": "agent-ops", "tool_id": "tool.deploy", "scope": "deploy:delete"},
		})
	if err != nil {
		t.Fatalf("DecideBusinessApproval returned error: %v", err)
	}
	if len(audit.events) != 1 {
		t.Fatalf("audit events = %+v", audit.events)
	}
	event := audit.events[0]
	if event.Metadata["agent_id"] != "agent-ops" || event.Metadata["tool_id"] != "tool.deploy" || event.Metadata["scope"] != "deploy:delete" {
		t.Fatalf("approval audit metadata = %+v", event.Metadata)
	}
	if event.CorrelationID != "corr-approval" {
		t.Fatalf("approval audit correlation = %+v", event)
	}
}

func TestCommandServiceRejectsMissingIdempotencyBeforeDeliveryPort(t *testing.T) {
	port := &fakeDeliveryPort{}
	service := newAuthorizedCommandService(port)

	_, err := service.SubmitAgentResult(context.Background(),
		missioncontrol.ViewerRef{ViewerID: "agent-1", ViewerKind: missioncontrol.ViewerKindAgent},
		missioncontrol.SubmitResultCommand{MissionID: "mission-1", TaskID: "task-1"})

	if !errors.Is(err, missioncontrol.ErrInvalidRequest) {
		t.Fatalf("error = %v, want ErrInvalidRequest", err)
	}
	if port.called {
		t.Fatal("delivery port should not be called for invalid request")
	}
}

type fakeMissionAuditRecorder struct {
	events []missioncontrol.AuditEvent
}

func (f *fakeMissionAuditRecorder) RecordAuditEvent(ctx context.Context, event missioncontrol.AuditEvent) error {
	f.events = append(f.events, event)
	return nil
}

type fakeDeliveryPort struct {
	submission missiontask.TaskSubmission
	err        error
	called     bool
	taskID     missiontask.TaskID
	result     agentcore.AgentResult
	opts       missiontask.SubmitOptions
}

func (f *fakeDeliveryPort) SubmitAgentResult(ctx context.Context, taskID missiontask.TaskID, result agentcore.AgentResult, opts missiontask.SubmitOptions) (missiontask.TaskSubmission, error) {
	f.called = true
	f.taskID = taskID
	f.result = result
	f.opts = opts
	return f.submission, f.err
}
