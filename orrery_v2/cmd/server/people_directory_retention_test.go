package main

import (
	"context"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/peopledirectory"
)

func TestPeopleDirectoryBackgroundSweepSkipsRecentSnapshotButPrunes(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	store := &fakePeopleDirectorySyncStore{latest: now.Add(-time.Minute), latestFound: true}
	scanner := &fakePeopleDirectoryScanner{}
	syncer := testProductionPeopleDirectorySync(store, scanner, now)

	if err := syncer.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if store.pruneCalls != 1 || scanner.calls != 0 {
		t.Fatalf("prune calls=%d scan calls=%d", store.pruneCalls, scanner.calls)
	}
}

func TestPeopleDirectoryBackgroundSweepScansWhenDue(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	store := &fakePeopleDirectorySyncStore{latest: now.Add(-16 * time.Minute), latestFound: true}
	scanner := &fakePeopleDirectoryScanner{}
	syncer := testProductionPeopleDirectorySync(store, scanner, now)

	if err := syncer.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if store.pruneCalls != 1 || scanner.calls != 1 || store.saveMatchCalls != 1 {
		t.Fatalf("prune calls=%d scan calls=%d match saves=%d", store.pruneCalls, scanner.calls, store.saveMatchCalls)
	}
}

func TestPeopleDirectoryManualScanIgnoresBackgroundInterval(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	store := &fakePeopleDirectorySyncStore{latest: now, latestFound: true}
	scanner := &fakePeopleDirectoryScanner{}
	syncer := testProductionPeopleDirectorySync(store, scanner, now)

	if _, err := syncer.ScanAndMatch(context.Background()); err != nil {
		t.Fatalf("ScanAndMatch: %v", err)
	}
	if store.pruneCalls != 0 || scanner.calls != 1 || store.saveMatchCalls != 1 {
		t.Fatalf("prune calls=%d scan calls=%d match saves=%d", store.pruneCalls, scanner.calls, store.saveMatchCalls)
	}
}

func testProductionPeopleDirectorySync(store *fakePeopleDirectorySyncStore, scanner *fakePeopleDirectoryScanner, now time.Time) *productionPeopleDirectorySync {
	return &productionPeopleDirectorySync{
		scanner:      scanner,
		store:        store,
		profiles:     fakePeopleDirectoryProfiles{},
		source:       peopledirectory.SourceFeishu,
		tenantID:     "tenant-1",
		syncInterval: 15 * time.Minute,
		retention: peopledirectory.SnapshotRetentionPolicy{
			KeepLatest: 2,
			MinimumAge: 24 * time.Hour,
			Limit:      100,
		},
		now: func() time.Time { return now },
	}
}

type fakePeopleDirectoryScanner struct {
	calls int
}

func (s *fakePeopleDirectoryScanner) Scan(context.Context, string) error {
	s.calls++
	return nil
}

type fakePeopleDirectorySyncStore struct {
	latest         time.Time
	latestFound    bool
	pruneCalls     int
	saveMatchCalls int
}

func (s *fakePeopleDirectorySyncStore) LatestSnapshotStartedAt(context.Context, string, string) (time.Time, bool, error) {
	return s.latest, s.latestFound, nil
}

func (s *fakePeopleDirectorySyncStore) PruneSnapshots(context.Context, time.Time, peopledirectory.SnapshotRetentionPolicy) (int, error) {
	s.pruneCalls++
	return 0, nil
}

func (s *fakePeopleDirectorySyncStore) Snapshot(context.Context, string) (peopledirectory.Snapshot, error) {
	return peopledirectory.Snapshot{Source: peopledirectory.SourceFeishu, TenantID: "tenant-1", Status: peopledirectory.SnapshotStatusComplete, Complete: true}, nil
}

func (s *fakePeopleDirectorySyncStore) StagedUsers(context.Context, string) ([]peopledirectory.DirectoryUser, error) {
	return nil, nil
}

func (s *fakePeopleDirectorySyncStore) SaveMatchResults(context.Context, string, []peopledirectory.MatchResult) error {
	s.saveMatchCalls++
	return nil
}

type fakePeopleDirectoryProfiles struct{}

func (fakePeopleDirectoryProfiles) QueryDirectory(context.Context, agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error) {
	return agentcore.AgentDirectoryPage{}, nil
}

func (fakePeopleDirectoryProfiles) GetProfile(context.Context, agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	return agentcore.AgentProfile{}, agentcore.ErrNotFound
}
