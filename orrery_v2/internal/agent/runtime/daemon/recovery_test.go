package daemon

import (
	"context"
	"testing"
)

func TestRecoverySweeperInvokesPlatformRecoveryStore(t *testing.T) {
	store := &fakeRecoveryStore{}
	err := (RecoverySweeper{Store: store, Limit: 5}).Sweep(context.Background())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if store.limit != 5 {
		t.Fatalf("limit = %d", store.limit)
	}
}

type fakeRecoveryStore struct{ limit int }

func (s *fakeRecoveryStore) RecoverStale(_ context.Context, limit int) (int, error) {
	s.limit = limit
	return 1, nil
}
