package migration

import (
	"strings"
	"testing"

	"orrery_v2/internal/platform/db"
)

func TestStatementsForDialectUsesPostgresOverride(t *testing.T) {
	migration := Migration{
		Version:     "001_payload",
		Description: "create payload table",
		Statements: []string{
			`CREATE TABLE payloads (id TEXT PRIMARY KEY, payload BLOB)`,
		},
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: {
				`CREATE TABLE payloads (id TEXT PRIMARY KEY, payload BYTEA)`,
			},
		},
	}

	sqliteStatements := statementsForDialect(migration, db.DialectSQLite)
	if len(sqliteStatements) != 1 || !strings.Contains(sqliteStatements[0], "BLOB") {
		t.Fatalf("sqlite statements = %#v, want generic BLOB statement", sqliteStatements)
	}
	postgresStatements := statementsForDialect(migration, db.DialectPostgres)
	if len(postgresStatements) != 1 || !strings.Contains(postgresStatements[0], "BYTEA") {
		t.Fatalf("postgres statements = %#v, want BYTEA override", postgresStatements)
	}
}

func TestPlatformMigrationsUsePostgresCompatiblePayloadTypes(t *testing.T) {
	var joined strings.Builder
	for _, migration := range PlatformMigrations() {
		for _, statement := range statementsForDialect(migration, db.DialectPostgres) {
			joined.WriteString(statement)
			joined.WriteByte('\n')
		}
	}
	sql := joined.String()
	if strings.Contains(sql, " BLOB") {
		t.Fatalf("postgres platform migrations still contain BLOB: %s", sql)
	}
	for _, want := range []string{
		"payload BYTEA",
		"payload_json TEXT",
		"queue_key TEXT NOT NULL DEFAULT 'default'",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("postgres platform migrations missing %q in:\n%s", want, sql)
		}
	}
}

func TestPlatformMigrationsUsePostgresBigintForUnixNanoColumns(t *testing.T) {
	var joined strings.Builder
	for _, migration := range PlatformMigrations() {
		for _, statement := range statementsForDialect(migration, db.DialectPostgres) {
			joined.WriteString(statement)
			joined.WriteByte('\n')
		}
	}
	sql := joined.String()
	for _, want := range []string{
		"next_attempt_at BIGINT",
		"locked_until BIGINT",
		"created_at BIGINT",
		"updated_at BIGINT",
		"expires_at BIGINT",
		"last_seen_at BIGINT",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("postgres platform migrations missing %q in:\n%s", want, sql)
		}
	}
}
