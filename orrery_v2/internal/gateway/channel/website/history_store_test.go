package website

import (
	"context"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
)

func TestSQLWebsiteHistoryStorePersistsOwnedMessagesWithoutTenantColumns(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer store.Close()
	for _, statement := range websiteHistoryMigrationStatements("INTEGER") {
		if _, err := store.Exec(ctx, statement); err != nil {
			t.Fatalf("migration: %v", err)
		}
	}
	rows, err := store.Query(ctx, `PRAGMA table_info(website_advisor_conversations)`)
	if err != nil {
		t.Fatalf("table info: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, fieldType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &fieldType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatalf("scan column: %v", err)
		}
		if strings.Contains(strings.ToLower(name), "tenant") {
			t.Fatalf("tenant column found: %s", name)
		}
	}

	repository := NewSQLWebsiteHistoryStore(store)
	now := time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC)
	turn := WebsiteTurn{
		ConversationID: "webconv_sql_1", SessionID: "adv_sql_1", ClientMessageID: "sql-message-1",
		UserText: "你好", AssistantText: "你好，我是 Orrery。", Locale: "zh-CN", InitialPagePath: "/zh-CN",
		CreatedAt: now, ExpiresAt: now.Add(defaultHistoryRetention),
	}
	if err := repository.SaveTurn(ctx, turn); err != nil {
		t.Fatalf("save anonymous turn: %v", err)
	}
	if adopted, err := repository.AdoptConversation(ctx, turn.ConversationID, "v1_owner_hash", now); err != nil || !adopted {
		t.Fatalf("adopted=%v err=%v", adopted, err)
	}
	conversation, err := repository.GetConversation(ctx, turn.ConversationID, "v1_owner_hash", now)
	if err != nil || len(conversation.Messages) != 2 || conversation.Messages[0].Role != "user" || conversation.Messages[1].Role != "assistant" {
		t.Fatalf("conversation=%+v err=%v", conversation, err)
	}
	if err := repository.SaveTurn(ctx, WebsiteTurn{
		ConversationID: turn.ConversationID, SessionID: "adv_sql_2", OwnerHash: "v1_owner_hash", ClientMessageID: "sql-message-2",
		UserText: "继续", AssistantText: "继续回答", Locale: "zh-CN", InitialPagePath: "/zh-CN",
		CreatedAt: now.Add(time.Minute), ExpiresAt: now.Add(defaultHistoryRetention),
	}); err != nil {
		t.Fatalf("save owned turn: %v", err)
	}
	conversation, err = repository.GetConversation(ctx, turn.ConversationID, "v1_owner_hash", now)
	if err != nil || len(conversation.Messages) != 4 {
		t.Fatalf("updated conversation=%+v err=%v", conversation, err)
	}
	if list, err := repository.ListConversations(ctx, "v1_owner_hash", now); err != nil || len(list) != 1 {
		t.Fatalf("list=%+v err=%v", list, err)
	}
	if removed, err := repository.CleanupExpired(ctx, now.Add(defaultHistoryRetention+time.Second)); err != nil || removed != 1 {
		t.Fatalf("removed=%d err=%v", removed, err)
	}
	var remainingMessages int
	if err := store.QueryRow(ctx, `SELECT COUNT(*) FROM website_advisor_messages WHERE conversation_id = ?`, turn.ConversationID).Scan(&remainingMessages); err != nil || remainingMessages != 0 {
		t.Fatalf("remaining messages=%d err=%v", remainingMessages, err)
	}
}
