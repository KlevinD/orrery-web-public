package realtime

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestPostgresOutboxAppendClaimPublishAndRecover(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{
		Driver: db.DriverPostgres,
		URL:    postgresTestURL(t),
		Name:   "postgres-realtime-outbox",
	})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()
	dropRealtimeTables(t, store)

	now := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	outbox, err := NewStore(ctx, store, StoreOptions{
		Now:         func() time.Time { return now },
		LeaseTTL:    time.Minute,
		MaxAttempts: 2,
	})
	if err != nil {
		t.Fatalf("NewStore returned error: %v", err)
	}
	channel, err := ConversationChannel("conv-postgres")
	if err != nil {
		t.Fatalf("ConversationChannel returned error: %v", err)
	}
	first, err := outbox.Append(ctx, AppendEvent{
		EventID:       "evt-postgres-1",
		Type:          EventDiscussionMessageCreated,
		Channel:       channel,
		AggregateType: AggregateConversation,
		AggregateID:   "conv-postgres",
		Payload:       json.RawMessage(`{"message_id":"msg-1"}`),
	})
	if err != nil {
		t.Fatalf("first Append returned error: %v", err)
	}
	duplicate, err := outbox.Append(ctx, AppendEvent{
		EventID:       "evt-postgres-1",
		Type:          EventDiscussionMessageCreated,
		Channel:       channel,
		AggregateType: AggregateConversation,
		AggregateID:   "conv-postgres",
		Payload:       json.RawMessage(`{"message_id":"duplicate"}`),
	})
	if err != nil {
		t.Fatalf("duplicate Append returned error: %v", err)
	}
	if duplicate.EventID != first.EventID || duplicate.Version != first.Version || string(duplicate.Payload) != string(first.Payload) {
		t.Fatalf("duplicate returned %+v, want existing %+v", duplicate, first)
	}
	second, err := outbox.Append(ctx, AppendEvent{
		EventID:       "evt-postgres-2",
		Type:          EventDiscussionMessageCompleted,
		Channel:       channel,
		AggregateType: AggregateConversation,
		AggregateID:   "conv-postgres",
		Payload:       json.RawMessage(`{"message_id":"msg-2"}`),
	})
	if err != nil {
		t.Fatalf("second Append returned error: %v", err)
	}
	if first.Version != 1 || second.Version != 2 {
		t.Fatalf("versions = %d,%d want 1,2", first.Version, second.Version)
	}

	claimed, err := outbox.Claim(ctx, "worker-postgres", 10)
	if err != nil {
		t.Fatalf("Claim returned error: %v", err)
	}
	if len(claimed) != 2 {
		t.Fatalf("claimed = %d want 2", len(claimed))
	}
	if err := outbox.MarkPublished(ctx, claimed[0].ID, "worker-postgres"); err != nil {
		t.Fatalf("MarkPublished returned error: %v", err)
	}
	if err := outbox.MarkFailed(ctx, claimed[1].ID, "worker-postgres", "temporary"); err != nil {
		t.Fatalf("MarkFailed returned error: %v", err)
	}
	failed, ok, err := outbox.Get(ctx, claimed[1].ID)
	if err != nil || !ok {
		t.Fatalf("Get failed event ok=%v err=%v", ok, err)
	}
	if failed.Status != StatusPending || failed.Attempts != 1 || failed.LastError != "temporary" {
		t.Fatalf("failed event = %+v, want retry pending attempt 1", failed)
	}

	outbox.now = func() time.Time { return now.Add(2 * time.Second) }
	reclaimed, err := outbox.Claim(ctx, "worker-postgres-2", 10)
	if err != nil {
		t.Fatalf("reclaim returned error: %v", err)
	}
	if len(reclaimed) != 1 || reclaimed[0].ID != claimed[1].ID {
		t.Fatalf("reclaimed = %+v, want failed event", reclaimed)
	}
	outbox.now = func() time.Time { return now.Add(2 * time.Minute) }
	recovered, err := outbox.RecoverExpired(ctx, 10)
	if err != nil {
		t.Fatalf("RecoverExpired returned error: %v", err)
	}
	if recovered != 1 {
		t.Fatalf("RecoverExpired recovered = %d, want 1", recovered)
	}
	afterRecover, ok, err := outbox.Get(ctx, reclaimed[0].ID)
	if err != nil || !ok {
		t.Fatalf("Get after recover ok=%v err=%v", ok, err)
	}
	if afterRecover.Status != StatusPending || afterRecover.WorkerID != "" {
		t.Fatalf("after recover = %+v, want pending without worker", afterRecover)
	}
}

func TestPostgresOutboxMigrationUsesByteaPayload(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{
		Driver: db.DriverPostgres,
		URL:    postgresTestURL(t),
		Name:   "postgres-realtime-migration",
	})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()
	dropRealtimeTables(t, store)

	migrationDef := OutboxMigration()
	if err := migration.NewRunner([]migration.Migration{{
		Version:             migrationDef.Version,
		Description:         migrationDef.Description,
		Statements:          migrationDef.Statements,
		StatementsByDialect: migrationDef.StatementsByDialect,
	}}).Up(ctx, store); err != nil {
		t.Fatalf("migration Up returned error: %v", err)
	}

	var dataType string
	if err := store.QueryRow(ctx, `
SELECT data_type
FROM information_schema.columns
WHERE table_schema = current_schema()
	AND table_name = 'realtime_outbox'
	AND column_name = 'payload_json'`).Scan(&dataType); err != nil {
		t.Fatalf("query realtime_outbox payload type: %v", err)
	}
	if dataType != "bytea" {
		t.Fatalf("payload_json data type = %s, want bytea", dataType)
	}
}

func postgresTestURL(t *testing.T) string {
	t.Helper()
	raw := os.Getenv("ORRERY_TEST_POSTGRES_URL")
	if raw == "" {
		t.Skip("ORRERY_TEST_POSTGRES_URL is not set")
	}
	return raw
}

func dropRealtimeTables(t *testing.T, store *db.Store) {
	t.Helper()
	if store.Dialect() != db.DialectPostgres {
		t.Fatalf("dropRealtimeTables requires postgres store, got %s", store.Dialect())
	}
	for _, table := range []string{"schema_migrations", "realtime_outbox"} {
		if _, err := store.Exec(context.Background(), `DROP TABLE IF EXISTS `+table+` CASCADE`); err != nil {
			t.Fatalf("drop %s: %v", table, err)
		}
	}
}
