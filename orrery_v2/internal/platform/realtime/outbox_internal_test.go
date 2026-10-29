package realtime

import (
	"errors"
	"strings"
	"testing"

	"orrery_v2/internal/platform/db"
)

func TestOutboxSchemaStatementsUseDialectSpecificPayloadType(t *testing.T) {
	sqliteStatements := outboxSchemaStatements(db.DialectSQLite)
	if !strings.Contains(sqliteStatements[0], "payload_json BLOB") {
		t.Fatalf("sqlite schema = %s, want BLOB payload", sqliteStatements[0])
	}

	postgresStatements := outboxSchemaStatements(db.DialectPostgres)
	if strings.Contains(postgresStatements[0], "payload_json BLOB") {
		t.Fatalf("postgres schema still uses SQLite BLOB: %s", postgresStatements[0])
	}
	if !strings.Contains(postgresStatements[0], "payload_json BYTEA") {
		t.Fatalf("postgres schema = %s, want BYTEA payload", postgresStatements[0])
	}
	for _, want := range []string{"version BIGINT", "locked_until BIGINT", "next_attempt_at BIGINT", "created_at BIGINT", "updated_at BIGINT", "published_at BIGINT"} {
		if !strings.Contains(postgresStatements[0], want) {
			t.Fatalf("postgres schema = %s, missing %q", postgresStatements[0], want)
		}
	}
}

func TestRealtimeAppendErrorClassifiersRecognizePostgresErrors(t *testing.T) {
	if !isDuplicateEventIDError(errors.New(`ERROR: duplicate key value violates unique constraint "realtime_outbox_pkey" (SQLSTATE 23505)`)) {
		t.Fatalf("postgres primary key duplicate was not recognized")
	}
	if !isRetryableAppendError(errors.New(`ERROR: duplicate key value violates unique constraint "idx_realtime_outbox_channel_version" (SQLSTATE 23505)`)) {
		t.Fatalf("postgres channel version duplicate was not recognized as retryable")
	}
}
