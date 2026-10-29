package uow

import (
	"context"
	"database/sql"
	"testing"

	"orrery_v2/internal/platform/db"
)

type dialectOnlyTx struct {
	db.Tx
	dialect db.Dialect
}

func (tx dialectOnlyTx) Dialect() db.Dialect {
	return tx.dialect
}

func TestMemoryTxReportsUnderlyingDialect(t *testing.T) {
	tx := &memoryTx{sqlTx: dialectOnlyTx{dialect: db.DialectPostgres}}
	if tx.Dialect() != db.DialectPostgres {
		t.Fatalf("Dialect = %s, want postgres", tx.Dialect())
	}

	tx = &memoryTx{sqlTx: noDialectTx{}}
	if tx.Dialect() != db.DialectSQLite {
		t.Fatalf("fallback Dialect = %s, want sqlite", tx.Dialect())
	}
}

func TestPutSQLRecordStripsNULCharactersBeforeWritingTextColumns(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()

	if err := store.WithTx(ctx, func(tx db.Tx) error {
		return putSQLRecord(ctx, tx, "items\x00bad", "one\x00bad", db.Record{"value": "hello\x00world"})
	}); err != nil {
		t.Fatalf("putSQLRecord returned error: %v", err)
	}

	record, ok, err := store.GetRecord(ctx, "itemsbad", "onebad")
	if err != nil {
		t.Fatalf("GetRecord returned error: %v", err)
	}
	if !ok {
		t.Fatalf("sanitized record was not stored")
	}
	if got := record["value"]; got != "helloworld" {
		t.Fatalf("value = %#v, want sanitized string", got)
	}
}

type noDialectTx struct{}

func (noDialectTx) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	panic("not used")
}

func (noDialectTx) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	panic("not used")
}

func (noDialectTx) QueryRowContext(context.Context, string, ...any) *sql.Row {
	panic("not used")
}
