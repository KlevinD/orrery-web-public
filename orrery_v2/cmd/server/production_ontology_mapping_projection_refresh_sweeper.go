package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
)

const (
	productionMappingProjectionRefreshBatchSize    = 50
	productionMappingProjectionRefreshMaxAttempts  = 5
	productionMappingProjectionRefreshRetryBackoff = time.Minute
	productionMappingProjectionRefreshMaxBackoff   = 30 * time.Minute
)

type productionMappingProjectionRefreshSweeper struct {
	store  *ontology.PlatformMappingProjectionRefreshStore
	core   *db.Store
	runner mappingProjectionRefresher
	now    func() time.Time
}

func registerProductionMappingProjectionRefreshSweeper(rt *platform.Runtime, core *db.Store, runner mappingProjectionRefresher) error {
	if rt == nil || core == nil || runner == nil {
		return fmt.Errorf("mapping projection refresh sweeper requires runtime, core database, and runner")
	}
	return rt.RegisterSweeper("ontology.mapping_projection_refresh", &productionMappingProjectionRefreshSweeper{
		store: ontology.NewPlatformMappingProjectionRefreshStore(core), core: core, runner: runner, now: time.Now,
	})
}

func (s *productionMappingProjectionRefreshSweeper) Sweep(ctx context.Context) error {
	if s == nil || s.store == nil || s.core == nil || s.runner == nil {
		return fmt.Errorf("mapping projection refresh sweeper dependencies are required")
	}
	now := s.currentTime()
	jobs, err := s.store.ListPendingMappingProjectionRefreshes(ctx, now, productionMappingProjectionRefreshBatchSize)
	if err != nil || len(jobs) == 0 {
		return err
	}
	if err := s.runner.RefreshProjections(ctx, ontology.NewPlatformRegistry(s.core)); err != nil {
		resultErr := err
		for _, job := range jobs {
			if recordErr := s.store.RecordMappingProjectionRefreshFailure(
				ctx, job.ReceiptID, "projection_refresh_failed", job.Attempts,
				mappingProjectionRefreshNextAttemptAt(job.Attempts, now), productionMappingProjectionRefreshMaxAttempts,
			); recordErr != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("record mapping projection refresh failure for %s: %w", job.ReceiptID, recordErr))
			}
		}
		return resultErr
	}
	for _, job := range jobs {
		if err := s.store.CompleteMappingProjectionRefresh(ctx, job.ReceiptID); err != nil {
			return err
		}
	}
	return nil
}

func (s *productionMappingProjectionRefreshSweeper) currentTime() time.Time {
	if s != nil && s.now != nil {
		return s.now().UTC()
	}
	return time.Now().UTC()
}

func mappingProjectionRefreshNextAttemptAt(attempts int, failedAt time.Time) time.Time {
	if attempts < 0 {
		attempts = 0
	}
	backoff := productionMappingProjectionRefreshRetryBackoff
	for step := 0; step < attempts && backoff < productionMappingProjectionRefreshMaxBackoff; step++ {
		backoff *= 2
		if backoff > productionMappingProjectionRefreshMaxBackoff {
			backoff = productionMappingProjectionRefreshMaxBackoff
		}
	}
	return failedAt.UTC().Add(backoff)
}
