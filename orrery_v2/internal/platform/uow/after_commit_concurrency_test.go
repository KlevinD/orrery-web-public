package uow_test

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/platform/testpg"
	"orrery_v2/internal/platform/uow"
)

func TestPostgresAfterCommitDoesNotBlockOtherTransactions(t *testing.T) {
	store := testpg.Open(t)
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "Do", true: "Begin"}[explicit], func(t *testing.T) {
			unit := uow.New(store)
			started, release, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			defer func() {
				close(release)
				select {
				case err := <-finished:
					if err != nil {
						t.Errorf("first transaction: %v", err)
					}
				case <-time.After(5 * time.Second):
					t.Error("hook did not finish")
				}
			}()
			hook := func(ctx context.Context) error { close(started); <-release; return nil }
			go func() {
				if explicit {
					tx, err := unit.Begin(context.Background())
					if err != nil {
						finished <- err
						return
					}
					tx.AfterCommit(hook)
					finished <- tx.Commit(context.Background())
					return
				}
				finished <- unit.Do(context.Background(), func(_ context.Context, tx uow.Tx) error { tx.AfterCommit(hook); return nil })
			}()
			select {
			case <-started:
			case err := <-finished:
				t.Fatalf("first transaction: %v", err)
			case <-time.After(5 * time.Second):
				t.Fatal("hook never started")
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := unit.Do(ctx, func(context.Context, uow.Tx) error { return nil }); err != nil {
				t.Fatalf("blocked by committed transaction's hook: %v", err)
			}
		})
	}
}
