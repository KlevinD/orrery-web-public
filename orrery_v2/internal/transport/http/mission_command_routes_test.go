package httptransport

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	missioncontrol "orrery_v2/internal/app/missioncontrol"
	missioncore "orrery_v2/internal/mission/core"
)

const testInternalViewerAuthorization = "Bearer test-internal-viewer"

func newTestRouter(opts RouterOptions) http.Handler {
	opts.TrustedViewerHeaders = true
	if strings.TrimSpace(opts.InternalViewerAuth) == "" {
		opts.InternalViewerAuth = testInternalViewerAuthorization
	}
	return NewRouter(opts)
}

func TestHTTPRoutesQADecisionThroughMissionCommandService(t *testing.T) {
	command := &fakeFullMissionCommand{receipt: missioncontrol.CommandReceiptDTO{CommandID: "qa-result-1", Status: "accepted", AcceptedAt: time.Date(2026, 6, 1, 18, 0, 0, 0, time.UTC)}}
	handler := newTestRouter(RouterOptions{MissionCommand: command})
	req := authedRequest(http.MethodPost, "/api/missions/mission-1/qa/qa-1/decision", `{"task_id":"task-1","decision":"approved","message":"ok","checked_criteria":[{"criterion_id":"c1","status":"passed"}]}`)
	req.Header.Set("Idempotency-Key", "qa-key")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if command.qa.QARef != "qa-1" || command.qa.TaskID != "task-1" || command.qa.IdempotencyKey != "qa-key" {
		t.Fatalf("qa command = %+v", command.qa)
	}
}

func TestHTTPRoutesDiscussionPostAndReplyThroughMissionCommandService(t *testing.T) {
	command := &fakeFullMissionCommand{receipt: missioncontrol.CommandReceiptDTO{CommandID: "message-1", Status: "accepted"}}
	handler := newTestRouter(RouterOptions{MissionCommand: command})
	post := authedRequest(http.MethodPost, "/api/missions/mission-1/discussion/messages", `{"discussion_group_id":"discussion-1","content":"hello","message_kind":"request","visibility":"mission","required_response":true}`)
	post.Header.Set("Idempotency-Key", "post-key")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, post)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("post status = %d body = %s", rec.Code, rec.Body.String())
	}
	if command.post.DiscussionGroupID != "discussion-1" || command.post.Content != "hello" || !command.post.RequiredResponse {
		t.Fatalf("post command = %+v", command.post)
	}

	reply := authedRequest(http.MethodPost, "/api/missions/mission-1/discussion/messages/message-1/reply", `{"discussion_group_id":"discussion-1","content":"reply","inbox_item_id":"inbox-1"}`)
	reply.Header.Set("Idempotency-Key", "reply-key")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, reply)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("reply status = %d body = %s", rec.Code, rec.Body.String())
	}
	if command.reply.MessageID != "message-1" || command.reply.InboxItemID != "inbox-1" || command.reply.IdempotencyKey != "reply-key" {
		t.Fatalf("reply command = %+v", command.reply)
	}
}

func TestHTTPRoutesInboxListReadAndCloseThroughMissionCommandService(t *testing.T) {
	command := &fakeFullMissionCommand{
		receipt: missioncontrol.CommandReceiptDTO{CommandID: "inbox-1", Status: "accepted"},
		inbox:   missioncontrol.InboxPageDTO{Items: []missioncontrol.InboxItemDTO{{InboxItemID: "inbox-1", OwnerID: "agent-1", Status: "pending"}}},
	}
	handler := newTestRouter(RouterOptions{MissionCommand: command})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/inbox?mission_id=mission-1&limit=10", ``))

	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body = %s", rec.Code, rec.Body.String())
	}
	if command.listInbox.MissionID != "mission-1" || command.listInbox.Limit != 10 {
		t.Fatalf("list command = %+v", command.listInbox)
	}

	read := authedRequest(http.MethodPost, "/api/inbox/inbox-1/read", `{}`)
	read.Header.Set("Idempotency-Key", "read-key")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, read)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("read status = %d body = %s", rec.Code, rec.Body.String())
	}
	if command.read.InboxItemID != "inbox-1" {
		t.Fatalf("read command = %+v", command.read)
	}

	closeReq := authedRequest(http.MethodPost, "/api/inbox/inbox-1/close", `{"reply_message_id":"reply-1"}`)
	closeReq.Header.Set("Idempotency-Key", "close-key")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, closeReq)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("close status = %d body = %s", rec.Code, rec.Body.String())
	}
	if command.close.InboxItemID != "inbox-1" || command.close.ReplyMessageID != "reply-1" {
		t.Fatalf("close command = %+v", command.close)
	}
}

func authedRequest(method, path, body string) *http.Request {
	var reader *bytes.Buffer
	if body == "" {
		reader = bytes.NewBuffer(nil)
	} else {
		reader = bytes.NewBufferString(body)
	}
	return authedHTTPTestRequest(method, path, reader)
}

func authedOntologyRequest(method, path, body string) *http.Request {
	req := authedRequest(method, path, body)
	tenantID := strings.TrimSpace(req.URL.Query().Get("tenant_id"))
	if tenantID == "" && body != "" {
		var payload struct {
			TenantID string `json:"tenant_id"`
		}
		if json.Unmarshal([]byte(body), &payload) == nil {
			tenantID = strings.TrimSpace(payload.TenantID)
		}
	}
	if tenantID != "" {
		req.Header.Set("X-Tenant-ID", tenantID)
	}
	return req
}

