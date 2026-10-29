package discussionsession

import (
	"context"
	"errors"
	"testing"
)

func TestServiceAppendNoticeIsPassiveIdempotentAndPreservesActiveTurn(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{}
	realtime := &recordingRealtimePublisher{}
	service := newTestService(ServiceOptions{
		Repository: repo, DiscussionAgent: runner, RealtimePublisher: realtime,
		Clock: fixedClock{now: testTime},
	})
	viewer := ViewerRef{ViewerID: "owner-1", ViewerKind: ViewerKindUser, TenantID: "tenant-a"}
	conversation, err := service.EnsureTodoConversation(ctx, viewer, EnsureTodoConversationCommand{
		TenantID: "tenant-a", TodoID: "todo-1", Title: "Todo 1", ManagedSource: "todo:tenant-a/todo-1",
	})
	if err != nil {
		t.Fatalf("EnsureTodoConversation: %v", err)
	}
	view, err := repo.GetConversation(ctx, conversation.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	turn := TurnRecord{
		TurnID: "turn-active", ConversationID: conversation.ConversationID, RootMessageID: "root-active",
		Mode: "group", Status: "running", CreatedAt: testTime, UpdatedAt: testTime,
	}
	if _, err := repo.ApplyTurnMutation(ctx, TurnAggregateMutation{Conversation: view.ConversationRecord, ExpectedRevision: view.Revision, Turn: &turn}); err != nil {
		t.Fatalf("seed active turn: %v", err)
	}

	command := AppendNoticeCommand{
		ConversationID: conversation.ConversationID, MissionID: "mission-1", MissionTaskID: "task-1",
		Content: "Task assigned to the responsibility coordinator.", Metadata: map[string]string{"kind": "todo_execution_dispatched"},
		IdempotencyKey: "notice-1",
	}
	if err := repo.SaveAttachment(ctx, AttachmentRecord{AttachmentID: "notice-file", ConversationID: conversation.ConversationID, FileName: "receipt.png", ContentType: "image/png", CreatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	command.AttachmentIDs = []string{"notice-file"}
	first, err := service.AppendNotice(ctx, viewer, command)
	if err != nil {
		t.Fatalf("AppendNotice: %v", err)
	}
	second, err := service.AppendNotice(ctx, viewer, command)
	if err != nil {
		t.Fatalf("AppendNotice replay: %v", err)
	}
	if runner.called {
		t.Fatal("passive notice invoked the discussion Agent runtime")
	}
	if len(first.Messages) != 1 || len(second.Messages) != 1 {
		t.Fatalf("notice replay duplicated messages: first=%+v second=%+v", first.Messages, second.Messages)
	}
	message := first.Messages[0]
	if len(message.Attachments) != 1 || message.Attachments[0].AttachmentID != "notice-file" {
		t.Fatalf("notice attachments=%+v", message.Attachments)
	}
	if message.Role != "system" || message.MissionID != "mission-1" || message.MissionTaskID != "task-1" || message.Metadata[discussionInterjectionDeliveryMetadataKey] != discussionNoticeDelivery {
		t.Fatalf("notice message = %+v", message)
	}
	if first.Revision != second.Revision || first.Revision <= conversation.Revision {
		t.Fatalf("notice revisions first=%d second=%d initial=%d", first.Revision, second.Revision, conversation.Revision)
	}
	persisted, err := repo.GetConversation(ctx, conversation.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.ActiveTurnID != "turn-active" {
		t.Fatalf("active turn = %q, want turn-active", persisted.ActiveTurnID)
	}
	for _, eventType := range []string{RealtimeEventDiscussionMessageCreated, RealtimeEventDiscussionMessageCompleted, RealtimeEventDiscussionSnapshotInvalidated, RealtimeEventUserDiscussionListInvalidated} {
		if got := len(realtime.eventsByType(eventType)); got != 1 {
			t.Fatalf("realtime %s count = %d, want 1", eventType, got)
		}
	}
}

func TestServiceAppendNoticeRequiresConversationMembershipAndTenant(t *testing.T) {
	ctx := context.Background()
	service := newTestService(ServiceOptions{Repository: newMemoryRepository(t), Clock: fixedClock{now: testTime}})
	owner := ViewerRef{ViewerID: "owner-1", ViewerKind: ViewerKindUser, TenantID: "tenant-a"}
	conversation, err := service.EnsureTodoConversation(ctx, owner, EnsureTodoConversationCommand{
		TenantID: "tenant-a", TodoID: "todo-1", ManagedSource: "todo:tenant-a/todo-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	command := AppendNoticeCommand{ConversationID: conversation.ConversationID, Content: "status", IdempotencyKey: "notice-1"}
	for name, viewer := range map[string]ViewerRef{
		"non-member":   {ViewerID: "outsider", ViewerKind: ViewerKindUser, TenantID: "tenant-a"},
		"other tenant": {ViewerID: "owner-1", ViewerKind: ViewerKindUser, TenantID: "tenant-b"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := service.AppendNotice(ctx, viewer, command); !errors.Is(err, ErrForbidden) {
				t.Fatalf("AppendNotice error = %v, want ErrForbidden", err)
			}
		})
	}
}
