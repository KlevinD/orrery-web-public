package discussionsession

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPersonalAssistantReplyKeepsModelContentAndAssistantRole(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	seedPersonalAssistantConversation(t, repo, "owned", "tenant-a", "alice", "owner-a", testTime)
	runner := &capturingDiscussionAgentRunner{}
	service := newTestService(ServiceOptions{Repository: repo, DiscussionAgent: runner})
	viewer := ViewerRef{TenantID: "tenant-a", ViewerID: "alice", OwnerID: "owner-a", ViewerKind: ViewerKindUser}
	const body = "旧的图谱维护运行已在清理时终止。\n这次没有产生业务结果。"
	if err := service.AppendPersonalAssistantReply(ctx, viewer, "scheduled-reply", body); err != nil {
		t.Fatal(err)
	}
	if err := service.AppendPersonalAssistantReply(ctx, viewer, "scheduled-reply", "different retry"); err != nil {
		t.Fatal(err)
	}
	view, err := repo.GetConversation(ctx, "owned")
	if err != nil || len(view.Messages) != 1 || runner.called {
		t.Fatalf("model reply was duplicated or reexecuted: %+v %v", view, err)
	}
	message := view.Messages[0]
	if message.Role != "assistant" || message.Content != body || message.Metadata["content_origin"] != "llm" || message.Metadata["kind"] != "scheduled_task_result" {
		t.Fatalf("model content was presented as a platform notice: %+v", message)
	}
}

func TestPersonalAssistantNoticeIsPassiveDurableAndPinnedAcrossNewChats(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	seedPersonalAssistantConversation(t, repo, "owned", "tenant-a", "alice", "owner-a", testTime)
	runner := &capturingDiscussionAgentRunner{}
	realtime := &recordingRealtimePublisher{}
	service := newTestService(ServiceOptions{Repository: repo, DiscussionAgent: runner, RealtimePublisher: realtime, Clock: fixedClock{now: testTime.Add(time.Minute)}})
	viewer := ViewerRef{TenantID: "tenant-a", ViewerID: "alice", OwnerID: "owner-a", ViewerKind: ViewerKindUser}
	view, _ := repo.GetConversation(ctx, "owned")
	turn := TurnRecord{TurnID: "active", ConversationID: "owned", Status: "running", CreatedAt: testTime, UpdatedAt: testTime}
	if _, err := repo.ApplyTurnMutation(ctx, TurnAggregateMutation{Conversation: view.ConversationRecord, ExpectedRevision: view.Revision, Turn: &turn}); err != nil {
		t.Fatal(err)
	}
	if err := service.AppendPersonalAssistantNotice(ctx, viewer, "todo:one", "Please review quote"); err != nil {
		t.Fatal(err)
	}
	seedPersonalAssistantConversation(t, repo, "new-chat", "tenant-a", "alice", "owner-a", testTime.Add(2*time.Minute))
	if err := service.AppendPersonalAssistantNotice(ctx, viewer, "todo:one", "Changed title"); err != nil {
		t.Fatal(err)
	}
	view, _ = repo.GetConversation(ctx, "owned")
	other, _ := repo.GetConversation(ctx, "new-chat")
	if runner.called || view.ActiveTurnID != "active" || len(view.Messages) != 1 || len(other.Messages) != 0 || view.Messages[0].Content != "Please review quote" {
		t.Fatalf("not passive or idempotent: %+v %+v", view, other)
	}
	if len(realtime.eventsByType(RealtimeEventUserDiscussionListInvalidated)) != 1 {
		t.Fatal("notice did not invalidate the inbox exactly once")
	}
}

