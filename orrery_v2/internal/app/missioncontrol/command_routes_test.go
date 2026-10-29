package missioncontrol_test

import (
	"context"
	"errors"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/missioncontrol"
	missioncore "orrery_v2/internal/mission/core"
	discussion "orrery_v2/internal/mission/discussion"
	"orrery_v2/internal/mission/projection"
	missiontask "orrery_v2/internal/mission/task"
)

func TestCommandServiceSubmitsQADecisionThroughQAPort(t *testing.T) {
	at := time.Date(2026, 6, 1, 16, 0, 0, 0, time.UTC)
	qa := &fakeQAPort{returned: missiontask.QAResultRecord{ID: "qa-result-1", TaskID: "task-1", Status: missiontask.QAStatusApproved, CheckedAt: at}}
	service := newAuthorizedCommandServiceWithPorts(nil, qa, nil, nil)

	receipt, err := service.SubmitQADecision(context.Background(), viewer(), missioncontrol.QADecisionCommand{
		IdempotencyKey:  "qa-1",
		MissionID:       "mission-1",
		TaskID:          "task-1",
		QARef:           "qa-1",
		Decision:        "approved",
		Message:         "Evidence is sufficient",
		CheckedCriteria: []missioncontrol.CheckedCriterionDTO{{CriterionID: "criterion-1", Status: "passed", Reason: "covered"}},
	})
	if err != nil {
		t.Fatalf("SubmitQADecision returned error: %v", err)
	}
	if qa.taskID != missiontask.TaskID("task-1") || qa.opts.IdempotencyKey != "qa-1" {
		t.Fatalf("qa call = task %q opts %+v", qa.taskID, qa.opts)
	}
	if qa.result.CheckedBy != agentcore.AgentID("agent-1") || qa.result.Status != missiontask.QAStatusApproved {
		t.Fatalf("qa result = %+v", qa.result)
	}
	if receipt.CommandID != "qa-result-1" || receipt.Status != "accepted" || !receipt.AcceptedAt.Equal(at) {
		t.Fatalf("receipt = %+v", receipt)
	}
}

func TestCommandServiceNotifiesResponsibilityForApprovedAndRejectedQA(t *testing.T) {
	tests := []struct {
		name     string
		status   missiontask.QAStatus
		approved bool
	}{
		{name: "approved", status: missiontask.QAStatusApproved, approved: true},
		{name: "rejected", status: missiontask.QAStatusRejected, approved: false},
		{name: "needs rework", status: missiontask.QAStatusNeedsRework, approved: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			qa := &fakeQAPort{returned: missiontask.QAResultRecord{ID: "qa-result-1", TaskID: "task-1", Status: test.status, CheckedAt: time.Date(2026, 6, 1, 16, 0, 0, 0, time.UTC)}}
			observer := &capturingTaskResponsibilityReviews{}
			service := newAuthorizedCommandServiceWithPorts(nil, qa, nil, nil)
			service.SetTaskResponsibilityReviewPort(observer)
			if _, err := service.SubmitQADecision(context.Background(), viewer(), missioncontrol.QADecisionCommand{
				IdempotencyKey: "qa-notify-" + test.name, MissionID: "mission-1", TaskID: "task-1", QARef: "qa-1", Decision: string(test.status),
			}); err != nil {
				t.Fatalf("SubmitQADecision returned error: %v", err)
			}
			if len(observer.signals) != 1 || observer.signals[0].TenantID != "global" || observer.signals[0].TaskID != "task-1" || observer.signals[0].Approved != test.approved {
				t.Fatalf("responsibility review signals = %+v", observer.signals)
			}
		})
	}
}

func TestCommandServicePostsDiscussionAndRepliesThroughDiscussionPort(t *testing.T) {
	at := time.Date(2026, 6, 1, 17, 0, 0, 0, time.UTC)
	port := &fakeDiscussionPort{message: discussion.Message{ID: "message-1", MissionID: "mission-1", DiscussionGroupID: "discussion-1", CreatedAt: at}}
	service := newAuthorizedCommandServiceWithPorts(nil, nil, port, nil)

	receipt, err := service.PostDiscussionMessage(context.Background(), viewer(), missioncontrol.PostDiscussionMessageCommand{
		IdempotencyKey:    "message-1",
		MissionID:         "mission-1",
		DiscussionGroupID: "discussion-1",
		Content:           "Please review @agent-2",
		MessageKind:       "request",
		Visibility:        "mission",
		RequiredResponse:  true,
	})
	if err != nil {
		t.Fatalf("PostDiscussionMessage returned error: %v", err)
	}
	if port.post.Author.Ref != "agent-1" || port.post.Content == "" || !port.post.RequiredResponse {
		t.Fatalf("post command = %+v", port.post)
	}
	if receipt.CommandID != "message-1" || receipt.Status != "accepted" {
		t.Fatalf("receipt = %+v", receipt)
	}

	port.message.ID = "reply-1"
	receipt, err = service.ReplyDiscussionMessage(context.Background(), viewer(), missioncontrol.ReplyDiscussionMessageCommand{
		IdempotencyKey:    "reply-1",
		MissionID:         "mission-1",
		DiscussionGroupID: "discussion-1",
		MessageID:         "message-1",
		Content:           "Reviewed",
		InboxItemID:       "inbox-1",
	})
	if err != nil {
		t.Fatalf("ReplyDiscussionMessage returned error: %v", err)
	}
	if port.reply.ReplyToMessageID != discussion.MessageID("message-1") || port.reply.InboxItemID != discussion.InboxItemID("inbox-1") {
		t.Fatalf("reply command = %+v", port.reply)
	}
	if receipt.CommandID != "reply-1" {
		t.Fatalf("reply receipt = %+v", receipt)
	}
}

