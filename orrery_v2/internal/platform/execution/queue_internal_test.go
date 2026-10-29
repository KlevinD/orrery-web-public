package execution

import (
	"strings"
	"testing"

	"orrery_v2/internal/platform/db"
)

func TestQueueTableSQLUsesDialectSpecificPayloadType(t *testing.T) {
	sqliteSQL := queueTableSQL(db.DialectSQLite)
	if !strings.Contains(sqliteSQL, "payload BLOB") {
		t.Fatalf("sqlite queue table SQL = %s, want BLOB payload", sqliteSQL)
	}

	postgresSQL := queueTableSQL(db.DialectPostgres)
	if strings.Contains(postgresSQL, "payload BLOB") {
		t.Fatalf("postgres queue table SQL still uses SQLite BLOB: %s", postgresSQL)
	}
	if !strings.Contains(postgresSQL, "payload BYTEA") {
		t.Fatalf("postgres queue table SQL = %s, want BYTEA payload", postgresSQL)
	}
	for _, want := range []string{"locked_until BIGINT", "created_at BIGINT", "updated_at BIGINT", "completed_at BIGINT"} {
		if !strings.Contains(postgresSQL, want) {
			t.Fatalf("postgres queue table SQL = %s, missing %q", postgresSQL, want)
		}
	}
}

func TestRecoverExpiredUsesTypedNullForPostgresTimestampColumns(t *testing.T) {
	if got := queueNullTimeSQL(db.DialectPostgres); got != "CAST(NULL AS BIGINT)" {
		t.Fatalf("postgres null time SQL = %q", got)
	}
	if got := queueNullTimeSQL(db.DialectSQLite); got != "NULL" {
		t.Fatalf("sqlite null time SQL = %q", got)
	}
}