func authedHTTPTestRequest(method, path string, body io.Reader) *http.Request {
	req := httptest.NewRequest(method, path, body)
	req.Header.Set("X-User-ID", "agent-1")
	req.Header.Set("X-Viewer-Kind", "agent")
	req.Header.Set("X-Tenant-ID", "system")
	req.Header.Set("X-Permission-Scope", "mission.read,mission.write,project.read,project.create,project.write,project.manage,discussion.read,discussion.write,approvals.read,approvals.decide,operations.read,operations.manage,org.read,org.manage,workforce.read,workforce.manage,roles.read,roles.manage,permissions.read,permissions.manage,audit.read,audit.manage,resource.read,resource.manage,tool.call,memory.read,memory.write,memory.review,memory.audit.read,memory.distill,knowledge.read,knowledge.write,knowledge.manage,scheduler.read,scheduler.write,scheduler.run,todo.read,todo.write,idea.read,idea.write,settings.read,settings.manage,ontology.read,ontology.manage")
	authorizeInternalViewerTestRequest(req)
	return req
}

func authorizeInternalViewerTestRequest(req *http.Request) {
	if req == nil {
		return
	}
	secured := withDeploymentSecurity(req, deploymentSecurityConfig{
		trustedViewerHeaders: true,
		internalViewerAuth:   testInternalViewerAuthorization,
	})
	*req = *secured
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("X-Orrery-Internal-Viewer-Authorization", testInternalViewerAuthorization)
}

type fakeFullMissionCommand struct {
	fakeMissionCommand
	receipt       missioncontrol.CommandReceiptDTO
	inbox         missioncontrol.InboxPageDTO
	qa            missioncontrol.QADecisionCommand
	post          missioncontrol.PostDiscussionMessageCommand
	reply         missioncontrol.ReplyDiscussionMessageCommand
	listInbox     missioncontrol.ListInboxCommand
	read          missioncontrol.InboxItemCommand
	close         missioncontrol.InboxItemCommand
	mission       missioncontrol.MissionDTO
	task          missioncontrol.TaskCardDTO
	createMission missioncontrol.CreateMissionCommand
	deleteMission string
	cancelMission missioncontrol.CancelMissionCommand
	createTask    missioncontrol.CreateTaskCommand
	assignTask    missioncontrol.AssignTaskCommand
	cancelRun     missioncontrol.CancelAgentRunCommand
	retryRun      missioncontrol.RetryAgentRunCommand
}

func (f *fakeFullMissionCommand) SubmitQADecision(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.QADecisionCommand) (missioncontrol.CommandReceiptDTO, error) {
	f.qa = cmd
	return f.receipt, nil
}

func (f *fakeFullMissionCommand) PostDiscussionMessage(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.PostDiscussionMessageCommand) (missioncontrol.CommandReceiptDTO, error) {
	f.post = cmd
	return f.receipt, nil
}

func (f *fakeFullMissionCommand) ReplyDiscussionMessage(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.ReplyDiscussionMessageCommand) (missioncontrol.CommandReceiptDTO, error) {
	f.reply = cmd
	return f.receipt, nil
}

func (f *fakeFullMissionCommand) ListInbox(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.ListInboxCommand) (missioncontrol.InboxPageDTO, error) {
	f.listInbox = cmd
	return f.inbox, nil
}

func (f *fakeFullMissionCommand) MarkInboxRead(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.InboxItemCommand) (missioncontrol.CommandReceiptDTO, error) {
	f.read = cmd
	return f.receipt, nil
}

func (f *fakeFullMissionCommand) CloseInboxItem(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.InboxItemCommand) (missioncontrol.CommandReceiptDTO, error) {
	f.close = cmd
	return f.receipt, nil
}

func (f *fakeFullMissionCommand) CreateMission(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.CreateMissionCommand) (missioncontrol.MissionDTO, error) {
	f.createMission = cmd
	return f.mission, nil
}

func (f *fakeFullMissionCommand) DeleteMission(ctx context.Context, viewer missioncontrol.ViewerRef, missionID missioncore.MissionID) error {
	f.deleteMission = missionID.String()
	return nil
}

func (f *fakeFullMissionCommand) CancelMission(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.CancelMissionCommand) (missioncontrol.CommandReceiptDTO, error) {
	f.cancelMission = cmd
	return f.receipt, nil
}

func (f *fakeFullMissionCommand) CreateTask(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.CreateTaskCommand) (missioncontrol.TaskCardDTO, error) {
	f.createTask = cmd
	return f.task, nil
}

func (f *fakeFullMissionCommand) AssignTask(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.AssignTaskCommand) (missioncontrol.CommandReceiptDTO, error) {
	f.assignTask = cmd
	return f.receipt, nil
}

func (f *fakeFullMissionCommand) StartAgentRun(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.StartAgentRunCommand) (missioncontrol.CommandReceiptDTO, error) {
	f.startRun = cmd
	return f.receipt, nil
}

func (f *fakeFullMissionCommand) CancelAgentRun(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.CancelAgentRunCommand) (missioncontrol.CommandReceiptDTO, error) {
	f.cancelRun = cmd
	return f.receipt, nil
}

func (f *fakeFullMissionCommand) RetryAgentRun(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.RetryAgentRunCommand) (missioncontrol.CommandReceiptDTO, error) {
	f.retryRun = cmd
	return f.receipt, nil
}

func decodeCommandReceipt(t *testing.T, rec *httptest.ResponseRecorder) missioncontrol.CommandReceiptDTO {
	t.Helper()
	var receipt missioncontrol.CommandReceiptDTO
	if err := json.NewDecoder(rec.Body).Decode(&receipt); err != nil {
		t.Fatalf("decode receipt: %v", err)
	}
	return receipt
}
