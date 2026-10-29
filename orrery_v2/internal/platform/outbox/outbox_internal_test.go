package outbox

import (
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
)

func TestDefaultHandlerTimeoutUsesFiniteLeaseMultiple(t *testing.T) {
	store := &Store{leaseTTL: 37 * time.Second}
	if got, want := defaultHandlerTimeout(store), 370*time.Second; got != want {
		t.Fatalf("defaultHandlerTimeout = %s, want %s", got, want)
	}
}

func TestEnqueueSQLUsesDialectSpecificConflictSyntax(t *testing.T) {
	sqliteSQL := enqueueSQL(db.DialectSQLite)
	if !strings.Contains(sqliteSQL, "INSERT OR IGNORE") {
		t.Fatalf("sqlite enqueue SQL = %s, want INSERT OR IGNORE", sqliteSQL)
	}
	postgresSQL := enqueueSQL(db.DialectPostgres)
	if strings.Contains(postgresSQL, "INSERT OR IGNORE") {
		t.Fatalf("postgres enqueue SQL still uses SQLite syntax: %s", postgresSQL)
	}
	if !strings.Contains(postgresSQL, "ON CONFLICT DO NOTHING") {
		t.Fatalf("postgres enqueue SQL = %s, want ON CONFLICT DO NOTHING", postgresSQL)
	}
}

func TestCreateOutboxTableSQLUsesDialectSpecificPayloadType(t *testing.T) {
	sqliteSQL := createOutboxTableSQL(db.DialectSQLite)
	if !strings.Contains(sqliteSQL, "payload BLOB") {
		t.Fatalf("sqlite create table SQL = %s, want BLOB payload", sqliteSQL)
	}
	postgresSQL := createOutboxTableSQL(db.DialectPostgres)
	if strings.Contains(postgresSQL, "payload BLOB") {
		t.Fatalf("postgres create table SQL still uses SQLite BLOB: %s", postgresSQL)
	}
	if !strings.Contains(postgresSQL, "payload BYTEA") {
		t.Fatalf("postgres create table SQL = %s, want BYTEA payload", postgresSQL)
	}
	for _, want := range []string{"next_attempt_at BIGINT", "locked_until BIGINT", "created_at BIGINT", "updated_at BIGINT"} {
		if !strings.Contains(postgresSQL, want) {
			t.Fatalf("postgres create table SQL = %s, missing %q", postgresSQL, want)
		}
	}
}

func TestPostgresClaimSQLUsesNonBlockingRowLockAndReturnsClaim(t *testing.T) {
	query := postgresClaimSQL()
	for _, want := range []string{"FOR UPDATE SKIP LOCKED", "UPDATE platform_outbox_jobs", "RETURNING jobs.id"} {
		if !strings.Contains(query, want) {
			t.Fatalf("postgres claim SQL = %s, missing %q", query, want)
		}
	}
}

func TestPostgresRecoverStaleSQLLocksAndRechecksCandidates(t *testing.T) {
	query := postgresRecoverStaleSQL()
	for _, want := range []string{"FOR UPDATE SKIP LOCKED", "FROM candidates", "jobs.status = ?", "jobs.locked_until <= ?", "THEN 0::BIGINT ELSE ?::BIGINT"} {
		if !strings.Contains(query, want) {
			t.Fatalf("postgres stale recovery SQL = %s, missing %q", query, want)
		}
	}
}
