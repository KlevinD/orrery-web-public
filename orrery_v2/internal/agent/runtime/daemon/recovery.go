package daemon

import (
	"context"
	"fmt"
)

type RecoveryStore interface {
	RecoverStale(ctx context.Context, limit int) (int, error)
}

type RecoverySweeper struct {
	Store RecoveryStore
	Limit int
}

func (s RecoverySweeper) Name() string { return "agent-runtime-recovery" }

func (s RecoverySweeper) Sweep(ctx context.Context) error {
	if s.Store == nil {
		return fmt.Errorf("recovery store is required")
	}
	limit := s.Limit
	if limit <= 0 {
		limit = 100
	}
	_, err := s.Store.RecoverStale(ctx, limit)
	return err
}
