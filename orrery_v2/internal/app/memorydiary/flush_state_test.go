package memorydiary

import (
	"context"
	"sort"
	"sync"
	"testing"
	"time"

	"orrery_v2/internal/platform/testkit"
)

func TestMemoryFlushStateStoreSerializesConcurrentMutations(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryFlushStateStore()
	start := make(chan struct{})
	results := make(chan int, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			state, err := store.MutateFlushState(ctx, "tenant-a", "agent-1", "session-1", func(state *FlushState, _ bool) error {
				state.AttemptCount++
				return nil
			})
			if err != nil {
				t.Errorf("MutateFlushState: %v", err)
				return
			}
			results <- state.AttemptCount
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	counts := make([]int, 0, 2)
	for count := range results {
		counts = append(counts, count)
	}
	sort.Ints(counts)
	if len(counts) != 2 || counts[0] != 1 || counts[1] != 2 {
		t.Fatalf("serialized attempt counts = %v, want [1 2]", counts)
	}
}

func TestPlatformFlushStateStorePersistsTenantAgentSessionCursor(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	store := NewPlatformFlushStateStore(env.DB)
	want := FlushState{
		TenantID: "tenant-a", AgentID: "agent-1", SessionID: "session-1",
		CurrentRevision: "revision-2", LastSuccessfulRevision: "revision-1",
		LastSuccessfulAt: time.Date(2026, 7, 17, 8, 0, 0, 0, time.UTC), LastSuccessfulDate: "2026-07-17",
		AttemptRevision: "revision-2", AttemptTrigger: TriggerRunTerminal, AttemptCount: 2,
		LastFailureAt: time.Date(2026, 7, 17, 8, 1, 0, 0, time.UTC), LastFailure: "temporary failure",
	}
	if err := store.SaveFlushState(ctx, want); err != nil {
		t.Fatalf("SaveFlushState: %v", err)
	}
	got, ok, err := NewPlatformFlushStateStore(env.DB).LoadFlushState(ctx, "tenant-a", "agent-1", "session-1")
	if err != nil || !ok {
		t.Fatalf("LoadFlushState = %+v, %t, %v", got, ok, err)
	}
	if got.TenantID != want.TenantID || got.AgentID != want.AgentID || got.SessionID != want.SessionID ||
		got.LastSuccessfulRevision != want.LastSuccessfulRevision || got.AttemptCount != want.AttemptCount || got.LastFailure != want.LastFailure {
		t.Fatalf("persisted state = %+v, want %+v", got, want)
	}
	if _, ok, err := store.LoadFlushState(ctx, "tenant-b", "agent-1", "session-1"); err != nil || ok {
		t.Fatalf("cross-tenant state lookup = ok:%t err:%v", ok, err)
	}
}

func TestPlatformFlushStateStoreReassignsTenant(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	store := NewPlatformFlushStateStore(env.DB)
	state := FlushState{TenantID: "legacy-org", AgentID: "agent-1", SessionID: "session-1", LastSuccessfulRevision: "r1", LastSuccessfulAt: time.Now().UTC()}
	if err := store.SaveFlushState(ctx, state); err != nil {
		t.Fatalf("SaveFlushState: %v", err)
	}
	moved, err := store.ReassignTenant(ctx, "legacy-org", "minvia")
	if err != nil || moved != 1 {
		t.Fatalf("ReassignTenant = %d, %v", moved, err)
	}
	got, found, err := store.LoadFlushState(ctx, "minvia", "agent-1", "session-1")
	if err != nil || !found || got.TenantID != "minvia" || got.LastSuccessfulRevision != "r1" {
		t.Fatalf("new state = %+v, %t, %v", got, found, err)
	}
	if _, found, err := store.LoadFlushState(ctx, "legacy-org", "agent-1", "session-1"); err != nil || found {
		t.Fatalf("legacy state = found:%t err:%v", found, err)
	}
}
