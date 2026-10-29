package main

import (
	"context"
	"fmt"
	"time"

	"orrery_v2/internal/platform"
)

type websiteDistillationRetentionStore interface {
	DeleteExpiredDistillationCandidates(context.Context, time.Time) (int, error)
}

func registerProductionWebsiteDistillationRetention(rt *platform.Runtime, store websiteDistillationRetentionStore) error {
	if rt == nil || store == nil {
		return fmt.Errorf("website distillation retention dependencies are required")
	}
	return rt.RegisterSweeper("website.distillation_retention", newProductionCadencedSweeper(
		productionWebsiteDistillationRetentionSweeper{store: store, now: rt.Clock.Now}, rt.Clock.Now, 24*time.Hour,
	))
}

type productionWebsiteDistillationRetentionSweeper struct {
	store websiteDistillationRetentionStore
	now   func() time.Time
}

func (s productionWebsiteDistillationRetentionSweeper) Sweep(ctx context.Context) error {
	_, err := s.store.DeleteExpiredDistillationCandidates(ctx, s.now().UTC().Add(-30*24*time.Hour))
	return err
}