func TestCommandServiceUsesCanonicalHumanIdentityForDiscussionCommands(t *testing.T) {
	port := &fakeDiscussionPort{message: discussion.Message{ID: "message-1", MissionID: "mission-1", DiscussionGroupID: "discussion-1", CreatedAt: time.Now().UTC()}}
	service := newAuthorizedCommandServiceWithPorts(nil, nil, port, nil)
	viewer := missioncontrol.ViewerRef{
		ViewerID:               "login-account-1",
		ViewerKind:             missioncontrol.ViewerKindUser,
		AuthorizationSubjectID: "human-1",
	}

	if _, err := service.PostDiscussionMessage(context.Background(), viewer, missioncontrol.PostDiscussionMessageCommand{
		IdempotencyKey: "message-1", MissionID: "mission-1", DiscussionGroupID: "discussion-1", Content: "Canonical author",
	}); err != nil {
		t.Fatalf("PostDiscussionMessage returned error: %v", err)
	}
	if port.post.Author != (discussion.ViewerRef{Kind: discussion.ViewerUser, Ref: "human-1"}) {
		t.Fatalf("discussion author = %+v, want canonical human identity", port.post.Author)
	}
}

func TestCommandServiceUsesSystemIdentityForBreakGlassDiscussionCommands(t *testing.T) {
	port := &fakeDiscussionPort{message: discussion.Message{ID: "message-1", MissionID: "mission-1", DiscussionGroupID: "discussion-1", CreatedAt: time.Now().UTC()}}
	service := newAuthorizedCommandServiceWithPorts(nil, nil, port, nil)
	viewer := missioncontrol.ViewerRef{
		ViewerID:               "superadmin",
		ViewerKind:             missioncontrol.ViewerKindUser,
		AuthorizationSubjectID: agentcore.SystemSuperadminPrincipalID,
	}

	if _, err := service.PostDiscussionMessage(context.Background(), viewer, missioncontrol.PostDiscussionMessageCommand{
		IdempotencyKey: "message-1", MissionID: "mission-1", DiscussionGroupID: "discussion-1", Content: "System command",
	}); err != nil {
		t.Fatalf("PostDiscussionMessage returned error: %v", err)
	}
	if port.post.Author != (discussion.ViewerRef{Kind: discussion.ViewerSystem, Ref: agentcore.SystemSuperadminPrincipalID}) {
		t.Fatalf("discussion author = %+v, want system identity", port.post.Author)
	}
}

func TestCommandServiceRejectsLegacyDiscussionWritesForTodoOriginMission(t *testing.T) {
	ctx := context.Background()
	repo := missioncontrol.NewMemoryProjectionRepository()
	if err := repo.Save(ctx, projection.ReadModel{
		TenantID: "global",
		Mission: projection.MissionView{
			TenantID: "global", MissionID: "mission-todo", OriginKind: "todo", OriginTodoID: "todo-1",
		},
	}); err != nil {
		t.Fatalf("save Todo-origin Mission projection: %v", err)
	}
	port := &fakeDiscussionPort{message: discussion.Message{ID: "unexpected"}}
	service := newAuthorizedCommandServiceWithPorts(nil, nil, port, nil)
	service.SetRuntimeCommandPorts(nil, repo, nil, nil, nil)

	_, err := service.PostDiscussionMessage(ctx, viewer(), missioncontrol.PostDiscussionMessageCommand{
		IdempotencyKey: "legacy-post", MissionID: "mission-todo", DiscussionGroupID: "legacy-group", Content: "must use canonical conversation",
	})
	if !errors.Is(err, missioncontrol.ErrInvalidRequest) {
		t.Fatalf("PostDiscussionMessage error = %v, want ErrInvalidRequest", err)
	}
	_, err = service.ReplyDiscussionMessage(ctx, viewer(), missioncontrol.ReplyDiscussionMessageCommand{
		IdempotencyKey: "legacy-reply", MissionID: "mission-todo", DiscussionGroupID: "legacy-group", MessageID: "message-1", Content: "must use canonical conversation",
	})
	if !errors.Is(err, missioncontrol.ErrInvalidRequest) {
		t.Fatalf("ReplyDiscussionMessage error = %v, want ErrInvalidRequest", err)
	}
	if port.post.MissionID != "" || port.reply.MissionID != "" {
		t.Fatalf("legacy discussion port was called: post=%+v reply=%+v", port.post, port.reply)
	}
}

