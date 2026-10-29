package db

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestQueuedTransactionsHonorDeadlineBeforeHolderReleases(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "WithTx", true: "BeginTx"}[explicit], func(t *testing.T) {
			store := &Store{}
			if err := store.lockTransaction(context.Background()); err != nil {
				t.Fatal(err)
			}
			defer store.releaseTransactionGate()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				if explicit {
					_, err := store.BeginTx(ctx)
					done <- err
					return
				}
				done <- store.WithTx(ctx, func(Tx) error { t.Error("canceled callback executed"); return nil })
			}()
			select {
			case err := <-done:
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("error = %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("queued transaction ignored deadline while holder still owns gate")
			}
		})
	}
}

func TestCanceledTransactionWaitDoesNotLeakGate(t *testing.T) {
	store := &Store{}
	for i := 0; i < 100; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := store.lockTransaction(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v", err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := store.lockTransaction(ctx); err != nil {
		t.Fatal(err)
	}
	store.releaseTransactionGate()
}

func TestQueuedRecordWritesHonorDeadline(t *testing.T) {
	store := &Store{}
	if err := store.lockTransaction(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer store.releaseTransactionGate()
	operations := map[string]func(context.Context) error{
		"put": func(ctx context.Context) error { return store.PutRecord(ctx, "test", "key", Record{}) },
		"create": func(ctx context.Context) error {
			_, err := store.CreateRecord(ctx, "test", "key", Record{})
			return err
		},
		"compare-delete": func(ctx context.Context) error {
			_, err := store.CompareAndDeleteRecord(ctx, "test", "key", Record{})
			return err
		},
		"delete": func(ctx context.Context) error { return store.DeleteRecord(ctx, "test", "key") },
	}
	for name, operation := range operations {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			if err := operation(ctx); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}
