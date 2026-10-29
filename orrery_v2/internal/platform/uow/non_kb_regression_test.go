package uow

import (
	"context"
	"errors"
	"orrery_v2/internal/platform/db"
	"testing"
	"time"
)

func TestTransactionWaitCanBeCanceled(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	unit := New(store)
	held, err := unit.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Rollback(ctx)
	waitCtx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- unit.Do(waitCtx, func(context.Context, Tx) error { t.Error("canceled waiter entered transaction"); return nil })
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled waiter still blocked")
	}
}

func TestMalformedRecordCannotCommitAsNotFound(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.Exec(ctx, `INSERT INTO platform_records(table_name,record_key,payload_json) VALUES(?,?,?)`, "agent_profiles", "broken", "{"); err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("profile not found")
	err = New(store).Do(ctx, func(ctx context.Context, tx Tx) error {
		_, found := tx.Get("agent_profiles", "broken")
		if found {
			t.Fatal("malformed record found")
		}
		return sentinel
	})
	if err == nil || errors.Is(err, sentinel) {
		t.Fatalf("decode failure was hidden: %v", err)
	}
}

func TestMalformedSnapshotCannotCommitAsEmpty(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.Exec(ctx, `INSERT INTO platform_records(table_name,record_key,payload_json) VALUES(?,?,?)`, "agent_profiles", "broken", "{"); err != nil {
		t.Fatal(err)
	}
	err = New(store).Do(ctx, func(ctx context.Context, tx Tx) error {
		tx.Snapshot()
		return nil
	})
	if err == nil {
		t.Fatal("snapshot decode failure was hidden")
	}
}