func TestCommandServiceInboxCommandsUseInboxPort(t *testing.T) {
	inbox := &fakeInboxPort{page: discussion.InboxPage{Items: []discussion.InboxItem{{ID: "inbox-1", MissionID: "mission-1", Recipient: discussion.ViewerRef{Kind: discussion.ViewerAgent, Ref: "agent-1"}, Status: discussion.InboxPending}}}}
	service := newAuthorizedCommandServiceWithPorts(nil, nil, nil, inbox)

	page, err := service.ListInbox(context.Background(), viewer(), missioncontrol.ListInboxCommand{MissionID: "mission-1", Limit: 20})
	if err != nil {
		t.Fatalf("ListInbox returned error: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].InboxItemID != "inbox-1" {
		t.Fatalf("page = %+v", page)
	}
	if inbox.query.MissionID != missioncore.MissionID("mission-1") || inbox.query.Limit != 20 {
		t.Fatalf("inbox query = %+v", inbox.query)
	}
	if _, err := service.MarkInboxRead(context.Background(), viewer(), missioncontrol.InboxItemCommand{InboxItemID: "inbox-1"}); err != nil {
		t.Fatalf("MarkInboxRead returned error: %v", err)
	}
	if inbox.readID != discussion.InboxItemID("inbox-1") {
		t.Fatalf("read id = %q", inbox.readID)
	}
	if _, err := service.CloseInboxItem(context.Background(), viewer(), missioncontrol.InboxItemCommand{InboxItemID: "inbox-1", ReplyMessageID: "reply-1"}); err != nil {
		t.Fatalf("CloseInboxItem returned error: %v", err)
	}
	if inbox.closeID != discussion.InboxItemID("inbox-1") || inbox.replyID != discussion.MessageID("reply-1") || inbox.closeViewer != (discussion.ViewerRef{Kind: discussion.ViewerAgent, Ref: "agent-1"}) {
		t.Fatalf("close = %q viewer = %+v reply = %q", inbox.closeID, inbox.closeViewer, inbox.replyID)
	}
}

func TestCommandServiceRejectsMissingCommandDependencies(t *testing.T) {
	service := newAuthorizedCommandService(nil)
	_, err := service.SubmitQADecision(context.Background(), viewer(), missioncontrol.QADecisionCommand{IdempotencyKey: "qa-1"})
	if !errors.Is(err, missioncontrol.ErrServiceDependency) {
		t.Fatalf("error = %v, want service dependency", err)
	}
}

func viewer() missioncontrol.ViewerRef {
	return missioncontrol.ViewerRef{ViewerID: "agent-1", ViewerKind: missioncontrol.ViewerKindAgent}
}

type fakeQAPort struct {
	returned missiontask.QAResultRecord
	result   missiontask.QAResultRecord
	taskID   missiontask.TaskID
	opts     missiontask.QASubmitOptions
}

type capturingTaskResponsibilityReviews struct {
	signals []missioncontrol.TaskResponsibilityReviewSignal
}

func (c *capturingTaskResponsibilityReviews) ObserveTaskReview(_ context.Context, signal missioncontrol.TaskResponsibilityReviewSignal) error {
	c.signals = append(c.signals, signal)
	return nil
}

func (f *fakeQAPort) SubmitQAResult(ctx context.Context, taskID missiontask.TaskID, result missiontask.QAResultRecord, opts missiontask.QASubmitOptions) (missiontask.QAResultRecord, error) {
	f.taskID = taskID
	f.result = result
	f.opts = opts
	return f.returned, nil
}

type fakeDiscussionPort struct {
	message discussion.Message
	post    discussion.PostMessageCommand
	reply   discussion.ReplyCommand
}

func (f *fakeDiscussionPort) PostMessage(ctx context.Context, cmd discussion.PostMessageCommand) (discussion.Message, error) {
	f.post = cmd
	return f.message, nil
}

func (f *fakeDiscussionPort) Reply(ctx context.Context, cmd discussion.ReplyCommand) (discussion.Message, error) {
	f.reply = cmd
	return f.message, nil
}

type fakeInboxPort struct {
	page        discussion.InboxPage
	query       discussion.InboxQuery
	readID      discussion.InboxItemID
	closeViewer discussion.ViewerRef
	closeID     discussion.InboxItemID
	replyID     discussion.MessageID
}

func (f *fakeInboxPort) ListInbox(ctx context.Context, recipient discussion.ViewerRef, q discussion.InboxQuery) (discussion.InboxPage, error) {
	f.query = q
	return f.page, nil
}

func (f *fakeInboxPort) MarkRead(ctx context.Context, itemID discussion.InboxItemID, viewer discussion.ViewerRef) error {
	f.readID = itemID
	return nil
}

func (f *fakeInboxPort) CloseWithReply(ctx context.Context, itemID discussion.InboxItemID, viewer discussion.ViewerRef, reply discussion.MessageID) (discussion.InboxItem, error) {
	f.closeID = itemID
	f.closeViewer = viewer
	f.replyID = reply
	for _, item := range f.page.Items {
		if item.ID == itemID {
			return item, nil
		}
	}
	return discussion.InboxItem{}, nil
}
