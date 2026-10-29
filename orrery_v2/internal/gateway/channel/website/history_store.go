package website

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

type MemoryWebsiteHistoryStore struct {
	mu            sync.Mutex
	conversations map[string]WebsiteConversation
}

func NewMemoryWebsiteHistoryStore() *MemoryWebsiteHistoryStore {
	return &MemoryWebsiteHistoryStore{conversations: map[string]WebsiteConversation{}}
}

func (s *MemoryWebsiteHistoryStore) SaveTurn(_ context.Context, turn WebsiteTurn) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	conversation := s.conversations[turn.ConversationID]
	if conversation.ConversationID == "" {
		conversation = newWebsiteConversation(turn)
	} else if conversation.OwnerHash != turn.OwnerHash {
		return errWebsiteHistoryNotFound
	}
	seen := map[string]struct{}{}
	for _, message := range conversation.Messages {
		seen[message.MessageID] = struct{}{}
	}
	for _, message := range websiteTurnMessages(turn, len(conversation.Messages)) {
		if _, exists := seen[message.MessageID]; !exists {
			conversation.Messages = append(conversation.Messages, message)
		}
	}
	conversation.UpdatedAt = turn.CreatedAt.UTC()
	conversation.ExpiresAt = turn.ExpiresAt.UTC()
	s.conversations[turn.ConversationID] = cloneWebsiteConversation(conversation)
	return nil
}

func (s *MemoryWebsiteHistoryStore) ListConversations(_ context.Context, ownerHash string, now time.Time) ([]WebsiteConversationSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	values := []WebsiteConversationSummary{}
	for _, conversation := range s.conversations {
		if conversation.OwnerHash == ownerHash && conversation.ExpiresAt.After(now) {
			values = append(values, conversation.WebsiteConversationSummary)
		}
	}
	sortWebsiteConversationSummaries(values)
	return values, nil
}

func (s *MemoryWebsiteHistoryStore) GetConversation(_ context.Context, conversationID, ownerHash string, now time.Time) (WebsiteConversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	conversation, ok := s.conversations[conversationID]
	if !ok || conversation.OwnerHash != ownerHash || !conversation.ExpiresAt.After(now) {
		return WebsiteConversation{}, errWebsiteHistoryNotFound
	}
	return cloneWebsiteConversation(conversation), nil
}

func (s *MemoryWebsiteHistoryStore) DeleteConversation(_ context.Context, conversationID, ownerHash string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	conversation, ok := s.conversations[conversationID]
	if !ok || conversation.OwnerHash != ownerHash {
		return false, nil
	}
	delete(s.conversations, conversationID)
	return true, nil
}

func (s *MemoryWebsiteHistoryStore) AdoptConversation(_ context.Context, conversationID, ownerHash string, now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	conversation, ok := s.conversations[conversationID]
	if !ok || !conversation.ExpiresAt.After(now) || (conversation.OwnerHash != "" && conversation.OwnerHash != ownerHash) {
		return false, nil
	}
	conversation.OwnerHash = ownerHash
	s.conversations[conversationID] = conversation
	return true, nil
}

func (s *MemoryWebsiteHistoryStore) CleanupExpired(_ context.Context, now time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	removed := 0
	for id, conversation := range s.conversations {
		if !conversation.ExpiresAt.After(now) {
			delete(s.conversations, id)
			removed++
		}
	}
	return removed, nil
}

type SQLWebsiteHistoryStore struct {
	store *db.Store
}

func NewSQLWebsiteHistoryStore(store *db.Store) *SQLWebsiteHistoryStore {
	return &SQLWebsiteHistoryStore{store: store}
}

func RegisterMigrations(coordinator *migration.Coordinator) error {
	if coordinator == nil {
		return nil
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, websiteHistoryMigration()); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, websiteGapMigration()); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, websiteFAQCatalogMigration()); err != nil {
		return err
	}
	return coordinator.RegisterInitializer(db.RoleCore, websiteDistillationMigration())
}

func websiteHistoryMigration() migration.Migration {
	return migration.Migration{
		Version: "0188_website_advisor_history", Description: "create owned website advisor history tables",
		Statements: websiteHistoryMigrationStatements("INTEGER"),
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: websiteHistoryMigrationStatements("BIGINT"),
		},
	}
}

