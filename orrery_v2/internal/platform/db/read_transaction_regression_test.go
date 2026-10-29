package db_test

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/testpg"
)

func TestPostgresStallReadSnapshotBypassesWriteQueueAndCannotWrite(t *testing.T) {
	store := testpg.Open(t)
	writer, err := store.BeginTx(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := store.WithReadTx(ctx, func(tx db.Tx) error {
		var result int
		return tx.QueryRowContext(ctx, "SELECT 1").Scan(&result)
	}); err != nil {
		t.Fatalf("read queued behind unrelated writer: %v", err)
	}
	if err := store.WithReadTx(ctx, func(tx db.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO platform_records(table_name, record_key, payload_json) VALUES ('read-only-test', 'must-not-write', '{}')")
		return err
	}); err == nil {
		t.Fatal("read snapshot allowed a write")
	}
}

func TestPostgresStallReadSnapshotIsConsistent(t *testing.T) {
	store := testpg.Open(t)
	ctx := context.Background()
	if err := store.PutRecord(ctx, "stall-snapshot", "record", db.Record{"value": "before"}); err != nil {
		t.Fatal(err)
	}
	if err := store.WithReadTx(ctx, func(tx db.Tx) error {
		first, _, err := db.GetRecordTx(ctx, tx, "stall-snapshot", "record")
		if err != nil {
			return err
		}
		if err := store.PutRecord(ctx, "stall-snapshot", "record", db.Record{"value": "after"}); err != nil {
			return err
		}
		second, _, err := db.GetRecordTx(ctx, tx, "stall-snapshot", "record")
		if err != nil {
			return err
		}
		if first["value"] != "before" || second["value"] != "before" {
			t.Errorf("read snapshot changed: first=%v second=%v", first, second)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresStallTransactionPanicRollsBackSQLLocks(t *testing.T) {
	store := testpg.Open(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	func() {
		defer func() {
			if recover() == nil {
				t.Error("callback did not panic")
			}
		}()
		_ = store.WithTx(ctx, func(tx db.Tx) error {
			if err := db.PutRecordTx(ctx, tx, "stall-panic", "key", db.Record{"value": "uncommitted"}); err != nil {
				t.Fatal(err)
			}
			panic("worker panic")
		})
	}()
	writeCtx, writeCancel := context.WithTimeout(ctx, 2*time.Second)
	defer writeCancel()
	if err := store.PutRecord(writeCtx, "stall-panic", "key", db.Record{"value": "after-panic"}); err != nil {
		t.Fatalf("panic leaked SQL transaction locks: %v", err)
	}
}
