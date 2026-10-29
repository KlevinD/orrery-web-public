package discussionsession

import (
	"context"
	"fmt"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
)

func TestConversationReadsPreserveAllAggregatePartsAndExactScope(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(newRepositoryTestRouter(t))
	// An identifier must stay a bound value, including quotes and SQL syntax.
	const id = "conversation-' OR 1=1 --"
	now := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: id, TenantID: "tenant-a", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	parts := []struct {
		table string
		value any
	}{
		{messageTable, MessageRecord{MessageID: "message", ConversationID: id, Content: "owner message", CreatedAt: now}},
		{memberTable, ConversationMemberRecord{MemberID: "owner", ConversationID: id, MemberKind: "user", CreatedAt: now}},
		{turnTable, TurnRecord{TurnID: "turn", ConversationID: id, Status: "completed", CreatedAt: now}},
		{runTable, RunRecord{RunID: "run", ConversationID: id, Status: "completed", CreatedAt: now}},
		{"core.discussion_session_events", EventRecord{EventID: "event", ConversationID: id, CreatedAt: now}},
		{missionLinkTable, LinkedMissionRecord{MissionID: "mission", ConversationID: id, CreatedAt: now}},
		{attachmentTable, AttachmentRecord{AttachmentID: "attachment", ConversationID: id, CreatedAt: now}},
	}
	for _, part := range parts {
		payload, err := encodePayload(part.value)
		if err != nil {
			t.Fatal(err)
		}
		for _, candidate := range []struct{ key, conversation string }{{"selected", id}, {"foreign", "other-conversation"}} {
			if err := repo.router.PutRecord(ctx, part.table, candidate.key, db.Record{"conversation_id": candidate.conversation, "payload": payload}); err != nil {
				t.Fatal(err)
			}
		}
	}
	view, err := repo.GetConversation(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	counts := []int{len(view.Messages), len(view.Members), len(view.Turns), len(view.Runs), len(view.Events), len(view.LinkedMissions), len(view.Attachments)}
	for i, count := range counts {
		if count != 1 {
			t.Fatalf("aggregate part %s count = %d, want 1", parts[i].table, count)
		}
	}
	if view.TenantID != "tenant-a" || view.Messages[0].Content != "owner message" || view.Runs[0].RunID != "run" {
		t.Fatal("conversation content changed")
	}
}

func TestConversationReadAndMessagePagesKeepHistoryBeyondRecordPageLimit(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(newRepositoryTestRouter(t))
	const id = "long-conversation"
	now := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: id, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	store, table, err := repo.router.StoreForTable(messageTable)
	if err != nil {
		t.Fatal(err)
	}
	const count = db.MaxRecordPageLimit + 3
	if err := store.WithTx(ctx, func(tx db.Tx) error {
		for i := 0; i < count; i++ {
			message := MessageRecord{MessageID: fmt.Sprintf("message-%04d", i), ConversationID: id, Content: "history", CreatedAt: now.Add(time.Duration(i) * time.Second)}
			if err := putPayloadRecordTx(ctx, tx, table, message.MessageID, message, db.Record{"conversation_id": id, "created_at": formatTime(message.CreatedAt)}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := repo.GetConversation(ctx, id)
	if err != nil || len(view.Messages) != count {
		t.Fatalf("full history count = %d, error = %v", len(view.Messages), err)
	}
	page, meta, err := repo.ListMessagesPage(ctx, id, 4, 1)
	if err != nil || meta.Total != count || len(page) != 4 || !meta.HasMore || page[0].MessageID != fmt.Sprintf("message-%04d", count-5) || page[3].MessageID != fmt.Sprintf("message-%04d", count-2) {
		t.Fatalf("history page changed: meta=%+v messages=%+v error=%v", meta, page, err)
	}
}
