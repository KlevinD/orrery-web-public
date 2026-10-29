package discussionsession

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
)

// This probe uses only VALUES/CTE fixtures in a read-only transaction. It never
// creates tables or reads/writes real platform_records, even on a live database.
func TestSessionHistoryPostgresReadOnlySyntheticSQL(t *testing.T) {
	dsn := os.Getenv("ORRERY_TEST_POSTGRES_URL")
	if dsn == "" {
		t.Skip("ORRERY_TEST_POSTGRES_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	database, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal("PostgreSQL connection setup failed")
	}
	defer database.Close()
	tx, err := database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal("PostgreSQL read-only transaction failed")
	}
	defer tx.Rollback()
	const tenant = "history-synthetic-only"
	values := []any{}
	for i, role := range []string{"user", "assistant", "system"} {
		message := MessageRecord{ConversationID: "synthetic", MessageID: string(rune('a' + i)), Role: role, Content: "\u94f6\u884c 100%_!", CreatedAt: testTime.Add(time.Duration(i) * time.Second)}
		payload, _ := encodePayload(message)
		raw, _ := json.Marshal(db.Record{"tenant_id": tenant, "conversation_id": "synthetic", "created_at": formatTime(message.CreatedAt), "payload": payload})
		values = append(values, string(raw))
	}
	cte := "WITH platform_records(record_key, payload_json) AS (VALUES ('a', $1::text), ('b', $2::text), ('c', $3::text)) "
	role := historyJSONField(db.DialectPostgres, "Role", true)
	content := historyJSONField(db.DialectPostgres, "Content", true)
	date := historyTimestamp(db.DialectPostgres, historyJSONField(db.DialectPostgres, "created_at", false))
	filter := role + " IN ('user', 'assistant') AND " + historyJSONField(db.DialectPostgres, "tenant_id", false) + " = $4 AND LOWER(" + content + ") LIKE LOWER($5) ESCAPE '!'"
	pattern := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace("100%_!")
	values = append(values, tenant, "%"+pattern+"%")
	query := cte + "SELECT record_key FROM platform_records WHERE " + filter + " AND " + date + " >= " + historyTimestamp(db.DialectPostgres, "$6") + " ORDER BY " + date + " DESC, record_key DESC LIMIT 1 OFFSET 0"
	var id string
	if err := tx.QueryRowContext(ctx, query, append(values, testTime.Format(time.RFC3339))...).Scan(&id); err != nil {
		t.Fatalf("synthetic history search SQL failed: %v", err)
	}
	if id != "b" {
		t.Fatalf("unexpected synthetic match %q", id)
	}
	query = cte + "SELECT position FROM (SELECT record_key, ROW_NUMBER() OVER (ORDER BY " + date + " ASC, record_key ASC) - 1 AS position FROM platform_records WHERE " + filter + ") messages WHERE record_key = 'b'"
	var position int
	if err := tx.QueryRowContext(ctx, query, values...).Scan(&position); err != nil || position != 1 {
		t.Fatalf("synthetic anchor lookup failed: position=%d err=%v", position, err)
	}
}
