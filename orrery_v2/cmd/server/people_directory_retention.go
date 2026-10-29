package main

import (
	"context"
	"fmt"
	"time"

	"orrery_v2/internal/app/peopledirectory"
)

const (
	productionPeopleDirectorySyncInterval       = 15 * time.Minute
	productionPeopleDirectorySnapshotMinimumAge = 24 * time.Hour
	productionPeopleDirectoryKeepLatest         = 2
	productionPeopleDirectoryPruneLimit         = 100
)

type peopleDirectoryScanner interface {
	Scan(ctx context.Context, snapshotID string) error
}

type peopleDirectorySyncStore interface {
	Snapshot(ctx context.Context, snapshotID string) (peopledirectory.Snapshot, error)
	StagedUsers(ctx context.Context, snapshotID string) ([]peopledirectory.DirectoryUser, error)
	SaveMatchResults(ctx context.Context, snapshotID string, matches []peopledirectory.MatchResult) error
	LatestSnapshotStartedAt(ctx context.Context, source, tenantID string) (time.Time, bool, error)
	PruneSnapshots(ctx context.Context, now time.Time, policy peopledirectory.SnapshotRetentionPolicy) (int, error)
}

func (s *productionPeopleDirectorySync) Sweep(ctx context.Context) error {
	if s == nil || s.store == nil || s.scanner == nil || s.profiles == nil || s.now == nil {
		return fmt.Errorf("production People directory sync is not configured")
	}
	now := s.now().UTC()
	if _, err := s.store.PruneSnapshots(ctx, now, s.retention); err != nil {
		return err
	}
	latest, found, err := s.store.LatestSnapshotStartedAt(ctx, s.source, s.tenantID)
	if err != nil {
		return err
	}
	if found && !latest.Before(now.Add(-s.syncInterval)) {
		return nil
	}
	_, err = s.ScanAndMatch(ctx)
	return err
}
