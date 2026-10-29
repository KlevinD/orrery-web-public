package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

type recordingWebsiteRetentionStore struct {
	cutoff time.Time
	calls  int
	err    error
}

func (s *recordingWebsiteRetentionStore) DeleteExpiredDistillationCandidates(_ context.Context, cutoff time.Time) (int, error) {
	s.cutoff = cutoff
	s.calls++
	return 0, s.err
}

func TestWebsiteDistillationRetentionRemainsDailyAndThirtyDays(t *testing.T) {
	now := time.Date(2026, 9, 6, 3, 0, 0, 0, time.FixedZone("CST", 8*3600))
	clock := func() time.Time { return now }
	store := &recordingWebsiteRetentionStore{}
	sweeper := newProductionCadencedSweeper(productionWebsiteDistillationRetentionSweeper{store: store, now: clock}, clock, 24*time.Hour)
	if err := sweeper.Sweep(context.Background()); err != nil || !store.cutoff.Equal(now.UTC().Add(-30*24*time.Hour)) {
		t.Fatalf("retention cutoff=%s err=%v", store.cutoff, err)
	}
	now = now.Add(time.Hour)
	if err := sweeper.Sweep(context.Background()); err != nil || store.calls != 1 {
		t.Fatalf("retention should not run on every tick: calls=%d err=%v", store.calls, err)
	}
	now = now.Add(23 * time.Hour)
	store.err = errors.New("retention unavailable")
	if err := sweeper.Sweep(context.Background()); !errors.Is(err, store.err) || store.calls != 2 {
		t.Fatalf("retention failure must be observable: calls=%d err=%v", store.calls, err)
	}
}