func websiteHistoryMigrationStatements(timeType string) []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS website_advisor_conversations (
			conversation_id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			owner_hash TEXT NOT NULL DEFAULT '',
			title TEXT NOT NULL,
			locale TEXT NOT NULL DEFAULT '',
			initial_page_path TEXT NOT NULL DEFAULT '',
			created_at ` + timeType + ` NOT NULL,
			updated_at ` + timeType + ` NOT NULL,
			expires_at ` + timeType + ` NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_website_advisor_conversations_owner_updated ON website_advisor_conversations (owner_hash, updated_at)`,
		`CREATE INDEX IF NOT EXISTS idx_website_advisor_conversations_expires ON website_advisor_conversations (expires_at)`,
		`CREATE TABLE IF NOT EXISTS website_advisor_messages (
			message_id TEXT PRIMARY KEY,
			conversation_id TEXT NOT NULL,
			client_message_id TEXT NOT NULL,
			role TEXT NOT NULL CHECK (role IN ('user', 'assistant')),
			content TEXT NOT NULL,
			created_at ` + timeType + ` NOT NULL,
			ordinal INTEGER NOT NULL,
			UNIQUE(conversation_id, client_message_id, role),
			FOREIGN KEY (conversation_id) REFERENCES website_advisor_conversations(conversation_id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_website_advisor_messages_conversation ON website_advisor_messages (conversation_id, ordinal)`,
	}
}

func (s *SQLWebsiteHistoryStore) SaveTurn(ctx context.Context, turn WebsiteTurn) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("website history store is not configured")
	}
	return s.store.WithTx(ctx, func(tx db.Tx) error {
		var ownerHash string
		err := tx.QueryRowContext(ctx, `SELECT owner_hash FROM website_advisor_conversations WHERE conversation_id = ?`, turn.ConversationID).Scan(&ownerHash)
		if errors.Is(err, sql.ErrNoRows) {
			conversation := newWebsiteConversation(turn)
			_, err = tx.ExecContext(ctx, `INSERT INTO website_advisor_conversations (
				conversation_id, session_id, owner_hash, title, locale, initial_page_path, created_at, updated_at, expires_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				conversation.ConversationID, conversation.SessionID, conversation.OwnerHash, conversation.Title,
				conversation.Locale, conversation.InitialPagePath, conversation.CreatedAt.UnixNano(),
				conversation.UpdatedAt.UnixNano(), conversation.ExpiresAt.UnixNano())
			if err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else if ownerHash != turn.OwnerHash {
			return errWebsiteHistoryNotFound
		} else {
			if _, err = tx.ExecContext(ctx, `UPDATE website_advisor_conversations SET session_id = ?, updated_at = ?, expires_at = ? WHERE conversation_id = ?`,
				turn.SessionID, turn.CreatedAt.UnixNano(), turn.ExpiresAt.UnixNano(), turn.ConversationID); err != nil {
				return err
			}
		}
		var ordinal int
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(ordinal), 0) FROM website_advisor_messages WHERE conversation_id = ?`, turn.ConversationID).Scan(&ordinal); err != nil {
			return err
		}
		for _, message := range websiteTurnMessages(turn, ordinal) {
			if _, err := tx.ExecContext(ctx, `INSERT INTO website_advisor_messages (
				message_id, conversation_id, client_message_id, role, content, created_at, ordinal
			) VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT(conversation_id, client_message_id, role) DO NOTHING`,
				message.MessageID, turn.ConversationID, message.ClientMessageID, message.Role, message.Content,
				message.CreatedAt.UnixNano(), message.Ordinal); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *SQLWebsiteHistoryStore) ListConversations(ctx context.Context, ownerHash string, now time.Time) ([]WebsiteConversationSummary, error) {
	rows, err := s.store.Query(ctx, `SELECT conversation_id, title, locale, initial_page_path, created_at, updated_at, expires_at
		FROM website_advisor_conversations WHERE owner_hash = ? AND expires_at > ? ORDER BY updated_at DESC, conversation_id ASC`, ownerHash, now.UnixNano())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []WebsiteConversationSummary{}
	for rows.Next() {
		var value WebsiteConversationSummary
		var createdAt, updatedAt, expiresAt int64
		if err := rows.Scan(&value.ConversationID, &value.Title, &value.Locale, &value.InitialPagePath, &createdAt, &updatedAt, &expiresAt); err != nil {
			return nil, err
		}
		value.CreatedAt, value.UpdatedAt, value.ExpiresAt = time.Unix(0, createdAt).UTC(), time.Unix(0, updatedAt).UTC(), time.Unix(0, expiresAt).UTC()
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *SQLWebsiteHistoryStore) GetConversation(ctx context.Context, conversationID, ownerHash string, now time.Time) (WebsiteConversation, error) {
	var value WebsiteConversation
	var createdAt, updatedAt, expiresAt int64
	err := s.store.QueryRow(ctx, `SELECT conversation_id, session_id, owner_hash, title, locale, initial_page_path, created_at, updated_at, expires_at
		FROM website_advisor_conversations WHERE conversation_id = ? AND owner_hash = ? AND expires_at > ?`, conversationID, ownerHash, now.UnixNano()).Scan(
		&value.ConversationID, &value.SessionID, &value.OwnerHash, &value.Title, &value.Locale, &value.InitialPagePath, &createdAt, &updatedAt, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return WebsiteConversation{}, errWebsiteHistoryNotFound
	}
	if err != nil {
		return WebsiteConversation{}, err
	}
	value.CreatedAt, value.UpdatedAt, value.ExpiresAt = time.Unix(0, createdAt).UTC(), time.Unix(0, updatedAt).UTC(), time.Unix(0, expiresAt).UTC()
	rows, err := s.store.Query(ctx, `SELECT message_id, client_message_id, role, content, created_at, ordinal FROM website_advisor_messages WHERE conversation_id = ? ORDER BY ordinal ASC`, conversationID)
	if err != nil {
		return WebsiteConversation{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var message WebsiteMessage
		var messageCreatedAt int64
		if err := rows.Scan(&message.MessageID, &message.ClientMessageID, &message.Role, &message.Content, &messageCreatedAt, &message.Ordinal); err != nil {
			return WebsiteConversation{}, err
		}
		message.CreatedAt = time.Unix(0, messageCreatedAt).UTC()
		value.Messages = append(value.Messages, message)
	}
	return value, rows.Err()
}

func (s *SQLWebsiteHistoryStore) DeleteConversation(ctx context.Context, conversationID, ownerHash string) (bool, error) {
	deleted := false
	err := s.store.WithTx(ctx, func(tx db.Tx) error {
		result, err := tx.ExecContext(ctx, `DELETE FROM website_advisor_conversations WHERE conversation_id = ? AND owner_hash = ?`, conversationID, ownerHash)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		deleted = affected == 1
		return err
	})
	return deleted, err
}

func (s *SQLWebsiteHistoryStore) AdoptConversation(ctx context.Context, conversationID, ownerHash string, now time.Time) (bool, error) {
	result, err := s.store.Exec(ctx, `UPDATE website_advisor_conversations SET owner_hash = ? WHERE conversation_id = ? AND expires_at > ? AND (owner_hash = '' OR owner_hash = ?)`, ownerHash, conversationID, now.UnixNano(), ownerHash)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}

func (s *SQLWebsiteHistoryStore) CleanupExpired(ctx context.Context, now time.Time) (int, error) {
	result, err := s.store.Exec(ctx, `DELETE FROM website_advisor_conversations WHERE expires_at <= ?`, now.UnixNano())
	if err != nil {
		return 0, err
	}
	affected, err := result.RowsAffected()
	return int(affected), err
}

func newWebsiteConversation(turn WebsiteTurn) WebsiteConversation {
	createdAt := turn.CreatedAt.UTC()
	return WebsiteConversation{
		WebsiteConversationSummary: WebsiteConversationSummary{
			ConversationID: turn.ConversationID, Title: truncateRunes(strings.TrimSpace(turn.UserText), 80),
			Locale: turn.Locale, InitialPagePath: turn.InitialPagePath,
			CreatedAt: createdAt, UpdatedAt: createdAt, ExpiresAt: turn.ExpiresAt.UTC(),
		},
		OwnerHash: turn.OwnerHash, SessionID: turn.SessionID,
	}
}

func websiteTurnMessages(turn WebsiteTurn, ordinal int) []WebsiteMessage {
	createdAt := turn.CreatedAt.UTC()
	return []WebsiteMessage{
		{MessageID: turn.ClientMessageID + ":user", ClientMessageID: turn.ClientMessageID, Role: "user", Content: turn.UserText, CreatedAt: createdAt, Ordinal: ordinal + 1},
		{MessageID: turn.ClientMessageID + ":assistant", ClientMessageID: turn.ClientMessageID, Role: "assistant", Content: turn.AssistantText, CreatedAt: createdAt, Ordinal: ordinal + 2},
	}
}

func cloneWebsiteConversation(value WebsiteConversation) WebsiteConversation {
	value.Messages = append([]WebsiteMessage(nil), value.Messages...)
	return value
}