func TestPersonalAssistantNoticeRecoversAfterRealtimeFailure(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	seedPersonalAssistantConversation(t, repo, "owned", "tenant-a", "alice", "owner-a", testTime)
	realtime := &failOnceTodoAutomationRealtime{failure: errors.New("realtime unavailable")}
	service := newTestService(ServiceOptions{Repository: repo, RealtimePublisher: realtime})
	viewer := ViewerRef{TenantID: "tenant-a", ViewerID: "alice", OwnerID: "owner-a", ViewerKind: ViewerKindUser}
	if err := service.AppendPersonalAssistantNotice(ctx, viewer, "notice", "pending"); err == nil {
		t.Fatal("expected first realtime failure")
	}
	if err := service.AppendPersonalAssistantNotice(ctx, viewer, "notice", "pending"); err != nil {
		t.Fatal(err)
	}
	view, _ := repo.GetConversation(ctx, "owned")
	if len(view.Messages) != 1 {
		t.Fatalf("duplicated notice: %+v", view.Messages)
	}
}

func TestPersonalAssistantNoticeDoesNotRecreateDeletedDeliveredChat(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	service := newTestService(ServiceOptions{Repository: repo})
	viewer := ViewerRef{TenantID: "tenant-a", ViewerID: "alice", OwnerID: "owner-a", ViewerKind: ViewerKindUser}
	if err := service.AppendPersonalAssistantNotice(ctx, viewer, "notice", "pending"); err != nil {
		t.Fatal(err)
	}
	view, found, err := service.findLatestPersonalAssistantConversation(ctx, viewer)
	if err != nil || !found {
		t.Fatal("missing chat")
	}
	if err := repo.DeleteConversation(ctx, view.ConversationID); err != nil {
		t.Fatal(err)
	}
	if err := service.AppendPersonalAssistantNotice(ctx, viewer, "notice", "pending"); err != nil {
		t.Fatal(err)
	}
	if _, found, err := service.findLatestPersonalAssistantConversation(ctx, viewer); err != nil || found {
		t.Fatalf("deleted chat recreated: %v %v", found, err)
	}
}

func TestPersonalAssistantNoticeCreatesPrivateChatWithoutUserMessage(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{}
	service := newTestService(ServiceOptions{Repository: repo, DiscussionAgent: runner})
	viewer := ViewerRef{TenantID: "tenant-a", ViewerID: "alice", OwnerID: "owner-a", ViewerKind: ViewerKindUser}
	if err := service.AppendPersonalAssistantNotice(ctx, viewer, "notice", "pending"); err != nil {
		t.Fatal(err)
	}
	view, found, err := service.findLatestPersonalAssistantConversation(ctx, viewer)
	if err != nil || !found {
		t.Fatalf("chat missing: %v %v", found, err)
	}
	stored, err := repo.GetConversation(ctx, view.ConversationID)
	if err != nil || stored.Status != "completed" || stored.ActiveTurnID != "" || len(stored.Messages) != 1 || runner.called {
		t.Fatalf("unexpected chat: %+v %v", stored, err)
	}
	if stored.OwnerID != viewer.OwnerID || stored.TenantID != viewer.TenantID {
		t.Fatal("wrong notice owner")
	}
}

func TestPersonalAssistantNoticeIsolatesOwnersTenantsAndRejectsNonHumans(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	service := newTestService(ServiceOptions{Repository: repo})
	for _, scope := range [][2]string{{"tenant-a", "owner-a"}, {"tenant-b", "owner-a"}, {"tenant-a", "owner-b"}} {
		viewer := ViewerRef{TenantID: scope[0], OwnerID: scope[1], ViewerID: "alice", ViewerKind: ViewerKindUser}
		if err := service.AppendPersonalAssistantNotice(ctx, viewer, "same-key", scope[0]+scope[1]); err != nil {
			t.Fatal(err)
		}
		view, found, err := service.findLatestPersonalAssistantConversation(ctx, viewer)
		if err != nil || !found || view.OwnerID != scope[1] || view.TenantID != scope[0] {
			t.Fatalf("scope leak: %+v %v", view, err)
		}
	}
	if err := service.AppendPersonalAssistantNotice(ctx, ViewerRef{TenantID: "tenant-a", OwnerID: "owner-a", ViewerID: "alice", ViewerKind: ViewerKindAgent}, "same-key", "pending"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("agent accepted: %v", err)
	}
}
