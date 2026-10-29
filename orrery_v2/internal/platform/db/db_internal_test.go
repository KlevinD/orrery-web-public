package db

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"
)

func TestPrepareSQLForPostgresRewritesQuestionMarkPlaceholders(t *testing.T) {
	got := prepareSQLForDialect(DialectPostgres, `SELECT * FROM records WHERE id = ? AND table_name = ?`)
	want := `SELECT * FROM records WHERE id = $1 AND table_name = $2`
	if got != want {
		t.Fatalf("prepared SQL = %q, want %q", got, want)
	}
}

func TestPrepareSQLForPostgresSkipsQuotedQuestionMarksAndComments(t *testing.T) {
	query := "SELECT '?' AS literal, \"weird?column\" FROM records -- ? comment\nWHERE id = ? AND note = 'still ? literal' /* ? block */ AND table_name = ?"
	got := prepareSQLForDialect(DialectPostgres, query)
	want := "SELECT '?' AS literal, \"weird?column\" FROM records -- ? comment\nWHERE id = $1 AND note = 'still ? literal' /* ? block */ AND table_name = $2"
	if got != want {
		t.Fatalf("prepared SQL = %q, want %q", got, want)
	}
}

func TestPrepareSQLForSQLiteLeavesQuestionMarks(t *testing.T) {
	query := `SELECT * FROM records WHERE id = ?`
	if got := prepareSQLForDialect(DialectSQLite, query); got != query {
		t.Fatalf("sqlite prepared SQL = %q, want %q", got, query)
	}
}

func TestRecordPayloadMatchPredicateUsesSemanticJSONForPostgres(t *testing.T) {
	if got, want := recordPayloadMatchPredicate(DialectPostgres), "payload_json::jsonb = CAST(? AS jsonb)"; got != want {
		t.Fatalf("postgres predicate = %q, want %q", got, want)
	}
	if got, want := recordPayloadMatchPredicate(DialectSQLite), "payload_json = ?"; got != want {
		t.Fatalf("sqlite predicate = %q, want %q", got, want)
	}
}

func TestOpenConfigUsesPgxDriverForPostgres(t *testing.T) {
	rawURL := "postgres://orrery:secret@localhost:5432/core?sslmode=disable"
	sqlDriver, dsn, path, dialect, err := openConfig(Config{Driver: DriverPostgres, URL: rawURL})
	if err != nil {
		t.Fatalf("openConfig returned error: %v", err)
	}
	if sqlDriver != "pgx" {
		t.Fatalf("sqlDriver = %s, want pgx", sqlDriver)
	}
	if dsn != rawURL {
		t.Fatalf("dsn = %s, want %s", dsn, rawURL)
	}
	if path != "" {
		t.Fatalf("path = %s, want empty", path)
	}
	if dialect != DialectPostgres {
		t.Fatalf("dialect = %s, want postgres", dialect)
	}

	if _, _, _, _, err := openConfig(Config{Driver: DriverPostgres}); err == nil {
		t.Fatalf("openConfig with empty postgres URL returned nil error")
	}
}

func TestSQLiteDSNAcceptsWindowsAbsolutePath(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows absolute sqlite URL parsing")
	}

	_, path, err := sqliteDSN(Config{Driver: DriverFile, URL: `sqlite://D:\tmp\orrery.db`})
	if err != nil {
		t.Fatalf("sqliteDSN returned error: %v", err)
	}
	if path != `D:\tmp\orrery.db` {
		t.Fatalf("path = %q, want Windows absolute path", path)
	}
}

func TestPreparedTxReportsDialect(t *testing.T) {
	tx := &preparedTx{dialect: DialectPostgres}
	if tx.Dialect() != DialectPostgres {
		t.Fatalf("Dialect = %s, want postgres", tx.Dialect())
	}

	var zero *preparedTx
	if zero.Dialect() != DialectSQLite {
		t.Fatalf("zero Dialect = %s, want sqlite", zero.Dialect())
	}
}

func TestExplicitTxReportsUnderlyingDialect(t *testing.T) {
	tx := &explicitTx{Tx: &preparedTx{dialect: DialectPostgres}}
	if tx.Dialect() != DialectPostgres {
		t.Fatalf("Dialect = %s, want postgres", tx.Dialect())
	}

	var zero *explicitTx
	if zero.Dialect() != DialectSQLite {
		t.Fatalf("zero Dialect = %s, want sqlite", zero.Dialect())
	}
}

func TestTransactionsDoNotBlockStoreReadinessChecks(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, Config{Driver: DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()

	assertReady := func() error {
		result := make(chan error, 1)
		go func() {
			result <- store.checkReady(ctx)
		}()
		select {
		case err := <-result:
			return err
		case <-time.After(250 * time.Millisecond):
			return errors.New("store readiness check blocked behind transaction")
		}
	}

	if err := store.WithTx(ctx, func(Tx) error {
		return assertReady()
	}); err != nil {
		t.Fatalf("WithTx blocked readiness check: %v", err)
	}

	tx, err := store.BeginTx(ctx)
	if err != nil {
		t.Fatalf("BeginTx returned error: %v", err)
	}
	if err := assertReady(); err != nil {
		_ = tx.Rollback()
		t.Fatalf("explicit transaction blocked readiness check: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("Rollback returned error: %v", err)
	}
}
