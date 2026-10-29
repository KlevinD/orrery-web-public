package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/testkit"
)

func TestProductionMappingProjectionRefreshSweeperRetriesDurableJobs(t *testing.T) {
	ctx := context.Background()
	runtime := testkit.NewInitializedTestRuntime(t, OntologyModule())
	core, err := runtime.Databases.Store(db.RoleCore)
	if err != nil {
		t.Fatalf("core store: %v", err)
	}
	store := ontology.NewPlatformMappingProjectionRefreshStore(core)
	if err := store.EnqueueMappingProjectionRefresh(ctx, "receipt-1", "tenant-1"); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	now := time.Now().UTC().Add(time.Second)
	sweeper := &productionMappingProjectionRefreshSweeper{store: store, core: core, runner: alwaysFailingMappingProjectionRefresher{}, now: func() time.Time { return now }}
	if err := sweeper.Sweep(ctx); err == nil {
		t.Fatal("failed refresh returned nil error")
	}
	pending, err := store.GetMappingProjectionRefresh(ctx, "receipt-1")
	if err != nil || pending.State != ontology.MappingProjectionRefreshPending || pending.Attempts != 1 {
		t.Fatalf("pending job = %+v err=%v", pending, err)
	}

	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatalf("new projection runner: %v", err)
	}
	sweeper.runner = runner
	if err := sweeper.Sweep(ctx); err != nil {
		t.Fatalf("backed-off sweep: %v", err)
	}
	stillPending, err := store.GetMappingProjectionRefresh(ctx, "receipt-1")
	if err != nil || stillPending.State != ontology.MappingProjectionRefreshPending || stillPending.Attempts != 1 {
		t.Fatalf("backed-off job = %+v err=%v", stillPending, err)
	}
	now = now.Add(productionMappingProjectionRefreshRetryBackoff)
	if err := sweeper.Sweep(ctx); err != nil {
		t.Fatalf("retry sweep: %v", err)
	}
	completed, err := store.GetMappingProjectionRefresh(ctx, "receipt-1")
	if err != nil || completed.State != ontology.MappingProjectionRefreshSucceeded || completed.Attempts != 2 {
		t.Fatalf("completed job = %+v err=%v", completed, err)
	}
}

func TestMappingProjectionRefreshBackoffIsBounded(t *testing.T) {
	failedAt := time.Unix(300, 0).UTC()
	if got := mappingProjectionRefreshNextAttemptAt(0, failedAt); !got.Equal(failedAt.Add(time.Minute)) {
		t.Fatalf("first retry = %v", got)
	}
	if got := mappingProjectionRefreshNextAttemptAt(100, failedAt); !got.Equal(failedAt.Add(productionMappingProjectionRefreshMaxBackoff)) {
		t.Fatalf("bounded retry = %v", got)
	}
}

func TestProductionMappingProjectionRefreshSweeperReturnsFailurePersistenceErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	runtime := testkit.NewInitializedTestRuntime(t, OntologyModule())
	core, err := runtime.Databases.Store(db.RoleCore)
	if err != nil {
		t.Fatalf("core store: %v", err)
	}
	store := ontology.NewPlatformMappingProjectionRefreshStore(core)
	if err := store.EnqueueMappingProjectionRefresh(ctx, "receipt-persist-failure", "tenant-1"); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	sweeper := &productionMappingProjectionRefreshSweeper{
		store:  store,
		core:   core,
		runner: cancelingMappingProjectionRefresher{cancel: cancel},
		now:    time.Now,
	}
	if err := sweeper.Sweep(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("sweep error = %v, want persisted context cancellation", err)
	}
	record, err := store.GetMappingProjectionRefresh(context.Background(), "receipt-persist-failure")
	if err != nil || record.Attempts != 0 {
		t.Fatalf("refresh record = %+v err=%v", record, err)
	}
}

type alwaysFailingMappingProjectionRefresher struct{}

func (alwaysFailingMappingProjectionRefresher) RefreshProjections(context.Context, ontology.Registry) error {
	return errors.New("refresh failed")
}

type cancelingMappingProjectionRefresher struct {
	cancel context.CancelFunc
}

func (r cancelingMappingProjectionRefresher) RefreshProjections(context.Context, ontology.Registry) error {
	r.cancel()
	return errors.New("refresh failed")
}
